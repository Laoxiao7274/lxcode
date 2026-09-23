package agent

import (
	"context"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// ---------- 估算器（纯函数） ----------

func TestEstimateContextUsageBreakdown(t *testing.T) {
	tc := llm.ToolCall{ID: "c1"}
	tc.Function.Name = "read_file"
	tc.Function.Arguments = `{"path":"a.go"}`
	history := []llm.Message{
		{Role: "user", Content: "读一下文件"},                                                           // → Messages
		{Role: "assistant", Content: "好的", ReasoningContent: "想一下", ToolCalls: []llm.ToolCall{tc}}, // → Messages + Reasoning
		{Role: "tool", ToolCallID: "c1", Content: "文件内容很长很长很长很长很长很长很长"},                            // → ToolResults
	}
	toolsWire := []llm.Tool{{Name: "read_file", Description: "读文件", Parameters: []byte(`{"type":"object"}`)}}
	u := estimateContextUsage("系统提示词内容", toolsWire, history)

	if u.System <= 0 {
		t.Fatalf("系统提示词（含工具声明）应计价: %+v", u)
	}
	if u.Messages <= 0 || u.ToolResults <= 0 || u.Reasoning <= 0 {
		t.Fatalf("三个分类都应非零: %+v", u)
	}
	if u.Used != u.total() {
		t.Fatalf("估算路径下 Used 应等于分类之和: used=%d total=%d", u.Used, u.total())
	}
	// 空历史只算 system（不该凭空产生分类）
	empty := estimateContextUsage("x", nil, nil)
	if empty.ToolResults != 0 || empty.Messages != 0 || empty.Reasoning != 0 {
		t.Fatalf("空历史不应有分类占用: %+v", empty)
	}
}

func TestContextUsageAnchoredToRealUsage(t *testing.T) {
	est := ContextUsage{System: 100, ToolResults: 300, Messages: 500, Reasoning: 100}
	est.Used = est.total() // 1000

	// 真实用量替换总量，分类等比缩放且**之和恒等于 Used**（UI 环形与占比条
	// 不能互相矛盾）
	got := est.anchoredTo(2500)
	if got.Used != 2500 {
		t.Fatalf("Used 应取真实值: %+v", got)
	}
	if got.total() != 2500 {
		t.Fatalf("分类之和应归一到 Used: %+v（total=%d）", got, got.total())
	}
	if got.System >= got.ToolResults || got.ToolResults >= got.Messages {
		t.Fatalf("缩放应保持比例关系: %+v", got)
	}

	// provider 不回报用量：原样保留估算（Used 不变）
	if same := est.anchoredTo(0); same.Used != 1000 {
		t.Fatalf("无真实用量时不应改动: %+v", same)
	}
	// 有真实总量但分类全零：全部记进 Messages，不为凑数编造分类
	zero := ContextUsage{}.anchoredTo(42)
	if zero.Messages != 42 || zero.total() != 42 {
		t.Fatalf("空分类应全部记入 Messages: %+v", zero)
	}
}

// ---------- 会话接线 ----------

// bindDefaultWithWindow 绑定带上下文窗口的 default 模型（压缩阈值要靠它）。
func bindDefaultWithWindow(t *testing.T, reg *config.Registry, window int) {
	t.Helper()
	m := config.ModelConfig{
		ID: "m1", BaseURL: "http://localhost:1", Model: "test-model",
		Format: config.FormatOpenAI, Enabled: true, ContextWindow: window,
		Capabilities: config.Capabilities{Tools: true},
	}
	if err := reg.Add(m); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
}

// textResultWithUsage 造一段带真实用量的假流（provider 回报的 prompt_tokens）。
func textResultWithUsage(content string, usage, prompt int) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.EventText, TextDelta: content},
		{Type: llm.EventDone, Result: &llm.ChatResult{
			Message:     llm.Message{Role: "assistant", Content: content},
			UsageTokens: usage, PromptTokens: prompt, FinishReason: llm.FinishStop,
		}},
	}
}

func TestSessionRecordsRealPromptTokens(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 32768)
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("好", 12, 777)}}).stream

	var mu = make(chan ContextUsage, 1)
	s.emit = func(ev Event) {
		if e, ok := ev.(TurnDoneEvent); ok {
			mu <- e.Context
		}
	}
	if err := s.Send("你好"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	got := s.ContextUsage()
	if got.Used != 777 {
		t.Fatalf("应记录 provider 回报的真实 prompt_tokens: %+v", got)
	}
	if got.Window != 32768 {
		t.Fatalf("窗口应取自模型配置: %+v", got)
	}
	if got.total() != 777 {
		t.Fatalf("分类之和应归一到真实总量: %+v（total=%d）", got, got.total())
	}
	// 事件携带同一份测量（宿主据此实时更新指示器）
	select {
	case evCtx := <-mu:
		if evCtx.Used != 777 || evCtx.Window != 32768 {
			t.Fatalf("TurnDoneEvent 应带上下文占用: %+v", evCtx)
		}
	default:
		t.Fatal("应收到 TurnDoneEvent")
	}
	// 快照同样携带（重连时客户端不必等下一轮）
	if snap := s.History(); snap.Context.Used != 777 {
		t.Fatalf("Snapshot 应带上下文占用: %+v", snap.Context)
	}
}

func TestSessionContextUsageFallsBackToEstimate(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 8192)
	// provider 不回报 usage（PromptTokens=0）→ 用估算值兜底，不能是 0
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{textResult("一段回复")}}).stream

	if err := s.Send("一段足够长的用户消息，用来让估算值明显大于零"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	got := s.ContextUsage()
	if got.Used <= 0 {
		t.Fatalf("无真实用量时应回落到估算: %+v", got)
	}
	if got.total() != got.Used {
		t.Fatalf("估算路径下分类之和应等于 Used: %+v", got)
	}
	if got.Window != 8192 {
		t.Fatalf("窗口应来自模型配置: %+v", got)
	}
}

// 子轮（dispatch 的子上下文）的占用不属于主会话压力——主指示器不能被它覆盖。
func TestSubRoundDoesNotTouchMainContextUsage(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	// 主轮先记下一个真实值
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("主", 5, 4321)}}).stream
	if err := s.Send("主轮"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if s.ContextUsage().Used != 4321 {
		t.Fatalf("主轮应记录用量: %+v", s.ContextUsage())
	}

	// 直接跑一轮子语境（dispatchID 非空）——子上下文有自己的窗口
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("子", 5, 99999)}}).stream
	if _, err := s.streamRound(context.Background(), "", "", nil, []llm.Message{{Role: "user", Content: "子任务"}}, "d1"); err != nil {
		t.Fatal(err)
	}
	if got := s.ContextUsage(); got.Used != 4321 {
		t.Fatalf("子轮不应覆盖主会话占用: %+v", got)
	}
}

func TestSessionContextUsageResetsOnSwitch(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 32768)
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("好", 5, 1234)}}).stream
	if err := s.Send("你好"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if s.ContextUsage().Used == 0 {
		t.Fatal("前置条件：应有用量")
	}
	if _, err := s.SwitchNew(""); err != nil {
		t.Fatal(err)
	}
	if got := s.ContextUsage(); got.Used != 0 || got.Window != 0 {
		t.Fatalf("新会话应清空占用测量（沿用旧值会误导压力判定）: %+v", got)
	}
}
