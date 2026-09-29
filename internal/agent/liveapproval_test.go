// 权限档的会话级实时语义（2026-09-29）：中途改档立刻作用于**运行中的**一轮。
//
// 为什么单独钉：权限档原先在 Send 里快照进这一轮的 ctx、runTools 每轮读那份快照，
// 用户跑到一半把权限放开，正在跑的那一轮完全不知道——「我已经说了完全放开，别再问我」
// 是用户实测的原始抱怨。这里的测试全部按「改档发生在轮中间」编排，读快照的实现必挂。
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// bashToolCall 造一次 bash 工具调用（ID 与命令都可指定——一个脚本里要区分多次调用）。
func bashToolCall(id, command string) []llm.StreamEvent {
	var tc llm.ToolCall
	tc.ID = id
	tc.Function.Name = "bash"
	tc.Function.Arguments = fmt.Sprintf(`{"command":%q}`, command)
	return []llm.StreamEvent{
		{Type: llm.EventToolCall, ToolCall: tc},
		{Type: llm.EventDone, Result: &llm.ChatResult{
			Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
			FinishReason: llm.FinishToolCalls,
		}},
	}
}

// TestLiveApprovalChangesMidTurn：一轮里第一条工具调用拿到 confirm 弹确认 →
// SetApproval("auto") → **同一个挂起确认被放行** → 后续工具调用不再弹确认。
//
// 这是用户实测现象的正向钉子：改档必须立刻作用于正在跑的那一轮，而不是等下一轮。
func TestLiveApprovalChangesMidTurn(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		bashToolCall("call-1", "echo first-marker"),
		bashToolCall("call-2", "echo second-marker"),
		textResult("两条都跑完了"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("跑两条命令"); err != nil { // 不带 approval = 默认 confirm
		t.Fatal(err)
	}
	// 第一条 bash 挂在确认门上（默认 confirm 档）
	waitFor(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		return len(cap.pending) > 0
	})
	// 轮跑到一半改档：挂起的那条直接放行，后续调用不再弹
	if got := s.SetApproval("auto"); got != "auto" {
		t.Fatalf("SetApproval 应返回规范化后的档位: %q", got)
	}
	waitFor(t, func() bool { return !s.Busy() })

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.pending) != 1 {
		t.Fatalf("改档后不该再弹确认（只应有第一次那一条）: %+v", cap.pending)
	}
	var first, second bool
	for _, r := range cap.results {
		if strings.Contains(r.Content, "用户拒绝") {
			t.Fatalf("挂起的确认应被放行，而不是当拒绝处理: %+v", r)
		}
		if strings.Contains(r.Content, "first-marker") {
			first = true
		}
		if strings.Contains(r.Content, "second-marker") {
			second = true
		}
	}
	if !first || !second {
		t.Fatalf("两条命令都应执行（第一条靠放行、第二条靠新档）: first=%v second=%v results=%+v",
			first, second, cap.results)
	}
}

// TestLiveApprovalStrictMidTurn：中途切 strict → 运行中的一轮立刻开始拒绝变更类工具
// （错误回填模型），不是等下一轮。
func TestLiveApprovalStrictMidTurn(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_file", `{"path":"no-such-file.txt"}`), // 低危：直接执行，不弹确认
		bashToolCall("call-2", "echo strict-marker"),
		textResult("收尾"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	var once sync.Once
	s.emit = func(ev Event) {
		// 第一轮的工具结果刚落地、第二轮还没开工——这是轮与轮之间的确定注入点
		//（emit 在回合 goroutine 上同步调用，且不持 s.mu，改档不会死锁）
		if e, ok := ev.(ToolResultEvent); ok && e.Name == "read_file" {
			once.Do(func() { s.SetApproval("strict") })
		}
		cap.handle(ev)
	}

	if err := s.Send("先读再改"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.pending) != 0 {
		t.Fatalf("strict 无放行语义，不该产生确认请求: %+v", cap.pending)
	}
	var sawReject, sawRead bool
	for _, r := range cap.results {
		switch r.Name {
		case "read_file":
			sawRead = true
			if r.IsError && strings.Contains(r.Content, "只读模式") {
				t.Fatalf("改档前的读取类调用不该被追溯拒绝: %+v", r)
			}
		case "bash":
			if !r.IsError || !strings.Contains(r.Content, "只读模式") {
				t.Fatalf("中途切 strict 后变更类工具应被拒绝: %+v", r)
			}
			sawReject = true
		}
		if strings.Contains(r.Content, "strict-marker") {
			t.Fatalf("strict 下命令不该执行: %+v", r)
		}
	}
	if !sawRead || !sawReject {
		t.Fatalf("应看到读文件正常 + bash 被只读模式拒绝: read=%v reject=%v results=%+v",
			sawRead, sawReject, cap.results)
	}
}

// liveDispatchStream 造假流：主轮派发一次 → 子会话按轮次跑 bash → 双方收尾。
//
// beforeChildRound 在子会话**每一轮开工前**同步调用（在子会话的回合 goroutine 上），
// 用来在轮与轮之间改父会话的权限档——这是「中途改档」唯一确定性的注入点
// （子会话的回合由父会话的 dispatch 触发，测试 goroutine 插不进去）。
func liveDispatchStream(t *testing.T, beforeChildRound func(round int)) StreamFn {
	t.Helper()
	mainRound, subRound := 0, 0
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			mainRound++
			go func() {
				defer close(ch)
				if mainRound > 1 {
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop}}
					return
				}
				tc := llm.ToolCall{ID: "call-a1"}
				tc.Function.Name = "agent_dispatch"
				tc.Function.Arguments = `{"agent":"coder","task":"跑两条命令"}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		subRound++
		round := subRound
		if beforeChildRound != nil {
			beforeChildRound(round)
		}
		go func() {
			defer close(ch)
			if round > 2 {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "子会话收尾。"}, FinishReason: llm.FinishStop}}
				return
			}
			tc := llm.ToolCall{ID: fmt.Sprintf("sub-%d", round)}
			tc.Function.Name = "bash"
			tc.Function.Arguments = fmt.Sprintf(`{"command":"echo round%d-marker"}`, round)
			ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
		}()
		return ch, nil
	}
}

// TestDispatchChildFollowsParentLiveApproval：子会话的档位是**每次现算**的
// stricterApproval(父会话此刻, 子 Agent 默认)——取严语义不变（子执行面不大于请求方），
// 但父会话中途改档必须传得到正在跑的子会话。
func TestDispatchChildFollowsParentLiveApproval(t *testing.T) {
	t.Run("父 auto + 子默认 confirm：子仍取严走确认门", func(t *testing.T) {
		s, cap := newApprovalDispatchSession(t, "confirm")
		if err := s.Send("派活", WithApproval("auto")); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool {
			cap.mu.Lock()
			defer cap.mu.Unlock()
			return len(cap.pending) > 0
		})
		cap.mu.Lock()
		pending := cap.pending[0]
		cap.mu.Unlock()
		if pending.Request.Name != "bash" {
			t.Fatalf("子 Agent 的 bash 应走确认门（父 auto 不放大子默认）: %+v", pending.Request)
		}
		if pending.Request.DispatchID == "" {
			t.Fatal("子会话的确认请求应带上 dispatch_id（卡内呈现）")
		}
		if err := s.Confirm(pending.Request.ID, true); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return !s.Busy() })
	})

	t.Run("父中途改成 auto：子默认 strict 不受影响", func(t *testing.T) {
		s, cap := newApprovalDispatchSession(t, "strict")
		s.SetStream(liveDispatchStream(t, func(round int) {
			if round == 1 {
				// 子会话开工前父会话就放开了：子 Agent 自己的 strict 仍压住它
				s.SetApproval("auto")
			}
		}))
		if err := s.Send("派活", WithApproval("confirm")); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return !s.Busy() })

		cap.mu.Lock()
		defer cap.mu.Unlock()
		if len(cap.pending) != 0 {
			t.Fatalf("子默认 strict 不该弹确认（无放行语义）: %+v", cap.pending)
		}
		var rejected bool
		for _, r := range cap.results {
			if strings.Contains(r.Content, "round1-marker") {
				t.Fatalf("子 Agent 默认 strict 应拒绝变更类工具: %+v", r)
			}
			if r.Name == "bash" && strings.Contains(r.Content, "只读模式") {
				rejected = true
			}
		}
		if !rejected {
			t.Fatalf("子 Agent 默认 strict 应压住父会话中途的 auto: %+v", cap.results)
		}
	})

	t.Run("父中途放宽：子未声明默认时跟随父的实时档", func(t *testing.T) {
		s, cap := newApprovalDispatchSession(t, "")
		s.SetStream(liveDispatchStream(t, func(round int) {
			if round == 2 {
				// 轮与轮之间放宽：正在跑的子会话必须立刻跟上
				s.SetApproval("auto")
			}
		}))
		if err := s.Send("派活", WithApproval("strict")); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return !s.Busy() })

		cap.mu.Lock()
		defer cap.mu.Unlock()
		if len(cap.pending) != 0 {
			t.Fatalf("子未声明默认时不应弹确认（继承父的实时档）: %+v", cap.pending)
		}
		var rejected, followed bool
		for _, r := range cap.results {
			if r.Name != "bash" {
				continue
			}
			if strings.Contains(r.Content, "round1-marker") {
				t.Fatalf("父 strict 下第一条命令不该执行: %+v", r)
			}
			if strings.Contains(r.Content, "只读模式") {
				rejected = true
			}
			if strings.Contains(r.Content, "round2-marker") {
				followed = true
			}
		}
		if !rejected {
			t.Fatalf("父 strict + 子未声明默认 → 子应继承 strict 拒绝变更类工具: %+v", cap.results)
		}
		if !followed {
			t.Fatalf("父中途改成 auto 后子会话应立刻跟随（不再弹确认直接执行）: %+v", cap.results)
		}
	})
}

// TestLiveApprovalDefaults：未指定过的会话回落 confirm；SetApproval 空串也规范化成
// confirm（回给客户端的是规范化后的档位，客户端据此对齐本地设置）。
func TestLiveApprovalDefaults(t *testing.T) {
	s, _ := newTestSession(t)
	if got := s.LiveApproval(); got != "confirm" {
		t.Fatalf("未指定过的会话应回落 confirm: %q", got)
	}
	if got := s.SetApproval(""); got != "confirm" {
		t.Fatalf("SetApproval 空串应规范化成 confirm: %q", got)
	}
	if got := s.LiveApproval(); got != "confirm" {
		t.Fatalf("空串规范化后应存成 confirm: %q", got)
	}
	if got := s.SetApproval("strict"); got != "strict" || s.LiveApproval() != "strict" {
		t.Fatalf("SetApproval 应立刻反映在 LiveApproval: %q / %q", got, s.LiveApproval())
	}
}

// TestSendWithoutApprovalKeepsUserChoice：CLI 路径（不带 approval 参数）不该把用户
// 中途选的档位重置回 Agent 默认/confirm——请求级参数的语义是「显式给了才覆盖」。
func TestSendWithoutApprovalKeepsUserChoice(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{textResult("一"), textResult("二")}}
	s.stream = fake.stream

	if got := s.SetApproval("auto"); got != "auto" {
		t.Fatalf("SetApproval: %q", got)
	}
	for _, text := range []string{"第一条", "第二条"} {
		if err := s.Send(text); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return !s.Busy() })
		if got := s.LiveApproval(); got != "auto" {
			t.Fatalf("不带 approval 参数的 Send 不该重置用户选的档位: %q", got)
		}
	}
}
