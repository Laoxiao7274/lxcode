// prune_test.go —— 确定性工具结果裁剪（A）与超窗强制升级（B）的契约钉：
//   - 纯函数层：候选选取（保留最近 K 条、短原文不裁）、占位替换（字节数正确、
//     配对不变量不破）、裁剪后历史过 toolpair 分析仍平衡；
//   - 降级路径：a9db 案例形态（摘要无收益、裁剪有收益）——轮间自动压缩必须能
//     落库降低占用（不再静默）；手动 Compact 无收益仍如实返回（契约不破）；
//   - fail-closed：落库失败回滚内存；裁剪期间会话被改动放弃两边；
//   - 超窗强制：超窗 + 全部降级失败 → TurnErrorEvent（不再静默）。
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/store"
)

// bigToolHistory 造「大量 8KB 级工具结果」的历史（a9db/fa6d 案例的形态——
// 被压段里大量工具结果，摘要模型省不了多少）。每轮 = user + assistant(带调用)
// + tool(长结果)，共 n 轮；工具名 read_file（占位文案要带上它）。
func bigToolHistory(n int) []llm.Message {
	var out []llm.Message
	for i := 0; i < n; i++ {
		var tc llm.ToolCall
		tc.ID, tc.Function.Name = "c"+string(rune('a'+i%26))+string(rune('0'+i/26)), "read_file"
		tc.Function.Arguments = `{"path":"a.go"}`
		out = append(out,
			llm.Message{Role: "user", Content: strings.Repeat("问题", 10)},
			llm.Message{Role: "assistant", Content: "我看一下", ToolCalls: []llm.ToolCall{tc}},
			llm.Message{Role: "tool", ToolCallID: tc.ID, Content: strings.Repeat("结果内容", 2048)},
		)
	}
	return out
}

// TestPruneRegionCandidatesKeepsRecentK：候选选取钉——保留最近 K 条原文，
// 更早的且够长的进入裁剪集，短原文不裁。
func TestPruneRegionCandidatesKeepsRecentK(t *testing.T) {
	// 12 条工具结果（每条 8KB）：最近 8 条保留，前 4 条进入裁剪集
	region := bigToolHistory(12)
	toolCount := 0
	for _, m := range region {
		if m.Role == "tool" {
			toolCount++
		}
	}
	if toolCount != 12 {
		t.Fatalf("前置条件：应有 12 条工具结果，得到 %d", toolCount)
	}
	pruneIdx, origLens := pruneRegionCandidates(region)
	if len(pruneIdx) != toolCount-pruneKeepRecentToolResults {
		t.Fatalf("应裁掉最近的 %d 条之外的全部: got %d", toolCount-pruneKeepRecentToolResults, len(pruneIdx))
	}
	// 裁剪集里的每条都够长
	for _, i := range pruneIdx {
		if len(region[i].Content) < prunePlaceholderMinBytes {
			t.Fatalf("候选下标 %d 的原文不够长，不该入选", i)
		}
	}
	if len(origLens) != len(pruneIdx) {
		t.Fatalf("origLens 与 pruneIdx 应等长")
	}
	// 全部短原文：没有候选
	short := make([]llm.Message, 0, 40)
	for i := 0; i < 12; i++ {
		var tc llm.ToolCall
		tc.ID, tc.Function.Name = "s"+string(rune('a'+i)), "read_file"
		short = append(short,
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
			llm.Message{Role: "tool", ToolCallID: tc.ID, Content: "短"},
		)
	}
	if idx, _ := pruneRegionCandidates(short); idx != nil {
		t.Fatalf("全部短于占位时不应有候选: %v", idx)
	}
}

// TestPrunedRegionPreservesPairing：占位替换的形态钉——tool 行数与 id 不动、
// 占位写明原字节数与工具名、裁剪后整段过 toolpair 分析仍平衡（配对不变量的
// 直接断言）。
func TestPrunedRegionPreservesPairing(t *testing.T) {
	region := bigToolHistory(12)
	pruneIdx, _ := pruneRegionCandidates(region)
	if pruneIdx == nil {
		t.Fatal("前置条件：应有候选")
	}
	// 工具名映射（与 runPruneOnly 同款拼法）
	names := make([]string, len(pruneIdx))
	for k, i := range pruneIdx {
		names[k] = toolNameOf(region, region[i].ToolCallID)
	}
	trimmed, saved := prunedRegion(region, pruneIdx, names)
	if saved <= 0 {
		t.Fatalf("裁剪应有正收益: %d", saved)
	}
	// 行数不变
	if len(trimmed) != len(region) {
		t.Fatalf("裁剪不得改变行数: %d → %d", len(region), len(trimmed))
	}
	// 配对不变量：id 集合逐条一致（tool 行数、id、调用侧全部不动）
	for i := range region {
		if region[i].Role != trimmed[i].Role {
			t.Fatalf("第 %d 行角色变了: %s → %s", i, region[i].Role, trimmed[i].Role)
		}
		if region[i].ToolCallID != trimmed[i].ToolCallID {
			t.Fatalf("第 %d 行 id 变了: %q → %q", i, region[i].ToolCallID, trimmed[i].ToolCallID)
		}
		if len(region[i].ToolCalls) != len(trimmed[i].ToolCalls) {
			t.Fatalf("第 %d 行调用数变了", i)
		}
	}
	// 整段过配对分析：必须仍平衡（与原区间同判）
	if p := AnalyzeToolPairing(trimmed); len(p.Unpaired) != 0 || len(p.Orphans) != 0 {
		t.Fatalf("裁剪后历史必须仍配对平衡: unpaired=%v orphans=%v", p.Unpaired, p.Orphans)
	}
	// 占位文案：写明原字节数与工具名
	for _, i := range pruneIdx {
		m := trimmed[i]
		if !strings.HasPrefix(m.Content, "[工具结果已裁剪：原 ") {
			t.Fatalf("占位文案形态不对: %q", m.Content)
		}
		if !strings.Contains(m.Content, "read_file") {
			t.Fatalf("占位应写明工具名: %q", m.Content)
		}
		if !strings.Contains(m.Content, " 字节，") {
			t.Fatalf("占位应写明原字节数: %q", m.Content)
		}
	}
	// 保留侧（未被裁的最近 K 条）原文不动
	keepSet := map[int]bool{}
	for _, i := range pruneIdx {
		keepSet[i] = true
	}
	for i := range region {
		if region[i].Role == "tool" && !keepSet[i] {
			if trimmed[i].Content != region[i].Content {
				t.Fatalf("保留侧第 %d 行原文不该动", i)
			}
		}
	}
}

// TestPrunedRegionShrinkCheck：占位必须真的更短——裁剪后每行估算都下降。
func TestPrunedRegionShrinkCheck(t *testing.T) {
	region := bigToolHistory(12)
	pruneIdx, _ := pruneRegionCandidates(region)
	names := make([]string, len(pruneIdx))
	for k, i := range pruneIdx {
		names[k] = toolNameOf(region, region[i].ToolCallID)
	}
	trimmed, saved := prunedRegion(region, pruneIdx, names)
	before := 0
	for _, m := range region {
		before += estimateMessageTokens(m)
	}
	after := 0
	for _, m := range trimmed {
		after += estimateMessageTokens(m)
	}
	if after >= before {
		t.Fatalf("裁剪后应更小: %d → %d", before, after)
	}
	if saved != before-after {
		t.Fatalf("saved 应等于前后差值: %d != %d", saved, before-after)
	}
}

// ---------- 降级路径（a9db 案例） ----------

// TestPruneOnlyPersistsAndDropsUsage：「摘要无收益但裁剪有收益」的历史，只裁剪
// 路径必须落库降低占用（a9db 的解药——原先这条路径整个不存在）。带存储验证
// 内存与库一致：Load 回放的对应行也是占位。
func TestPruneOnlyPersistsAndDropsUsage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	// 手工把历史灌进历史 + 落库（AppendForTest 走真实 append + 落库路径，Seq 有值）
	for _, m := range bigToolHistory(12) {
		s.AppendForTest(m)
	}
	beforeUsage := 0
	for _, m := range s.History().Messages {
		beforeUsage += estimateMessageTokens(m)
	}
	// 真实锚点（锚定算术的基准——没有锚点时 reanchoredUsage 按历史重估）
	s.mu.Lock()
	s.context = ContextUsage{Used: beforeUsage + 5000, Window: 100000, SampledTokens: beforeUsage, Messages: beforeUsage + 5000}
	s.mu.Unlock()

	res, err := s.runPruneOnly(context.Background(), nil, "", 16000)
	if err != nil {
		t.Fatalf("只裁剪降级路径应成功: %v", err)
	}
	if res.Pruned == 0 || res.Saved <= 0 {
		t.Fatalf("应有裁剪收益: %+v", res)
	}
	if res.Shadowed != 0 {
		t.Fatalf("只裁剪不产生检查点（不替换行）: %+v", res)
	}
	// 内存历史：长度不变、行数不变、配对仍平衡、早期 tool 行变成占位
	msgs := s.History().Messages
	if len(msgs) != 12*3 {
		t.Fatalf("裁剪不得改变行数: %d", len(msgs))
	}
	if p := AnalyzeToolPairing(msgs); len(p.Unpaired) != 0 || len(p.Orphans) != 0 {
		t.Fatalf("裁剪后内存历史必须仍配对平衡: %+v", p)
	}
	prunedCount := 0
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "[工具结果已裁剪：原 ") {
			prunedCount++
		}
	}
	if prunedCount != res.Pruned {
		t.Fatalf("内存里占位数应等于 Pruned: %d != %d", prunedCount, res.Pruned)
	}
	// 库里回放：同一批行也是占位（内存与库一致——a9db 案例的另一面是重启后回弹）
	stored, err := st.Load(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	storedPruned := 0
	for _, m := range stored {
		if m.Role == "tool" && strings.Contains(m.Content, "[工具结果已裁剪：原 ") {
			storedPruned++
		}
	}
	if storedPruned != prunedCount {
		t.Fatalf("库里回放的占位数应与内存一致: %d != %d", storedPruned, prunedCount)
	}
	// 占用测量跟着降（锚定算术）
	if got := s.ContextUsage().Used; got >= beforeUsage+5000 {
		t.Fatalf("裁剪后占用应下降: %d（前 %d）", got, beforeUsage+5000)
	}
	// protectHead（子会话语义）在裁剪路径同样生效：头部任务消息永远不被裁
	// ——纯函数层已由 selectCompactRange 的 firstIdx 钉住（TestSelectCompactRangeProtectsHead），
	// 这里钉 runPruneOnly 把 protectHead 原样传下去的接缝（上面 retain=16000 的
	// 区间不含第 0 条的情形已覆盖）。
}

// TestPruneOnlyFailClosedOnStoreError：裁剪落库失败 → 内存原样（fail-closed）。
func TestPruneOnlyFailClosedOnStoreError(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	for _, m := range bigToolHistory(12) {
		s.AppendForTest(m)
	}
	before := append([]llm.Message(nil), s.History().Messages...)

	// PruneToolResults 必败的替身（其余方法委托真 store）
	failing := &failingPruneStore{Persistence: st}
	s.st = failing

	if _, err := s.runPruneOnly(context.Background(), nil, "", 16000); err == nil {
		t.Fatal("落库失败应返回错误")
	}
	after := s.History().Messages
	if len(after) != len(before) {
		t.Fatalf("落库失败不得改动历史行数: %d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i].Content != after[i].Content {
			t.Fatalf("落库失败不得改动历史内容（第 %d 行）", i)
		}
	}
}

// failingPruneStore 只让 PruneToolResults 失败：其余方法原样委托。
type failingPruneStore struct {
	Persistence
}

func (f *failingPruneStore) PruneToolResults(sessionID string, seqs []int64, contents []string) error {
	return errors.New("磁盘写满（模拟）")
}

// TestMaybeCompactFallsBackToPruneOnly：轮间自动压缩的降级链——摘要无收益
// （模型回超长摘要）时自动走只裁剪，占用落库下降、事件发出（a9db 的端到端钉）。
func TestMaybeCompactFallsBackToPruneOnly(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000) // 阈值 80000 / 保留 16000
	env := &compactionEnv{}
	s.emit = env.emitter()
	for _, m := range bigToolHistory(40) {
		s.AppendForTest(m)
	}
	// 真实锚点超窗（fa6d 形态：129%）→ 强制模式
	s.mu.Lock()
	s.context = ContextUsage{Used: 129000, Window: 100000, SampledTokens: historyTokens(s.history), Messages: 129000}
	s.mu.Unlock()
	// 摘要调用回超长正文（约 42k tokens——比「裁剪后的区间」还大，缩水检查必拒
	// ；裁剪预演救不了它 → 摘要路径失败 → 降级只裁剪）
	s.stream = compactionStream(t, env, nil, strings.Repeat("很长的摘要内容", 24000), false)

	s.maybeCompact(context.Background(), nil, "")
	if got := env.compacted(); len(got) != 1 {
		t.Fatalf("降级成功应广播一条压缩事件: %+v", env.compacted())
	}
	res := env.compacted()[0].Result
	if res.Pruned == 0 || res.Saved <= 0 {
		t.Fatalf("降级路径应有裁剪收益: %+v", res)
	}
	// 历史长度不变（不产生检查点）、配对仍平衡、早期结果已是占位
	msgs := s.History().Messages
	if len(msgs) != 40*3 {
		t.Fatalf("只裁剪不改变行数: %d", len(msgs))
	}
	if p := AnalyzeToolPairing(msgs); len(p.Unpaired) != 0 || len(p.Orphans) != 0 {
		t.Fatalf("降级后历史必须仍配对平衡: %+v", p)
	}
	hasPlaceholder := false
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "[工具结果已裁剪：原 ") {
			hasPlaceholder = true
		}
	}
	if !hasPlaceholder {
		t.Fatal("降级路径应产出占位行")
	}
	// 占用不再停在 129%
	if got := s.ContextUsage().Used; got >= 129000 {
		t.Fatalf("降级后占用必须下降（不再静默）: %d", got)
	}
}

// TestMaybeCompactForcedEmitsTurnError：超窗 + 全部降级失败 → TurnErrorEvent
// （带告警文案），不再静默——fa6d 的「跑完全程零痕迹」反面钉。
func TestMaybeCompactForcedEmitsTurnError(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	env := &compactionEnv{}
	s.emit = env.emitter()
	// 短历史（无工具结果、条数少）——摘要没收益、裁剪也没收益、缩 retain 也没戏
	s.history = bigHistory(4)
	s.mu.Lock()
	s.context = ContextUsage{Used: 129000, Window: 100000, SampledTokens: historyTokens(s.history), Messages: 129000}
	s.mu.Unlock()
	s.stream = compactionStream(t, env, nil, summaryText, false)

	s.maybeCompact(context.Background(), nil, "")
	var turnErr *TurnErrorEvent
	for _, ev := range env.events {
		if e, ok := ev.(TurnErrorEvent); ok && e.Force {
			turnErr = &e
		}
	}
	if turnErr == nil {
		t.Fatalf("超窗且全部降级失败应发 TurnErrorEvent（Force）: %+v", env.events)
	}
	if !strings.Contains(turnErr.Message, "超过模型窗口") {
		t.Fatalf("告警文案应说明超窗: %q", turnErr.Message)
	}
	if len(env.compacted()) != 0 {
		t.Fatalf("没有收益就不该有压缩事件: %+v", env.compacted())
	}
}

// TestMaybeCompactNormalPressureStaysQuiet：正常压力（0.8~1.0×）下摘要无收益
// 是常态——不发 TurnError、不发压缩事件（行为与旧版一致，只是多了日志）。
func TestMaybeCompactNormalPressureStaysQuiet(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000) // 阈值 80000
	env := &compactionEnv{}
	s.emit = env.emitter()
	s.history = bigHistory(4) // 历史太短，摘要无收益
	s.mu.Lock()
	s.context = ContextUsage{Used: 85000, Window: 100000, SampledTokens: historyTokens(s.history), Messages: 85000}
	s.mu.Unlock()
	s.stream = compactionStream(t, env, nil, summaryText, false)

	s.maybeCompact(context.Background(), nil, "")
	for _, ev := range env.events {
		if e, ok := ev.(TurnErrorEvent); ok && e.Force {
			t.Fatalf("正常压力下不该发超窗告警: %+v", e)
		}
	}
	if got := env.compacted(); len(got) != 0 {
		t.Fatalf("正常压力下无收益不该有压缩事件: %+v", got)
	}
}

// TestCompactManualUnchangedContract：手动压缩契约不破——无收益仍返回
// ErrNothingToCompact（历史未改动），不是把降级塞给用户。
func TestCompactManualUnchangedContract(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	s.history = bigHistory(6)
	before := append([]llm.Message(nil), s.History().Messages...)
	s.stream = compactionStream(t, nil, nil, strings.Repeat("很长的摘要内容", 4000), false)

	if _, err := s.Compact(""); err == nil || !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("手动压缩无收益应如实返回: %v", err)
	}
	if len(s.History().Messages) != len(before) {
		t.Fatal("手动压缩失败不得改动历史")
	}
}

// TestCompactSummaryInputPruned：摘要成功路径的裁剪预演——摘要调用收到的
// 输入里，早期工具结果已是占位（prefill 变小），保留侧原文不动。
func TestCompactSummaryInputPruned(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefaultWithWindow(t, reg, 100000)
	env := &compactionEnv{}
	s.emit = env.emitter()
	s.history = bigToolHistory(12)
	s.stream = compactionStream(t, env, nil, summaryText, false)

	res, err := s.Compact("")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pruned == 0 {
		t.Fatalf("摘要路径应带裁剪（预演计入收益）: %+v", res)
	}
	call := env.summaryMessages()
	if call == nil {
		t.Fatal("摘要调用应发生")
	}
	// 调用输入里应有占位行（摘要模型看到的是裁剪后的区间）
	sawPlaceholder, sawOriginal := false, false
	for _, m := range call {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content, "[工具结果已裁剪：原 ") {
			sawPlaceholder = true
		}
		if len(m.Content) > 4000 {
			sawOriginal = true // 保留侧的原文（8KB 级）
		}
	}
	if !sawPlaceholder {
		t.Fatal("摘要输入应含占位行（裁剪预演）")
	}
	if !sawOriginal {
		t.Fatal("摘要输入应保留最近 K 条原文")
	}
}

// 编译期钉：store 实现满足含 PruneToolResults 的 Persistence（接口扩了口子，
// 实现方漏实现要在编译期爆）。
var _ Persistence = (*store.Store)(nil)
