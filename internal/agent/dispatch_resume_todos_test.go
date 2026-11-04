// dispatch_resume_todos_test.go —— todo 跨派发残留修复（2026-12 用户拍板）的内核钉子：
//
//   ① 主根因（续跑残留清单）：续跑（agent_dispatch 的 session 参数）复用同一子会话
//      id，AttachTo 会把上一轮定格清单恢复进 child.todos（UI 顶着「全部完成」显示、
//      模型被旧清单误导）。修复后：主 Agent 没带新清单（call.Todos 空）且库里确有
//      旧清单 → SaveTodos(childID, nil) 清库归零；
//   ② 模型侧信息不丢：续跑没带新清单时，composeTaskMessage 把上一轮定格状态写进
//      任务消息末尾（[上一轮清单状态] N/M 完成…，docs/todo-contract.md §2.3）；
//      带新初稿时不附旧状态（初稿就是最新意图）；
//   ③ 续跑归属校验：call.Session 只允许是**本会话**的子会话（st.SessionParentID ==
//      本会话 id）——堵「跨会话续跑污染别人的行」；无存储（st==nil）跳过校验。
//
// 全部走真实入口 openChildSession / runDispatch（与生产同一条路），不测私有副本。
package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestResumeClearsStaleTodos：续跑没带新清单 → 子会话行的旧清单被清掉。
func TestResumeClearsStaleTodos(t *testing.T) {
	env, st := newChildSessionEnv(t)
	// 先建父会话行（CreateChild 要有父可挂）
	env.s.SetStream((&fakeStream{script: [][]llm.StreamEvent{textResult("好")}}).stream)
	if err := env.s.Send("先建父会话"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)

	// 模拟上一轮派发的残留：子会话行带一份定格清单
	childID, err := st.CreateChild(env.s.SessionID(), "coder", "call-prev", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	stale := []sessiondata.TodoItem{{Content: "旧任务", Status: "done"}}
	if err := st.SaveTodos(childID, stale); err != nil {
		t.Fatal(err)
	}

	// 续跑（call.Session 非空、不带新 todos）——走真实入口（OwnerSessionID 按
	// 生产路径由 runTools 填：发起调度的会话 id）。
	call := tools.DispatchCall{Agent: "coder", Task: "接着干", Session: childID, DispatchID: "call-next", OwnerSessionID: env.s.SessionID()}
	child, gotID, _, err := env.s.openChildSession(call, &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotID != childID {
		t.Fatalf("续跑应附着同一子会话 id: %s", gotID)
	}
	// 库里归零
	items, ok, err := st.TodosOf(childID)
	if err != nil {
		t.Fatal(err)
	}
	if ok && len(items) > 0 {
		t.Fatalf("续跑没带新清单时旧清单应清库归零，got %+v", items)
	}
	// 内存也归零（AttachTo 先恢复了旧清单，清库必须同步清内存——否则这一轮的
	// 快照注入还在给模型注入旧清单）
	if got := child.Todos(); len(got) != 0 {
		t.Fatalf("子会话内存里的残留清单应一并清掉，got %+v", got)
	}
}

// TestResumeTaskMessageCarriesPrevTodos：续跑没带新清单时，任务消息末尾带上一轮
// 定格清单的状态（模型侧信息不丢——库清了，文本还在）。
func TestResumeTaskMessageCarriesPrevTodos(t *testing.T) {
	prev := []tools.TodoItem{
		{Content: "定位泄漏点", Status: "done"},
		{Content: "补集成测试", Status: "active"},
	}
	call := tools.DispatchCall{Agent: "coder", Task: "接着干", Session: "child-1"}
	msg := composeTaskMessage(call, prev)
	if !strings.Contains(msg, "[上一轮清单状态]") ||
		!strings.Contains(msg, "1/2 完成") ||
		!strings.Contains(msg, "补集成测试（进行中）") {
		t.Fatalf("续跑任务消息应带上一轮清单状态: %q", msg)
	}
	// 带新初稿时不附旧状态（初稿是最新意图，附旧状态反而混乱）
	call.Todos = []tools.TodoItem{{Content: "新清单项", Status: "pending"}}
	msg = composeTaskMessage(call, prev)
	if strings.Contains(msg, "上一轮清单状态") || strings.Contains(msg, "定位泄漏点") {
		t.Fatalf("带新初稿的续跑不该附旧清单状态: %q", msg)
	}
	// 非续跑（新派发）不附
	call.Session = ""
	msg = composeTaskMessage(call, prev)
	if strings.Contains(msg, "上一轮清单状态") {
		t.Fatalf("非续跑的任务消息不该出现上一轮清单状态: %q", msg)
	}
}

// TestResumeRejectsForeignChildID：续跑 id 归属校验——顶层会话 id 与不存在的
// id 不能从这里续跑，错误文案引导模型去掉 session 参数新开。
func TestResumeRejectsForeignChildID(t *testing.T) {
	env, st := newChildSessionEnv(t)
	env.s.SetStream((&fakeStream{script: [][]llm.StreamEvent{textResult("好")}}).stream)
	if err := env.s.Send("先建父会话"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)
	parentID := env.s.SessionID()
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder"}}

	// 不存在的 id：拒绝并说明
	if _, _, _, err := env.s.openChildSession(tools.DispatchCall{Agent: "coder", Task: "t", Session: "someone-else", DispatchID: "call-y", OwnerSessionID: parentID}, ac); err == nil {
		t.Fatal("续跑不存在的会话 id 应被拒绝")
	} else if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("错误应说明 id 不存在: %v", err)
	}
	// 顶层会话 id（parent_id = ""）：同样不是「本会话的子会话」——校验按
	// parent_id 判等，不放过任何「id 存在但不归本会话」的输入（跨会话 id、
	// 顶层 id 同一条防线；错误文案引导模型纠正）。
	if _, _, _, err := env.s.openChildSession(tools.DispatchCall{Agent: "coder", Task: "t", Session: parentID, DispatchID: "call-y", OwnerSessionID: parentID}, ac); err == nil {
		t.Fatal("续跑顶层会话 id 应被拒绝")
	} else if !strings.Contains(err.Error(), "不是本会话的子会话") {
		t.Fatalf("错误文案应引导模型纠正: %v", err)
	}

	// 自己的子会话：放行（合法路径不被误伤）
	mine, err := st.CreateChild(parentID, "coder", "call-mine", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, got, _, err := env.s.openChildSession(tools.DispatchCall{Agent: "coder", Task: "t", Session: mine, DispatchID: "call-y", OwnerSessionID: parentID}, ac); err != nil {
		t.Fatalf("续跑自己的子会话应放行: %v", err)
	} else if got != mine {
		t.Fatalf("应附着同一个 id: %s", got)
	}
}

// TestResumeNoStoreSkipsOwnershipCheck：无存储模式（st==nil）退化为内存子会话，
// 归属校验跳过（没有库可查），call.Session 直接忽略返回内存会话（id 为空）。
func TestResumeNoStoreSkipsOwnershipCheck(t *testing.T) {
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(reg, tools.New(), nil)
	t.Cleanup(s.Close)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"coder": {Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true}},
	}})
	child, childID, _, err := s.openChildSession(
		tools.DispatchCall{Agent: "coder", Task: "t", Session: "some-id", DispatchID: "call-z"},
		&sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder"}})
	if err != nil {
		t.Fatalf("无存储模式不应拒绝续跑调用（校验跳过）: %v", err)
	}
	if childID != "" {
		t.Fatalf("无存储子会话没有 id: %q", childID)
	}
	if child == nil {
		t.Fatal("应返回内存子会话")
	}
}
