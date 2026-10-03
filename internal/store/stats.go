// 会话统计：把**整段日志**折叠成一组不随历史改写而变的数字（DSH 的 sessionStats +
// tokenUsage 两个投影，见 sessiondata.SessionStats 的说明）。
//
// 为什么折叠在 store 而不是 agent：折叠的输入是**库里的全部消息行**（含被压缩检查点
// 影子掉的那些——压缩不该让「跑了多少步」变小），而 agent 的内存历史只有当前存活的那段。
// 撤回是唯一真的删行的操作，统计跟着变小才是对的。
//
// 为什么不做增量累加（把数字存进 sessions 行）：撤回会删行、压缩会影子行，累加器在
// 这两种改写之后就是错的，而要修正它得反推被删掉的那段——比每次重算一遍复杂得多，
// 也更容易漂移。折叠是纯函数，重算一次的成本是扫一遍这张会话的消息（本地 SQLite，
// 一条会话几千行的量级）。
package store

import (
	"fmt"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// SessionStatsOf 折叠一条会话的整段日志统计。会话不存在时报错（与其他按 id 读的方法
// 同语义——调用方据此区分「空会话」与「打错了 id」）。
func (s *Store) SessionStatsOf(id string) (sessiondata.SessionStats, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&exists); err != nil {
		return sessiondata.SessionStats{}, fmt.Errorf("会话 %s 不存在", id)
	}
	rows, err := s.readRows(id)
	if err != nil {
		return sessiondata.SessionStats{}, err
	}
	return foldSessionStats(rows), nil
}

// foldSessionStats 是折叠本体（纯函数，单测直接喂行）。
//
// 逐条口径：
//   - turns：真实用户消息数。**提示条不算**（notice=1 的重复调用提醒/后台任务通告是
//     注入的，不是用户说的话），压缩检查点也不算（checkpoint=1 是摘要，不是用户消息）。
//     与右栏「轮次」面板同一口径（一条用户消息开一轮）——两处数不一样会让人以为
//     有一处在骗他。
//   - steps：assistant 消息数 = 模型调用次数（每个 assistant 消息就是一次调用）。
//   - llmMs：assistant 的 duration_ms 之和（请求发出 → 收尾）。
//   - toolMs：tool 消息的 duration_ms 之和（工具执行耗时）。被拒绝/取消而没执行的
//     调用没有这个值（0 = 未知），不计入——把"没跑"算成"0ms"会让均值失真。
//   - ttft/decode：只算**有首字**的步（工具轮与非流式回放没有"首字"这个时刻，
//     FirstTokenMs 缺席），且 decode 只算同时有输出 token 的步——速度的分母与分子必须
//     来自同一批步，否则"没报用量的步"会把它稀释成假速度。
//   - 四桶：assistant 消息上 provider 回报的用量（未回报 = 0，不计）。
//   - 早期记录（本功能上线前落库的行）：那时的 `usage_tokens` 是 provider 的**总量**
//     （输入+输出），口径与今天的"输出"不同——混进来会把速度报得离谱（实测 687.8 tok/s）。
//     它们单独累加进 LegacyTokens，**不进**输出桶、也不进 decode（见 legacyUsageRow）。
func foldSessionStats(rows []rowData) sessiondata.SessionStats {
	var st sessiondata.SessionStats
	for _, r := range rows {
		switch r.msg.Role {
		case "user":
			if r.checkpoint || r.notice {
				continue // 摘要与提示条都不是用户说的话
			}
			st.Turns++
		case "assistant":
			st.Steps++
			st.LLMMs += maxInt64(0, r.msg.DurationMs)
			// 首字延迟与 token 口径无关（那时也是如实测的）——早期行照样计入
			if r.msg.FirstTokenMs > 0 {
				st.TTFTMs += r.msg.FirstTokenMs
				st.TTFTSteps++
			}
			if legacyUsageRow(r) {
				// 总量已知、拆分未知：单独记账，不混进四桶、不参与速度
				st.LegacyTokens += r.msg.UsageTokens
				continue
			}
			st.InputTokens += r.msg.InputTokens
			st.CacheReadTokens += r.msg.CacheReadTokens
			st.CacheWriteTokens += r.msg.CacheWriteTokens
			st.OutputTokens += r.msg.UsageTokens
			if r.msg.FirstTokenMs > 0 && r.msg.UsageTokens > 0 {
				// 纯生成耗时 = 整轮耗时 − 首字延迟（口径与 agent.OutputTokensPerSec
				// 一致：首字延迟是 prefill/排队，算进分母会把"排队久"误报成"吐字慢"）。
				// 分子与分母必须来自**同一批步**（都有首字与输出 token 的步）——
				// 早期记录不进这里：它的 usage_tokens 是总量，算进来会把速度报高十倍
				st.DecodeMs += maxInt64(0, r.msg.DurationMs-r.msg.FirstTokenMs)
				st.DecodeTokens += r.msg.UsageTokens
			}
		case "tool":
			st.ToolMs += maxInt64(0, r.msg.DurationMs)
		}
	}
	return st
}

// legacyUsageRow 判断一条 assistant 消息是不是**老口径**落的（那时 usage_tokens 装的是
// provider 的 total_tokens = 输入+输出，而且当时没有输入侧那三列）。
//
// 判据是**写边界的一位标记**（`usage_split`，与 notice 位同一套做法）：旧二进制写的行
// 拿到列默认值 0，新代码写的行一律置 1。为什么不用"输入侧三列全 0"去猜：真有端点只报
// completion_tokens 不报 prompt_tokens 时，那种新行会被误判成老行（少显示，不编数，但没必要）。
//
// 首字/耗时与口径无关（那时也是如实测的），所以老行照样计入 llmMs 与 ttft——
// 只有 token 桶与速度把它们排除。
func legacyUsageRow(r rowData) bool {
	return !r.usageSplit && r.msg.UsageTokens > 0
}

// boolInt 把 bool 落成 SQLite 的 0/1（modernc 驱动不认 bool→INTEGER 的隐式转换）。
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
