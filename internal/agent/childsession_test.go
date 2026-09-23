// 子会话（2026-09-22 用户拍板：子 Agent = 独立会话）的内核测试：
// 派发开一个自己的会话行 + 自己的历史 + 自己的压缩检查点；续跑附着同一个 id；
// 结论只把最终文本带回主会话（主历史不被子过程污染）。
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// newChildSessionEnv 造"挂了存储"的派发会话：子会话要落库才谈得上续跑。
func newChildSessionEnv(t *testing.T) (*dispatchEnv, *store.Store) {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		ContextWindow: 100000, Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	env := &dispatchEnv{}
	s := New(reg, tools.New(), func(ev Event) {
		env.mu.Lock()
		env.events = append(env.events, ev)
		env.mu.Unlock()
	})
	t.Cleanup(s.Close)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main": {
			Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true,
				Tools: []string{"agent_dispatch"}, Delegates: []string{"coder"}},
			Delegates: []sessiondata.AgentDef{
				{ID: "coder", Name: "代码 Agent", Desc: "写代码", Enabled: true},
			},
		},
		"coder": {
			Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6",
				Tools: []string{"read_file", "edit"}, Approval: "confirm"},
		},
	}})
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	env.s = s
	return env, st
}

// dispatchStream 造"主 Agent 派发一次 + 子会话给出结论"的假流。
// taskJSON 是 agent_dispatch 的参数；subText 是子会话的最终回复。
// mainRounds 控制主轮数（第一轮派发，之后收尾）。
func dispatchStream(t *testing.T, taskJSON, subText string) StreamFn {
	t.Helper()
	mainRound := 0
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		// 主 Agent 语境（种子 main 的调度协议文本）
		if strings.Contains(msgs[0].Content, "调度中枢") {
			mainRound++
			if mainRound > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-d1"}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = taskJSON
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境（种子 coder 的执行协议）
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: subText}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: subText}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

func TestChildSessionIsPersisted(t *testing.T) {
	env, st := newChildSessionEnv(t)
	env.s.SetStream(dispatchStream(t, `{"agent":"coder","task":"跑一遍测试"}`, "子会话结论：全绿。"))

	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)

	kids, err := st.ChildrenOf(env.s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 {
		t.Fatalf("派发应开一个子会话: %+v", kids)
	}
	if kids[0].AgentID != "coder" || kids[0].ParentID != env.s.SessionID() {
		t.Fatalf("子会话应记住父与 Agent: %+v", kids[0])
	}
	// 子会话有自己的历史：任务消息 + 子 Agent 的结论
	msgs, err := st.Load(kids[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Role != "user" || !strings.Contains(msgs[0].Content, "跑一遍测试") {
		t.Fatalf("子会话第一条应是任务消息: %+v", msgs)
	}
	if msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Content, "全绿") {
		t.Fatalf("子会话应有自己的结论: %+v", msgs)
	}
	// 主历史里只有回填的工具结果（子过程不进主历史）
	var toolResults []string
	for _, m := range env.s.History().Messages {
		if m.Role == "tool" {
			toolResults = append(toolResults, m.Content)
		}
	}
	if len(toolResults) != 1 || !strings.Contains(toolResults[0], "全绿") {
		t.Fatalf("主历史应只有回填的结论: %+v", toolResults)
	}
	// 主历史 = user + assistant(派发调用) + tool(结论) + assistant(主 Agent 验收)
	main := env.s.History().Messages
	if len(main) != 4 {
		t.Fatalf("主历史应是 user + assistant(调用) + tool(结论) + assistant(验收): %+v", main)
	}
	if !strings.Contains(main[3].Content, "已验收") {
		t.Fatalf("主轮应继续收尾: %+v", main[3])
	}
}

// 续跑：第二次派发带 session = 上一个子会话 id → 附着同一会话，历史接着长。
func TestDispatchResumesChildSession(t *testing.T) {
	env, st := newChildSessionEnv(t)
	env.s.SetStream(dispatchStream(t, `{"agent":"coder","task":"跑一遍测试"}`, "第一轮结论"))
	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)
	kids, _ := st.ChildrenOf(env.s.SessionID())
	if len(kids) != 1 {
		t.Fatalf("应有 1 个子会话: %+v", kids)
	}
	childID := kids[0].ID

	// 第二轮：显式续跑同一个子会话
	env.s.SetStream(dispatchStream(t,
		`{"agent":"coder","task":"继续补一个用例","session":"`+childID+`"}`, "第二轮结论"))
	if err := env.s.Send("接着补"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)

	kids2, _ := st.ChildrenOf(env.s.SessionID())
	if len(kids2) != 1 {
		t.Fatalf("续跑不应再开新子会话: %+v", kids2)
	}
	msgs, _ := st.Load(childID)
	if len(msgs) != 4 {
		t.Fatalf("续跑应在同一子会话里接着长（任务+结论+追问+结论）: %+v", msgs)
	}
	if !strings.Contains(msgs[2].Content, "继续补一个用例") {
		t.Fatalf("续跑的任务消息应进子会话历史: %q", msgs[2].Content)
	}
}

// S5：子会话跑的是**同一套压缩**——检查点落在子会话自己的行上，
// 父会话的历史与检查点都不受影响（"压缩啥的也是一样的"）。
func TestChildSessionCompactionLandsOnChildRow(t *testing.T) {
	env, st := newChildSessionEnv(t)
	// 先让父会话落一行（行是懒建的：父会话必须先有过消息，子会话才有父可挂）
	env.s.SetStream((&fakeStream{script: [][]llm.StreamEvent{textResult("好")}}).stream)
	if err := env.s.Send("先建父会话"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)
	parentID := env.s.SessionID()
	if parentID == "" {
		t.Fatal("前置条件：父会话应有行")
	}
	childID, err := st.CreateChild(parentID, "coder", "call-x")
	if err != nil {
		t.Fatal(err)
	}
	// 子会话：自己的 Session 附着到自己的行（与 runDispatch 内部同一路径）
	child := New(env.s.reg, env.s.tools, env.s.emit)
	child.SetAgentResolver(env.s.agents)
	if err := child.AttachTo(st, childID); err != nil {
		t.Fatal(err)
	}
	// 造一段够长的历史（压缩要有收益）：走真实路径 child.append（它同时进内存
	// 与库）——库与内存必须一致，不一致时 AppendCheckpoint 会拒绝（那是
	// "落盘落后"的守卫，不是 bug）。
	for _, m := range bigHistory(6) {
		child.append(m)
	}
	child.SetStream(compactionStream(t, nil, nil, summaryText, false))

	res, err := child.Compact("")
	if err != nil {
		t.Fatal(err)
	}
	if res.Shadowed == 0 {
		t.Fatalf("子会话应压缩成功: %+v", res)
	}
	// 检查点行落在**子会话**上（不是父会话）
	childMsgs, err := st.Load(childID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childMsgs) != 2 || !isCheckpointContent(childMsgs[0].Content) {
		t.Fatalf("子会话历史应以检查点开头: %+v", childMsgs)
	}
	// 父会话没有被这次压缩碰到（它自己的行还没建，Load 应报"不存在"）
	if _, err := st.Load(parentID); err == nil {
		// 父会话可能已经有行（EnablePersistence 建的）——那就断言它没有检查点
		pmsgs, _ := st.Load(parentID)
		for _, m := range pmsgs {
			if isCheckpointContent(m.Content) {
				t.Fatal("子会话的压缩不该写进父会话")
			}
		}
	}
	// 子会话的占用测量跟着更新（子会话有自己的窗口）
	if child.ContextUsage().Used == 0 {
		t.Fatal("子会话压缩后应更新自己的占用测量")
	}
}

// 事件归属：子会话的增量带 dispatch_id（前端挂卡内）；清单与忙闲不上抛。
func TestChildSessionEventAttribution(t *testing.T) {
	env, _ := newChildSessionEnv(t)
	env.s.SetStream(dispatchStream(t, `{"agent":"coder","task":"跑一遍测试"}`, "子结论"))
	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)

	var sawSubDelta, sawSubTodo bool
	var busyTrue, busyFalse int
	for _, ev := range env.snapshot() {
		switch e := ev.(type) {
		case DeltaEvent:
			if e.DispatchID != "" {
				sawSubDelta = true
			}
		case TodoUpdatedEvent:
			if len(e.Items) > 0 {
				sawSubTodo = true
			}
		case BusyEvent:
			if e.Busy {
				busyTrue++
			} else {
				busyFalse++
			}
		}
	}
	if !sawSubDelta {
		t.Fatal("子会话的增量应带上 dispatch_id 上抛（前端挂卡内）")
	}
	if sawSubTodo {
		t.Fatal("子会话的清单不该上抛（清单归子会话自己）")
	}
	if busyTrue != 1 || busyFalse != 1 {
		t.Fatalf("busy 只应来自主轮（1 真 1 假），实际 true=%d false=%d", busyTrue, busyFalse)
	}
}

// 子会话压缩保护任务说明书：openChildSession 是开子会话的唯一入口（新建与续跑同一条
// 路），它置位 protectHead——压缩后历史第 0 条仍是派发的那条任务原文，摘要落在它之后；
// 库里回放同一顺序（检查点按影子锚点插回被替换段的位置，不是一律排最前）。
func TestChildSessionCompactionKeepsTaskBrief(t *testing.T) {
	env, st := newChildSessionEnv(t)
	// 先让父会话落一行（行是懒建的：父会话必须先有过消息，子会话才有父可挂）
	env.s.SetStream((&fakeStream{script: [][]llm.StreamEvent{textResult("好")}}).stream)
	if err := env.s.Send("先建父会话"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)

	// 走真实入口开子会话（runDispatch 用的就是它）
	call := tools.DispatchCall{Agent: "coder", Task: "跑一遍测试", DispatchID: "call-x"}
	child, childID, err := env.s.openChildSession(call,
		&sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder"}})
	if err != nil {
		t.Fatal(err)
	}
	if childID == "" {
		t.Fatal("前置条件：子会话应落库")
	}
	if !child.protectHead {
		t.Fatal("子会话应保护头部任务说明书（openChildSession 置位）")
	}
	// 历史第 0 条 = 派发的任务说明书；其后是子会话自己跑出来的长历史
	task := composeTaskMessage(call)
	child.append(llm.Message{Role: "user", Content: task})
	for _, m := range bigHistory(6) {
		child.append(m)
	}
	child.SetStream(compactionStream(t, nil, nil, summaryText, false))

	res, err := child.Compact("")
	if err != nil {
		t.Fatal(err)
	}
	if res.Shadowed != 5 {
		t.Fatalf("被压的应是任务消息之后的 5 条（任务说明书不参与，尾部留一条）: %+v", res)
	}
	msgs := child.History().Messages
	if len(msgs) != 3 || msgs[0].Content != task {
		t.Fatalf("任务说明书必须留在历史首条: %+v", msgs)
	}
	if !isCheckpointContent(msgs[1].Content) {
		t.Fatalf("摘要应落在任务消息之后: %q", msgs[1].Content)
	}
	if msgs[2].Content != bigHistory(6)[5].Content {
		t.Fatalf("尾部一条应保留原文: %q", msgs[2].Content)
	}
	// 压缩后子会话仍在自己的行上，且库里回放的顺序与内存一致
	stored, err := st.Load(childID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(msgs) || stored[0].Content != task || !isCheckpointContent(stored[1].Content) {
		t.Fatalf("库里回放应与内存一致（任务说明书在前、摘要在后）: %+v", stored)
	}
}
