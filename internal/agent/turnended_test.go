// turnended_test.go —— 「撤回自动取消并等待收尾」与「panic 补配对」的会话层契约：
//
//  1. busy 时 Rewind 自动取消当前轮并成功（settleBusyForRewind）；
//  2. 收尾信号等不到时（turnEnded 缺失）如实报错而不是假装成功；
//  3. panic 后历史里的未配对 tool_call 被补上合成结果（repairUnpairedAfterPanic）。
//
// 锁纪律在测试里顺带验证：settle 等待期间若持有 s.mu，会与 runTurn defer 的
// close 抢锁死锁（超时即红）。
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// TestRewindAutoCancelsBusyTurn：busy（LLM 流挂住）时点撤回 → 自动取消当前轮 →
// 等它收尾 → 撤回成功。
func TestRewindAutoCancelsBusyTurn(t *testing.T) {
	s := newPersistSession(t, t.TempDir()) // 已绑定 default 模型（m1），历史带真实序号
	seedHistory(t, s, 2)                   // 已有两条历史，撤回锚点 = 第 2 条

	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		<-ctx.Done() // 挂住直到取消
		return nil, errors.New("cancelled")
	}
	if err := s.Send("挂住的一轮"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s.Busy)

	anchor := s.History().Messages[1].Seq
	res, err := s.Rewind(anchor)
	if err != nil {
		t.Fatalf("busy 时撤回应自动取消并成功: %v", err)
	}
	if res.Removed == 0 {
		t.Fatalf("撤回应移走本轮消息: %+v", res)
	}
	waitFor(t, func() bool { return !s.Busy() })
}

// TestRewindBusyWithoutEndedSignal：turnEnded 缺失时（模拟收尾信号拿不到的异常
// 场景）如实报错——不假装成功。
func TestRewindBusyWithoutEndedSignal(t *testing.T) {
	s, _ := newTestSession(t)
	s.mu.Lock()
	s.busy = true
	s.cancel = nil
	s.turnEnded = nil // 故意不给收尾信号
	s.mu.Unlock()

	if _, err := s.Rewind(1); err == nil || !strings.Contains(err.Error(), "正在收尾") {
		t.Fatalf("turnEnded 缺失应报错: %v", err)
	}
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

// TestRepairUnpairedAfterPanic：历史里有未等到结果的 tool_call 时，补配对给每个
// 未配对 id 落一条合成 tool 结果（文案如实说「异常终止」），之后配对平衡。
func TestRepairUnpairedAfterPanic(t *testing.T) {
	s, _ := newTestSession(t)
	s.append(llm.Message{Role: "user", Content: "跑个工具"})
	s.append(callMsg("c1", "c2")) // assistant 带两个 tool_call，结果永远不会来

	pairing := AnalyzeToolPairing(s.History().Messages)
	if len(pairing.Unpaired) != 2 {
		t.Fatalf("前置应有两个未配对调用: %+v", pairing.Unpaired)
	}
	s.repairUnpairedAfterPanic()

	msgs := s.History().Messages
	pairing = AnalyzeToolPairing(msgs)
	if len(pairing.Unpaired) != 0 {
		t.Fatalf("补配对后应无未配对调用: %+v", pairing.Unpaired)
	}
	var repaired int
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "异常终止") {
			repaired++
		}
	}
	if repaired != 2 {
		t.Fatalf("应补 2 条合成结果（含「异常终止」文案）: %d", repaired)
	}
}

// TestRepairUnpairedAfterPanicNoop：历史本身平衡时补配对是空操作（不写历史）。
func TestRepairUnpairedAfterPanicNoop(t *testing.T) {
	s, _ := newTestSession(t)
	seedHistory(t, s, 3)
	before := len(s.History().Messages)
	s.repairUnpairedAfterPanic()
	if after := len(s.History().Messages); after != before {
		t.Fatalf("平衡历史不应写入: before=%d after=%d", before, after)
	}
}
