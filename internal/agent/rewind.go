// 会话回退（撤回 / rewind）：用户在某条自己发的消息上点「撤回」——那条消息及其之后的
// 全部历史从会话里删掉（内存 + 库），那条消息的正文回到输入框，用户改完重发。
//
// 与压缩（compaction.go）的分工：压缩是「把长历史换成摘要」，撤回是「把历史砍掉一截」。
// 两者共享三条纪律——空闲才允许、落库成功才改内存（fail-closed）、占用测量要跟着走。
//
// 用户的原话：「撤回会把下面的对话清除，然后自己发的这条回到输入框，**记得当前的上下文里
// 也得清理掉对应的**」——后半句就是"内存里的 s.history 也要截断"，只删库的话模型下一轮
// 仍然看得见被撤回的对话（它才是真正发给模型的那份历史）。

package agent

import (
	"errors"
	"fmt"

	"github.com/moyunteng/lxcode/internal/llm"
)

// RewindResult 是一次撤回的结果（宿主广播 + 测试断言）。
type RewindResult struct {
	Seq     int64 // 撤回锚点（那条用户消息的序号）
	Removed int   // 从会话历史里删掉的条数（0 = 幂等空操作：锚点已经不在历史里）
}

// Rewind 撤回锚点 seq 那条消息**及其之后的全部历史**。
//
// 为什么必须空闲（busy → ErrBusy，与 Compact 同款）：正在跑的那一轮手里握着开轮那一刻的
// 历史快照，抽掉它脚下的历史不会让那一轮停下来——它会按一份已经不存在的上下文继续生成，
// 然后把 assistant/tool 消息追加到一段"穿越"了撤回点的历史上（甚至可能把刚被撤回的用户消息
// 之后的对话重新接回来）。这类竞态无法用锁修补，只能拒绝。
//
// 为什么先落库再改内存（fail-closed，与压缩同款）：落库失败而内存已经截断，下一次 Load
// 会得到另一段历史——内存与库分叉是这类改动最贵的故障（它不可见，直到重启）。
//
// 为什么锚点必须是配对平衡的切点：撤回锚点是 user 消息（轮边界），切点天然平衡；但库里
// 真存在 P5 之前留下的畸形历史（取消时没补配对）。在那种历史上切一刀会留下孤儿 tool_call，
// 严格端点直接 400 拒收整轮（toolpair.go 的不变量）。这里**守住**这条不变量，而不是指望
// 调用方——拒绝撤回比产出一个发不出去的历史好。
func (s *Session) Rewind(seq int64) (RewindResult, error) {
	if seq <= 0 {
		// 0 不是合法序号：没有落库的消息在内存里 Seq 也是 0，拿它当锚点会误伤
		//（第一条没落库的消息之后的所有历史）。宁可报错也不猜。
		return RewindResult{}, errors.New("撤回锚点必须是消息序号（正整数）")
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return RewindResult{}, fmt.Errorf("%w（先取消当前生成再撤回）", ErrBusy)
	}
	idx := -1
	for i, m := range s.history {
		if m.Seq == seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		// 幂等：锚点已经不在历史里（重复撤回、两个客户端同时点撤回、那条消息没落库）
		// ——不是错误，返回 0 让调用方与客户端都当作空操作
		s.mu.Unlock()
		return RewindResult{}, nil
	}
	if !AnalyzeToolPairing(s.history).BalancedBefore(idx) {
		s.mu.Unlock()
		return RewindResult{}, errors.New("撤回锚点不是配对平衡的切点（之前有未回填的工具调用），拒绝撤回")
	}
	// 被删段先取副本：下面要拿它算占用差值，而截断之后就再也读不到它们了
	dropped := append([]llm.Message(nil), s.history[idx:]...)
	removed := len(dropped)

	if s.st != nil && s.id != "" {
		if _, err := s.st.Rewind(s.id, seq); err != nil {
			s.mu.Unlock()
			return RewindResult{}, fmt.Errorf("撤回落库失败（历史未改动）: %w", err)
		}
	}
	// 内存历史才是运行真源：这一步不能省。新切片而不是原地截断——共享底层数组的话，
	// 之后的 append 会就地覆写"已经删掉"的那些元素，撤回的对话仍在内存里（只是看不见）。
	s.history = append([]llm.Message(nil), s.history[:idx]...)
	// 占用测量跟着走（压缩同款锚定算术）：不重算的话指示器停在撤回前的数字
	removedTokens := 0
	for _, m := range dropped {
		removedTokens += estimateMessageTokens(m)
	}
	s.context = reanchoredUsage(s.context, removedTokens, s.history)
	usage := s.context
	st, id := s.st, s.id
	s.mu.Unlock()

	// 新占用落库（与 recordContextUsage 同款）：不落的话用户重启后会看到撤回**之前**
	// 的数字——live（chat.rewound 带的 context）与 replay（chat.history）又分叉一次。
	// 失败只记日志：撤回本身已经成功，占用是展示信息。
	persistContextUsage(st, id, usage)

	s.emit(RewoundEvent{Seq: seq, Removed: removed, Context: usage})
	return RewindResult{Seq: seq, Removed: removed}, nil
}
