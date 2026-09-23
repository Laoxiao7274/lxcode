package store

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// openAt 在同一个目录上反复开库（迁移测试要"重开 = 跑迁移"）。
// Windows 上句柄不关会挡住 t.TempDir 的清理，所以每个都挂 cleanup。
func openAt(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestMigrateDispatchToolID：老库里的旧工具名（白名单 + 历史两处）在 Open 时
// 自动改成现行名，幂等，且不误伤别的字段。
func TestMigrateDispatchToolID(t *testing.T) {
	dir := t.TempDir()

	// ---- 造一个"老库" ----
	s1 := openAt(t, dir)
	agents, err := s1.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) == 0 || !agents[0].IsMain {
		t.Fatalf("首个应是主 Agent: %+v", agents)
	}
	main := agents[0]
	main.Tools = []string{oldDispatchToolID, "read_file"}
	if err := s1.UpdateAgent(main); err != nil {
		t.Fatal(err)
	}
	// 迁移是**唯一刻意碰用户数据**的地方（种子同步一直避开 custom=1 行）——这条
	// 断言就是那条边界：用户改过的行也必须迁（否则白名单指着不存在的工具）。
	if _, err := s1.db.Exec(`UPDATE agents SET custom = 1 WHERE id = ?`, main.ID); err != nil {
		t.Fatal(err)
	}

	sid, err := s1.Create()
	if err != nil {
		t.Fatal(err)
	}
	var call llm.ToolCall
	call.ID, call.Type = "c1", "function"
	call.Function.Name = oldDispatchToolID
	call.Function.Arguments = `{"agent":"coder","task":"干活"}`
	if err := s1.AppendMsg(sid, llm.Message{Role: "assistant", Content: "派活", ToolCalls: []llm.ToolCall{call}}); err != nil {
		t.Fatal(err)
	}

	// 顺手放一条"正文里出现同名字面量"的消息：JSON 层改写不该动到它
	if err := s1.AppendMsg(sid, llm.Message{Role: "user", Content: "记一下：" + oldDispatchToolID + " 这个名字要改"}); err != nil {
		t.Fatal(err)
	}
	var beforeRaw string
	if err := s1.db.QueryRow(`SELECT tool_calls FROM messages WHERE session_id = ? AND seq = 1`, sid).Scan(&beforeRaw); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// ---- 重开 = 跑迁移 ----
	s2 := openAt(t, dir)

	// 白名单：旧名换成现行名，其余原样
	after, err := s2.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	var gotMain *sessiondata.AgentDef
	for i := range after {
		if after[i].ID == main.ID {
			gotMain = &after[i]
		}
	}
	if gotMain == nil {
		t.Fatal("主 Agent 不见了")
	}
	if slices.Contains(gotMain.Tools, oldDispatchToolID) {
		t.Fatalf("白名单里的旧名没迁掉: %v", gotMain.Tools)
	}
	if !slices.Contains(gotMain.Tools, newDispatchToolID) || !slices.Contains(gotMain.Tools, "read_file") {
		t.Fatalf("白名单应保留原有序与其它工具: %v", gotMain.Tools)
	}
	// 迁移目标必须真的是注册表里的那个名字（否则白名单迁成了另一个悬空 id）
	if !slices.Contains(tools.New().Order(), newDispatchToolID) {
		t.Fatalf("迁移目标 %q 不在工具注册表里", newDispatchToolID)
	}

	// 历史：名字改了，id 与参数原样（配对与回放按 id，不能碰）
	msgs, err := s2.Load(sid)
	if err != nil {
		t.Fatal(err)
	}
	var assistant *llm.Message
	for i := range msgs {
		if len(msgs[i].ToolCalls) > 0 {
			assistant = &msgs[i]
		}
	}
	if assistant == nil {
		t.Fatal("历史里的工具调用不见了")
	}
	got := assistant.ToolCalls[0]
	if got.Function.Name != newDispatchToolID {
		t.Fatalf("历史里的工具名没迁: %q", got.Function.Name)
	}
	if got.ID != "c1" || got.Function.Arguments != `{"agent":"coder","task":"干活"}` {
		t.Fatalf("迁移只该改名字，别的字段不许动: %+v", got)
	}
	// 正文里的同名字面量不该被碰（JSON 层改写而非字符串替换）
	if !strings.Contains(msgs[1].Content, oldDispatchToolID) {
		t.Fatalf("正文被误改了: %q", msgs[1].Content)
	}

	// ---- 幂等：再开一次，行字节完全一样 ----
	var afterRaw string
	if err := s2.db.QueryRow(`SELECT tool_calls FROM messages WHERE session_id = ? AND seq = 1`, sid).Scan(&afterRaw); err != nil {
		t.Fatal(err)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}
	s3 := openAt(t, dir)
	var againRaw string
	if err := s3.db.QueryRow(`SELECT tool_calls FROM messages WHERE session_id = ? AND seq = 1`, sid).Scan(&againRaw); err != nil {
		t.Fatal(err)
	}
	if againRaw != afterRaw {
		t.Fatalf("迁移不幂等: %q → %q", afterRaw, againRaw)
	}
	// 落库形状仍是合法 JSON（别把列写坏）
	if !json.Valid([]byte(againRaw)) {
		t.Fatalf("迁移后 tool_calls 不是合法 JSON: %q", againRaw)
	}
}
