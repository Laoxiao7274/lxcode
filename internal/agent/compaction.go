package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// 压缩（compaction）：把历史的一段替换成一份摘要检查点，让长会话能继续跑而不撞
// 模型窗口。设计对齐 DSH 的 compaction-basic：
//
//   - **触发**：轮与轮之间用上一次请求的真实 prompt_tokens 判压力（那时它就是
//     精确值，不用估算）；端点报超长时强制压一次再重试该轮；用户手动 /compact
//     不受阈值约束（空闲即可）；
//   - **选区间**：从尾部往前累加到"保留预算"，再回退到最近的**配对平衡**切点
//     （tool-pairing 不变量——切开 assistant 的 tool_calls 与它的 tool 结果就是
//     畸形历史，严格端点会 400）；区间起点通常是 0（主会话压缩前缀），子会话
//     保护头部的任务说明书时从 1 开始（见 Session.protectHead）；
//   - **fail-closed**：摘要失败、摘要没缩水、落库失败，都不改历史——半截压缩
//     比不压缩更糟（历史被截断却没有任何解释）；
//   - **落库**：检查点行记**影子区间**（`shadow_start_seq`/`shadow_end_seq` +
//     `shadowed_seqs`），被影子的原文行**不删**——翻旧账仍可查，只是历史回放时
//     跳过它们，并把检查点放回被影子段原本占据的位置。
const (
	// compactionThresholdRatio 压力阈值占窗口比例（0.8——对齐 DSH 默认）。
	compactionThresholdRatio = 0.8
	// compactionRetainRatio 保留尾部占窗口比例（0.16——对齐 DSH 默认）。
	compactionRetainRatio = 0.16
	// compactionMaxTokens 摘要单次生成的输出上限（对齐 DSH 默认 8192）。
	compactionMaxTokens = 8192
)

// ErrNothingToCompact 没有可压的收益（历史太短，或摘要并不比被压缩段更小）。
// 这是正常态不是错误——服务端据此回 compacted=false + reason，而不是报错码。
// 包装时用 %w（...）补一句人话原因（为什么没压），供 UI 直接显示。
var ErrNothingToCompact = errors.New("没有可压缩的历史")

// CompactResult 是一次压缩的结果（宿主广播 + 测试断言）。
type CompactResult struct {
	Before   int    // 压缩前该段历史的估算占用
	After    int    // 压缩后（估算）
	Shadowed int    // 被替换的历史条数（影子区间长度）
	Summary  string // 摘要正文
}

// compactionBudgets 按模型窗口换算两个预算（窗口未知返回 false）。
func compactionBudgets(window int) (threshold, retain int, ok bool) {
	if window <= 0 {
		return 0, 0, false
	}
	threshold = int(float64(window) * compactionThresholdRatio)
	retain = int(float64(window) * compactionRetainRatio)
	if retain >= threshold {
		// 窗口小到两个预算撞在一起（配置异常）：不压（压了也留不住东西）
		return 0, 0, false
	}
	return threshold, retain, true
}

// selectCompactRange 选可压缩区间：从尾部往前累加到保留预算，再回退到最近的
// 配对平衡切点。返回 [start, end] 闭区间下标与是否可选。
//
// protectHead 为真时区间起点从 1 开始——历史第 0 条是**子会话的任务说明书**
// （派发时那条 user 任务消息，子 Agent 的全部依据：它看不到主对话历史），压进摘要
// 就等于让子 Agent 在续跑/长任务里逐渐忘掉自己在干什么。主会话没有这条，所以
// 起点仍是 0（系统提示词每轮现组装、不在历史里，两种情形都不需要保护）。
func selectCompactRange(history []llm.Message, retainTokens int, protectHead bool) (start, end int, ok bool) {
	if len(history) == 0 {
		return 0, 0, false
	}
	firstIdx := 0
	if protectHead {
		firstIdx = 1
	}
	accumulated := 0
	keepFrom := len(history)
	for i := len(history) - 1; i >= firstIdx; i-- {
		accumulated += estimateMessageTokens(history[i])
		keepFrom = i
		if accumulated >= retainTokens {
			break
		}
	}
	if keepFrom <= firstIdx {
		return 0, 0, false
	}
	pairing := AnalyzeToolPairing(history)
	for keepFrom > firstIdx {
		if pairing.BalancedBefore(keepFrom) {
			break
		}
		keepFrom--
	}
	if keepFrom <= firstIdx {
		return 0, 0, false
	}
	return firstIdx, keepFrom - 1, true
}

// compactionSystemPrompt 组装摘要调用用的系统提示词（与真实请求同一套组装，
// 唯一目的就是让这次辅助调用成为上次请求的前缀——KV 缓存可复用）。
func (s *Session) compactionSystemPrompt(ac *sessiondata.AgentContext, workDir string) string {
	allow := agentToolsOf(ac)
	docs := s.projectDocsFor(workDir)
	if ac != nil {
		return ComposeSystemPrompt(s.tools, workDir, ac, allow, docs)
	}
	return BuildSystemPrompt(s.tools, workDir, docs)
}

// summarizeRange 让模型把一段历史压成检查点正文（不经过 streamRound：这次
// 辅助调用的增量不进主时间线，宿主只在收尾收到一条 CompactedEvent）。
func (s *Session) summarizeRange(ctx context.Context, ac *sessiondata.AgentContext, workDir string, region []llm.Message) (string, error) {
	m, err := s.modelFor(ac)
	if err != nil {
		return "", err
	}
	prompt := s.compactionSystemPrompt(ac, workDir)
	allow := agentToolsOf(ac)
	msgs := make([]llm.Message, 0, len(region)+2)
	msgs = append(msgs, llm.Message{Role: "system", Content: prompt})
	msgs = append(msgs, region...)
	msgs = append(msgs, llm.Message{Role: "user", Content: compactionInstruction})

	// 工具声明照发（对齐 DSH）：这次调用的前缀与上次真实请求一致，
	// provider 的 KV 缓存能复用——上下文越长这一步省得越多。
	opts := []llm.Option{llm.WithTools(llmToolsFiltered(s.tools, allow))}
	maxOut := compactionMaxTokens
	if m.MaxOutputTokens > 0 && m.MaxOutputTokens < maxOut {
		maxOut = m.MaxOutputTokens
	}
	if maxOut > 0 {
		opts = append(opts, llm.WithMaxTokens(maxOut))
	}

	ch, err := s.stream(ctx, m, msgs, opts)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	var streamErr error
	for ev := range ch {
		switch ev.Type {
		case llm.EventText:
			text.WriteString(ev.TextDelta)
		case llm.EventDone:
			// 有的端点只在最终结果里给正文（openai 带工具走非流式回放）：
			// 增量没攒到东西时用最终结果兜底
			if ev.Result != nil && text.Len() == 0 {
				text.WriteString(ev.Result.Message.Content)
			}
		case llm.EventError:
			if ev.Err != nil {
				streamErr = ev.Err
			} else {
				streamErr = errors.New("摘要调用流式错误")
			}
		}
	}
	if streamErr != nil {
		return "", streamErr
	}
	out := strings.TrimSpace(text.String())
	if out == "" {
		// 模型可能无视"不要调用工具"去发工具调用（我们照发工具声明换缓存复用）：
		// 没有正文就是没有摘要——失败而不是拿半截内容当检查点
		return "", errors.New("摘要没有产出文本（模型可能改去调用工具了）")
	}
	return out, nil
}

// runCompaction 执行一次压缩事务：选区间 → 摘要 → 缩水检查 → 提交（内存历史
// 替换 + 检查点落库，同一临界区）。任何一步失败都不改历史（fail-closed），
// 错误交回调用方决定是记日志还是回给人。
func (s *Session) runCompaction(ctx context.Context, ac *sessiondata.AgentContext, workDir string, retainTokens int) (CompactResult, error) {
	s.mu.Lock()
	snapshot := append([]llm.Message(nil), s.history...)
	id := s.id
	protectHead := s.protectHead
	s.mu.Unlock()

	start, end, ok := selectCompactRange(snapshot, retainTokens, protectHead)
	if !ok {
		// 没有可压区间（历史还太短 / 配对回退把整段吃掉）——不是错误
		return CompactResult{}, fmt.Errorf("%w（历史还太短）", ErrNothingToCompact)
	}
	region := snapshot[start : end+1]
	regionTokens := 0
	for _, m := range region {
		regionTokens += estimateMessageTokens(m)
	}
	// 压缩前的整段上下文（被压段 + 保留尾部）——这是宿主看到的"压缩前占用"
	beforeTotal := 0
	for _, m := range snapshot {
		beforeTotal += estimateMessageTokens(m)
	}
	summary, err := s.summarizeRange(ctx, ac, workDir, region)
	if err != nil {
		return CompactResult{}, fmt.Errorf("摘要失败（历史未改动）: %w", err)
	}
	// 缩水检查：替换后必须真的更小，否则这次压缩只花了一次调用却让上下文更大。
	// 比的是**被压段**与摘要（保留尾部两边都在，抵消）——不是整段历史。
	summaryTokens := blockOverhead + estimateText(checkpointPreamble) + estimateText(summary)
	if summaryTokens >= regionTokens {
		return CompactResult{}, fmt.Errorf(
			"%w（摘要 %d tokens 不小于被压缩的 %d tokens）", ErrNothingToCompact, summaryTokens, regionTokens)
	}

	checkpoint := llm.Message{Role: "user", Content: buildCheckpoint(summary)}
	shadowed := end - start + 1

	// 提交在同一临界区里做：替换内存历史 + 写检查点行，落库失败就把内存回滚
	// （fail-closed）——内存与库分叉会让下一次 Load 得到另一段历史。
	s.mu.Lock()
	prev := s.context // 压缩前的占用测量（锚定算术的基准）
	if len(s.history) != len(snapshot) {
		// 摘要期间会话被改动（手动压缩撞上并发的 Send）：放弃，两边都不动
		s.mu.Unlock()
		return CompactResult{}, errors.New("会话在压缩期间被改动，已放弃（历史未改动）")
	}
	// 检查点顶替被压段的位置：区间起点为 0 时它就是历史首条；保护头部（子会话
	// 的任务说明书）时落在任务消息之后——与 store 回放时的锚点插入同一套语义。
	replaced := make([]llm.Message, 0, len(s.history)-shadowed+1)
	replaced = append(replaced, s.history[:start]...)
	replaced = append(replaced, checkpoint)
	replaced = append(replaced, s.history[end+1:]...)
	s.history = replaced
	if s.st != nil && id != "" {
		if err := s.st.AppendCheckpoint(id, checkpoint, start, shadowed); err != nil {
			s.history = snapshot // 回滚：内存与库必须一致
			s.mu.Unlock()
			return CompactResult{}, fmt.Errorf("检查点落库失败（历史未改动）: %w", err)
		}
	}
	after := 0
	for _, m := range s.history {
		after += estimateMessageTokens(m)
	}
	// 占用测量要跟着压缩走：不更新的话指示器会一直停在压缩前的数字
	//（实测踩过——压缩成功了但环里还是旧占用）。用锚定算术：新占用 =
	// 旧真实占用 − 被压段估算 + 检查点估算（真实锚点保留，只调整差值）。
	if prev.Used > 0 {
		used := prev.Used - regionTokens + summaryTokens
		if used < 0 {
			used = 0
		}
		hist := estimateContextUsage("", nil, s.history)
		u := ContextUsage{
			System: prev.System, ToolResults: hist.ToolResults,
			Messages: hist.Messages, Reasoning: hist.Reasoning,
		}
		u = u.anchoredTo(used)
		u.Window = prev.Window
		s.context = u
	} else {
		// 还没有真实锚点（本会话没跑过主轮）：按估算重建，窗口沿用旧值
		u := estimateContextUsage("", nil, s.history)
		u.Window = prev.Window
		s.context = u
	}
	s.mu.Unlock()

	return CompactResult{Before: beforeTotal, After: after, Shadowed: shadowed, Summary: summary}, nil
}

// maybeCompact 是自动触发（轮与轮之间）：上一次请求的真实用量超阈值才压。
// 窗口未知（模型没配 context_window）时不压——算不出阈值就不猜。
func (s *Session) maybeCompact(ctx context.Context, ac *sessiondata.AgentContext, workDir string) {
	usage := s.ContextUsage()
	threshold, retain, ok := compactionBudgets(usage.Window)
	if !ok {
		return
	}
	if usage.Used < threshold {
		return
	}
	res, err := s.runCompaction(ctx, ac, workDir, retain)
	if err != nil {
		if errors.Is(err, ErrNothingToCompact) {
			return // 没得压（历史还短/配对回退吃掉整段）——正常态
		}
		log.Printf("自动压缩失败（历史未改动，下一轮再试）: %v", err)
		return
	}
	s.emit(CompactedEvent{Result: res})
}

// Compact 手动压缩（用户 /compact 或前端入口）：不受阈值约束，空闲才允许。
// 返回哨兵错误（ErrBusy）供服务端映射错误码。
func (s *Session) Compact(agentID string) (CompactResult, error) {
	ac, err := s.resolveAgent(agentID)
	if err != nil {
		return CompactResult{}, err
	}
	if _, err := s.modelFor(ac); err != nil {
		return CompactResult{}, err
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return CompactResult{}, fmt.Errorf("%w（先取消当前生成再压缩）", ErrBusy)
	}
	workDir := s.workDir
	s.mu.Unlock()

	res, err := s.runCompaction(context.Background(), ac, workDir, 0)
	if err != nil {
		return CompactResult{}, err
	}
	s.emit(CompactedEvent{Result: res, Manual: true})
	return res, nil
}
