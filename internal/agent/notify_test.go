package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// noticeRecorder 是可观测的假 LLM：记录每次调用收到的消息，按脚本回放事件，
// 并可在「正在跑的时候」注入副作用（用来模拟忙时投递通告）。
type noticeRecorder struct {
	mu     sync.Mutex
	calls  [][]llm.Message
	script [][]llm.StreamEvent
	hook   func(call int)
}

func (r *noticeRecorder) stream(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
	r.mu.Lock()
	i := len(r.calls)
	r.calls = append(r.calls, append([]llm.Message(nil), msgs...))
	hook := r.hook
	var evs []llm.StreamEvent
	if i < len(r.script) {
		evs = r.script[i]
	}
	r.mu.Unlock()
	if hook != nil {
		hook(i) // 在「本轮正在跑」的时刻注入（模拟 settle 事件在忙时到达）
	}
	ch := make(chan llm.StreamEvent, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (r *noticeRecorder) callMessages() [][]llm.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]llm.Message, len(r.calls))
	copy(out, r.calls)
	return out
}

func hasContent(msgs []llm.Message, want string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, want) {
			return true
		}
	}
	return false
}

// TestNotifyIdleStartsTurn：空闲时投递通告 = 开一轮（通告作为 user 消息进历史）。
func TestNotifyIdleStartsTurn(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	rec := &noticeRecorder{script: [][]llm.StreamEvent{textResult("收到")}}
	s.stream = rec.stream

	if err := s.Notify("后台任务 X 结束（退出码 0）。用 job_output 读输出。"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	calls := rec.callMessages()
	if len(calls) != 1 {
		t.Fatalf("空闲通告应开一轮: %d", len(calls))
	}
	if !hasContent(calls[0], "后台任务 X 结束") {
		t.Fatalf("通告应作为 user 消息进历史: %+v", calls[0])
	}
	h := s.History()
	if len(h.Messages) != 2 || h.Messages[0].Role != "user" {
		t.Fatalf("通告应是 user 消息（模型当作用户回合回应）: %+v", h.Messages)
	}
}

// TestNotifyBusyInjectsAtRoundBoundary：owner 忙 → 通告排队，在**轮边界**
// 并入历史（不是硬塞：忙时 Send 会 ErrBusy）。
func TestNotifyBusyInjectsAtRoundBoundary(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	rec := &noticeRecorder{
		script: [][]llm.StreamEvent{
			toolCallResult("todo", `{"items":[]}`), // 第一轮：要求调工具（低危自动执行）
			textResult("第二轮"),                      // 第二轮：收尾
		},
	}
	rec.hook = func(call int) {
		if call == 0 {
			// 正处在第一轮里（busy=true）：通告只能排队
			if err := s.Notify("忙时通告"); err != nil {
				t.Errorf("忙时通告应排队而不是报错: %v", err)
			}
		}
	}
	s.stream = rec.stream

	if err := s.Send("干活"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	calls := rec.callMessages()
	if len(calls) != 2 {
		t.Fatalf("应有两次模型调用（工具循环）: %d", len(calls))
	}
	if hasContent(calls[0], "忙时通告") {
		t.Fatal("通告不该在它入队的那一轮就被塞进去（那一轮的历史快照已固定）")
	}
	if !hasContent(calls[1], "忙时通告") {
		t.Fatalf("通告应在**轮边界**并入下一轮历史: %+v", calls[1])
	}
}

// TestQueueNoticeWaitsForUserSend：唤醒预算耗尽时通告只入队（不开新一轮），
// 用户下次说话后由轮边界投递——**绝不丢弃**。
func TestQueueNoticeWaitsForUserSend(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	rec := &noticeRecorder{script: [][]llm.StreamEvent{textResult("收到")}}
	s.stream = rec.stream

	if err := s.QueueNotice("预算耗尽后的通告"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if len(rec.callMessages()) != 0 {
		t.Fatal("QueueNotice 不该开新一轮")
	}
	if s.Busy() {
		t.Fatal("只入队不该把会话置忙")
	}
	if err := s.Send("用户消息"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	calls := rec.callMessages()
	if len(calls) != 1 {
		t.Fatalf("用户消息应开一轮: %d", len(calls))
	}
	// 用户消息在前、通告紧随其后（轮边界注入）
	userIdx, noticeIdx := -1, -1
	for i, m := range calls[0] {
		if m.Content == "用户消息" {
			userIdx = i
		}
		if strings.Contains(m.Content, "预算耗尽后的通告") {
			noticeIdx = i
		}
	}
	if userIdx < 0 || noticeIdx < 0 || noticeIdx < userIdx {
		t.Fatalf("通告应在用户消息之后的轮边界并入: %+v", calls[0])
	}
}

// TestWakeGateBlocksNewTurn：唤醒判定只约束「空闲 → 开一轮」；
// 预算耗尽时通告留在队列里（不丢），放开判定后立刻投递。
func TestWakeGateBlocksNewTurn(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	rec := &noticeRecorder{script: [][]llm.StreamEvent{textResult("收到")}}
	s.stream = rec.stream

	var mu sync.Mutex
	allow := false
	s.SetWakeGate(func() bool {
		mu.Lock()
		defer mu.Unlock()
		return allow
	})
	if err := s.QueueNotice("等预算的通告"); err != nil {
		t.Fatal(err)
	}
	s.flushNotices()
	time.Sleep(80 * time.Millisecond)
	if len(rec.callMessages()) != 0 {
		t.Fatal("预算耗尽时不该开新一轮")
	}
	mu.Lock()
	allow = true
	mu.Unlock()
	s.flushNotices()
	waitFor(t, func() bool { return !s.Busy() })
	calls := rec.callMessages()
	if len(calls) != 1 || !hasContent(calls[0], "等预算的通告") {
		t.Fatalf("放开预算后应投递并开一轮: %+v", calls)
	}
}

// TestNotifyRejectsEmpty：空通告是编程错误（不是用户输入）。
func TestNotifyRejectsEmpty(t *testing.T) {
	s, _ := newTestSession(t)
	if err := s.Notify("   "); err == nil {
		t.Fatal("空通告应报错")
	}
	if err := s.QueueNotice(""); err == nil {
		t.Fatal("空通告入队应报错")
	}
}
