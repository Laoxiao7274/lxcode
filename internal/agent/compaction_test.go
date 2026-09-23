package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// ---------- 选区间（纯函数） ----------

func TestSelectCompactRangeKeepsTailBudget(t *testing.T) {
	// 五条等长消息：保留预算只够一条 → 区间应为前四条，尾部留一条
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("a", 400)},
		{Role: "assistant", Content: strings.Repeat("b", 400)},
		{Role: "user", Content: strings.Repeat("c", 400)},
		{Role: "assistant", Content: strings.Repeat("d", 400)},
		{Role: "user", Content: strings.Repeat("e", 400)},
	}
	start, end, ok := selectCompactRange(history, 100)
	if !ok {
		t.Fatal("应可选出一个区间")
	}
	if start != 0 {
		t.Fatalf("区间起点恒为 0（历史里没有 system 消息）: %d", start)
	}
	// 每条 400 字节 ≈ 104 tokens；预算 100 只够留一条 → end 应为 len-2
	if end != len(history)-2 {
		t.Fatalf("应只保留最后一条（预算 100 tokens）：end=%d", end)
	}
	// 预算为 0（手动/溢出）：同样至少留一条
	_, end0, ok0 := selectCompactRange(history, 0)
	if !ok0 || end0 != len(history)-2 {
		t.Fatalf("预算 0 应保留最后一条: end=%d ok=%v", end0, ok0)
	}
}

// 配对不变量：区间末尾不能落在 assistant 的 tool_calls 与它的 tool 结果之间。
func TestSelectCompactRangeRespectsToolPairing(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("x", 400)},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("r", 400)},
		{Role: "user", Content: strings.Repeat("y", 400)},
	}
	// 保留预算 200：天然切点落在 tool 结果上（前两条 ≈104，加它才够 200），
	// 那里不配对（声明还没回填）→ 必须往前回退到 user 之后
	_, end, ok := selectCompactRange(history, 200)
	if !ok {
		t.Fatal("应可选区间")
	}
	if end != 0 {
		t.Fatalf("切点必须回退到配对平衡处（end=0），实际 %d", end)
	}
	// 回退后保留下来的尾部必须仍含完整的一对（调用 + 结果）
	tail := history[end+1:]
	if len(tail) != 3 || len(tail[0].ToolCalls) != 1 || tail[1].ToolCallID != "c1" {
		t.Fatalf("尾部应含完整配对: %+v", tail)
	}
}

func TestSelectCompactRangeNothingWhenTooShort(t *testing.T) {
	if _, _, ok := selectCompactRange(nil, 100); ok {
		t.Fatal("空历史不可压")
	}
	// 只有一条消息：区间会空掉（keepFrom <= firstIdx）
	if _, _, ok := selectCompactRange([]llm.Message{{Role: "user", Content: "x"}}, 0); ok {
		t.Fatal("只剩一条时不可压")
	}
	// 历史以未配对调用收尾（用户中断过）：可以压前缀，但**绝不能切开那一对**——
	// 切点只能落在 user 之后，配对整体留在尾部
	unpaired := []llm.Message{
		{Role: "user", Content: strings.Repeat("x", 400)},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
	}
	_, end, ok := selectCompactRange(unpaired, 0)
	if !ok {
		t.Fatal("前缀仍可压")
	}
	if end != 0 {
		t.Fatalf("切点不能切开未配对的调用（end=0），实际 %d", end)
	}
}

func TestCompactionBudgets(t *testing.T) {
	threshold, retain, ok := compactionBudgets(1000)
	if !ok || threshold != 800 || retain != 160 {
		t.Fatalf("预算换算不符: threshold=%d retain=%d ok=%v", threshold, retain, ok)
	}
	// 窗口未知/异常：不压（不猜）
	if _, _, ok := compactionBudgets(0); ok {
		t.Fatal("窗口未知不应压")
	}
	if _, _, ok := compactionBudgets(1); ok {
		t.Fatal("窗口小到两个预算撞在一起不应压")
	}
}

// ---------- 压缩事务 ----------

// compactionEnv 记录一次压缩过程中的可观测面：摘要调用收到的消息 + 主时间线事件。
type compactionEnv struct {
	mu          sync.Mutex
	summaryCall []llm.Message
	events      []Event
}

func (e *compactionEnv) emitter() Emitter {
	return func(ev Event) {
		e.mu.Lock()
		e.events = append(e.events, ev)
		e.mu.Unlock()
	}
}

func (e *compactionEnv) compacted() []CompactedEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []CompactedEvent
	for _, ev := range e.events {
		if c, ok := ev.(CompactedEvent); ok {
			out = append(out, c)
		}
	}
	return out
}

// summaryMessages 返回摘要调用收到的消息序列（nil = 没发生摘要调用）。
func (e *compactionEnv) summaryMessages() []llm.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.summaryCall
}

// compactionStream 造假 LLM：摘要调用（最后一条消息是指令）返回短摘要，
// 普通轮次按脚本回放。
func compactionStream(t *testing.T, env *compactionEnv, script [][]llm.StreamEvent, sum string, summarizeErr bool) StreamFn {
	t.Helper()
	i := 0
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		last := msgs[len(msgs)-1]
		if last.Role == "user" && strings.Contains(last.Content, "上下文压缩引擎") {
			if env != nil {
				env.mu.Lock()
				env.summaryCall = append([]llm.Message(nil), msgs...)
				env.mu.Unlock()
			}
			ch := make(chan llm.StreamEvent, 2)
			if summarizeErr {
				ch <- llm.StreamEvent{Type: llm.EventError, Err: context.Canceled}
			} else {
				ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: sum}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: sum}, FinishReason: llm.FinishStop,
				}}
			}
			close(ch)
			return ch, nil
		}
		if i >= len(script) {
			t.Fatalf("脚本耗尽（未预期的多轮调用）")
		}
		evs := script[i]
		i++
		ch := make(chan llm.StreamEvent, len(evs))
		for _, ev := range evs {
			ch <- ev
		}
		close(ch)
		return ch, nil
	}
}

const summaryText = "## 主要请求与意图\n- 修一个 bug\n\n## 当前工作\n- 正在改 dispatch.go"

// bigHistory 造一段够长的历史（估算值远超保留预算）。
func bigHistory(n int) []llm.Message {
	var out []llm.Message
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		out = append(out, llm.Message{Role: role, Content: strings.Repeat("历史内容", 200)})
	}
	return out
}

func TestCompactReplacesPrefixWithCheckpoint(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	env := &compactionEnv{}
	s.emit = env.emitter()
	s.history = bigHistory(6)
	s.stream = compactionStream(t, env, nil, summaryText, false)

	res, err := s.Compact("")
	if err != nil {
		t.Fatal(err)
	}
	if res.Shadowed == 0 || res.Summary != summaryText {
		t.Fatalf("压缩结果不符: %+v", res)
	}
	if res.After >= res.Before {
		t.Fatalf("压缩后应更小: %d → %d", res.Before, res.After)
	}
	hist := s.History().Messages
	if len(hist) != 2 {
		t.Fatalf("预算 0 应保留检查点 + 最后一条原文: %d 条", len(hist))
	}
	if !isCheckpointContent(hist[0].Content) {
		t.Fatalf("历史首条应是检查点: %q", hist[0].Content)
	}
	if !strings.Contains(hist[0].Content, "修一个 bug") {
		t.Fatalf("检查点应含摘要正文: %q", hist[0].Content)
	}
	if hist[1].Content != bigHistory(6)[5].Content {
		t.Fatal("预算 0 应保留最后一条原文")
	}
	// 历史回放要能把检查点标出来（前端渲染「已压缩历史」块的依据）
	idx := s.History().Checkpoints
	if len(idx) != 1 || idx[0] != 0 {
		t.Fatalf("检查点下标不符: %v", idx)
	}
	// 事件：手动压缩标记 Manual
	got := env.compacted()
	if len(got) != 1 || !got[0].Manual {
		t.Fatalf("应发一条 Manual 的压缩事件: %+v", got)
	}
	// 摘要调用必须带上会话自己的 system 提示词 + 区间历史 + 指令（前缀复用）
	call := env.summaryMessages()
	if len(call) < 3 || call[0].Role != "system" {
		t.Fatalf("摘要调用应以 system 开头: %+v", call)
	}
	if !strings.Contains(call[len(call)-1].Content, "上下文压缩引擎") {
		t.Fatalf("最后一条应是摘要指令: %q", call[len(call)-1].Content)
	}
	if len(call) != res.Shadowed+2 {
		t.Fatalf("摘要调用应重放整个被压缩区间（%d 条 = 影子 %d + system + 指令）: %d 条",
			res.Shadowed+2, res.Shadowed, len(call))
	}
}

// 压缩后占用测量必须跟着更新：否则指示器停在压缩前的数字（真链路实测抓到过）。
func TestCompactUpdatesContextUsage(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	s.history = bigHistory(6)
	// 先造一个真实锚点（跑一轮）
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{textResultWithUsage("好", 5, 90000)}}).stream
	if err := s.Send("你好"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	beforeUsed := s.ContextUsage().Used
	if beforeUsed != 90000 {
		t.Fatalf("前置条件：应有真实锚点: %d", beforeUsed)
	}

	s.stream = compactionStream(t, nil, nil, summaryText, false)
	res, err := s.Compact("")
	if err != nil {
		t.Fatal(err)
	}
	after := s.ContextUsage()
	if after.Used >= beforeUsed {
		t.Fatalf("压缩后占用应下降: %d → %d", beforeUsed, after.Used)
	}
	if after.Window != 100000 {
		t.Fatalf("窗口应保留: %+v", after)
	}
	if after.total() != after.Used {
		t.Fatalf("分类之和应归一到新占用: %+v", after)
	}
	// 锚定算术的语义：新占用 = 旧真实占用 − 被压段 + 检查点（估算差值为正）
	if after.Used == 0 || res.After <= 0 {
		t.Fatalf("压缩结果与占用测量都应有值: res=%+v usage=%+v", res, after)
	}
}

func TestCompactFailClosedOnSummaryError(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	s.history = bigHistory(6)
	before := append([]llm.Message(nil), s.History().Messages...)
	s.stream = compactionStream(t, nil, nil, summaryText, true) // 摘要调用报错

	if _, err := s.Compact(""); err == nil {
		t.Fatal("摘要失败应返回错误")
	}
	if len(s.History().Messages) != len(before) {
		t.Fatalf("摘要失败不得改动历史: %d → %d", len(before), len(s.History().Messages))
	}
}

func TestCompactFailClosedOnNoShrink(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	s.history = bigHistory(6)
	before := append([]llm.Message(nil), s.History().Messages...)
	// 摘要比被替换的区间还长 → 缩水检查必须拒绝（历史未改动）
	s.stream = compactionStream(t, nil, nil, strings.Repeat("很长的摘要内容", 4000), false)

	if _, err := s.Compact(""); err == nil || !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("摘要没缩水应作为「没有收益」拒绝: %v", err)
	}
	if len(s.History().Messages) != len(before) {
		t.Fatal("缩水检查失败不得改动历史")
	}
}

// 自动触发：上一轮真实用量超阈值 → 轮间压缩，然后继续跑下一轮。
func TestMaybeCompactTriggersBetweenRounds(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000) // 阈值 80000 / 保留 16000
	env := &compactionEnv{}
	s.emit = env.emitter()
	// 历史要够长：保留预算 16000 按条估算约 80 条（每条 800 字节 ≈ 204 tokens）。
	// 注意阈值用的是**真实用量**（含系统提示词与工具声明），保留预算用的是
	// **历史条估算**——两者尺度不同，测试里历史必须真的够大才压得动。
	s.history = bigHistory(100)

	// 脚本：第 0 轮发工具调用（真实用量 90000 → 超阈值）→ 摘要 → 第 1 轮收尾
	first := llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
		Message:     llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		UsageTokens: 20, PromptTokens: 90000, FinishReason: llm.FinishToolCalls,
	}}
	s.stream = compactionStream(t, env, [][]llm.StreamEvent{
		{first},
		textResult("收尾"),
	}, summaryText, false)

	if err := s.Send("开始"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	// 历史里应出现检查点（前缀被替换），且摘要调用确实发生了
	if env.summaryMessages() == nil {
		t.Fatal("超阈值应触发压缩（应发生摘要调用）")
	}
	if len(env.compacted()) != 1 {
		t.Fatalf("应广播一条压缩事件: %+v", env.compacted())
	}
	var hasCheckpoint bool
	for _, m := range s.History().Messages {
		if isCheckpointContent(m.Content) {
			hasCheckpoint = true
		}
	}
	if !hasCheckpoint {
		t.Fatal("压缩后历史里应有检查点")
	}
}

// 未超阈值不压（历史原样）。
func TestMaybeCompactBelowThreshold(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000) // 阈值 80000
	env := &compactionEnv{}
	s.emit = env.emitter()
	s.history = bigHistory(100)

	first := llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
		Message:     llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		UsageTokens: 10, PromptTokens: 100, FinishReason: llm.FinishToolCalls,
	}}
	s.stream = compactionStream(t, env, [][]llm.StreamEvent{{first}, textResult("收尾")}, summaryText, false)
	if err := s.Send("开始"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	if env.summaryMessages() != nil {
		t.Fatal("未超阈值不应压缩")
	}
	if len(env.compacted()) != 0 {
		t.Fatalf("未超阈值不应广播压缩事件: %+v", env.compacted())
	}
}

// 窗口未知（模型没配 context_window）：算不出阈值就不压。
func TestMaybeCompactSkipsUnknownWindow(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg) // 不带 ContextWindow
	env := &compactionEnv{}
	s.emit = env.emitter()

	first := llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
		Message:     llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		UsageTokens: 10, PromptTokens: 999999, FinishReason: llm.FinishToolCalls,
	}}
	s.stream = compactionStream(t, env, [][]llm.StreamEvent{{first}, textResult("收尾")}, summaryText, false)
	if err := s.Send("开始"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if env.summaryMessages() != nil {
		t.Fatal("窗口未知不应压缩（不猜阈值）")
	}
}

// 溢出兜底：端点报"超长"→ 强制压一次再重试同一轮。
func TestOverflowForcesCompactionAndRetries(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	s.history = bigHistory(6)
	env := &compactionEnv{}
	s.emit = env.emitter()

	// 第 0 轮直接报超长 → 压缩 → 重试同一轮成功
	overflow := llm.StreamEvent{Type: llm.EventError, Err: &llm.APIError{
		StatusCode: 400, Body: "This model's maximum context length is 128000 tokens",
	}}
	s.stream = compactionStream(t, env, [][]llm.StreamEvent{{overflow}, textResult("重试成功")}, summaryText, false)

	if err := s.Send("开始"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if env.summaryMessages() == nil {
		t.Fatal("端点报超长应触发强制压缩")
	}
	if len(env.compacted()) != 1 {
		t.Fatalf("应广播一条压缩事件: %+v", env.compacted())
	}
	// 重试后的历史应以检查点开头，并以收尾回复结束
	hist := s.History().Messages
	if !isCheckpointContent(hist[0].Content) {
		t.Fatalf("重试后历史首条应是检查点: %+v", hist)
	}
	if last := hist[len(hist)-1]; last.Content != "重试成功" {
		t.Fatalf("重试轮应完成: %q", last.Content)
	}
	// 不应无限重试：事件里不该出现第二次压缩
	if got := env.compacted(); len(got) != 1 {
		t.Fatalf("压缩只应发生一次: %+v", got)
	}
}
