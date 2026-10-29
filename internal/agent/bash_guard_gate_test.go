package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// bash_guard_gate_test.go —— bash 高危命令模式确认门的集成级契约（auto 档例外口）：
//
// auto 档跳过常规 Confirm 门（TestApprovalAutoSkipsConfirmGate 钉住），但 bash
// 命中 HighRiskBashCommand（git push / rm -rf 根目录等）时必须仍弹确认门
// （ConfirmForced）；批准后正常执行（逃生可用），拒绝走现有拒绝路径；
// confirm 档行为不变；未命中高危模式的命令在 auto 档照常静默执行。

// TestApprovalAutoForcesConfirmOnHighRiskBash：auto 档下 bash 调 git push
// 必须产生确认请求（确认文案含模式名与命令原文）——auto 不是无条件的完全访问。
func TestApprovalAutoForcesConfirmOnHighRiskBash(t *testing.T) {
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
	req := waitBashConfirmPending(t, cap)
	if req == nil {
		t.Fatalf("auto 档下高危命令应弹确认门，实际未请求确认（结果: %+v）", snapshotResults(cap))
	}
	if req.Name != "bash" {
		t.Fatalf("确认请求应归属 bash: %+v", req)
	}
	if !strings.Contains(req.Prompt, "高危命令模式命中") || !strings.Contains(req.Prompt, "git push") {
		t.Fatalf("确认文案应含模式名与命令原文: %s", req.Prompt)
	}
	// 批准 = 逃生：正常执行，拿到命令输出
	if err := s.Confirm(req.ID, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	results := snapshotResults(cap)
	if len(results) != 1 || results[0].IsError {
		t.Fatalf("批准后 bash 应正常执行: %+v", results)
	}
}

// TestApprovalAutoRejectHighRiskBashFillsDenial：auto 档下命中高危命令、用户拒绝
// → 走现有拒绝路径（错误回填模型，不执行）。
func TestApprovalAutoRejectHighRiskBashFillsDenial(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash", `{"command":"git push --force"}`),
		textResult("好的，不推了"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("强推", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	req := waitBashConfirmPending(t, cap)
	if req == nil {
		t.Fatalf("应弹确认门（结果: %+v）", snapshotResults(cap))
	}
	if err := s.Confirm(req.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	results := snapshotResults(cap)
	if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, "用户拒绝") {
		t.Fatalf("拒绝应回填拒绝错误: %+v", results)
	}
}

// TestApprovalAutoSilentForNonHighRisk：auto 档下未命中高危模式的命令不弹门
// （现行 auto 语义不回归）。
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

// waitBashConfirmPending 等到一次 bash 的确认请求挂起并返回它（无挂起且会话
// 已空闲时返回 nil——用例自行判定失败方式）。
func waitBashConfirmPending(t *testing.T, cap *toolEventCapture) *ConfirmRequest {
	t.Helper()
	waitFor(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		if len(cap.pending) > 0 {
			return true
		}
		return false
	})
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.pending) > 0 {
		return cap.pending[len(cap.pending)-1].Request
	}
	return nil
}

// snapshotResults 复制当前收集到的工具结果（避免锁外读切片）。
func snapshotResults(cap *toolEventCapture) []ToolResultEvent {
	cap.mu.Lock()
	defer cap.mu.Unlock()
	return append([]ToolResultEvent(nil), cap.results...)
}
