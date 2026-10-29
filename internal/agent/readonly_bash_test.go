// 调研 Agent 的执行面（2026-12 用户反馈）：**只读 bash 在 strict 档下可用**。
//
// 为什么单独钉：调研 Agent 的 approval=strict，strict 的语义是「变更类工具直接
// 拒绝」——只读 bash 之所以能过，是因为它 `Mutates=false`（工具层的结构声明），
// 而不是权限门对它开了特例。这条链路一旦被谁改回 `Mutates=true`，调研 Agent 就
// 会在运行期被拒（用户看到的现象又是「读不了本地文件」），而种子测试抓不到
// （它只看白名单，不看运行期判定）。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// TestStrictAllowsReadOnlyBashInChild：strict 子会话里 bash_readonly 正常执行、
// 完整 bash 照旧被拒。
func TestStrictAllowsReadOnlyBashInChild(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"researcher": {Def: sessiondata.AgentDef{
			ID: "researcher", Name: "调研 Agent", Enabled: true, Approval: "strict",
			Tools: []string{"bash_readonly", "bash"},
		}},
	}})
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash_readonly", `{"command":"echo ro-marker"}`),
		toolCallResult("bash", `{"command":"echo full-marker"}`),
		textResult("结论"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("调研", WithAgent("researcher"), WithApproval("strict")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.pending) != 0 {
		t.Fatalf("strict 档不该弹确认（只读 bash 无确认门）: %+v", cap.pending)
	}
	var sawRO, sawFullReject bool
	for _, r := range cap.results {
		switch r.Name {
		case "bash_readonly":
			if r.IsError {
				t.Fatalf("只读 bash 在 strict 档下应可用: %+v", r)
			}
			if !strings.Contains(r.Content, "ro-marker") {
				t.Fatalf("只读 bash 应真的执行了命令: %+v", r)
			}
			sawRO = true
		case "bash":
			if !r.IsError || !strings.Contains(r.Content, "只读模式") {
				t.Fatalf("完整 bash 在 strict 档下应被拒: %+v", r)
			}
			sawFullReject = true
		}
	}
	if !sawRO || !sawFullReject {
		t.Fatalf("应看到只读 bash 放行 + 完整 bash 被拒: ro=%v fullReject=%v results=%+v",
			sawRO, sawFullReject, cap.results)
	}
}

// TestReadOnlyBashRejectsWritesInStrictSession：strict 子会话里，只读 bash 的
// 命令级判据仍然生效（工具层的拒绝理由回填给模型，并给出该派给谁）。
func TestReadOnlyBashRejectsWritesInStrictSession(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"researcher": {Def: sessiondata.AgentDef{
			ID: "researcher", Name: "调研 Agent", Enabled: true, Approval: "strict",
			Tools: []string{"bash_readonly"},
		}},
	}})
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash_readonly", `{"command":"rm -rf build"}`),
		textResult("结论"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("调研", WithAgent("researcher"), WithApproval("strict")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.results) != 1 {
		t.Fatalf("应有一个工具结果: %+v", cap.results)
	}
	// 工具执行层的拒绝以「错误: …」文本回填（与 Registry.Execute 的既有约定一致，
	// IsError 只用于权限门拒绝），所以断言看正文。
	got := cap.results[0]
	if !strings.Contains(got.Content, "错误:") || !strings.Contains(got.Content, "只读") {
		t.Fatalf("写命令应被只读判据拒绝: %+v", got)
	}
	if !strings.Contains(got.Content, "代码 Agent") {
		t.Fatalf("拒绝理由应给出路（该派给谁）: %+v", got)
	}
}
