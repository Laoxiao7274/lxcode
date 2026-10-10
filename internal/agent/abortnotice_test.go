// 「用户主动停止」感知 + 子会话停止（2026-10-10）：
//   - 流式阶段被停（纯半截 assistant 入历史）时，被动通告入队——下一轮真的
//     开始时由轮边界注入，模型才知道这轮是用户停的、不要续写；
//   - 子会话运行时登记进父会话（children），CancelChild 能停掉真正在跑的
//     子轮（服务端 chat.cancel 对子会话 id 的寻址依据）。
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// abortStream：第一轮发一个 delta 后挂住，取消后以「error + 部分内容」收尾
//（对齐真实客户端 llm.emitFinal 的中断契约）；后续轮正常收尾。
func abortStream(t *testing.T, laterText string) StreamFn {
	t.Helper()
	turn := 0
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		turn++
		ch := make(chan llm.StreamEvent, 2)
		if turn == 1 {
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "已经生成了"}
				<-ctx.Done() // 取消前挂住
				ch <- llm.StreamEvent{Type: llm.EventError, Err: context.Canceled,
					Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "已经生成了"}}}
			}()
			return ch, nil
		}
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: laterText}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// aborted 后被动通告入队；下一次真实发送的轮边界把它注入历史（Notice=true），
// 模型由此知道上一轮是用户停的。通告**不会**自动开轮（flushNotices 不碰被动
// 队列）——取消后 busy 保持 false。
func TestAbortedNoticeInjectedNextRound(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	s.SetStream(abortStream(t, "收到新指示"))

	if err := s.Send("慢慢说"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Busy() })
	time.Sleep(30 * time.Millisecond) // 让 delta 先流出
	s.Cancel()
	waitFor(t, func() bool { return !s.Busy() })

	// 取消后不开新轮：被动通告只入队
	if s.Busy() {
		t.Fatal("取消后不应自动开新一轮（被动通告不是唤醒）")
	}
	// 下一轮（用户真的说话）→ 轮边界注入
	if err := s.Send("换个方向"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	var sawNotice, sawPartial bool
	for _, m := range s.History().Messages {
		if m.Role != "user" {
			if m.Role == "assistant" && m.Content == "已经生成了" {
				sawPartial = true
			}
			continue
		}
		if strings.Contains(m.Content, "用户中断了这次生成") {
			sawNotice = true
		}
	}
	if !sawPartial {
		t.Fatal("前置：被中断的半截回答应已入历史")
	}
	if !sawNotice {
		t.Fatalf("下一轮历史应含「用户中断」通告: %+v", s.History().Messages)
	}
}

// 子会话跑长任务（流挂住）时，父会话 CancelChild 能真停子轮；父轮随后继续
// 跑完（工具结果 = 已取消），SendWait 收尾后登记注销（再调返回 false）。
// 未命中（unknown id）返回 false——调用方走原路径兜底。
func TestCancelChildStopsRunningChild(t *testing.T) {
	env, st := newChildSessionEnv(t)
	calls := 0
	env.s.SetStream(func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if strings.Contains(msgs[0].Content, "调度中枢") {
			calls++
			if calls == 1 {
				tc := llm.ToolCall{ID: "call-d1"}
				tc.Function.Name = "agent_dispatch"
				tc.Function.Arguments = `{"agent":"coder","task":"慢慢跑"}`
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
				}()
				return ch, nil
			}
			// 主轮收尾：父轮在子会话被停后继续跑完
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "已处理中断。"}, FinishReason: llm.FinishStop}}
			}()
			return ch, nil
		}
		// 子会话：发一个 delta 后挂住（模拟长任务）
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "干着活"}
			<-ctx.Done()
			ch <- llm.StreamEvent{Type: llm.EventError, Err: context.Canceled,
				Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "干着活"}}}
		}()
		return ch, nil
	})

	// 未命中：陌生 id 返回 false（不 panic、不误停）
	if env.s.CancelChild("ghost-child") {
		t.Fatal("未登记的 id 不应命中 CancelChild")
	}

	if err := env.s.Send("派个活"); err != nil {
		t.Fatal(err)
	}
	// 等子会话开起来并登记进父会话；登记命中即真停（首次 true 就是取消动作）
	var childID string
	waitFor(t, func() bool {
		kids, err := st.ChildrenOf(env.s.SessionID())
		if err != nil || len(kids) != 1 {
			return false
		}
		childID = kids[0].ID
		return env.s.CancelChild(childID)
	})
	waitDispatchIdle(t, env.s)

	// 父轮继续跑完：工具结果报已取消，随后主 Agent 给出收尾答复
	var toolResult, final string
	for _, m := range env.s.History().Messages {
		switch m.Role {
		case "tool":
			toolResult = m.Content
		case "assistant":
			if strings.Contains(m.Content, "已处理中断") {
				final = m.Content
			}
		}
	}
	if !strings.Contains(toolResult, "已取消") {
		t.Fatalf("子会话被停后工具结果应报已取消: %q", toolResult)
	}
	if final == "" {
		t.Fatalf("父轮应在子会话被停后继续跑完: %+v", env.s.History().Messages)
	}
	// 子会话自己的历史：任务消息 + 半截 assistant（被动通告只入队未注入）
	msgs, err := st.Load(childID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Content, "干着活") {
		t.Fatalf("子会话历史应是任务消息 + 半截回答: %+v", msgs)
	}
	// SendWait 收尾即注销：再次 CancelChild 未命中（兜底路径接管）
	if env.s.CancelChild(childID) {
		t.Fatal("子会话收尾后登记应已注销")
	}
}
