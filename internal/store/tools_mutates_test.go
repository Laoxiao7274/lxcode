// Mutates 声明（2026-12 P5）的持久化契约：随目录条目落库、老库幂等 ALTER
// （默认 1 = 未声明按变更处理）、种子同步给 rg 写 0，用户改过的行不被种子
// 回填吃掉。
package store

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func toolByID(t *testing.T, s *Store, id string) sessiondata.ToolSpec {
	t.Helper()
	list, err := s.ListTools()
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range list {
		if tl.ID == id {
			return tl
		}
	}
	t.Fatalf("目录里没有工具 %s", id)
	return sessiondata.ToolSpec{}
}

// TestSeedRipgrepDeclaredReadonly：种子里 rg 声明 Mutates=false，落库后读回
// 仍是 false（strict 只读档据此放行）。
func TestSeedRipgrepDeclaredReadonly(t *testing.T) {
	s := openTestStore(t)
	rg := toolByID(t, s, "ripgrep")
	if rg.Mutates == nil || *rg.Mutates {
		t.Fatalf("ripgrep 应声明 Mutates=false（纯检索不改文件），读到: %v", rg.Mutates)
	}
	// 未声明 Mutates 的条目读回 nil（如 read_file——内置工具的 Mutates 在
	// 注册表里写死，目录不参与判定）
	rf := toolByID(t, s, "read_file")
	if rf.Mutates != nil {
		t.Fatalf("未声明 Mutates 的条目应读回 nil（=按变更处理），读到: %v", rf.Mutates)
	}
}

// TestMutatesPersistsThroughUpdate：用户在拓展页改条目（UpdateTool）时声明
// 随行落库——false 不被写回 true。
func TestMutatesPersistsThroughUpdate(t *testing.T) {
	s := openTestStore(t)
	spec := sessiondata.ToolSpec{
		ID: "probe-ro", Desc: "只读探针", Risk: "low", Source: "binary", Custom: true,
		Command: "probe {pattern}",
		Params:  []sessiondata.ToolParam{{Name: "pattern", Type: "string", Required: true}},
	}
	f := false
	spec.Mutates = &f
	if err := s.AddTool(spec); err != nil {
		t.Fatal(err)
	}
	if got := toolByID(t, s, "probe-ro"); got.Mutates == nil || *got.Mutates {
		t.Fatal("AddTool 后 Mutates=false 应落库")
	}
	// 更新（不改 Mutates 字段）：声明保持
	spec.Desc = "改了说明"
	if err := s.UpdateTool(spec); err != nil {
		t.Fatal(err)
	}
	if got := toolByID(t, s, "probe-ro"); got.Mutates == nil || *got.Mutates {
		t.Fatal("UpdateTool 后 Mutates=false 不应被重置")
	}
	// 用户改回 true：写路径尊重显式声明。列只存两态（0=声明只读 / 1=其余），
	// 显式 true 与 nil 落库同值——判定语义相同（都按变更处理），指针回 nil 是
	// 有损但无害的归一。
	tr := true
	spec.Mutates = &tr
	if err := s.UpdateTool(spec); err != nil {
		t.Fatal(err)
	}
	if got := toolByID(t, s, "probe-ro"); got.Mutates != nil && !*got.Mutates {
		t.Fatal("显式 Mutates=true 落库后不得变成「声明只读」")
	}
	// 清空声明（nil）：与显式 true 同语义（按变更处理）
	spec.Mutates = nil
	if err := s.UpdateTool(spec); err != nil {
		t.Fatal(err)
	}
	if got := toolByID(t, s, "probe-ro"); got.Mutates != nil && !*got.Mutates {
		t.Fatal("nil 声明回落默认（按变更处理），不得变成「声明只读」")
	}
}

// TestMutatesOldDBMigration：老库（无 mutates 列）打开时幂等 ALTER，默认 1；
// 存量 rg 行被种子同步翻成 0（user_modified=0 的种子行按代码更新全字段）。
func TestMutatesOldDBMigration(t *testing.T) {
	s := openTestStore(t)
	// 模拟老库：删掉列（SQLite 3.35+ 支持 DROP COLUMN；modernc 支持）
	if _, err := s.db.Exec(`CREATE TABLE tools_old_mig AS SELECT id, desc, risk, source, params, doc, server, command, example, package_file, custom, created_at, updated_at FROM tools`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE tools`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE tools_old_mig RENAME TO tools`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// 重开：ALTER 补列 + 种子同步（user_modified=0 的 rg 行按代码更新全字段）
	s2, err := Open(s.dir)
	if err != nil {
		t.Fatalf("老库（无 mutates 列）打开应成功: %v", err)
	}
	defer s2.Close()
	rg := toolByID(t, s2, "ripgrep")
	if rg.Mutates == nil || *rg.Mutates {
		t.Fatalf("老库升级后 rg 的 Mutates 声明应经种子同步落库为 false，读到: %v", rg.Mutates)
	}
}

// TestMutatesUserModifiedRowRespected：用户改过的条目（custom=1）不被种子
// 回填吃掉——用户把 rg 的声明改回 true 后，种子同步不碰它。
func TestMutatesUserModifiedRowRespected(t *testing.T) {
	s := openTestStore(t)
	// 模拟用户编辑：custom=1 且 Mutates=true
	if _, err := s.db.Exec(`UPDATE tools SET mutates=1, custom=1 WHERE id='ripgrep'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	rg := toolByID(t, s2, "ripgrep")
	if rg.Mutates != nil && !*rg.Mutates {
		t.Fatal("用户改过的行（custom=1）不被种子同步覆盖——声明保持用户改的（非只读）")
	}
}
