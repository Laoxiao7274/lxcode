// dispatch_todos_test.go —— todo 契约批③的端到端钉子：
// 主 Agent 派发带 todos → 子会话初稿装载（内存 + 落库 + 事件带归属）→
// 子会话执行中更新清单 → 派发结束定格归档（落库 + DispatchEnd.FinalTodos + 
// 结果文本带 N/M 与残留项）。
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// dispatchTodosStream 主轮第一轮派发（带 todos 参数），子会话第一轮用 todo 更新
// 清单、第二轮给结论；主轮第二轮收尾。
func dispatchTodosStream(t *testing.T, taskJSON, subUpdateJSON string) StreamFn {
	t.Helper()
	mainRound := 0
	subRound := 0
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
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
		// 子会话语境
		subRound++
		if subRound == 1 {
			tc := llm.ToolCall{ID: "call-t1"}
			tc.Function.Name = "todo"
			tc.Function.Arguments = subUpdateJSON
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "子会话结论：做了一半。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "子会话结论：做了一半。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

func waitDispatchSettled(t *testing.T, s *Session) {
	t.Helper()
	for i := 0; i < 500 && s.Busy(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatchTodosEndToEnd(t *testing.T) {
	env, st := newChildSessionEnv(t)
	// 子 Agent 的白名单要有 todo 工具（初稿装载与更新都经它）
	env.s.SetStream(dispatchTodosStream(t,
		`{"agent":"coder","task":"修内存泄漏","todos":[{"content":"定位泄漏点","status":"pending"},{"content":"修复","status":"pending"},{"content":"补测试","status":"pending"}]}`,
		`{"items":[{"content":"定位泄漏点","status":"done"},{"content":"修复","status":"active"},{"content":"补测试","status":"pending"}]}`))

	if err := env.s.Send("修内存泄漏"); err != nil {
		t.Fatal(err)
	}
	waitDispatchSettled(t, env.s)

	// ① 子会话行的 todos 定格为子会话自己更新后的值（子修订覆盖初稿）
	kids, err := st.ChildrenOf(env.s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 {
		t.Fatalf("应有 1 个子会话，got %d", len(kids))
	}
	saved, ok, err := st.TodosOf(kids[0].ID)
	if err != nil || !ok {
		t.Fatalf("子会话清单应落库: ok=%v err=%v", ok, err)
	}
	if len(saved) != 3 || saved[0].Status != "done" || saved[1].Status != "active" {
		t.Fatalf("定格值应是子会话修订后的清单: %+v", saved)
	}

	// ② 事件流：DispatchStart 带 todos 初稿；todo.updated 带归属上抛；
	// DispatchEnd 带 FinalTodos 定格值
	env.mu.Lock()
	var startTodos, finalTodos []tools.TodoItem
	var dispatchedTodoUpdates, endResult int
	for _, ev := range env.events {
		switch e := ev.(type) {
		case DispatchStartEvent:
			startTodos = e.Todos
		case TodoUpdatedEvent:
			if e.DispatchID == "call-d1" {
				dispatchedTodoUpdates++
			}
		case DispatchEndEvent:
			if e.DispatchID == "call-d1" {
				finalTodos = e.FinalTodos
				endResult++
			}
		}
	}
	env.mu.Unlock()
	if len(startTodos) != 3 || startTodos[0].Content != "定位泄漏点" {
		t.Fatalf("DispatchStart 应带清单初稿: %+v", startTodos)
	}
	if dispatchedTodoUpdates < 2 { // 初稿装载 1 次 + 子会话更新 1 次
		t.Fatalf("带归属的 todo.updated 应至少 2 次（初稿+子更新），got %d", dispatchedTodoUpdates)
	}
	if endResult != 1 || len(finalTodos) != 3 || finalTodos[1].Status != "active" {
		t.Fatalf("DispatchEnd 应带定格清单: n=%d %+v", endResult, finalTodos)
	}

	// ③ 主 Agent 上下文里的调度结果文本带清单终态（N/M + 残留项点名）
	env.mu.Lock()
	var dispatchOutput string
	for _, ev := range env.events {
		if e, ok := ev.(ToolResultEvent); ok && e.Name == tools.DispatchToolName {
			dispatchOutput = e.Content
		}
	}
	env.mu.Unlock()
	if !strings.Contains(dispatchOutput, "子会话清单最终状态：1/3 完成") ||
		!strings.Contains(dispatchOutput, "修复（进行中）") || !strings.Contains(dispatchOutput, "补测试（待办）") {
		t.Fatalf("调度结果应带清单终态与残留项: %q", dispatchOutput)
	}
}
