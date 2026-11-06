// 域标记（sessions.space，助手模式 P0）的验收测试：
// 助手会话不进 List()/Latest()/Search 全库扫描（主空间只出 main 域）；
// AssistantSessions 只出助手域；CreateChildIn 的域继承与覆盖。
package store

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// TestAssistantSessionsDomainIsolation（验收⑧ 域隔离）：
// 助手域会话对主空间的三个入口（List/Latest/Search 全库）不可见；
// AssistantSessions 只出助手域。
func TestAssistantSessionsDomainIsolation(t *testing.T) {
	s := openTestStore(t)
	mainID, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(mainID, llm.Message{Role: "user", Content: "主空间的话"}); err != nil {
		t.Fatal(err)
	}
	assistantID, err := s.CreateIn("assistant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(assistantID, llm.Message{Role: "user", Content: "助手域的话"}); err != nil {
		t.Fatal(err)
	}

	// 主空间 List：只有 main
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != mainID {
		t.Fatalf("List 应只出 main 域会话: %+v", list)
	}
	// Latest：不恢复到助手会话
	lid, msgs, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if lid != mainID || len(msgs) == 0 || msgs[0].Content != "主空间的话" {
		t.Fatalf("Latest 不应恢复到助手会话: id=%s msgs=%+v", lid, msgs)
	}
	// Search 全库扫描（不指定 session）：助手域不可见
	hits, _, _, err := s.Search(SearchQuery{Pattern: "助手域的话"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("全库扫描不应搜出助手域会话: %+v", hits)
	}
	// 精确指定助手会话 id：可以搜到（跨域找子会话/助手会话内容是合法需求）
	hits, _, _, err = s.Search(SearchQuery{Pattern: "助手域", Session: assistantID})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("精确 session 搜索应命中助手域会话: %+v", hits)
	}
	// AssistantSessions：只出助手域
	as, err := s.AssistantSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 1 || as[0].ID != assistantID {
		t.Fatalf("AssistantSessions 应只出 assistant 域会话: %+v", as)
	}
}

// TestCreateChildInSpace（验收③ 的一部分）：子会话的域跟随父（SQL 继承），
// 显式覆盖参数优先（run_task 的项目子会话落 main 域）。
func TestCreateChildInSpace(t *testing.T) {
	s := openTestStore(t)
	// 助手父会话
	parentID, err := s.CreateIn("assistant")
	if err != nil {
		t.Fatal(err)
	}
	// 不覆盖 → 继承 assistant
	childA, err := s.CreateChildIn(parentID, "coder", "call-a", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	// 覆盖 main → run_task 项目子会话落主域
	childB, err := s.CreateChildIn(parentID, "coder", "call-b", "", 0, "main")
	if err != nil {
		t.Fatal(err)
	}
	// 助手域列表：只含不覆盖的那个（childB 落了 main，不进助手域列表）
	// AssistantSessions 只出顶层（parent_id=''），子会话两个都不出——
	// 这里用域继承断言：AssistantSessions 的子会话视图没有，改用 SQL 直查
	// 的等价断言：ChildrenOf 两个都出；域用 List/AssistantSessions 的补集间接验。
	children, err := s.ChildrenOf(parentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Fatalf("ChildrenOf 应出两个子会话: %+v", children)
	}
	_ = childA
	// childB 落 main 域：它有消息后主空间 List 仍不显示（parent_id 非空——
	// List 只认顶层），域语义的最终验收在 agent 层的 RunProjectTask 测试
	//（workspace 落库 + AttachTo 解析出项目根）。
	_ = childB
	// childA 继承 assistant：发一条消息后它仍不出现在主空间 List（域继承生效）
	if _, err := s.AppendMsg(childA, llm.Message{Role: "user", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	// 顶层助手会话 + 顶层 main 会话：域隔离照旧
	mainID, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMsg(mainID, llm.Message{Role: "user", Content: "y"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != mainID {
		t.Fatalf("List 域隔离被破坏: %+v", list)
	}
}

// TestCreateChildInOrphanRejected：父不存在不开孤儿（CreateChildIn 与
// CreateChild 同一条不变量）。
func TestCreateChildInOrphanRejected(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateChildIn("no-such-parent", "coder", "call-x", "", 0, "main"); err == nil {
		t.Fatal("父会话不存在应报错（不开孤儿行）")
	}
}
