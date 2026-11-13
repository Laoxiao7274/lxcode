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
	// pruneKeepRecentToolResults 确定性裁剪保留的最近工具结果条数（对齐 DSH
	// dsh-compaction-tool-result-pruner 的"keep recent"策略）：更早的结果替换为
	// 占位——模型保留"做过什么"的线索，需要细节时重跑该工具。
	pruneKeepRecentToolResults = 8
	// prunePlaceholderMinBytes 参与裁剪的 tool 结果最小字节数：占位文本本身有
	// 几十字节，比它短的原文裁了没有收益（甚至更长）。
	prunePlaceholderMinBytes = 256
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
	// Saved 是本次压缩省下的空间估算（before − after，含裁剪收益）：>0 才算
	// 真的有收益——「压了没降下来」（a9db 案例）的归因就靠它。
	Saved int
	// Pruned 是确定性裁剪的行数（摘要路径与只裁剪路径都填）。0 = 本次没有裁剪。
	Pruned int
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
// 配对安全切点。返回 [start, end] 闭区间下标与是否可选。
//
// protectHead 为真时区间起点从 1 开始——历史第 0 条是**子会话的任务说明书**
// （派发时那条 user 任务消息，子 Agent 的全部依据：它看不到主对话历史），压进摘要
// 就等于让子 Agent 在续跑/长任务里逐渐忘掉自己在干什么。主会话没有这条，所以
// 起点仍是 0（系统提示词每轮现组装、不在历史里，两种情形都不需要保护）。
//
// 「安全切点」的判定是 **tailValidAt**（保留侧 id 自洽），不是 AnalyzeToolPairing
// 的计数游标（2026-12 用户实测修订）：游标语义是**全历史顺序敏感**的——悬空调用
// 排在历史开头、自愈补的合成结果追加在末尾（append-only 存储插不进中间）时，
// 从调用处到末尾游标恒 >0，**每一个**切点都判不平衡，可压区间被回退吃光只剩 1 条，
// 压缩永远拒绝（真实会话 809 条活历史、占用 81% 仍报「历史还太短」）。但压缩只切
// **一刀**，被压段整体替换成一条检查点、不重放给模型（摘要失败就整段不动，
// fail-closed；llm 读侧 backfill 还会兜调用缺结果这一侧）——被压段**内部**配不配对
// 无关请求合法性；真正决定请求能不能发出去的只有**保留侧**（真发出去的部分）：
// 它的每个 tool 结果都要有前置调用（防孤儿结果 400）、每条 assistant 调用都要有
// 结果（防悬空调用 400，按 id 精确判——llm 读侧 backfill 只兜「调用后面紧跟结果
// 窗口」的形态，不兜顺序错乱的）。
func selectCompactRange(history []llm.Message, retainTokens int, protectHead bool) (start, end int, ok bool) {
	if len(history) == 0 {
		return 0, 0, false
	}
	firstIdx := 0
	if protectHead {
		firstIdx = 1
	}
	// retainTokens == 0（手动压缩）显式全压：保留侧为空。预算循环「至少累加最后
	// 一条」会隐式留一条——但保留侧只要有**任何一条**，就受保留侧配对自洽约束；
	// 用户实测会话的尾部是一条孤儿 tool 结果（它的调用被旧检查点影子掉了，任何
	// 切点都保不住它），隐式留一条 = 每个切点都非法 = 手动压缩永远拒绝。空保留侧
	// 没有消息要发出去，不存在配对约束——这正是手动压缩「能压多少压多少」的语义。
	if retainTokens <= 0 {
		if len(history)-firstIdx < 2 {
			return 0, 0, false // 全压区间不足 2 条（含 protectHead 吃掉任务消息的情形）
		}
		return firstIdx, len(history) - 1, true
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
	tailValid := func(keepFrom int) bool {
		// 保留侧「声明了调用但还没见到结果」的 id 集合——随扫描逐条闭合：
		// tool 结果消费掉自己的调用 id，扫完必须没有残留（悬空调用）；
		// 结果的 id 不在集合里（没有前置调用）= 孤儿结果。集合**只从
		// assistant 的声明里长出来**，不收集 tool 自身的 id——否则孤儿
		// 结果会把自己的 id 塞进去自我证明（keep=尾条时假合法）。
		pending := make(map[string]bool, len(history)-keepFrom)
		for i := keepFrom; i < len(history); i++ {
			m := history[i]
			switch m.Role {
			case "tool":
				if m.ToolCallID == "" || !pending[m.ToolCallID] {
					return false
				}
				delete(pending, m.ToolCallID)
			case "assistant":
				for _, tc := range m.ToolCalls {
					if tc.ID == "" {
						return false
					}
					pending[tc.ID] = true
				}
			}
		}
		return len(pending) == 0
	}
	for keepFrom > firstIdx && !tailValid(keepFrom) {
		keepFrom--
	}
	if keepFrom <= firstIdx {
		return 0, 0, false
	}
	if keepFrom-firstIdx < 2 {
		// 下限保护（2026-12「每次只压 1 条」根因）：可压区间不足 2 条时按
		// ErrNothingToCompact 拒绝。单条压缩没有收益（检查点开销就能吃掉它），
		// 而且存量会话的悬空 tool_call 会把切点卡在只剩 1 条——压 1 条后历史
		// 里仍是悬空调用 + 检查点，下一次还是 1 条，自我延续。不足 2 条宁可不
		// 压：压缩入口的配对自愈（runCompaction → repairUnpairedAtCompaction）
		// 会先把悬空调用补齐，让区间重新变宽。
		return 0, 0, false
	}
	return firstIdx, keepFrom - 1, true
}

// toolNameOf 从配对 id 找工具名（占位文案要写清楚被裁的是哪个工具的结果）。
func toolNameOf(region []llm.Message, callID string) string {
	for _, m := range region {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == callID && tc.Function.Name != "" {
				return tc.Function.Name
			}
		}
	}
	return ""
}

// pruneRegionCandidates 分析一段区间的 tool 结果，返回可裁剪（要替换成占位）的
// 下标集合：**保留最近 K 条**（按出现顺序），且原文必须够长（比占位短没有收益）。
// 区间末尾的「最近」按工具结果出现的先后数——同一次调用群（并行工具）的结果
// 相邻，数到最后 K 个即最近的 K 次。
func pruneRegionCandidates(region []llm.Message) (pruneIdx []int, origLen []int) {
	var toolIdx []int
	for i, m := range region {
		if m.Role == "tool" {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= pruneKeepRecentToolResults {
		return nil, nil // 总数不到 K 条：不裁（保留侧本来就该看到全部）
	}
	keepFrom := len(toolIdx) - pruneKeepRecentToolResults
	for _, i := range toolIdx[:keepFrom] {
		m := region[i]
		if len(m.Content) < prunePlaceholderMinBytes {
			continue // 比占位还短的原文裁了没收益
		}
		pruneIdx = append(pruneIdx, i)
		origLen = append(origLen, len(m.Content))
	}
	return pruneIdx, origLen
}

// prunedRegion 返回裁剪后的区间副本（区间本体不动——fail-closed 的原则：提交前
// 只动副本）与节省的 token 估算。占位是合法 tool 消息内容（Role/ToolCallID 不动），
// 配对不变量天然成立：toolpair 的游标按 id 计数，不读 content。
func prunedRegion(region []llm.Message, pruneIdx []int, names []string) (out []llm.Message, savedTokens int) {
	out = make([]llm.Message, len(region))
	copy(out, region)
	for k, i := range pruneIdx {
		m := region[i]
		placeholder := fmt.Sprintf("[工具结果已裁剪：原 %d 字节，%s。需要细节时重新发起该工具调用。]",
			len(m.Content), names[k])
		out[i] = llm.Message{Role: "tool", ToolCallID: m.ToolCallID, Content: placeholder,
			Seq: m.Seq, DurationMs: m.DurationMs}
		savedTokens += estimateMessageTokens(m) - estimateMessageTokens(out[i])
		if savedTokens < 0 {
			savedTokens = 0 // 防御：原文本应远大于占位，不该出现负收益
		}
	}
	return out, savedTokens
}

// compactionSystemPrompt 组装摘要调用用的系统提示词（与真实请求同一套组装，
// 唯一目的就是让这次辅助调用成为上次请求的前缀——KV 缓存可复用）。
func (s *Session) compactionSystemPrompt(ac *sessiondata.AgentContext, workDir string) string {
	allow := agentToolsOf(ac)
	docs := s.projectDocsFor(workDir)
	wt, isChild := s.worktreeInfo()
	if ac != nil {
		return composeSystemPrompt(s.tools, workDir, ac, allow, wt, isChild, docs, s.projectPanoramaFor(ac), s.activityFor(ac), s.TodoPromptMode())
	}
	return BuildSystemPrompt(s.tools, workDir, wt, isChild, docs, s.TodoPromptMode())
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
	opts := []llm.Option{llm.WithTools(llmToolsFiltered(s.tools, allow, s.TodoPromptMode()))}
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
	// chat.compacting 开始/结束信号（单一发出点，覆盖全部出口）：入口发 true，
	// defer 发 false——成功/失败/panic 都经过 defer，前端状态不会卡死。自动压缩
	// 发生在轮间（busy=true 中）天然被阻塞，但事件照发（多客户端一致性）；
	// runCompaction 是自动路径的唯一入口（maybeCompact / 超窗重试 / 降级序列
	// 都汇到这里），所以不会对同一会话重复发 true。
	s.emit(CompactingEvent{Compacting: true})
	defer s.emit(CompactingEvent{Compacting: false})
	s.repairUnpairedAtCompaction()

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
	// 区间里有既有检查点时**放行**（旧检查点一起影子掉，摘要就地合并）。
	//
	// 2026-12 修订（「87% 却说历史太短」用户实测的另一半根因）：先前这里对含
	// 检查点的区间 fail-closed 拒绝，前提是"正常历史的区间不会包含检查点——它们
	// 在保留预算的尾部之外"。手动压缩 retain=0 时区间是**全前缀**（只留最后一条），
	// 多次压缩过的会话里旧检查点必然落在区间里——每次都被拒，拒绝文案又是
	// "历史还太短"，用户完全无法理解。而摘要指令本就有旧检查点的合并措辞
	// （compactionInstruction 规则第 5 条：保留仍然成立的事实、丢掉过期的、合并
	// 进同一份结构）；store 的 AppendCheckpoint 按位置记影子集合，不关心被影子行
	// 是不是检查点；surfaceRows 的锚点插入对「影子含多个检查点」同样成立（锚点 =
	// 影子集合最小 seq）。三处都支持，拒绝才是真正的 bug。
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
	// 确定性裁剪（A，治本对齐 DSH dsh-compaction-tool-result-pruner）：摘要**之前**
	// 先把区间里更早的工具结果替换成占位。两条纪律：
	//   - 只改摘要输入的副本，区间本体不动——摘要失败时历史与内存都原样（fail-closed）；
	//   - 缩水检查的「被压段」按**裁剪后**的区间算：裁剪省下的字节也算压缩收益
	//     ——这正是联合路径的意义（摘要 + 裁剪保证有收益，a9db 案例形态）。
	// 占位是合法 tool 消息内容（id/角色不动），配对不变量不受影响。
	pruneIdx, _ := pruneRegionCandidates(region)
	var names []string
	if pruneIdx != nil {
		names = make([]string, len(pruneIdx))
		for k, i := range pruneIdx {
			names[k] = toolNameOf(region, region[i].ToolCallID)
		}
	}
	summaryInput := region
	prunedTokens := regionTokens
	if pruneIdx != nil {
		trimmed, _ := prunedRegion(region, pruneIdx, names)
		summaryInput = trimmed
		prunedTokens = 0
		for _, m := range trimmed {
			prunedTokens += estimateMessageTokens(m)
		}
	}
	summary, err := s.summarizeRange(ctx, ac, workDir, repairRegionForSummary(summaryInput))
	if err != nil {
		return CompactResult{}, fmt.Errorf("摘要失败（历史未改动）: %w", err)
	}
	// 缩水检查：替换后必须真的更小，否则这次压缩只花了一次调用却让上下文更大。
	// 比的是**被压段（裁剪后口径）**与摘要（保留尾部两边都在，抵消）——不是整段
	// 历史。被压段按裁剪后算：裁剪的收益已计入，摘要只需小于「占位化后的区间」。
	summaryTokens := blockOverhead + estimateText(checkpointPreamble) + estimateText(summary)
	if summaryTokens >= prunedTokens {
		return CompactResult{}, fmt.Errorf(
			"%w（摘要 %d tokens 不小于被压缩的 %d tokens）", ErrNothingToCompact, summaryTokens, prunedTokens)
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
	s.bumpHistoryLocked() // 历史被压缩改写：派生值缓存失效（history_cache.go）
	if s.st != nil && id != "" {
		seq, err := s.st.AppendCheckpoint(id, checkpoint, start, shadowed)
		if err != nil {
			s.history = snapshot  // 回滚：内存与库必须一致
			s.bumpHistoryLocked() // 回滚也是一次改写：缓存同样失效
			s.mu.Unlock()
			return CompactResult{}, fmt.Errorf("检查点落库失败（历史未改动）: %w", err)
		}
		// 检查点的序号也要进内存那一条（chat.history 直接回放内存历史）：
		// 不写的话"刷新后"与"不刷新"的同一条摘要有两个不同的号
		s.history[start].Seq = seq
		s.bumpHistoryLocked() // Seq 回填改了消息内容（快照里的副本要跟着换）
	}
	after := 0
	for _, m := range s.history {
		after += estimateMessageTokens(m)
	}
	// 占用测量要跟着压缩走：不更新的话指示器会一直停在压缩前的数字
	//（实测踩过——压缩成功了但环里还是旧占用）。锚定算术见 reanchoredUsage：
	// 被压段换成摘要，差值 = 被压段估算（裁剪后口径）− 摘要估算。
	s.context = reanchoredUsage(prev, prunedTokens-summaryTokens, s.history)
	updated, st := s.context, s.st
	s.mu.Unlock()

	// 新占用要落库（与 recordContextUsage 同款）：不落的话后端一重启，用户看到的是
	// 压缩**之前**的数字——live（chat.compacted）与 replay（chat.history）又分叉一次。
	// 落库失败只记日志：占用是展示信息，压缩本身已经成功，不能因此回滚历史。
	persistContextUsage(st, id, updated)

	// CompactResult 是「估算占用」口径：before − after = 真实省下的空间
	//（摘要 + 裁剪联合），事件与日志按它说话。
	saved := regionTokens - after
	if saved < 0 {
		saved = 0
	}
	res := CompactResult{Before: beforeTotal, After: after, Shadowed: shadowed, Summary: summary, Saved: saved}
	if pruneIdx != nil {
		res.Pruned = len(pruneIdx)
	}
	return res, nil
}

// maybeCompact 是自动触发（轮与轮之间）：上一次请求的真实用量超阈值才压。
// 窗口未知（模型没配 context_window）时不压——算不出阈值就不猜。
//
// 正常压力（0.8~1.0×窗口）走常规摘要压缩（无收益是正常态，DEBUG 留痕不惊动用户）。
// **超窗强制模式**（used > 1.0×窗口，fa6d 案例：端点真实容量大于配置窗口、超窗请求
// 照常成功，IsContextOverflow 永不触发——常规摘要又因「摘要≥被压段」没收益，旧版
// 在这里静默 return，占用一路涨到 129% 跑完全程）下失败不再静默：走降级序列
// （摘要+裁剪 → 只裁剪 → 减小 retain 重试一次），仍失败则 WARN 日志 + TurnError
// 事件（走既有 chat.error 通道，前端已有错误条渲染，不新发明事件形态）。
func (s *Session) maybeCompact(ctx context.Context, ac *sessiondata.AgentContext, workDir string) {
	usage := s.ContextUsage()
	threshold, retain, ok := compactionBudgets(usage.Window)
	if !ok {
		return
	}
	if usage.Used < threshold {
		return
	}
	forced := usage.Window > 0 && usage.Used > usage.Window
	res, err := s.runCompaction(ctx, ac, workDir, retain)
	if err == nil {
		s.logAndEmitCompaction(res, "自动压缩（摘要）")
		return
	}
	if !errors.Is(err, ErrNothingToCompact) {
		if forced {
			s.compactEscalation(ctx, ac, workDir, err, retain)
		} else {
			log.Printf("会话 %s 自动压缩失败（历史未改动，下一轮再试）: %v", s.SessionID(), err)
		}
		return
	}
	// 常规摘要无收益：正常压力下这是正常态（历史太短/配对回退吃掉整段）——留痕不升级。
	if !forced {
		log.Printf("会话 %s 自动压缩无收益（DEBUG 级常态，占用 %.0f%%）: %v",
			s.SessionID(), float64(usage.Used)/float64(usage.Window)*100, err)
		return
	}
	// 超窗 + 常规摘要无收益：a9db 案例形态——自动降级「只裁剪不摘要」，有收益就落库。
	log.Printf("会话 %s 超窗（%.0f%%）且常规摘要无收益，降级只裁剪: %v",
		s.SessionID(), float64(usage.Used)/float64(usage.Window)*100, err)
	pres, perr := s.runPruneOnly(ctx, ac, workDir, retain)
	if perr == nil {
		s.logAndEmitCompaction(pres, "自动压缩（只裁剪）")
		return
	}
	s.compactEscalation(ctx, ac, workDir, perr, retain)
}

// compactEscalation 是强制模式的最后两级：减小 retain 再试一次常规压缩，仍失败
// 则 WARN 日志 + TurnError 事件给前端（错误条渲染，不再静默——用户至少知道
// 「上下文已经满了而且压不动，该手动 /compact 或开新会话了」）。
func (s *Session) compactEscalation(ctx context.Context, ac *sessiondata.AgentContext, workDir string, cause error, retain int) {
	// 减小 retain 重试一次：保留预算减半（再退配对切点自己会兜）——区间更宽，
	// 有机会压出收益。只重试一次：连降级都压不动的历史，多试只是多花 prefill。
	if retain > 0 {
		if res, err := s.runCompaction(ctx, ac, workDir, retain/2); err == nil {
			s.logAndEmitCompaction(res, "自动压缩（强制·缩小保留）")
			return
		} else {
			cause = err
		}
	}
	id := s.SessionID()
	log.Printf("会话 %s 上下文已超窗且自动压缩全部失败（历史未改动）: %v", id, cause)
	s.emit(TurnErrorEvent{Message: "上下文占用已超过模型窗口，且自动压缩未能降低占用（" +
		cause.Error() + "）。建议手动压缩（/compact）或开新会话继续。", Force: true})
}

// logAndEmitCompaction 统一「压缩成功」的收尾：INFO 日志（session id、before/after、
// 是否含裁剪）+ CompactedEvent 广播。原先成功只发事件不打日志——a9db 案例排查时
// 「压了没降下来」完全无法归因（失败有日志、成功反而没有）。
func (s *Session) logAndEmitCompaction(res CompactResult, via string) {
	log.Printf("会话 %s %s完成: %d → %d tokens（省 %d，裁剪 %d 条工具结果）",
		s.SessionID(), via, res.Before, res.After, res.Saved, res.Pruned)
	s.emit(CompactedEvent{Result: res, Reason: via})
}

// runPruneOnly 是「只裁剪不摘要」的降级路径（a9db 案例的解药）：确定性把更早的
// 工具结果替换成占位，不调用模型、不产生检查点。
//
// 落库形态（与影子机制的共存设计）：裁剪**不缩短历史**——行数、配对结构、检查点
// 位置全不变，影子机制的「区间替换成检查点」形态套不上它，硬造成本远大于收益。
// 所以独立落库：store.PruneToolResults 按 seq 原地 UPDATE 正文（占位是合法 tool
// 消息内容，配对不变量天然成立），内存同临界区替换 + bumpHistoryLocked；失败回滚
// 内存（fail-closed 与检查点路径同一条纪律）。原文行被 UPDATE 覆盖——比检查点的
// 影子行「原文永久保留」弱一档，这是有意的取舍：占位写明原字节数与工具名，Search
// 翻旧账仍有线索；换来的是不引入第二种历史形状。
//
// 裁剪后的占用走锚定算术（同 runCompaction）：被压段变小，真实锚点保留、差值折算。
func (s *Session) runPruneOnly(ctx context.Context, ac *sessiondata.AgentContext, workDir string, retainTokens int) (CompactResult, error) {
	_ = ctx     // 摘要降级路径不调用模型；ctx 保留在签名上与 runCompaction 同形
	_ = ac      // 同上
	_ = workDir // 同上
	// 只裁剪降级路径也是压缩流程的一部分（耗时短但同样要阻塞发送）：
	// 开始/结束事件与 runCompaction 同款（入口 true + defer false）。
	s.emit(CompactingEvent{Compacting: true})
	defer s.emit(CompactingEvent{Compacting: false})
	s.repairUnpairedAtCompaction()

	s.mu.Lock()
	snapshot := append([]llm.Message(nil), s.history...)
	id := s.id
	protectHead := s.protectHead
	st := s.st
	prev := s.context
	s.mu.Unlock()

	start, end, ok := selectCompactRange(snapshot, retainTokens, protectHead)
	if !ok {
		return CompactResult{}, fmt.Errorf("%w（历史还太短，裁剪无从下手）", ErrNothingToCompact)
	}
	region := snapshot[start : end+1]
	pruneIdx, _ := pruneRegionCandidates(region)
	if pruneIdx == nil {
		return CompactResult{}, fmt.Errorf(
			"%w（区间内没有可裁剪的工具结果：不足 %d 条或全部短于 %d 字节）",
			ErrNothingToCompact, pruneKeepRecentToolResults+1, prunePlaceholderMinBytes)
	}
	names := make([]string, len(pruneIdx))
	for k, i := range pruneIdx {
		names[k] = toolNameOf(region, region[i].ToolCallID)
	}
	trimmed, savedTokens := prunedRegion(region, pruneIdx, names)
	if savedTokens <= 0 {
		return CompactResult{}, fmt.Errorf("%w（裁剪没有收益）", ErrNothingToCompact)
	}

	// 提交：库先动、内存随后——落库失败时内存还没动（与「AppendCheckpoint 失败回滚
	// 内存」同一条 fail-closed 纪律，顺序反过来能省一次回滚）。
	beforeTotal := 0
	for _, m := range snapshot {
		beforeTotal += estimateMessageTokens(m)
	}
	seqs := make([]int64, 0, len(pruneIdx))
	contents := make([]string, 0, len(pruneIdx))
	for _, i := range pruneIdx {
		msg := region[i]
		if msg.Seq <= 0 {
			// 没落库的消息（纯内存模式 Seq==0）：库侧无从裁剪。内存照裁（降级路径
			// 的收益主体在内存历史），库侧行跳过——下一次压缩会再处理。
			continue
		}
		seqs = append(seqs, msg.Seq)
		contents = append(contents, trimmed[i].Content)
	}
	if st != nil && len(seqs) > 0 {
		if err := st.PruneToolResults(id, seqs, contents); err != nil {
			return CompactResult{}, fmt.Errorf("裁剪落库失败（历史未改动）: %w", err)
		}
	}
	s.mu.Lock()
	if len(s.history) != len(snapshot) {
		// 裁剪期间会话被改动（撞上并发的 Send）：放弃，两边都不动
		s.mu.Unlock()
		return CompactResult{}, errors.New("会话在裁剪期间被改动，已放弃（历史未改动）")
	}
	copy(s.history[start:end+1], trimmed)
	s.bumpHistoryLocked() // 内容改写：派生值缓存失效（history_cache.go）
	s.context = reanchoredUsage(prev, savedTokens, s.history)
	updated := s.context
	s.mu.Unlock()
	persistContextUsage(st, id, updated)

	return CompactResult{
		Before:   beforeTotal,
		After:    beforeTotal - savedTokens,
		Shadowed: 0, // 不替换行
		Saved:    savedTokens,
		Pruned:   len(pruneIdx),
	}, nil
}

// Compact 手动压缩（用户 /compact 或前端入口）：不受阈值约束，空闲才允许。
// 返回哨兵错误（ErrBusy）供服务端映射错误码。
//
// 压缩期间置 compacting 防并发重入（2026-12「压缩失败: 请求超时」的顺手加固）：
// runCompaction 的摘要阶段不持 busy，同一会话同时收到两个 chat.compact 会并行
// 跑两次全量 prefill（大历史双倍费用/时间），且第二个的提交撞上第一个的
// 「会话在压缩期间被改动」防线后两边都失败。摘要慢（分钟级）所以这个窗口不小。
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
	if s.compacting {
		s.mu.Unlock()
		return CompactResult{}, fmt.Errorf("%w（压缩已在进行中，请等它完成）", ErrBusy)
	}
	s.compacting = true
	workDir := s.workDir
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.compacting = false
		s.mu.Unlock()
	}()

	res, err := s.runCompaction(context.Background(), ac, workDir, 0)
	if err != nil {
		return CompactResult{}, err
	}
	s.emit(CompactedEvent{Result: res, Manual: true})
	return res, nil
}
