// 内置 Agent 定义的 hash 热更新（2026-10）的验收用例：
//   - 新库建库 → 种子化 + hash 写入；
//   - 改 seed 的 Prompt → 模拟老库（seed_hash 空、user_modified=0）→ 重开库 →
//     库中为新 Prompt + 新 hash；
//   - user_modified=1 → 不更新（连 top-up 之外的整条更新也跳过）；
//   - user_modified=0 且 seed 未变 → 不动库（updated_at 原样）；
//   - AgentEditor 保存（store.UpdateAgent）→ user_modified 置 1；
//   - merger 的 ask_user 白名单 top-up 场景仍生效（user_modified=1 行按基线补齐）。
package store

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// agentColOf 读一行 Agent 的某个文本列（测试直查库，绕过 ListAgents 的全量读）。
func agentColOf(t *testing.T, s *Store, id, col string) string {
	t.Helper()
	var v string
	if err := s.db.QueryRow(`SELECT `+col+` FROM agents WHERE id = ?`, id).Scan(&v); err != nil {
		t.Fatalf("读 agents.%s（%s）失败: %v", col, id, err)
	}
	return v
}

// agentIntColOf 读一行 Agent 的某个整数列。
func agentIntColOf(t *testing.T, s *Store, id, col string) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow(`SELECT `+col+` FROM agents WHERE id = ?`, id).Scan(&v); err != nil {
		t.Fatalf("读 agents.%s（%s）失败: %v", col, id, err)
	}
	return v
}

// seedAgentOf 按 id 取种子定义。
func seedAgentOf(id string) sessiondata.AgentDef {
	for _, s := range seedAgents {
		if s.ID == id {
			return s
		}
	}
	panic("种子里没有 " + id)
}

// TestSeedHashWrittenOnFreshDB：新库建库 → 每个种子 Agent 的 seed_hash 非空、
// user_modified=0，且 hash 与当前种子定义一致。
func TestSeedHashWrittenOnFreshDB(t *testing.T) {
	s := openAt(t, t.TempDir())
	for _, seed := range seedAgents {
		if got := agentColOf(t, s, seed.ID, "seed_hash"); got != seedHashOf(seed) {
			t.Fatalf("种子 Agent %s 的 seed_hash 应为当前定义的 hash: got %q want %q", seed.ID, got, seedHashOf(seed))
		}
		if got := agentIntColOf(t, s, seed.ID, "user_modified"); got != 0 {
			t.Fatalf("种子 Agent %s 的 user_modified 应为 0: %d", seed.ID, got)
		}
	}
}

// TestSeedHotUpdateFlushesPrompt：模拟老库——merger 行是旧版提示词、seed_hash 为空、
// user_modified=0 → 重开库触发热更新 → 行变成当前 seed 的 Prompt + 新 hash。
func TestSeedHotUpdateFlushesPrompt(t *testing.T) {
	dir := t.TempDir()
	s1 := openAt(t, dir)
	if _, err := s1.db.Exec(`UPDATE agents SET prompt = '旧版提示词', seed_hash = '' WHERE id = 'merger'`); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close() // 显式关掉，Windows 上不关会挡 TempDir 清理

	s2 := openAt(t, dir)
	seed := seedAgentOf("merger")
	if got := agentColOf(t, s2, "merger", "prompt"); got != seed.Prompt {
		t.Fatalf("热更新应把旧 Prompt 刷成当前 seed 版本:\n got %q\nwant %q", got, seed.Prompt)
	}
	if got := agentColOf(t, s2, "merger", "seed_hash"); got != seedHashOf(seed) {
		t.Fatalf("热更新后应写入新 hash: got %q want %q", got, seedHashOf(seed))
	}
	if got := agentIntColOf(t, s2, "merger", "user_modified"); got != 0 {
		t.Fatalf("系统热更新不得置位 user_modified: %d", got)
	}
}

// TestSeedHotUpdateRespectsUserModified：user_modified=1 的行不更新（提示词保持
// 用户改过的样子，hash 也不动）。
func TestSeedHotUpdateRespectsUserModified(t *testing.T) {
	dir := t.TempDir()
	s1 := openAt(t, dir)
	if _, err := s1.db.Exec(`UPDATE agents SET prompt = '用户自己的提示词', user_modified = 1, seed_hash = 'sha256:old' WHERE id = 'coder'`); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()

	s2 := openAt(t, dir)
	if got := agentColOf(t, s2, "coder", "prompt"); got != "用户自己的提示词" {
		t.Fatalf("user_modified=1 的行不得被热更新: %q", got)
	}
	if got := agentColOf(t, s2, "coder", "seed_hash"); got != "sha256:old" {
		t.Fatalf("user_modified=1 的行 hash 也不该被碰: %q", got)
	}
}

// TestSeedHotUpdateNoWriteWhenUnchanged：user_modified=0 且内容与当前种子一致
// （老行第一次遇到 hash 列）→ 只补 hash，不伪造更新（updated_at 原样）。
func TestSeedHotUpdateNoWriteWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	s1 := openAt(t, dir)
	// 清掉 hash 但内容保持当前 seed：模拟「hash 列上线前的老库、内容已是最新」
	if _, err := s1.db.Exec(`UPDATE agents SET seed_hash = '' WHERE id = 'tester'`); err != nil {
		t.Fatal(err)
	}
	var updatedAt string
	if err := s1.db.QueryRow(`SELECT updated_at FROM agents WHERE id = 'tester'`).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()

	s2 := openAt(t, dir)
	if got := agentColOf(t, s2, "tester", "seed_hash"); got != seedHashOf(seedAgentOf("tester")) {
		t.Fatalf("应补上 hash: %q", got)
	}
	if got := agentColOf(t, s2, "tester", "updated_at"); got != updatedAt {
		t.Fatalf("内容一致时不得产生内容写（updated_at 应原样）: got %q want %q", got, updatedAt)
	}
}

// TestUpdateAgentMarksUserModified：AgentEditor 保存（协议 agent.update →
// store.UpdateAgent）→ user_modified 置 1；且之后热更新跳过该行。
func TestUpdateAgentMarksUserModified(t *testing.T) {
	dir := t.TempDir()
	s1 := openAt(t, dir)
	agents, err := s1.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	var coder sessiondata.AgentDef
	for _, a := range agents {
		if a.ID == "coder" {
			coder = a
		}
	}
	if coder.ID == "" {
		t.Fatal("找不到种子 coder")
	}
	coder.Prompt = "用户在 AgentEditor 里改过的提示词"
	if err := s1.UpdateAgent(coder); err != nil {
		t.Fatal(err)
	}
	if got := agentIntColOf(t, s1, "coder", "user_modified"); got != 1 {
		t.Fatalf("用户侧保存应置位 user_modified: %d", got)
	}
	_ = s1.Close()

	// 重开库：热更新不得把用户的提示词刷回去
	s2 := openAt(t, dir)
	if got := agentColOf(t, s2, "coder", "prompt"); got != "用户在 AgentEditor 里改过的提示词" {
		t.Fatalf("用户自定义提示词不得被热更新覆盖: %q", got)
	}
}

// TestSeedTopUpStillAppliesToUserModified：merger 的 ask_user 白名单 top-up 场景
// 仍生效——user_modified=1（用户改过提示词）但白名单仍是上一版基线 → 新工具照常补上。
func TestSeedTopUpStillAppliesToUserModified(t *testing.T) {
	dir := t.TempDir()
	s1 := openAt(t, dir)
	// 造老库形态：merger 白名单退回上一版基线（无 ask_user）、描述为上一版、
	// 用户改过提示词（user_modified=1）
	if _, err := s1.db.Exec(`UPDATE agents SET tools = ?, desc = ?, user_modified = 1, seed_hash = '' WHERE id = 'merger'`,
		`["read_file","search","edit","write_file","bash"]`, "把会话分支的改动汇总到集成分支：处理冲突、跑构建测试，如实报告结果。"); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()

	s2 := openAt(t, dir)
	var tools []string
	if err := json.Unmarshal([]byte(agentColOf(t, s2, "merger", "tools")), &tools); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tools, "ask_user") {
		t.Fatalf("user_modified=1 的行白名单仍应按基线补齐新工具: %v", tools)
	}
	// 描述同理按基线补齐到当前 seed 版本
	seed := seedAgentOf("merger")
	if got := agentColOf(t, s2, "merger", "desc"); got != seed.Desc {
		t.Fatalf("user_modified=1 的行描述仍应按基线补齐:\n got %q\nwant %q", got, seed.Desc)
	}
	// 但提示词保持用户自定义（整条热更新跳过）
	if got := agentColOf(t, s2, "merger", "prompt"); strings.Contains(got, "ask_user") && got != seed.Prompt {
		t.Fatalf("提示词不应被整条热更新碰: %q", got)
	}
}

// TestSeedHotUpdateToolWhitelist：改 seed 的工具白名单 → user_modified=0 的老行
// 整条跟上（hash 热更新覆盖面比 top-up 大：不止加工具，是整条对齐当前 seed）。
func TestSeedHotUpdateToolWhitelist(t *testing.T) {
	dir := t.TempDir()
	s1 := openAt(t, dir)
	if _, err := s1.db.Exec(`UPDATE agents SET tools = '["read_file","search"]', seed_hash = '' WHERE id = 'researcher'`); err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()

	s2 := openAt(t, dir)
	seed := seedAgentOf("researcher")
	var tools []string
	if err := json.Unmarshal([]byte(agentColOf(t, s2, "researcher", "tools")), &tools); err != nil {
		t.Fatal(err)
	}
	for _, want := range seed.Tools {
		if !slices.Contains(tools, want) {
			t.Fatalf("整条热更新后白名单应对齐当前 seed: got %v want 含 %v", tools, want)
		}
	}
}
