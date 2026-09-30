// 每轮生成的计时与吞吐：首 token 延迟、总耗时、输出 token 数、实际用的模型。
//
// 为什么单列一个文件：这三个值是**观测数据**，与工具循环/压缩/确认门的逻辑无关，
// 但必须和消息走同一条路（落库 → 回放 → wire），所以它的"写入点"只有一处
// （stampRoundTiming，在 streamRound 的出口）。放这里是为了让读它的人一眼看到
// 全部口径（尤其是"什么算未知"），而不是在 turn.go 里翻出来。

package agent

import (
	"fmt"
	"log"
	"time"

	"github.com/moyunteng/lxcode/internal/llm"
)

// msSince 返回从起点到现在的毫秒数，**至少 1**。
//
// 为什么下限是 1：0 在本包里是"没测到"的哨兵（没有增量 / 非流式回放 / 消息没经过
// 一轮）。若把"快到不足 1ms"也记成 0，一次真实的极快首字就会与"未知"撞值，前端
// 只能显示中性态——把有数据的情形伪装成没数据，与把未知填成 0 是同一类错误（只是
// 方向相反）。向上取整到 1ms 是唯一能同时保住"有测到 ⇒ > 0"与"0 ⇒ 没测到"的写法。
func msSince(start time.Time) int64 {
	ms := time.Since(start).Milliseconds()
	if ms < 1 {
		return 1
	}
	return ms
}

// stampRoundTiming 把一轮的计时/用量/模型写进这条 assistant 消息。
//
// 写在**消息上**（而不是另开一份统计）是照 Seq 的先例：这份数字有三条路径要给前端
// （chat.done 实时、chat.history 回放、重启后 Load），各算一遍必然漂移——同一屏两个
// 数字互相矛盾（AGENTS.md §2.2 的教训）。所以由这一处写进消息，落库与 wire 都读它。
//
// firstTokenMs 为 0 = 本轮没测到首 token（没有文字/思考增量，或这轮是非流式回放）：
// 原样保留 0，让 wire 上整键缺席，**绝不**填一个数字冒充。
func stampRoundTiming(m *llm.Message, modelID string, start time.Time, firstTokenMs int64) {
	m.FirstTokenMs = firstTokenMs
	m.DurationMs = msSince(start)
	m.Model = modelID
	if m.DurationMs > 0 {
		// 服务端日志是排查"端点怎么这么慢"的第一现场：一行一条轮次，含口径
		// （首字 / 总耗时 / 输出 token / 吞吐）。吞吐用 OutputTokensPerSec
		// 那一个公式算——日志与前端显示不能各算一遍。
		//
		// 首字未知时打 "-" 而不是 "0ms"：日志里出现"首字=0ms"与前端把未知
		// 显示成"0ms 首字"是同一个错误（把没有数据说成有一个很快的数据）。
		first := "-"
		if m.FirstTokenMs > 0 {
			first = fmt.Sprintf("%dms", m.FirstTokenMs)
		}
		log.Printf("轮次计时: 模型=%s 首字=%s 耗时=%dms 输出=%dtok 吞吐=%.1ftok/s",
			m.Model, first, m.DurationMs, m.UsageTokens,
			OutputTokensPerSec(m.FirstTokenMs, m.DurationMs, m.UsageTokens))
	}
}

// OutputTokensPerSec 算生成吞吐（输出 token / 秒）；无法计算时返回 0。
//
// 分母选**生成耗时**（总耗时 − 首 token 延迟），不是总耗时：首 token 延迟是
// prefill/排队等待，与"每秒吐多少字"不是一回事——把它算进分母，一个只是排队久的
// 端点会被显示成"吐字慢"，两个完全不同的瓶颈被压成一个数字。
//
// 首 token 未知（工具轮没有增量 / 非流式回放）时退化成整轮耗时：那时拿不到解码段
// 的起点，只能给一个**含 prefill 的**整体速率——调用方要清楚这个值口径不同（这也是
// 我们只在首 token 已知时才声称它是"生成速度"的原因）。
func OutputTokensPerSec(firstTokenMs, durationMs int64, usageTokens int) float64 {
	if usageTokens <= 0 || durationMs <= 0 {
		return 0 // 缺任一项就是算不出来：不编一个数
	}
	genMs := durationMs - firstTokenMs
	if genMs <= 0 {
		genMs = durationMs
	}
	return float64(usageTokens) * 1000 / float64(genMs)
}
