package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// bash_guard_gate_test.go —— auto 档完全访问契约（2026-10 用户拍板）：
//
// auto = 完全无确认——跳过常规 Confirm 门（TestApprovalAutoSkipsConfirmGate
// 钉住），bash 命中 HighRiskBashCommand（git push / rm -rf 根目录等）也**不弹门**
// （ConfirmForced 硬闸不再被 runTools 消费，机制保留在 tools 层待将来档位设计）。
// confirm 档行为不变；未命中高危模式的命令在 auto 档照常静默执行。

// TestApprovalAutoExecutesHighRiskBashWithoutConfirm：auto 档下 bash 调
// git push 直接执行成功——无确认请求、无确认挂起、结果非 error。
// 「我的完全访问就是什么也不弹」。
func TestApprovalAutoExecutesHighRiskBashWithoutConfirm(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash", `{"command":"git push origin main"}`),
		textResult("完成"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("推代码", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	// 会话直接跑到空闲——全程没有任何确认门拦截（中途挂起 = 失败）。
	waitFor(t, func() bool { return !s.Busy() })
	cap.mu.Lock()
	pending := len(cap.pending)
	cap.mu.Unlock()
	if pending != 0 {
		t.Fatalf("auto 档下高危命令也不应弹确认门: %d 个确认请求", pending)
	}
	results := snapshotResults(cap)
	if len(results) != 1 || results[0].IsError {
		t.Fatalf("auto 档下高危命令应直接执行成功: %+v", results)
	}
	if strings.Contains(results[0].Content, "用户拒绝") {
		t.Fatalf("不应出现拒绝路径: %+v", results)
	}
}

// TestApprovalAutoSilentForNonHighRisk：auto 档下未命中高危模式的命令不弹门
// （auto 完全访问语义不回归）。
func TestApprovalAutoSilentForNonHighRisk(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash", `{"command":"echo auto-quiet"}`),
		textResult("完成"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("跑命令", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	cap.mu.Lock()
	pending := len(cap.pending)
	cap.mu.Unlock()
	if pending != 0 {
		t.Fatalf("普通命令不应弹门: %d 个确认请求", pending)
	}
	results := snapshotResults(cap)
	if len(results) != 1 || !strings.Contains(results[0].Content, "auto-quiet") {
		t.Fatalf("普通命令应直接执行: %+v", results)
	}
}

// snapshotResults 复制当前收集到的工具结果（避免锁外读切片）。
func snapshotResults(cap *toolEventCapture) []ToolResultEvent {
	cap.mu.Lock()
	defer cap.mu.Unlock()
	return append([]ToolResultEvent(nil), cap.results...)
}
