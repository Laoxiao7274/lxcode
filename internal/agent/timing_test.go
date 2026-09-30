// 每轮生成的计时与吞吐（2026-09-30 用户需求："缺少首 token 吞吐速度这些展示"）。
//
// 三条口径在这里钉死：
//   - 首 token 延迟只在**真的看到文字/思考增量**时才有值；没有增量（工具轮/空回）或
//     这轮是非流式回放时，字段整键缺席（0）——不填 0 冒充"0ms 首字"；
//   - 耗时是"请求发出 → 收尾"（含首 token 延迟）；
//   - 输出 token 只认 provider 回报的 usage，拿不到就缺席（估算的 tok/s 是编数据）。
//
// 落库回读也在这里钉：live（内存/事件）与 replay（Load）必须是同一份数字——本仓库
// 为"两条路径不一致"吃过三次亏（子 Agent 卡退化、路径分叉、seq 只在一条路径上）。
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// delayedStream 造"隔一段延迟才吐第一个增量"的假流：首 token 延迟必须真的可测
// （本机毫秒级 sleep 足够——真实端点的首字延迟是几十到几千毫秒）。
func delayedStream(delay time.Duration, first llm.StreamEvent, rest ...llm.StreamEvent) StreamFn {
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, len(rest)+1)
		go func() {
			defer close(ch)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			ch <- first
			for _, ev := range rest {
				ch <- ev
			}
		}()
		return ch, nil
	}
}

// assistantMsgAt 取历史里最后一条 assistant 消息（计时字段挂在它上面）。
func assistantMsgAt(t *testing.T, msgs []llm.Message) llm.Message {
	t.Helper()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			return msgs[i]
		}
	}
	t.Fatal("历史里没有 assistant 消息")
	return llm.Message{}
}

// ---------- 首 token 延迟 ----------

func TestRoundTimingRecordsFirstToken(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	// 首字延迟 20ms：假流真的等一会儿才吐第一个增量
	s.stream = delayedStream(20*time.Millisecond,
		llm.StreamEvent{Type: llm.EventText, TextDelta: "你好"},
		llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message:     llm.Message{Role: "assistant", Content: "你好"},
			UsageTokens: 20, FinishReason: llm.FinishStop}})

	// live 路径（chat.done）也要带同一份数字：不等刷新就能显示
	done := make(chan TurnDoneEvent, 1)
	s.emit = func(ev Event) {
		if e, ok := ev.(TurnDoneEvent); ok {
			select {
			case done <- e:
			default:
			}
		}
	}
	if err := s.Send("打个招呼"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msg := assistantMsgAt(t, s.History().Messages)
	if msg.FirstTokenMs <= 0 {
		t.Fatalf("有增量时应记录首 token 延迟（> 0）: %+v", msg)
	}
	if msg.FirstTokenMs < 20 {
		t.Fatalf("首 token 延迟应不小于假流注入的延迟（20ms）: %d", msg.FirstTokenMs)
	}
	if msg.DurationMs < msg.FirstTokenMs {
		t.Fatalf("总耗时应不小于首 token 延迟: first=%d duration=%d", msg.FirstTokenMs, msg.DurationMs)
	}
	if msg.Model != "m1" {
		t.Fatalf("应记录本轮实际用的模型 id: %q", msg.Model)
	}
	if msg.UsageTokens != 20 {
		t.Fatalf("应记录 provider 回报的输出 token: %d", msg.UsageTokens)
	}
	// 事件里那份与历史里那份**同源**（同一个字段、同一组数字）
	select {
	case e := <-done:
		if e.Message.FirstTokenMs != msg.FirstTokenMs || e.Message.DurationMs != msg.DurationMs ||
			e.Message.Model != msg.Model || e.Message.UsageTokens != msg.UsageTokens {
			t.Fatalf("TurnDoneEvent 的计时与历史不一致: %+v vs %+v", e.Message, msg)
		}
	default:
		t.Fatal("应收到 TurnDoneEvent")
	}
}

func TestRoundTimingAbsentWithoutDelta(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	// 工具轮/空回：整轮没有任何文字或思考增量（openai 非流式回放且正文为空时就是
	// 这个样子）——首 token 键必须缺席，不能是 0 冒充"0ms 首字"。
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{{
		{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: "直接给结论"}, FinishReason: llm.FinishStop}},
	}}}).stream
	if err := s.Send("问一句"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msg := assistantMsgAt(t, s.History().Messages)
	if msg.FirstTokenMs != 0 {
		t.Fatalf("没有增量时首 token 必须是 0（未知），不能编一个值: %d", msg.FirstTokenMs)
	}
	if msg.DurationMs <= 0 {
		t.Fatalf("耗时照样要记（这一轮确实跑了）: %+v", msg)
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "first_token_ms") {
		t.Fatalf("未知的首 token 应整键缺席: %s", b)
	}
}

func TestRoundTimingAbsentOnNonStreamReplay(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	// openai 带工具时 ChatAuto 走非流式回放：增量与 done 同一瞬间到达，记下来就是
	// "首字延迟 == 整轮耗时"的假数据。事件带 Replay 标记，计时必须跳过它。
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{{
		{Type: llm.EventText, TextDelta: "回放的正文", Replay: true},
		{Type: llm.EventDone, Replay: true, Result: &llm.ChatResult{
			Message:     llm.Message{Role: "assistant", Content: "回放的正文"},
			UsageTokens: 7, FinishReason: llm.FinishStop}},
	}}}).stream
	if err := s.Send("带工具的一轮"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msg := assistantMsgAt(t, s.History().Messages)
	if msg.FirstTokenMs != 0 {
		t.Fatalf("非流式回放没有首 token 概念（整键缺席）: %d", msg.FirstTokenMs)
	}
	if msg.DurationMs <= 0 || msg.UsageTokens != 7 {
		t.Fatalf("耗时与用量照常记录: %+v", msg)
	}
}

// ---------- 吞吐 ----------

func TestOutputTokensPerSec(t *testing.T) {
	// 分母 = **生成耗时**（总耗时 − 首 token 延迟）：首字延迟是 prefill/排队等待，
	// 算进分母会把"排队久"误报成"吐字慢"——两个不同的瓶颈压成一个数字。
	if got := OutputTokensPerSec(200, 1200, 100); got != 100 {
		t.Fatalf("100 token / (1200-200)ms = 100 tok/s, got %v", got)
	}
	// 首 token 未知（工具轮没有增量 / 非流式回放）时退化成整轮耗时：拿不到解码段
	// 的起点，只能给一个**含 prefill 的**整体速率（口径不同，所以只在首 token
	// 已知时才声称它是"生成速度"）。
	if got := OutputTokensPerSec(0, 1000, 50); got != 50 {
		t.Fatalf("首 token 未知时应退化成整轮耗时: got %v", got)
	}
	// 首 token 不比总耗时小（时钟异常/边界）时同样退化，绝不产生负值或除零
	if got := OutputTokensPerSec(1500, 1000, 10); got != 10 {
		t.Fatalf("首 token 大于总耗时时应退化: got %v", got)
	}
	// 缺任一项就是算不出来——返回 0（未知），不编一个数
	if got := OutputTokensPerSec(100, 1000, 0); got != 0 {
		t.Fatalf("没有输出 token 时无法计算: got %v", got)
	}
	if got := OutputTokensPerSec(100, 0, 10); got != 0 {
		t.Fatalf("没有耗时时无法计算: got %v", got)
	}
}

// ---------- 落库 + 回读（live 与 replay 同源） ----------

func TestRoundTimingSurvivesReload(t *testing.T) {
	s := newPersistSession(t, t.TempDir()) // 挂了真 store（default 角色 = m1）
	s.stream = delayedStream(15*time.Millisecond,
		llm.StreamEvent{Type: llm.EventText, TextDelta: "落盘一轮"},
		llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message:     llm.Message{Role: "assistant", Content: "落盘一轮"},
			UsageTokens: 42, FinishReason: llm.FinishStop}})
	if err := s.Send("记一次计时"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	id := s.SessionID()
	if id == "" {
		t.Fatal("应已创建会话")
	}
	live := assistantMsgAt(t, s.History().Messages)
	// replay：重启后 Load 走的就是这条路（同一份历史必须给出同一组数字）
	disk, err := s.st.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	replay := assistantMsgAt(t, disk)

	if replay.FirstTokenMs != live.FirstTokenMs || replay.FirstTokenMs <= 0 {
		t.Fatalf("首 token 延迟未落库/回读不一致: live=%d replay=%d", live.FirstTokenMs, replay.FirstTokenMs)
	}
	if replay.DurationMs != live.DurationMs || replay.DurationMs <= 0 {
		t.Fatalf("耗时未落库/回读不一致: live=%d replay=%d", live.DurationMs, replay.DurationMs)
	}
	if replay.UsageTokens != live.UsageTokens || replay.UsageTokens != 42 {
		t.Fatalf("输出 token 未落库/回读不一致: live=%d replay=%d", live.UsageTokens, replay.UsageTokens)
	}
	if replay.Model != live.Model || replay.Model != "m1" {
		t.Fatalf("模型未落库/回读不一致: live=%q replay=%q", live.Model, replay.Model)
	}
}

// ---------- 会话模型 ----------

func TestSessionModelID(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg) // m1 = default 角色
	if err := reg.Add(config.ModelConfig{
		ID: "m2", BaseURL: "http://localhost:2", Model: "test-model-2",
		Format: config.FormatOpenAI, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main":  {Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true}},
		"coder": {Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Model: "m2"}},
	}})

	// 主会话：主 Agent 没绑模型 → 回落 default 角色
	if got := s.ModelID(); got != "m1" {
		t.Fatalf("主会话应回落 default 角色: %q", got)
	}
	// 子会话：用它自己 Agent 绑定的模型（不是 default）
	s.SetAgentID("coder")
	if got := s.ModelID(); got != "m2" {
		t.Fatalf("子会话应用自己 Agent 的模型: %q", got)
	}
	// Agent 没绑模型 → 回落 default
	s.SetAgentID("main")
	if got := s.ModelID(); got != "m1" {
		t.Fatalf("未绑定模型应回落 default: %q", got)
	}
	// 归属 Agent 不在名单里（被删了）→ 未知（空串），不回落成"主 Agent 的模型"
	s.SetAgentID("ghost")
	if got := s.ModelID(); got != "" {
		t.Fatalf("未知 Agent 应返回空（wire 上整键缺席）: %q", got)
	}
}

func TestSessionModelIDUnknownWithoutDefault(t *testing.T) {
	// 没有任何模型可用（没绑 default、Agent 也没绑）→ 空串：wire 上整键缺席，
	// 前端显示中性态而不是编一个模型名。
	s, _ := newTestSession(t)
	if got := s.ModelID(); got != "" {
		t.Fatalf("拿不到模型时应返回空: %q", got)
	}
}
