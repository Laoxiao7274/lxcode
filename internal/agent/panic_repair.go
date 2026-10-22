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
