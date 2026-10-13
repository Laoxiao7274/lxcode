// panic 防护的钉子（健壮性加固，2026-10）：生成 goroutine 里的 panic 转成
// TurnError 事件（用户看到「本轮生成失败」的人话），busy 复位走 runTurn 自己的
// defer（panic 传播路径上照常执行），会话随后可以正常开新一轮——进程存活由
// 「测试本身没崩」证明。
package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

func TestRunTurnPanicBecomesTurnError(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		panic("boom: 注入的假 stream panic")
	}

	var mu sync.Mutex
	var gotErr bool
	s.emit = func(ev Event) {
		if e, ok := ev.(TurnErrorEvent); ok {
			mu.Lock()
			gotErr = true
			mu.Unlock()
			if e.Message == "" {
				t.Error("TurnError 应带人话消息")
			}
		}
	}

	if err := s.Send("触发 panic"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return gotErr })

	// 会话没有卡死：换回正常 stream，下一轮照常完成
	fake := &fakeStream{script: [][]llm.StreamEvent{textResult("恢复后的回复")}}
	s.stream = fake.stream
	if err := s.Send("再来一次"); err != nil {
		t.Fatalf("panic 后会话应可正常开新一轮: %v", err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	h := s.History()
	if len(h.Messages) < 2 {
		t.Fatalf("panic 后的新一轮应有完整历史: %+v", h.Messages)
	}
}

// SendWait 路径同样要能收尾：panic 转成 TurnError 后 done 关闭，宿主不悬挂
//（dispatch 等它）。错误经由返回值/事件二选一可见——这里钉「不悬挂 + 会话复活」。
func TestSendWaitPanicDoesNotHang(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		panic("boom: SendWait 路径的 panic")
	}
	s.emit = func(Event) {}
	done := make(chan error, 1)
	go func() { done <- s.SendWait(context.Background(), "触发 panic") }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("panic 后 SendWait 应收尾（done 关闭），而不是永远悬挂")
	}
	waitFor(t, func() bool { return !s.Busy() })
	// 复活
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}, FinishReason: llm.FinishStop}}
		close(ch)
		return ch, nil
	}
	if err := s.SendWait(context.Background(), "再来"); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("panic 后 SendWait 应可正常完成: %v", err)
	}
}
