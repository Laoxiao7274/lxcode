// panic 补配对：生成 goroutine panic 时，assistant 可能已带着 tool_calls 落进
// 历史，而对应 tool 结果永远等不到了（runTurn 的 defer 只负责资源收尾，不管
// 配对）。缺配对的代价见 toolpair.go：严格端点 400 拒收整轮，压缩切点永久卡死。
// 与「取消时补配对」（tools.go 的 sinkSkippedToolResults）同一不变量、同款写法，
// 差别只在触发时机（panic vs 取消/失败）与调用清单来源（扫历史 vs 循环现场）。

package agent

import (
	"log"

	"github.com/moyunteng/lxcode/internal/llm"
)

// repairPanicNote 是 panic 补配对的合成结果文案（与 skippedCancelNote /
// skippedFailureNote 同族——三个常量分开定义而不是复用同一句：模型看到的
// 原因不同，措辞要如实）。
const repairPanicNote = "本轮生成异常终止，该调用未执行。需要时请重新发起。"

// repairUnpairedAfterPanic 在 panic recover 之后补齐历史里的未配对工具调用。
//
// 前提：调用方在 recover 分支里调它——runTurn 的 defer 已经先跑完（busy=false、
// turnEnded 已关），历史不再被本轮写入，这里读历史是安全的。
//
// 流程：锁内 AnalyzeToolPairing 取 Unpaired id 清单 + 建全历史 id→ToolCall 映射；
// 锁外逐个补合成结果（sinkSkippedToolResults 内部自己拿锁写历史）。dispatchID
// 传空串——panic 可能发生在任意一轮（主轮或子轮收尾阶段），归属无法可靠判定，
// 与 turn.go 流式失败路径同款（空串 = 无归属，事件走主时间线）。
//
// Unpaired 为空直接返回（常态：panic 发生在请求前/流式中，没有未配对调用）。
func (s *Session) repairUnpairedAfterPanic() {
	s.mu.Lock()
	pairing := AnalyzeToolPairing(s.history)
	if len(pairing.Unpaired) == 0 {
		s.mu.Unlock()
		return
	}
	byID := make(map[string]llm.ToolCall, len(pairing.Unpaired))
	for _, m := range s.history {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if _, ok := byID[tc.ID]; !ok {
				byID[tc.ID] = tc
			}
		}
	}
	sink := func(m llm.Message) { s.append(m) }
	s.mu.Unlock()

	var calls []llm.ToolCall
	for _, id := range pairing.Unpaired {
		tc, ok := byID[id]
		if !ok {
			// id 找不到映射（理论上到不了：Unpaired 就是从 assistant 的
			// ToolCalls 里攒出来的）。缺调用就没法补结果——记日志不猜。
			log.Printf("会话 %s panic 补配对：未配对调用 %s 找不到 ToolCall 映射（跳过）", s.id, id)
			continue
		}
		calls = append(calls, tc)
	}
	if len(calls) == 0 {
		return
	}
	log.Printf("会话 %s panic 补配对：%d 个未配对工具调用已补合成结果", s.id, len(calls))
	s.sinkSkippedToolResults(sink, calls, "", repairPanicNote)
}

// repairUnpairedAtCompaction 是压缩入口的配对自愈：选区间之前把历史里的
// **存量**未配对 tool_call 补上合成结果。
//
// 为什么必须有这一步（2026-12 用户实测）：P5 修复（2026-09-22）之前的存量会话
// 里存在取消时未补配对的悬空调用——AnalyzeToolPairing 的游标在悬空处之后再
// 不平衡，selectCompactRange 的配对回退会把切点一路退到悬空调用之前，于是
// 每次压缩只剩 1 条可压（甚至压不出来）；压缩成功后历史里又有悬空调用 +
// 检查点，下一次压缩同样卡死——「每次只压缩一条」的自我延续。与 panic 补配对
// 是同一个不变量、同一条补齐路径（sinkSkippedToolResults），差别只在触发时机：
// panic 路径补的是"本轮刚产生的"，这里补的是"库里本来就有的"。
//
// 为什么放在压缩入口而不是加载路径：加载路径修的话每次 EnablePersistence/
// AttachTo 都要写库（读路径变写路径，故障面变大），而悬空调用只有两个实际
// 危害（请求 400——llm 层读侧已有 backfill 兜底；压缩切点卡死）里压缩是唯一
// 必须真修历史的，在它的入口修一次就够。
//
// 并发安全：与 panic 路径同一条纪律——只在会话空闲时到达（Compact 有 busy 守卫，
// maybeCompact 在轮边界），锁内判定、锁外经 append 落库。
func (s *Session) repairUnpairedAtCompaction() {
	s.mu.Lock()
	pairing := AnalyzeToolPairing(s.history)
	if len(pairing.Unpaired) == 0 {
		s.mu.Unlock()
		return
	}
	byID := make(map[string]llm.ToolCall, len(pairing.Unpaired))
	for _, m := range s.history {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if _, ok := byID[tc.ID]; !ok {
				byID[tc.ID] = tc
			}
		}
	}
	sink := func(m llm.Message) { s.append(m) }
	id := s.id
	s.mu.Unlock()

	var calls []llm.ToolCall
	for _, cid := range pairing.Unpaired {
		tc, ok := byID[cid]
		if !ok {
			// 与 panic 路径同款：缺映射记日志不猜（Unpaired 本就来自 ToolCalls，
			// 理论到不了这里）。
			log.Printf("会话 %s 压缩补配对：未配对调用 %s 找不到 ToolCall 映射（跳过）", id, cid)
			continue
		}
		calls = append(calls, tc)
	}
	if len(calls) == 0 {
		return
	}
	log.Printf("会话 %s 压缩前补配对：%d 个存量未配对工具调用已补合成结果", id, len(calls))
	s.sinkSkippedToolResults(sink, calls, "", repairPanicNote)
}

// repairRegionForSummary 确保一段历史能作为消息序列**原样重放**给模型（摘要
// 调用把 region 整段重放）。被压段不像保留侧——它真的会发给端点，所以它的
// 配对完整性必须按 wire 的实际约束检查：
//
//   - llm 读侧 backfill 只兜「assistant 调用后面**紧跟** tool 结果窗口」的缺口
//     （anthropic: next.Role=="user" 且已有 tool_result；openai: 连续 tool 窗口
//     非空才补）——悬空调用后面跟的是 user/assistant 消息时（结果与调用不相邻，
//     append-only 存储里补在末尾的合成结果正是这种形态），backfill 不动手，
//     严格端点 400 拒收整次摘要调用；
//   - 孤儿 tool 结果（无前置调用）两种 wire 都拒收。
//
// 修法是把配对重排到 wire 能接受的形态：悬空调用的合成结果插在调用侧 assistant
// 之后（两种 wire 的合法位置），孤儿结果直接丢弃（它没有信息量：调用侧根本
// 不存在，丢掉不改变任何模型可见的事实）。
//
// **只改 region 的副本**（摘要调用的入参），不改 s.history——历史是事实记录，
// 这里做的是「为了这次调用把 wire 形态整理合法」，提交路径用的仍是原历史。
func repairRegionForSummary(region []llm.Message) []llm.Message {
	if regionWireValid(region) {
		return region
	}
	out := make([]llm.Message, 0, len(region)+4)
	// 已见但还没拿到结果的调用 id（按 wire 顺序）；孤儿结果直接丢弃
	pending := map[string]bool{}
	dropped := 0
	for _, m := range region {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					pending[tc.ID] = true
				}
			}
			out = append(out, m)
			// 调用侧 assistant 之后**立刻**补上还没拿到结果的调用——合成结果
			// 插在这里才是两种 wire 的合法位置（openai 要求 tool 紧跟调用侧
			// assistant；anthropic 由 backfill 并进下一条消息）。
			for _, tc := range m.ToolCalls {
				if tc.ID != "" && pending[tc.ID] {
					out = append(out, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: repairPanicNote})
					delete(pending, tc.ID)
				}
			}
		case "tool":
			if m.ToolCallID != "" && pending[m.ToolCallID] {
				// 原始结果合法：跟着保留（合成结果已在 assistant 后面，原结果
				// 在后面追加，id 配对不受影响；anthropic 侧连续 tool_result
				// 并进同一条 user 消息，次序无关紧要）
				out = append(out, m)
				delete(pending, m.ToolCallID)
			} else {
				// 孤儿结果（无前置调用或调用已有结果）：丢弃——调用侧不存在
				// 或已闭合，它没有信息量
				dropped++
			}
		default:
			out = append(out, m)
		}
	}
	// pending 里剩下的调用在补合成结果时已闭合（合成结果紧跟 assistant），
	// 理论上不会残留；防御性记日志。
	if dropped > 0 {
		log.Printf("摘要重放前整理 wire 形态：丢弃 %d 条孤儿工具结果", dropped)
	}
	return out
}

// regionWireValid 判断一段历史是否已满足两种 wire 的配对约束（相邻性 + id 配对）：
// 每条 assistant 调用后**紧跟**的 tool 结果窗口覆盖它的全部调用 id，且每条 tool
// 结果都有前置调用。干净历史（绝大多数）在这里原样返回，零拷贝零分配。
func regionWireValid(region []llm.Message) bool {
	open := 0
	window := map[string]bool{}
	for _, m := range region {
		switch m.Role {
		case "assistant":
			if open > 0 {
				return false // 上一批调用没被结果窗口闭合
			}
			open = len(m.ToolCalls)
			window = make(map[string]bool, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				if tc.ID == "" {
					return false
				}
				window[tc.ID] = true
			}
		case "tool":
			if open == 0 || m.ToolCallID == "" || !window[m.ToolCallID] {
				return false
			}
			delete(window, m.ToolCallID)
			open--
		}
	}
	return open == 0
}
