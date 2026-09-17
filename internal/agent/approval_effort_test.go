package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// ---------- 权限模式三档 ----------

// toolEventCapture 收集工具结果与确认请求事件。
type toolEventCapture struct {
	mu      sync.Mutex
	results []ToolResultEvent
	pending []ConfirmRequestEvent
}

func (c *toolEventCapture) handle(ev Event) {
	if e, ok := ev.(ToolResultEvent); ok {
		c.mu.Lock()
		c.results = append(c.results, e)
		c.mu.Unlock()
	}
	if e, ok := ev.(ConfirmRequestEvent); ok {
		c.mu.Lock()
		c.pending = append(c.pending, e)
		c.mu.Unlock()
	}
}

// TestApprovalStrictRejectsMutatingTools：strict 模式下 bash/edit/write_file
// 直接拒绝（错误回填模型），读取类工具不受影响——edit 是低危但变更文件，
// 必须被拒（Mutates 与 Risk 正交，按 Mutates 判定）。
func TestApprovalStrictRejectsMutatingTools(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash", `{"command":"echo hi"}`),
		toolCallResult("edit", `{"path":"a.txt","old_string":"x","new_string":"y"}`),
		toolCallResult("read_file", `{"path":"no-such.txt"}`),
		textResult("受限完成"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("干活", WithApproval("strict")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.pending) != 0 {
		t.Fatalf("strict 模式不应产生确认请求（只读无放行语义）: %+v", cap.pending)
	}
	if len(cap.results) != 3 {
		t.Fatalf("应有 3 个工具结果: %+v", cap.results)
	}
	if !cap.results[0].IsError || !strings.Contains(cap.results[0].Content, "只读模式") {
		t.Fatalf("bash 应被只读模式拒绝: %+v", cap.results[0])
	}
	if !cap.results[1].IsError || !strings.Contains(cap.results[1].Content, "只读模式") {
		t.Fatalf("edit（低危但变更文件）也应被拒绝: %+v", cap.results[1])
	}
	// read_file 正常执行（文件不存在是执行结果，不是模式拒绝）
	if cap.results[2].IsError && strings.Contains(cap.results[2].Content, "只读模式") {
		t.Fatalf("read_file 不应被模式拒绝: %+v", cap.results[2])
	}
}

// TestApprovalAutoSkipsConfirmGate：auto 模式高危工具跳过确认直接执行。
func TestApprovalAutoSkipsConfirmGate(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash", `{"command":"echo auto-mode"}`),
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
	defer cap.mu.Unlock()
	if len(cap.pending) != 0 {
		t.Fatalf("auto 模式不应有确认请求: %+v", cap.pending)
	}
	if len(cap.results) != 1 || !strings.Contains(cap.results[0].Content, "auto-mode") {
		t.Fatalf("bash 应直接执行: %+v", cap.results)
	}
}

// TestApprovalConfirmIsDefault：不带参数 = confirm（现行默认语义不回归）。
func TestApprovalConfirmIsDefault(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("bash", `{"command":"echo x"}`),
		textResult("好"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	// 不带 WithApproval（CLI / 旧客户端路径）
	if err := s.Send("跑"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		return len(cap.pending) > 0
	})
	var gotPending bool
	cap.mu.Lock()
	gotPending = len(cap.pending) > 0
	cap.mu.Unlock()
	if !gotPending {
		t.Fatal("默认模式 bash 应触发确认（现行语义）")
	}
	s.Cancel()
	waitFor(t, func() bool { return !s.Busy() })
}

// ---------- effort 端到端（真 streamWithLLM 对 httptest 端点）----------

// TestEffortReachesOpenAIRequest：声明 reasoning 能力的模型 + effort →
// 请求体含 reasoning_effort；未声明能力的模型 → 不含（对非推理端点传参
// 会直接 400，能力门控是硬需求）。走真 streamWithLLM（不注入假流），
// 完整穿过 streamRound → ChatAuto → 请求体。
func TestEffortReachesOpenAIRequest(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":1}}`))
	}))
	defer srv.Close()

	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := config.ModelConfig{
		ID: "m1", BaseURL: srv.URL, Model: "test-model",
		Format: config.FormatOpenAI, Enabled: true,
		Capabilities: config.Capabilities{Tools: true, Reasoning: true},
	}
	if err := reg.Add(m); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}

	s := New(reg, tools.New(), nil)
	t.Cleanup(s.Close)
	// s.stream 默认即 streamWithLLM——完整链路

	if err := s.Send("你好", WithEffort("high")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	// 未声明能力的模型：effort 不进请求体
	m2 := config.ModelConfig{
		ID: "m2", BaseURL: srv.URL, Model: "plain-model",
		Format: config.FormatOpenAI, Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}
	if err := reg.Add(m2); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Send("再来", WithEffort("high")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("应发出两个请求: %d", len(bodies))
	}
	if bodies[0]["reasoning_effort"] != "high" {
		t.Fatalf("声明 reasoning 能力 + effort=high → 请求体应含 reasoning_effort: %v", bodies[0])
	}
	if _, has := bodies[1]["reasoning_effort"]; has {
		t.Fatalf("未声明 reasoning 能力时不应传 reasoning_effort（非推理端点会 400）: %v", bodies[1])
	}
}

// TestEffortWithoutCapabilityIgnoredSilently：effort 档位在协议层已校验，
// 这里钉住 session 层不会因档位/能力组合报错（静默不适用 = 可接受的
// 降级——UI 只对声明能力的模型展示选择器）。
func TestEffortWithoutCapabilityIgnoredSilently(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg) // 无 reasoning 能力
	fake := &fakeStream{script: [][]llm.StreamEvent{textResult("普通回复")}}
	s.stream = fake.stream
	if err := s.Send("问", WithEffort("medium")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
}
