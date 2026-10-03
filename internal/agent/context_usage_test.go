package agent

import (
	"context"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
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
	if u.Used != usageTotal(u) {
		t.Fatalf("估算路径下 Used 应等于分类之和: used=%d total=%d", u.Used, usageTotal(u))
	}
	// 空历史只算 system（不该凭空产生分类）
	empty := estimateContextUsage("x", nil, nil)
	if empty.ToolResults != 0 || empty.Messages != 0 || empty.Reasoning != 0 {
		t.Fatalf("空历史不应有分类占用: %+v", empty)
	}
}

func TestContextUsageAnchoredToRealUsage(t *testing.T) {
	est := ContextUsage{System: 100, ToolResults: 300, Messages: 500, Reasoning: 100}
	est.Used = usageTotal(est) // 1000

	// 真实用量替换总量，分类等比缩放且**之和恒等于 Used**（UI 环形与占比条
	// 不能互相矛盾）
	got := anchoredUsage(est, 2500)
	if got.Used != 2500 {
		t.Fatalf("Used 应取真实值: %+v", got)
	}
	if usageTotal(got) != 2500 {
		t.Fatalf("分类之和应归一到 Used: %+v（total=%d）", got, usageTotal(got))
	}
	if got.System >= got.ToolResults || got.ToolResults >= got.Messages {
		t.Fatalf("缩放应保持比例关系: %+v", got)
	}

	// provider 不回报用量：原样保留估算（Used 不变）
	if same := anchoredUsage(est, 0); same.Used != 1000 {
		t.Fatalf("无真实用量时不应改动: %+v", same)
	}
	// 有真实总量但分类全零：全部记进 Messages，不为凑数编造分类
	zero := anchoredUsage(ContextUsage{}, 42)
	if zero.Messages != 42 || usageTotal(zero) != 42 {
		t.Fatalf("空分类应全部记入 Messages: %+v", zero)
	}
}

// ---------- 投影（测量之后历史又长了多少） ----------

// TestProjectedUsageFollowsSurface：展示值 = 真实锚点 + 测量之后历史的变化量
// （DSH 的 projectedTokens）。没有这一步，工具跑得越久指示器偏得越多。
func TestProjectedUsageFollowsSurface(t *testing.T) {
	base := []llm.Message{{Role: "user", Content: "你好"}}
	sampled := historyTokens(base)
	u := ContextUsage{
		Used: 1000, Window: 8192, System: 300, Messages: 700,
		SampledTokens: sampled,
	}

	// 历史没变：原样返回（不投影、不重算）
	if same := projectedUsage(u, base); same != u {
		t.Fatalf("历史没变时不该改动: %+v", same)
	}

	// 历史长了一条工具结果：展示值跟着涨，且分类之和仍等于 Used
	grew := append(append([]llm.Message(nil), base...), llm.Message{Role: "tool", ToolCallID: "c1", Content: "结果"})
	got := projectedUsage(u, grew)
	delta := historyTokens(grew) - sampled
	if delta <= 0 {
		t.Fatalf("夹具前提：历史应真的变长了（delta=%d）", delta)
	}
	if got.Used != 1000+delta {
		t.Fatalf("展示值应 = 锚点 + 历史增量（want=%d）: %+v", 1000+delta, got)
	}
	if usageTotal(got) != got.Used {
		t.Fatalf("投影后分类之和仍应等于 Used: %+v（total=%d）", got, usageTotal(got))
	}
	if got.ToolResults <= 0 {
		t.Fatalf("新增的工具结果应落进 ToolResults 分类: %+v", got)
	}
	if got.Estimated {
		t.Fatalf("锚点是真实用量 → 投影后仍不是估算值（Estimated 标的是「整份都是估的」）: %+v", got)
	}
	if got.Window != 8192 {
		t.Fatalf("窗口应原样保留: %+v", got)
	}

	// 历史变短（锚点没跟着重算的那种情形）：展示值按**被移除段的估算**下降
	//（不是降到 0——锚点里还有 system/tools 与 provider 与估算的差额）
	shorter := projectedUsage(u, nil)
	if shorter.Used != 1000-sampled {
		t.Fatalf("历史清空后展示值应减去被移除段的估算（want=%d）: %+v", 1000-sampled, shorter)
	}
	// 减成负数时钳到 0（不出现负的占用）
	tiny := ContextUsage{Used: 10, Window: 8192, Messages: 10, SampledTokens: 1000}
	if got := projectedUsage(tiny, nil); got.Used != 0 {
		t.Fatalf("展示值不能为负: %+v", got)
	}
}

// TestProjectedUsageWithoutBaseline：没有采样基线/没有锚点时不投影——算不出增量，
// 编一个"大概长了一点"就是编数字。
func TestProjectedUsageWithoutBaseline(t *testing.T) {
	grew := []llm.Message{{Role: "user", Content: "你好"}, {Role: "assistant", Content: "回复"}}
	// 老库里的测量（没有 sampled_tokens 字段）
	legacy := ContextUsage{Used: 1000, Window: 8192, Messages: 1000}
	if got := projectedUsage(legacy, grew); got != legacy {
		t.Fatalf("没有采样基线时不该投影: %+v", got)
	}
	// 本会话还没跑过主轮（Used=0）
	none := ContextUsage{Window: 8192}
	if got := projectedUsage(none, grew); got != none {
		t.Fatalf("没有真实锚点时不该投影: %+v", got)
	}
	// 库里没有测量、按历史回落出来的估算值（persist.go 的第 2 条路）：它本来就是按
	// **当前**历史算出来的，没有基线也不该投影——再加一次增量等于把同一段历史算两遍
	est := ContextUsage{Used: 1000, Window: 8192, Messages: 1000, Estimated: true}
	if got := projectedUsage(est, grew); got != est {
		t.Fatalf("回落估算值不该投影: %+v", got)
	}
	// 但**有基线**的估算锚点要投影：端点不回报 usage 时锚点本身就是估算的，
	// 它同样会随着历史变长而过时（估的锚点 + 估的增量，Estimated 保持为真）
	estAnchored := ContextUsage{Used: 1000, Window: 8192, Messages: 1000, Estimated: true, SampledTokens: historyTokens(grew)}
	grown := append(append([]llm.Message(nil), grew...), llm.Message{Role: "tool", Content: "又一条结果"})
	gotEst := projectedUsage(estAnchored, grown)
	if gotEst.Used <= 1000 || !gotEst.Estimated {
		t.Fatalf("有基线的估算锚点也要投影，且仍标估算: %+v", gotEst)
	}
}

// TestProjectionDoesNotFeedCompaction：投影值只给用户看，**判定**（压缩阈值）读的是真实锚点。
//
// 为什么这是一条硬纪律：投影值里含估算的历史增量，拿它去比阈值会让压缩在"其实还没到"
// 的时候触发（用户会看到一次没有必要的摘要调用 + 历史被改写）。DSH 同样把两者分开
// （pressureTokens 判定、projectedTokens 展示）。
func TestProjectionDoesNotFeedCompaction(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000) // 阈值 80000 / 保留 16000
	s.history = bigHistory(100)           // 估算约 60400 tokens：够压得动

	// 锚点**刚好在阈值之下**，但历史比采样基线长得多 → 投影值远超阈值
	s.context = ContextUsage{Used: 79999, Window: 100000, Messages: 79999, SampledTokens: 1}
	if projected := s.History().Context.Used; projected <= 80000 {
		t.Fatalf("前置条件：投影值应超过阈值（实际 %d）", projected)
	}

	var called bool
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		called = true
		return (&fakeStream{script: [][]llm.StreamEvent{textResult("不该发生")}}).stream(ctx, m, msgs, opts)
	}
	s.maybeCompact(context.Background(), nil, "")
	if called {
		t.Fatal("投影值超阈值不该触发压缩——判定读的是真实锚点（79999 < 80000）")
	}

	// 正对照：把锚点抬到阈值之上，同一套装置**必须**触发（否则上面的断言什么也没证明）
	s.context.Used = 80001
	s.maybeCompact(context.Background(), nil, "")
	if !called {
		t.Fatal("锚点超阈值必须触发压缩（正对照）")
	}
}

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

	// 锚点 = provider 回报的真实 prompt_tokens（判定用：压缩阈值比的就是它）
	got := s.ContextUsage()
	if got.Used != 777 {
		t.Fatalf("应记录 provider 回报的真实 prompt_tokens: %+v", got)
	}
	if got.Window != 32768 {
		t.Fatalf("窗口应取自模型配置: %+v", got)
	}
	if usageTotal(got) != 777 {
		t.Fatalf("分类之和应归一到真实总量: %+v（total=%d）", got, usageTotal(got))
	}
	// 采样基线 = 那次请求实际发出去的历史的估算量（本次只有那条 user 消息）
	if want := historyTokens(s.History().Messages[:1]); got.SampledTokens != want {
		t.Fatalf("采样基线应记下请求那一刻的历史估算量（want=%d）: %+v", want, got)
	}

	// 展示值 = 锚点 + 测量之后历史的变化量（助手回复被 append 进去了）：
	// 预测"下一次请求的 prompt 有多大"，比停在请求那一刻更准（DSH 的 projectedTokens）
	wantProjected := 777 + estimateMessageTokens(llm.Message{Role: "assistant", Content: "好"})
	var evCtx ContextUsage
	select {
	case evCtx = <-mu:
		if evCtx.Used != wantProjected || evCtx.Window != 32768 {
			t.Fatalf("TurnDoneEvent 应带投影后的占用（want used=%d）: %+v", wantProjected, evCtx)
		}
		if usageTotal(evCtx) != wantProjected {
			t.Fatalf("投影后分类之和仍应等于 Used: %+v", evCtx)
		}
	default:
		t.Fatal("应收到 TurnDoneEvent")
	}
	// 快照与事件必须同一份数字（live vs replay 分叉是本仓库吃过三次亏的坑）
	if snap := s.History(); snap.Context != evCtx {
		t.Fatalf("Snapshot 与 TurnDoneEvent 应同一份数字: snap=%+v ev=%+v", snap.Context, evCtx)
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
	if usageTotal(got) != got.Used {
		t.Fatalf("估算路径下分类之和应等于 Used: %+v", got)
	}
	if got.Window != 8192 {
		t.Fatalf("窗口应来自模型配置: %+v", got)
	}
}

// 每个会话记**自己**的占用：子会话的轮次记进子会话自己的 context，主会话那份不被碰。
//
// 2026-09-30 改写：原用例在**同一个** Session 上直接跑一轮 `dispatchID="d1"`，断言"子轮不覆盖
// 主会话占用"。但真链路里子轮跑在**子会话自己的 Session** 上（`runDispatch` → `child.SendWait`，
// 见 AGENTS.md §2.3），而且 `runTurn` 传给 `streamRound` 的 dispatchID 恒为空串（归属是
// `childEmitter` 事后盖的）——所以那个场景生产里不存在。真正要守的不变量是"各记各的"：
// 子会话记它自己的，主会话的数字纹丝不动。
func TestSubRoundDoesNotTouchMainContextUsage(t *testing.T) {
	parent, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	// 主轮先记下一个真实值
	parent.stream = (&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("主", 5, 4321)}}).stream
	if err := parent.Send("主轮"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !parent.Busy() })
	if parent.ContextUsage().Used != 4321 {
		t.Fatalf("主轮应记录用量: %+v", parent.ContextUsage())
	}

	// 子会话是**另一个 Session**（真链路就是如此：dispatch 给子会话自己的 Session）。
	// 复用同一份注册表（模型已绑好），不重复 Add——重复注册同一个模型 id 会直接报错。
	child := New(reg, tools.New(), nil)
	t.Cleanup(child.Close)
	child.stream = (&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("子", 5, 99999)}}).stream
	if _, err := child.streamRound(context.Background(), "", "", nil, []llm.Message{{Role: "user", Content: "子任务"}}, "d1"); err != nil {
		t.Fatal(err)
	}
	// 子会话记下它自己的占用（子会话页要显示的就是这一份）
	if got := child.ContextUsage(); got.Used != 99999 {
		t.Fatalf("子会话应记录自己的占用: %+v", got)
	}
	// 主会话那份纹丝不动
	if got := parent.ContextUsage(); got.Used != 4321 {
		t.Fatalf("子轮的占用不该写进主会话: %+v", got)
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
