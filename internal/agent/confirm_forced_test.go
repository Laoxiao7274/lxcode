// ConfirmForced 硬闸的 auto 档消费（2026-12）：project_create 在 auto（完全
// 无确认）下也必须弹确认门——用户拒绝则不执行、错误结果回填；bash 的高危命令
// 模式按 2026-10 拍板在 auto 档仍不弹（名单显式点名，不误伤）。confirm 档的
// 常规门行为不变（TestRunProjectTask* / TestApproval* 已覆盖）。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestConfirmForcedFiresUnderAuto：auto 档下 project_create 弹确认门（唯一例外口）。
func TestConfirmForcedFiresUnderAuto(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult(tools.ProjectCreateToolName, `{"name":"demo","parent":"C:/repo"}`),
		textResult("好"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("建项目", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		return len(cap.pending) > 0
	})
	cap.mu.Lock()
	name := cap.pending[0].Request.Name
	prompt := cap.pending[0].Request.Prompt
	cap.mu.Unlock()
	if name != tools.ProjectCreateToolName {
		t.Fatalf("确认请求应归属 project_create: %q", name)
	}
	if !strings.Contains(prompt, "demo") || !strings.Contains(prompt, "C:/repo") {
		t.Fatalf("确认文案应带 name 与 parent: %q", prompt)
	}
	// 不裁决直接取消收尾（门被拒 = 工具不执行——执行与否由 WS 端到端测试钉）
	s.Cancel()
	waitFor(t, func() bool { return !s.Busy() })
}

// TestBashHighRiskStillSilentUnderAuto：bash 的高危命令模式在 auto 档不弹
//（2026-10 拍板不回归——ConfirmForced 例外口按名单点名，不「有回调就弹」）。
func TestBashHighRiskStillSilentUnderAuto(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	// git push 命中 HighRiskBashCommand 的高危模式
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash", `{"command":"git push"}`),
		textResult("完成"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("推一下", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.pending) != 0 {
		t.Fatalf("auto 档下 bash 高危命令模式不应弹确认（2026-10 拍板）: %+v", cap.pending)
	}
}
