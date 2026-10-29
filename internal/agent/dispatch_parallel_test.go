// 一轮里的多个 agent_dispatch 是否真的并行——用墙钟量。
//
// 模型表达「并行需求」的唯一方式就是一条 assistant 消息里发多个 dispatch 调用
// （工具文档就这么教）；串行跑的话模型以为并行了、实际一个接一个，墙钟是 N 倍。
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// TestDispatchCallsInOneRoundRunInParallel：一轮里的两个 dispatch 必须并行。
//
// 判据用墙钟：每个子会话各睡 childWork，两个串行 ≈ 2×childWork、并行 ≈ childWork。
// 取 1.5×childWork 当阈值——串行必然超过它，并行留了半个 childWork 的调度余量。
func TestDispatchCallsInOneRoundRunInParallel(t *testing.T) {
	const childWork = 250 * time.Millisecond
	var mainCalls int
	env := newDispatchSession(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			mainCalls++
			if mainCalls == 1 {
				// 一条 assistant 消息里两个 dispatch 调用（模型表达并行的方式）
				c1 := llm.ToolCall{ID: "call-1"}
				c1.Function.Name = "agent_dispatch"
				c1.Function.Arguments = `{"agent":"coder","task":"任务一"}`
				c2 := llm.ToolCall{ID: "call-2"}
				c2.Function.Name = "agent_dispatch"
				c2.Function.Arguments = `{"agent":"coder","task":"任务二"}`
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: c1}
					ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: c2}
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{c1, c2}},
						FinishReason: llm.FinishToolCalls,
					}}
				}()
				return ch, nil
			}
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "已汇总。"}, FinishReason: llm.FinishStop,
				}}
			}()
			return ch, nil
		}
		// 子轮：睡 childWork 模拟子 Agent 的真实耗时（LLM 往返）
		go func() {
			defer close(ch)
			time.Sleep(childWork)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "子结论"}, FinishReason: llm.FinishStop,
			}}
		}()
		return ch, nil
	})
	start := time.Now()
	if err := env.s.Send("并行做两件事"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)
	elapsed := time.Since(start)
	if mainCalls < 2 {
		t.Fatalf("主轮调用数不符: %d", mainCalls)
	}
	if elapsed > childWork*3/2 {
		t.Fatalf("一轮里的两个 dispatch 是串行执行的：耗时 %v（并行应 ≈ %v，串行 ≈ %v）", elapsed, childWork, 2*childWork)
	}
	t.Logf("两个 dispatch 墙钟 %v（串行会是 ≈ %v）", elapsed, 2*childWork)
}

// TestAwaitConfirmSerializesConcurrentRequests：确认门同时只能挂一个。
//
// 为什么需要：全应用只有"同时一个挂起确认"这条不变式（见 confirmProxy 注释），
// 而并行 dispatch（runTools 阶段二）会让多个子会话同时来要确认——确认槽只有一个
// （s.pending/s.confirm），不串行化的话后到的会顶掉先到的：先到的那次永远等不到
// 裁决，子会话卡死在 busy（用户看到的就是"卡住不动"）。
func TestAwaitConfirmSerializesConcurrentRequests(t *testing.T) {
	s, _ := newTestSession(t)
	// 谁挂上确认就替谁裁决（模拟用户在确认框上点"允许"）
	s.emit = func(ev Event) {
		req, ok := ev.(ConfirmRequestEvent)
		if !ok {
			return
		}
		go func(id string) {
			time.Sleep(20 * time.Millisecond)
			_ = s.Confirm(id, true)
		}(req.Request.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type answer struct {
		id  string
		out ConfirmOutcome
		ok  bool
	}
	results := make(chan answer, 2)
	for _, id := range []string{"c1", "c2"} {
		go func(id string) {
			out, ok := s.awaitConfirm(ctx, &ConfirmRequest{ID: id, Name: "bash", Prompt: "跑命令"})
			results <- answer{id, out, ok}
		}(id)
	}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if !r.ok || !r.out.Allow {
				t.Fatalf("并发确认 %s 没拿到裁决（被后到的顶掉了）: ok=%v allow=%v", r.id, r.ok, r.out.Allow)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("并发确认超时——确认槽被顶掉，先到的那次永远等不到裁决")
		}
	}
}
