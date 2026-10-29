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
	start, end, ok := selectCompactRange(history, 100, false)
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
	// 预算为 0（手动压缩）：显式全压（保留侧为空——空保留侧没有要发出去的
	// 消息，不受保留侧配对自洽约束；这正是手动压缩「能压多少压多少」的语义）
	_, end0, ok0 := selectCompactRange(history, 0, false)
	if !ok0 || end0 != len(history)-1 {
		t.Fatalf("预算 0 应全压: end=%d ok=%v", end0, ok0)
	}
}

// 配对不变量：区间末尾不能落在 assistant 的 tool_calls 与它的 tool 结果之间。
func TestSelectCompactRangeRespectsToolPairing(t *testing.T) {
	// 7 条：user / assistant(垫) / assistant(带调用) / tool / user / assistant / user。
	// 调用侧 assistant 放在第 2 条——配对回退退到它之前时区间仍有 2 条，
	// 能同时验证「回退」与「下限 ≥2」两个不变量。
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("a", 400)},
		{Role: "assistant", Content: strings.Repeat("b", 400)},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("r", 400)},
		{Role: "user", Content: strings.Repeat("y", 400)},
		{Role: "assistant", Content: strings.Repeat("z", 400)},
		{Role: "user", Content: strings.Repeat("w", 400)},
	}
	// 保留预算 350：天然切点落在 tool 调用侧 assistant 上（尾部 3 条 ≈312
	// 不够，加上它才 416）——那里不配对（声明还没回填）→ 必须往前回退到
	// 调用侧 assistant 之前
	start, end, ok := selectCompactRange(history, 350, false)
	if !ok {
		t.Fatal("应可选区间")
	}
	if start != 0 || end != 1 {
		t.Fatalf("切点必须回退到配对平衡处（start=0 end=1），实际 start=%d end=%d", start, end)
	}
	// 回退后保留下来的尾部必须仍含完整的一对（调用 + 结果）与之后的对话
	tail := history[end+1:]
	if len(tail) != 5 || len(tail[0].ToolCalls) != 1 || tail[1].ToolCallID != "c1" {
		t.Fatalf("尾部应含完整配对: %+v", tail)
	}
	// 悬空**尾部**（调用在区间末尾、结果缺失）：保留侧只要含它就非法——回退
	// 一路退到头也保不住（c9 在最后一条），区间不足 → 拒绝（宁可不压）
	danglingTail := append(append([]llm.Message(nil), history[:6]...),
		llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c9"}}})
	if _, _, ok2 := selectCompactRange(danglingTail, 350, false); ok2 {
		t.Fatalf("尾部悬空调用任何切点都非法（结果缺失）应拒绝: ok=%v", ok2)
	}
	// 孤儿结果在尾部（无前置调用）：保留侧含它就非法（发请求 400，openai wire
	// 要求 tool 消息跟在调用它的 assistant 之后）——回退一路到头也保不住它
	//（它在最后一条），区间不足 → 拒绝（宁可不压）。手动 retain=0 全压才放行
	//（见下一条与 TestSelectCompactRangeOrphanTailBlocksAllCuts）。
	orphanTail := append(append([]llm.Message(nil), history[:6]...),
		llm.Message{Role: "tool", ToolCallID: "c-none", Content: strings.Repeat("r", 400)})
	if _, _, ok3 := selectCompactRange(orphanTail, 350, false); ok3 {
		t.Fatal("尾部孤儿结果任何切点都非法，有保留预算时应拒绝")
	}
	// 同样的孤儿结果历史，手动（retain=0）全压：保留侧为空，孤儿结果也被压掉
	if _, end4, ok4 := selectCompactRange(orphanTail, 0, false); !ok4 || end4 != len(orphanTail)-1 {
		t.Fatalf("retain=0 全压不受保留侧约束: end=%d ok=%v", end4, ok4)
	}
}

// TestSelectCompactRangeOrphanTailBlocksAllCuts 钉住 2026-12 用户实测事故的判定
// 形态：尾部一条孤儿 tool 结果（它的调用被旧检查点影子掉了），任何**非全压**切点
// 都保不住它（保留侧含孤儿结果 → 端点 400）→ 有保留预算时无可压区间（拒绝是
// 正确的）；手动 retain=0 全压放行——这正是「87% 占用却报历史太短」会话的解封路径。
func TestSelectCompactRangeOrphanTailBlocksAllCuts(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("a", 400)},
		{Role: "assistant", Content: strings.Repeat("b", 400)},
		{Role: "user", Content: strings.Repeat("c", 400)},
		{Role: "assistant", Content: strings.Repeat("d", 400)},
		// 尾部孤儿结果：调用侧不在历史里（被影子掉），结果留在了活历史
		{Role: "tool", ToolCallID: "c-gone", Content: strings.Repeat("r", 400)},
	}
	// 有保留预算：所有切点的保留侧都含孤儿结果 → 无合法切点 → 拒绝
	if _, _, ok := selectCompactRange(history, 100, false); ok {
		t.Fatal("尾部孤儿结果让所有切点非法，有保留预算时应拒绝")
	}
	// retain=0 全压：保留侧为空，孤儿结果随整段被压掉 → 放行
	start, end, ok := selectCompactRange(history, 0, false)
	if !ok || start != 0 || end != len(history)-1 {
		t.Fatalf("retain=0 全压应放行: ok=%v [%d,%d]", ok, start, end)
	}
}

// TestRepairRegionForSummary 钉住摘要重放前的 wire 整理：被压段含「调用后面
// 不跟结果窗口」的悬空调用（append-only 补在末尾的合成结果正是这种错位形态）
// 与孤儿结果时，重放入参必须整理成两种 wire 都能接受的形态——否则摘要调用
// 被端点 400 拒收（llm 读侧 backfill 只兜「调用紧跟结果窗口」的缺口）。
func TestRepairRegionForSummary(t *testing.T) {
	// 错位形态：c1 的结果补在末尾（中间隔了后续对话）
	region := []llm.Message{
		{Role: "user", Content: "第一段"},
		{Role: "assistant", Content: "我看一下", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		{Role: "user", Content: "继续"},
		{Role: "assistant", Content: "回复"},
		{Role: "tool", ToolCallID: "c1", Content: "合成结果"},
	}
	out := repairRegionForSummary(region)
	// 孤儿结果没有，悬空调用 c1 有末尾结果——形态不变（末尾 tool 前面有 assistant
	// 调用侧……不，中间隔着 user/assistant，wire 上 tool 必须紧跟调用侧 assistant）
	p := AnalyzeToolPairing(out)
	if len(p.Unpaired) != 0 || len(p.Orphans) != 0 {
		t.Fatalf("整理后仍不配对: unpaired=%v orphans=%v", p.Unpaired, p.Orphans)
	}
	// c1 的合成结果应被搬到 c1 调用侧 assistant 之后
	if len(out) != 5 || out[2].Role != "tool" || out[2].ToolCallID != "c1" {
		t.Fatalf("合成结果应紧跟调用侧 assistant: %+v", out[:3])
	}
	// 孤儿结果：直接丢弃（调用侧不存在，没有信息量）
	orphan := []llm.Message{
		{Role: "user", Content: "x"},
		{Role: "tool", ToolCallID: "c-none", Content: "孤儿"},
		{Role: "user", Content: "y"},
	}
	out2 := repairRegionForSummary(orphan)
	if len(out2) != 2 || out2[1].Role == "tool" {
		t.Fatalf("孤儿结果应被丢弃: %+v", out2)
	}
	// 干净历史原样返回（同一 slice，零拷贝路径）
	clean := []llm.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}
	if got := repairRegionForSummary(clean); len(got) != 2 {
		t.Fatalf("干净历史应原样返回: %+v", got)
	}
}

func TestSelectCompactRangeNothingWhenTooShort(t *testing.T) {
	if _, _, ok := selectCompactRange(nil, 100, false); ok {
		t.Fatal("空历史不可压")
	}
	// 只有一条消息：区间会空掉（keepFrom <= firstIdx）
	if _, _, ok := selectCompactRange([]llm.Message{{Role: "user", Content: "x"}}, 0, false); ok {
		t.Fatal("只剩一条时不可压")
	}
	// 历史以未配对调用收尾（用户中断过，未补配对的存量数据）：保留侧含悬空
	// 调用（发请求会 400，llm 读侧 backfill 兜不到这种形态）——回退到调用
	// 之前只剩 1 条，不足下限（2 条）时**宁可不压**（ErrNothingToCompact）；
	// 配对自愈（压缩入口）补齐后区间重新变宽。
	unpaired := []llm.Message{
		{Role: "user", Content: strings.Repeat("x", 400)},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
	}
	if _, _, ok := selectCompactRange(unpaired, 100, false); ok {
		t.Fatal("只剩 1 条可压（悬空调用把切点卡死）时应拒绝而不是压 1 条")
	}
	// 补上配对结果后同样的历史就能正常压缩
	repaired := []llm.Message{
		{Role: "user", Content: strings.Repeat("x", 400)},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1"}}},
		{Role: "tool", ToolCallID: "c1", Content: strings.Repeat("r", 400)},
		{Role: "assistant", Content: strings.Repeat("z", 400)},
		{Role: "user", Content: strings.Repeat("y", 400)},
	}
	start, end, ok := selectCompactRange(repaired, 0, false)
	if !ok {
		t.Fatal("补齐配对后应可压")
	}
	// retain=0 全压：区间 = 整段历史
	if start != 0 || end != len(repaired)-1 {
		t.Fatalf("预算 0 应全压（start=0 end=%d）: start=%d end=%d", len(repaired)-1, start, end)
	}
	// 有保留预算时：配对整体留在尾部（调用 + 结果不跨切点）。
	// 预算 200 → 天然切点 keepFrom=3（保留 z/y 两条），tailValid(3)=true，
	// 区间 [0,2] 共 3 条（含完整一对 c1 调用+结果）。
	start2, end2, ok2 := selectCompactRange(repaired, 200, false)
	if !ok2 || start2 != 0 || end2 != 2 {
		t.Fatalf("有预算时应可选区间: ok=%v start=%d end=%d", ok2, start2, end2)
	}
	tail2 := repaired[end2+1:]
	if len(tail2) > 1 && tail2[0].Role == "assistant" && len(tail2[0].ToolCalls) > 0 {
		// 尾部以带调用的 assistant 开头时，其结果必须在保留侧
		found := false
		for _, m := range tail2 {
			if m.Role == "tool" && m.ToolCallID == tail2[0].ToolCalls[0].ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("保留侧调用缺结果: %+v", tail2)
		}
	}
}

// 头部保护（protectHead）：子会话压缩不碰历史第 0 条——那是派发的任务说明书，
// 子 Agent 唯一的任务依据。主会话没有这条头部，区间起点仍是 0。
func TestSelectCompactRangeProtectsHead(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("任务说明书", 100)},
		{Role: "assistant", Content: strings.Repeat("b", 400)},
		{Role: "user", Content: strings.Repeat("c", 400)},
		{Role: "assistant", Content: strings.Repeat("d", 400)},
		{Role: "user", Content: strings.Repeat("e", 400)},
	}
	start, end, ok := selectCompactRange(history, 0, true)
	if !ok {
		t.Fatal("应可选出一个区间")
	}
	if start != 1 {
		t.Fatalf("保护头部时区间应从第 1 条开始（任务说明书留下）: %d", start)
	}
	if end != len(history)-1 {
		t.Fatalf("预算 0 应全压（任务说明书留下）: end=%d", end)
	}
	// 主会话（不保护）：起点仍是 0——压缩前缀，行为与旧版一致
	if s0, _, ok0 := selectCompactRange(history, 0, false); !ok0 || s0 != 0 {
		t.Fatalf("主会话区间起点应为 0: ok=%v start=%d", ok0, s0)
	}
	// 只有头部一条（或头部之后无内容可压）时：不可压
	if _, _, ok1 := selectCompactRange(history[:1], 0, true); ok1 {
		t.Fatal("只剩任务说明书一条时不可压")
	}
	if _, _, ok2 := selectCompactRange(history[:2], 0, true); ok2 {
		t.Fatal("头部 + 一条尾部（区间会空掉）时不可压")
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
	// retain=0 显式全压（2026-12 修订）：区间 = 整段历史，压缩后只剩检查点一条
	if len(hist) != 1 {
		t.Fatalf("预算 0 应全压只留检查点: %d 条", len(hist))
	}
	if !isCheckpointContent(hist[0].Content) {
		t.Fatalf("历史首条应是检查点: %q", hist[0].Content)
	}
	if !strings.Contains(hist[0].Content, "修一个 bug") {
		t.Fatalf("检查点应含摘要正文: %q", hist[0].Content)
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
	if usageTotal(after) != after.Used {
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

// TestCompactRejectsConcurrentReentry 钉住手动压缩的防并发重入：摘要阶段不持
// busy（它不是一轮生成），没有 compacting 标志的话同一会话并行两个 chat.compact
// 会跑两次全量 prefill，且后提交的那个撞「压缩期间被改动」防线后两边一起失败。
// 摘要是分钟级调用，这个窗口不小——必须在入口拒绝（ErrBusy）。
func TestCompactRejectsConcurrentReentry(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	s.history = bigHistory(6)

	// 摘要调用挂住不放（模拟大会话的分钟级 prefill），等第一次 Compact 进入摘要
	gate := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		last := msgs[len(msgs)-1]
		if last.Role == "user" && strings.Contains(last.Content, "上下文压缩引擎") {
			once.Do(func() { close(gate) })
			<-release // 挂住：第一次压缩停在摘要阶段
			ch := make(chan llm.StreamEvent, 2)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: summaryText}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: summaryText}, FinishReason: llm.FinishStop,
			}}
			close(ch)
			return ch, nil
		}
		t.Fatalf("未预期的非摘要调用")
		return nil, nil
	}

	done := make(chan struct{})
	var firstErr error
	go func() {
		_, firstErr = s.Compact("")
		close(done)
	}()
	<-gate // 第一次压缩已进入摘要阶段

	// 此时第二个 chat.compact 必须被拒（ErrBusy），而不是并行跑第二次摘要
	_, err := s.Compact("")
	if err == nil || !errors.Is(err, ErrBusy) {
		t.Fatalf("压缩进行中应拒绝重入（ErrBusy）: %v", err)
	}

	close(release)
	<-done
	if firstErr != nil {
		t.Fatalf("第一次压缩应正常完成: %v", firstErr)
	}
	if n := len(s.History().Messages); n != 1 {
		t.Fatalf("压缩应完成（只留检查点）: %d 条", n)
	}
	// 标志复位：结束后再压一次不被拒（这次走正常摘要流）
	s.stream = compactionStream(t, nil, nil, summaryText, false)
	s.history = bigHistory(6)
	if _, err := s.Compact(""); err != nil {
		t.Fatalf("压缩结束后应可再次压缩（标志已复位）: %v", err)
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

// TestCompactAutoRepairsDanglingCalls：悬空 tool_call 的存量历史（P5 修复前
// 的形态）经一次手动压缩后被入口自愈补齐，且整段（不止 1 条）压成检查点——
// 「每次只压缩一条」自我延续的端到端回归钉。
func TestCompactAutoRepairsDanglingCalls(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	env := &compactionEnv{}
	s.emit = env.emitter()
	var call llm.ToolCall
	call.ID, call.Function.Name, call.Function.Arguments = "c1", "read_file", `{"path":"x"}`
	s.history = []llm.Message{
		{Role: "user", Content: strings.Repeat("第一段", 100)},
		{Role: "assistant", Content: strings.Repeat("铺垫", 100)},
		{Role: "assistant", Content: "我先看一下", ToolCalls: []llm.ToolCall{call}},
		{Role: "user", Content: strings.Repeat("第二段", 100)},
		{Role: "assistant", Content: strings.Repeat("回复", 100)},
		{Role: "user", Content: strings.Repeat("第三段", 100)},
		{Role: "assistant", Content: strings.Repeat("收尾", 100)},
	}
	s.stream = compactionStream(t, env, nil, summaryText, false)

	res, err := s.Compact("")
	if err != nil {
		t.Fatalf("自愈后应整段压缩成功: %v", err)
	}
	if res.Shadowed < 2 {
		t.Fatalf("自愈后应压缩多条（而不是 1 条）: shadowed=%d", res.Shadowed)
	}
	hist := s.History().Messages
	// retain=0 全压：压缩后只剩检查点一条
	if len(hist) != 1 || !isCheckpointContent(hist[0].Content) {
		t.Fatalf("历史应以检查点开头（全压）: %+v", hist)
	}
	if p := AnalyzeToolPairing(hist); len(p.Unpaired) != 0 {
		t.Fatalf("自愈后历史里不应再有未配对调用: %v", p.Unpaired)
	}
	var sawRepair bool
	for _, ev := range env.events {
		if tr, ok := ev.(ToolResultEvent); ok && tr.ID == "c1" && tr.IsError {
			sawRepair = true
		}
	}
	if !sawRepair {
		t.Fatal("压缩入口的自愈应发合成 tool_result 事件")
	}
}

// TestCompactMergesRegionContainingCheckpoint：选出的区间里有既有检查点时
// **放行**（2026-12 修订——旧检查点一起影子掉，摘要就地合并）：手动压缩 retain=0
// 的区间是全前缀，多次压缩过的会话里旧检查点必然落在区间里，旧版 fail-closed
// 拒绝会让这些会话永远压不动（拒绝文案还是误导性的「历史还太短」）。
func TestCompactMergesRegionContainingCheckpoint(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	env := &compactionEnv{}
	s.emit = env.emitter()
	s.history = append([]llm.Message{
		{Role: "user", Content: buildCheckpoint(summaryText)},
	}, bigHistory(6)...)
	before := len(s.history)
	s.stream = compactionStream(t, env, nil, summaryText, false)

	res, err := s.Compact("")
	if err != nil {
		t.Fatalf("区间含既有检查点应放行（摘要合并）: %v", err)
	}
	if res.Shadowed != before { // 全压：旧检查点 + 全部原文都被影子掉
		t.Fatalf("应整段影子（含旧检查点）: shadowed=%d", res.Shadowed)
	}
	hist := s.History().Messages
	if len(hist) != 1 || !isCheckpointContent(hist[0].Content) {
		t.Fatalf("压缩后应只剩新检查点: %+v", hist)
	}
}
