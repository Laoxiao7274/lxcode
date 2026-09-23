// 派发时的审批取严（2026-09-23）：子 Agent 的执行面不大于请求方——
// 父轮的授权面与子 Agent 自己的权限默认取更严的一档。
//
// 为什么单独钉：effectiveApproval 是"请求级优先"（UI 里选 auto 就是这一轮不要
// 确认，见 compose_test.go 的表），而派发时"请求级"就是父轮的审批——直接透传会把
// 子 Agent 更严的默认放大成父轮的 auto（代码注释写着取严而实现不是，自相矛盾）。
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestStricterApproval：取严表——auto < confirm < strict；空值 = 未声明默认，
// 按"继承另一档"处理。
func TestStricterApproval(t *testing.T) {
	cases := []struct{ parent, child, want string }{
		{"auto", "confirm", "confirm"}, // 子更严 → 取子（父轮 auto 不放大子默认）
		{"confirm", "auto", "confirm"}, // 父更严 → 取父
		{"auto", "strict", "strict"},
		{"strict", "auto", "strict"},
		{"confirm", "strict", "strict"},
		{"strict", "confirm", "strict"},
		{"auto", "", "auto"},     // 子未声明默认 → 继承请求方
		{"", "strict", "strict"}, // 请求方未声明 → 取子默认
		{"confirm", "confirm", "confirm"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := stricterApproval(c.parent, c.child); got != c.want {
			t.Fatalf("stricterApproval(%q,%q) = %q, want %q", c.parent, c.child, got, c.want)
		}
	}
}

// newApprovalDispatchSession 造「主 Agent 派发 → 子 Agent 跑 bash」的会话：
// childApproval 是子 Agent 自己的权限默认（空 = 未声明）。
// 返回会话与事件捕获面（工具结果 + 确认请求）。
func newApprovalDispatchSession(t *testing.T, childApproval string) (*Session, *toolEventCapture) {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	cap := &toolEventCapture{}
	s := New(reg, tools.New(), cap.handle)
	t.Cleanup(s.Close)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main": {
			Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true,
				Tools: []string{"agent.dispatch"}, Delegates: []string{"coder"}},
			Delegates: []sessiondata.AgentDef{
				{ID: "coder", Name: "代码 Agent", Desc: "跑命令", Enabled: true},
			},
		},
		"coder": {
			Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6",
				Tools: []string{"bash"}, Approval: childApproval},
		},
	}})
	s.SetStream(approvalDispatchStream(t))
	return s, cap
}

// approvalDispatchStream 造假流：主轮派发一次 → 子会话调 bash（echo 一个标记）。
// 子会话的第二轮（bash 结果回来后的收尾）给文本，避免脚本耗尽。
func approvalDispatchStream(t *testing.T) StreamFn {
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
				tc.Function.Name = "agent.dispatch"
				tc.Function.Arguments = `{"agent":"coder","task":"跑一条命令"}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		subRound++
		go func() {
			defer close(ch)
			if subRound > 1 {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "子会话收尾。"}, FinishReason: llm.FinishStop}}
				return
			}
			tc := llm.ToolCall{ID: "sub-a1"}
			tc.Function.Name = "bash"
			tc.Function.Arguments = `{"command":"echo approval-marker"}`
			ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
		}()
		return ch, nil
	}
}

// TestDispatchChildApprovalTakesStricter：父轮 auto + 子 Agent 默认 confirm 时，
// 子 Agent 的高危调用（bash）仍走确认门，并且确认请求带上 dispatch_id 归属进卡；
// 未裁决前命令不执行。
func TestDispatchChildApprovalTakesStricter(t *testing.T) {
	s, cap := newApprovalDispatchSession(t, "confirm")
	if err := s.Send("派活", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	// 子会话挂起等父会话裁决（取严后的 confirm 走确认门）
	waitFor(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		return len(cap.pending) > 0
	})
	cap.mu.Lock()
	pending := cap.pending[0]
	results := append([]ToolResultEvent(nil), cap.results...)
	cap.mu.Unlock()
	if pending.Request.Name != "bash" {
		t.Fatalf("子 Agent 的 bash 应走确认门: %+v", pending.Request)
	}
	if pending.Request.DispatchID == "" {
		t.Fatal("子会话的确认请求应带上 dispatch_id（卡内呈现）")
	}
	for _, r := range results {
		if strings.Contains(r.Content, "approval-marker") {
			t.Fatalf("未裁决前不该执行子 Agent 的命令: %+v", r)
		}
	}
	// 拒绝 → 命令不执行，子会话照旧收尾（不是卡死）
	if err := s.Confirm(pending.Request.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	cap.mu.Lock()
	defer cap.mu.Unlock()
	for _, r := range cap.results {
		if strings.Contains(r.Content, "approval-marker") {
			t.Fatalf("拒绝后不该执行: %+v", r)
		}
	}
}

// TestDispatchChildWithoutApprovalInheritsRequest：子 Agent 未声明权限默认时按
// 继承请求方处理——父轮 auto 下不产生多余确认（取严不等于一律收紧）。
func TestDispatchChildWithoutApprovalInheritsRequest(t *testing.T) {
	s, cap := newApprovalDispatchSession(t, "")
	if err := s.Send("派活", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.pending) != 0 {
		t.Fatalf("子 Agent 未声明默认时不应产生确认请求（继承父轮 auto）: %+v", cap.pending)
	}
	var ran bool
	for _, r := range cap.results {
		if strings.Contains(r.Content, "approval-marker") {
			ran = true
		}
	}
	if !ran {
		t.Fatalf("子 Agent 的命令应直接执行（父轮 auto + 无更严默认）: %+v", cap.results)
	}
}
