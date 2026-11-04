// dbquery.go — db_query：对指定 SQLite 文件的**只读**查询工具（P8-1）。
//
// 为什么需要（2026-12 用户反馈 + 真库数据）：调研 Agent 用 bash_readonly 跑
// sqlite3 命令 763 次，且 readonly.go 的 SQL 关键字判据把 replace() 这类纯函数
// 误拒了 23 次（`replace(content,'a','b')` 命中写关键字 replace——那是文本函数，
// 不是 upsert）。独立的 db_query 把「查库」做成结构化工具：
//
//   - **只读是结构性保证**（不是靠 SQL 校验）：连接串 mode=ro——驱动层拒绝一切
//     写操作，INSERT/UPDATE/DELETE 在 sqlite 连接层直接报错（readonly attempt），
//     单测钉住这条。SQL 前缀白名单只是**快速失败**的用户体验层（提前回人话）。
//   - 参数就两个：path（库文件绝对/相对路径）+ sql（单条查询）。没有点命令、
//     没有输出格式开关——那些是 sqlite3 CLI 的概念，结构化结果不需要。
//
// 安全纪律（全部硬性）：
//   1. 单条语句：除结尾一个分号外不允许出现分号（多语句拒——mode=ro 下第二条
//      写语句也会被驱动拒，但读语句的多条拼接同样不该放：一个工具调用 = 一个意图）；
//   2. 前缀白名单：只允许 SELECT / EXPLAIN / WITH 开头（PRAGMA 从简不放开——
//      现在没人需要它，放开了就得多写一整张只读 PRAGMA 白名单表）；
//   3. mode=ro 打开（上面的结构性保证）；
//   4. 行数上限 100、每单元格截断 500 字符（上下文纪律：全表 dump 灌爆历史）；
//   5. 查询超时 5s（context——大表全扫不该挂住工具循环）。
package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动（与 store 同一份；只读模式走连接串）
)

// dbQueryToolName 是 db_query 工具名（单处定义，种子目录引用同一字面量）。
const dbQueryToolName = "db_query"

// dbQuery 行数/单元格上限与超时。
const (
	dbQueryMaxRows     = 100
	dbQueryCellCap     = 500
	dbQueryMaxCols     = 40
	dbQueryTimeout     = 5 * time.Second
	dbQueryDSNTimeout  = 10 * time.Second // 连接建立与整体执行的兜底上限
	dbQueryMaxSQLBytes = 64 * 1024        // 单条 SQL 的字节上限（防御性）
)

// dbQueryDef 构造 db_query 工具。
//
// RiskLow + Mutates=false：只读连接是驱动层保证，strict 只读档照常可用
//（这正是调研 Agent 需要它的形态——每查一次库弹一次确认等于不可用）。
func dbQueryDef() *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "SQLite 数据库文件路径（相对会话工作目录或绝对路径）"},
			"sql": {"type": "string", "description": "单条查询语句：只允许 SELECT / EXPLAIN / WITH 开头（结尾一个分号可有）"}
		},
		"required": ["path", "sql"]
	}`)
	return &Def{
		Name: dbQueryToolName,
		Description: "对指定 SQLite 数据库文件执行**只读**查询（单条 SELECT/EXPLAIN/WITH）。" +
			"连接以只读模式打开，任何写操作在驱动层被拒绝。" +
			"适合查数据、统计、看表结构之外的查询；改库请交给代码 Agent（或明确告知用户）。",
		Parameters: schema,
		Risk:       RiskLow,
		Mutates:    false,
		Exec:       execDBQuery,
	}
}

// execDBQuery 执行一次只读查询并渲染成文本。
//
// 返回 JSON（列名 + 行数组），而不是表格文本：模型读 JSON 不存在对齐歧义，
// 且截断单元格时用「…（截断）」后缀明确标注。
func execDBQuery(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
		SQL  string `json:"sql"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	path := strings.TrimSpace(a.Path)
	if path == "" {
		return "", fmt.Errorf("path 不能为空（要查询的 SQLite 数据库文件路径）")
	}
	sqlText := strings.TrimSpace(a.SQL)
	if sqlText == "" {
		return "", fmt.Errorf("sql 不能为空")
	}
	if len(sqlText) > dbQueryMaxSQLBytes {
		return "", fmt.Errorf("sql 过长（%d 字节，上限 %d）——拆成多次查询", len(sqlText), dbQueryMaxSQLBytes)
	}
	// 单条语句：除结尾一个分号外不允许分号。注释里的分号同样拒绝（从简：
	// -- 注释、/* */ 里的分号静态判定有歧义面，查询语句里不需要注释）。
	if reason := dbQueryReject(sqlText); reason != "" {
		return "", fmt.Errorf("%s", reason)
	}
	// 整体兜底 10s（ctx 没有截止时补上；调用方给了更紧的就以它为准）。
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dbQueryDSNTimeout)
		defer cancel()
	}
	// mode=ro：驱动层只读——这是**硬保证**。immutable=0：文件可能被并发写
	//（我们的会话库就是 WAL），不声明不可变。_time_format=sqlite 兜底时间格式。
	dsn := "file:" + filepath.ToSlash(resolveToolPath(ctx, path)) + "?mode=ro&immutable=0&_time_format=sqlite"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", fmt.Errorf("打开数据库失败: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// 查询超时 5s：context 贯穿 QueryContext——超时后驱动中断语句。
	qctx, cancel := context.WithTimeout(ctx, dbQueryTimeout)
	defer cancel()
	rows, err := db.QueryContext(qctx, sqlText)
	if err != nil {
		// 超时单独点出（模型要知道这是「查询太重」而不是「SQL 写错」）。
		if qctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("查询超时（%s）：查询太重——加 WHERE/LIMIT 收窄，或先 EXPLAIN 看计划", dbQueryTimeout)
		}
		return "", fmt.Errorf("查询失败: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("读取列名失败: %v", err)
	}
	// 列数上限（SELECT * 的宽表一次吐 200 列会灌爆结果）。
	if len(cols) > dbQueryMaxCols {
		return "", fmt.Errorf("结果有 %d 列（上限 %d）：请指定需要的列名", len(cols), dbQueryMaxCols)
	}
	type rowSet struct {
		Columns []string          `json:"columns"`
		Rows    [][]string        `json:"rows"`
		Truncated bool            `json:"rows_truncated,omitempty"`
	}
	rs := rowSet{Columns: cols}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if len(rs.Rows) >= dbQueryMaxRows {
			rs.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", fmt.Errorf("读取行失败: %v", err)
		}
		out := make([]string, len(cols))
		for i, v := range vals {
			out[i] = dbQueryCell(v)
		}
		rs.Rows = append(rs.Rows, out)
	}
	if err := rows.Err(); err != nil {
		// 已拿到的行照常返回（附错误说明）——半截结果比没有结果有用。
		data, _ := json.Marshal(rs)
		return fmt.Sprintf("查询在返回 %d 行后中断: %v\n已获得的部分结果:\n%s", len(rs.Rows), err, data), nil
	}
	data, err := json.Marshal(rs)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %v", err)
	}
	return string(data), nil
}

// dbQueryReject 是 SQL 前缀白名单与单条语句校验（快速失败层——真正的写拒绝
// 在驱动层，mode=ro）。
func dbQueryReject(sqlText string) string {
	// 单条语句：剥掉结尾分号后不得再出现分号。
	body := strings.TrimRight(sqlText, " \t\r\n;")
	if strings.Contains(body, ";") {
		return "sql 只允许单条语句（多余的 \";\"——一次调用一个意图，多条查询请分开调用）"
	}
	// 前缀白名单：SELECT / EXPLAIN / WITH（大小写不敏感）。
	head := body
	if i := strings.IndexAny(body, " \t\r\n("); i >= 0 {
		head = body[:i]
	}
	switch strings.ToUpper(head) {
	case "SELECT", "EXPLAIN", "WITH":
		return ""
	case "PRAGMA":
		return "PRAGMA 未开放（db_query 只放行 SELECT/EXPLAIN/WITH）"
	}
	return "sql 只允许 SELECT / EXPLAIN / WITH 开头的查询语句（写入操作在只读连接上本来就会被拒绝）"
}

// dbQueryCell 把一列的值渲染成文本单元格（截断 + 中文安全的 rune 切割）。
//
// 类型分支逐个说明（driver value 的实际取值面）：
//   - []byte：BLOB 列——只报类型与大小，不吐二进制乱码进上下文；
//   - time.Time：时间列按 RFC3339 渲染；
//   - nil：SQL NULL（不是空串——语义不同）；
//   - 其余（int64/float64/string/bool）：fmt 渲染。
func dbQueryCell(v any) string {
	var s string
	switch t := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return fmt.Sprintf("<blob %d 字节>", len(t))
	case time.Time:
		s = t.Format(time.RFC3339)
	default:
		s = fmt.Sprintf("%v", t)
	}
	// 截断到 500 **字符**（不是字节——中文按字符算），越界加后缀标注。
	if utf8.RuneCountInString(s) > dbQueryCellCap {
		r := []rune(s)
		return string(r[:dbQueryCellCap]) + "…（截断）"
	}
	return s
}
