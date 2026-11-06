// 会话级派生值缓存（2026-12 会话切换优化批 3）。
//
// 背景：chat.history 的应答路径（Session.History → TailHistory → server.history
// → sessionStatsOf）每次都做三件 O(全历史) 的事——
//
//	① historyTokens(s.history)（projectedUsage 的增量估算，逐消息扫一遍）；
//	② AnalyzeToolPairing(s.history)（配对切点，逐消息扫一遍）；
//	③ checkpointIndexes(msgs)（逐消息扫一遍）。
//
// 大会话（几千条消息）上这三遍扫描在**每次切会话**都重跑——历史没变的话，结果
// 也不可能变。本文件给这三样加**版本号失效**的会话级缓存：历史被改写时代数 +1，
// 读路径命中同代数直接复用。
//
// 失效纪律（**全部写入点**——漏一个就是「缓存里留着旧历史」的静默漂移，因此
// 入口收敛为一个 bumpHistoryLocked，所有改写 s.history 的临界区必须调用它）：
//   - append（persist.go：工具结果/assistant/通告/合成补配对——一切走 append 的）；
//   - Send 的首条用户消息（session.go：不走 append 的唯一追加点）；
//   - Rewind 纯内存截断 / 撤回后重载（rewind.go ×2）；
//   - SetLeaf 切叶重载（rewind.go）；
//   - runCompaction 的历史替换 / 落库失败回滚 / 检查点 Seq 回填（compaction.go ×3）；
//   - AttachTo / EnablePersistence 的装载（persist.go ×2）；
//   - SwitchNew 清空 / SwitchTo 切换装载（switch.go ×2）。
//
// 不改历史长度的字段写（如 s.context 更新）**不**升代数——它们不走这个缓存
// （s.context 在 History() 里现读，History 快照缓存只锁「消息序列」这一部分）。
package agent

import "github.com/moyunteng/lxcode/internal/llm"

// histDerived 是「历史序列的纯函数派生值」的缓存槽位（s.history 不变 ⇒ 全部不变）。
type histDerived struct {
	valid   bool
	tokens  int         // historyTokens(s.history)
	pairing ToolPairing // AnalyzeToolPairing(s.history)
	// History() 的消息副本与检查点下标（History 每次都要 append 一份副本，
	// 大会话上这是最大的一块拷贝；副本是不可变共享——调用方不该改它，History
	// 的契约本来也是「返回快照」）。
	msgs        []llm.Message
	checkpoints []int
}

// bumpHistoryLocked 历史被改写：代数 +1，全部派生值失效。调用方**必须持锁**
// （它与 s.history 的每次写同一临界区）。
func (s *Session) bumpHistoryLocked() {
	s.histCache.valid = false
}

// histDerivedFor 返回当前历史的派生值（缓存命中直接复用；未命中现算并落缓存）。
// 调用方持锁。
func (s *Session) histDerivedFor() *histDerived {
	if !s.histCache.valid {
		msgs := append([]llm.Message(nil), s.history...)
		s.histCache = histDerived{
			valid:       true,
			tokens:      historyTokens(s.history),
			pairing:     AnalyzeToolPairing(s.history),
			msgs:        msgs,
			checkpoints: checkpointIndexes(msgs),
		}
	}
	return &s.histCache
}
