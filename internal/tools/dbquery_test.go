// db_query 工具的单测（dbquery.go）。
//
// 核心断言是「只读拒写走**驱动层**」：mode=ro 连接上 INSERT 直接报错——
// 这不是 SQL 校验层的拒绝（校验只做快速失败），是结构性保证。
package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// buildTestDB 建一个临时 SQLite 库（写侧用普通连接——只读约束只该作用在
// db_query 的连接上）。
func buildTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, score REAL, blob BLOB, ts TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 150; i++ {
		if _, err := db.Exec(`INSERT INTO t (name, score, ts) VALUES (?, ?, ?)`,
			"n"+strings.Repeat("x", i%7), float64(i)*0.5, "2026-01-02T03:04:05Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO t (name, blob) VALUES ('bl', ?)`, []byte{0x00, 0x01, 0xff}); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustQuery(t *testing.T, path, sqlText string) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"path": path, "sql": sqlText})
	return execDBQuery(context.Background(), raw)
}

// TestDBQueryBasic：正常查询返回列名与行（JSON）。
func TestDBQueryBasic(t *testing.T) {
	path := buildTestDB(t)
	out, err := mustQuery(t, path, "SELECT id, name FROM t WHERE id = 1")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	var rs struct {
		Columns []string   `json:"columns"`
		Rows    [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		t.Fatalf("结果不是 JSON: %v\n%s", err, out)
	}
	if len(rs.Columns) != 2 || rs.Columns[0] != "id" || rs.Columns[1] != "name" {
		t.Fatalf("列名不对: %v", rs.Columns)
	}
	if len(rs.Rows) != 1 || rs.Rows[0][0] != "1" || rs.Rows[0][1] != "nx" {
		// id=1 → name = "n" + 1 个 x = "nx"（i%7=1）。
		t.Fatalf("行内容不对: %v", rs.Rows)
	}
}

// TestDBQueryReadOnlyRejectsWrite：**驱动层**拒写——INSERT/UPDATE/CREATE 全部
// 失败，且库文件保持不变（行数仍是 151）。
func TestDBQueryReadOnlyRejectsWrite(t *testing.T) {
	path := buildTestDB(t)
	for _, bad := range []string{
		"INSERT INTO t (name) VALUES ('x')",
		"UPDATE t SET name = 'x'",
		"DELETE FROM t WHERE id = 1",
		"CREATE TABLE evil (x TEXT)",
	} {
		_, err := mustQuery(t, path, bad)
		if err == nil {
			t.Fatalf("%q 在只读连接上竟然成功了", bad)
		}
	}
	// 库没有被写：行数不变。
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 151 {
		t.Fatalf("库被写入了？行数 = %d", n)
	}
}

// TestDBQueryRejectsMultiStatements：多语句拒绝（含「中间分号 + 结尾分号」形态）。
func TestDBQueryRejectsMultiStatements(t *testing.T) {
	path := buildTestDB(t)
	for _, bad := range []string{
		"SELECT 1; SELECT 2",
		"SELECT 1 ; SELECT 2;",
		"WITH x AS (SELECT 1) SELECT * FROM x; DROP TABLE t",
	} {
		_, err := mustQuery(t, path, bad)
		if err == nil {
			t.Fatalf("多语句应拒绝: %q", bad)
		}
		if !strings.Contains(err.Error(), "单条语句") {
			t.Fatalf("拒绝理由应说明单条语句限制: %v", err)
		}
	}
	// 结尾一个分号放行。
	if _, err := mustQuery(t, path, "SELECT COUNT(*) FROM t;"); err != nil {
		t.Fatalf("结尾一个分号应放行: %v", err)
	}
}

// TestDBQueryPrefixWhitelist：前缀白名单——SELECT/EXPLAIN/WITH 放行，
// INSERT/PRAGMA 等快速失败。
func TestDBQueryPrefixWhitelist(t *testing.T) {
	path := buildTestDB(t)
	// EXPLAIN 与 WITH 都要真跑通过。
	if _, err := mustQuery(t, path, "EXPLAIN SELECT id FROM t LIMIT 1"); err != nil {
		t.Fatalf("EXPLAIN 应放行: %v", err)
	}
	if _, err := mustQuery(t, path, "WITH x AS (SELECT 1 AS v) SELECT v FROM x"); err != nil {
		t.Fatalf("WITH 应放行: %v", err)
	}
	for _, bad := range []string{"PRAGMA table_info(t)", "VACUUM", "ATTACH DATABASE 'x' AS y"} {
		_, err := mustQuery(t, path, bad)
		if err == nil {
			t.Fatalf("%q 应被前缀白名单拒绝", bad)
		}
	}
}

// TestDBQueryRowLimit：行数上限 100，超出标 rows_truncated。
func TestDBQueryRowLimit(t *testing.T) {
	path := buildTestDB(t)
	out, err := mustQuery(t, path, "SELECT id FROM t ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var rs struct {
		Rows      [][]string `json:"rows"`
		Truncated bool       `json:"rows_truncated"`
	}
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		t.Fatal(err)
	}
	if len(rs.Rows) != dbQueryMaxRows {
		t.Fatalf("行数应截到 %d，实际 %d", dbQueryMaxRows, len(rs.Rows))
	}
	if !rs.Truncated {
		t.Fatal("应标 rows_truncated")
	}
}

// TestDBQueryCellTruncation：单元格截断到 500 字符（中文按字符算）。
func TestDBQueryCellTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE s (v TEXT)`); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("汉", 900)
	if _, err := db.Exec(`INSERT INTO s VALUES (?)`, long); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out, err := mustQuery(t, path, "SELECT v FROM s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "…（截断）") {
		t.Fatalf("超长单元格应标注截断: %s", out)
	}
	// 截断后恰好 500 字符 + 后缀。
	var rs struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		t.Fatal(err)
	}
	cell := rs.Rows[0][0]
	if got := len([]rune(strings.TrimSuffix(cell, "…（截断）"))); got != dbQueryCellCap {
		t.Fatalf("截断应到 %d 字符，实际 %d", dbQueryCellCap, got)
	}
}

// TestDBQueryBlob：BLOB 列不吐二进制——报类型与大小。
func TestDBQueryBlob(t *testing.T) {
	path := buildTestDB(t)
	out, err := mustQuery(t, path, "SELECT blob FROM t WHERE name = 'bl'")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "blob 3") {
		t.Fatalf("BLOB 应报大小而不是内容: %s", out)
	}
}

// TestDBQueryTimeout：超时 5s——用一个重查询（递归 CTE 大量生成）验证
// 返回的是超时错误而不是挂死。上限放宽到 20s（慢机器）。
func TestDBQueryTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE anchor (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	start := time.Now()
	_, err = mustQuery(t, path, `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT COUNT(*) FROM c`)
	if err == nil {
		t.Fatal("无界递归查询应超时失败")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("应报超时而不是别的错: %v", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("超时兜底失效（%s）", d)
	}
}

// TestDBQueryRelativePath：相对路径按 ctx 工作目录解析（与其它工具同一条路径）。
func TestDBQueryRelativePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rel.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE r (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO r VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, _ := json.Marshal(map[string]string{"path": "rel.db", "sql": "SELECT v FROM r"})
	ctx := WithWorkDir(context.Background(), dir)
	out, err := execDBQuery(ctx, raw)
	if err != nil {
		t.Fatalf("相对路径应按工作目录解析: %v", err)
	}
	if !strings.Contains(out, "7") {
		t.Fatalf("结果不对: %s", out)
	}
}

// TestDBQueryMissingFile：文件不存在时报错（不是返回空结果——path 打错了
// 要让人看见）。
func TestDBQueryMissingFile(t *testing.T) {
	if _, err := mustQuery(t, filepath.Join(t.TempDir(), "nope.db"), "SELECT 1"); err == nil {
		t.Fatal("库文件不存在应报错")
	}
}

// TestDBQueryRegistered：注册表里有 db_query，RiskLow + Mutates=false。
func TestDBQueryRegistered(t *testing.T) {
	reg := New()
	d, ok := reg.Get(dbQueryToolName)
	if !ok {
		t.Fatal("db_query 未注册")
	}
	if d.Risk != RiskLow {
		t.Fatalf("db_query 应为低危: %v", d.Risk)
	}
	if d.Mutates {
		t.Fatal("db_query 应声明 Mutates=false（strict 只读档可用）")
	}
}

// TestDBQueryRejectMessage：白名单外的前缀给出人话拒绝（含成因说明）。
func TestDBQueryRejectMessage(t *testing.T) {
	if r := dbQueryReject("SELECT 1; UPDATE t SET x=1"); r == "" {
		t.Fatal("应拒多语句")
	}
	if r := dbQueryReject("INSERT INTO t VALUES (1)"); !strings.Contains(r, "SELECT") {
		t.Fatalf("拒绝理由应说明白名单: %q", r)
	}
}

// 保证 os 引用（测试文件里的 TempDir 已用）——防止误删 import 后的占位。
var _ = os.Getenv
