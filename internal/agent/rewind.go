// 会话回退（撤回 / rewind）与切叶（setLeaf）：两者都只是**移动叶指针**——
// 一行历史都不删，旧分支完整留在树里，切回去就回来。
//
// 为什么从「真删行」改成「移 leaf」（docs/session-tree.md §B.1，2026-12 用户拍板）：
// 用户点「撤回」的意图是「从这条之前重来」，而旧实现把后面的历史真的删了——那条
// 分支再也找不回来。移 leaf 保住同样的可观察行为（时间线截断到锚点之前），
// 但数据一条不丢。
//
// 两个入口的落点差一格（**必须写成两个具名方法，不在调用处各算一遍**）：
//
//	Rewind(anchor)  → leaf = parent(anchor)   -- 锚点自己也离开路径（要重写它）
//	SetLeaf(seq)    → leaf = seq              -- 「从这里接着长」
//
// 三条共享纪律（与压缩同款）：空闲才允许、落库成功才改内存（fail-closed）、
// 占用测量要跟着走（**不能沿用落库那份测量**——那是切分支之前那条路径的）。
package agent

import (
	"errors"
	"fmt"

	"github.com/moyunteng/lxcode/internal/llm"
)

// RewindResult 是一次「回到这里」的结果（宿主广播 + 测试断言）。
type RewindResult struct {
	Seq     int64 // 锚点（那条用户消息的序号）
	Removed int   // 离开当前路径的条数（与旧删除语义逐例相同，见 store.SetLeaf 的注释）
	// LeafSeq 是移动后的叶（0 = 空路径）。客户端用它对齐时间线：新历史就是
	// path(leaf)——它本地 planRewind 截断出来的前缀恰好就是这条路径。
	LeafSeq int64
}

// SetLeafResult 是一次切叶的结果。
type SetLeafResult struct {
	LeafSeq int64
	Removed int
}

// Rewind 回到 seq 那条消息**之前**（叶移到它的父节点）。
//
// 为什么必须空闲（busy → ErrBusy，与 Compact 同款）：正在跑的那一轮手里握着开轮那一刻的
// 历史快照，抽掉它脚下的历史不会让那一轮停下来——它会按一份已经不存在的上下文继续生成，
// 然后把 assistant/tool 消息追加到一段"穿越"了撤回点的历史上。
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
	prevLeaf := s.leafLocked()
	st, id := s.st, s.id
	s.mu.Unlock()

	// 落库（fail-closed）：库里的叶指针是事实源，内存历史从它重载。
	// 不重载而是"内存截断"的话，两条路径（内存/库）迟早分叉——重载就是真相。
	leaf := int64(0)
	removed := 0
	if st != nil && id != "" {
		var err error
		removed, leaf, err = st.RewindTo(id, seq)
		if err != nil {
			return RewindResult{}, fmt.Errorf("撤回落库失败（历史未改动）: %w", err)
		}
	}
	s.mu.Lock()
	if st == nil || id == "" {
		// 纯内存模式（无存储）：没有 leaf 可移，退化成旧的截断语义（内存里只有一份
		// 历史，截断它不丢任何"还能找回"的东西——没有库就没有分支）。
		removed = len(s.history) - idx
		s.history = append([]llm.Message(nil), s.history[:idx]...)
		leaf = s.leafLocked()
	} else {
		msgs, err := st.Load(id)
		if err != nil {
			s.mu.Unlock()
			return RewindResult{}, fmt.Errorf("撤回后重载历史失败: %w", err)
		}
		s.history = msgs
	}
	s.mu.Unlock()

	usage, err := s.afterLeafMove(st, id, leaf, prevLeaf, removed)
	if err != nil {
		return RewindResult{}, err
	}
	// rewind **两个事件都发**（docs §B.3）：chat.rewound 保住老客户端的本地截断
	// 路径（它按 planRewind 截到锚点之前，得到的恰好是新路径），chat.leafChanged
	// 给新客户端与树面板（带新叶与 prev 叶）。
	s.emit(RewoundEvent{Seq: seq, Removed: removed, LeafSeq: leaf, Context: usage})
	return RewindResult{Seq: seq, Removed: removed, LeafSeq: leaf}, nil
}

// SetLeaf 把叶直接移到 seq（0 = 空路径）：继续对话就从这里长出新分支。
//
// 与 Rewind 的差别只有落点（seq 自己留在路径上）——见文件头。
//
// 三条硬规则（每条都有测试）：
//   - **seq 必须属于本会话**（0 = 空路径合法）：坏号报错而**不静默当 0**——
//     静默会把用户送到一个他没点的位置；
//   - **以 seq 结尾的路径必须配对平衡**：切在 assistant 的 tool_calls 与它的
//     tool 结果之间会让下一轮请求直接被端点 400 拒收。协议是公开的（CLI/安卓/
//     脚本都能发），服务端必须自己守——不指望客户端只允许点 user 节点；
//   - 切到**被压缩影子掉的行**是合法的，且语义很自然：那条行的链不经过检查点
//     （A6.parent = CK，而 A1..A5 自成一条链），所以切过去之后路径里没有生效的
//     检查点 → 原文自动"解压"可见。这正是「回到压缩前的位置继续」想要的行为。
func (s *Session) SetLeaf(seq int64) (SetLeafResult, error) {
	if seq < 0 {
		return SetLeafResult{}, errors.New("seq 不能为负（0 = 空路径，回到第一条消息之前）")
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return SetLeafResult{}, fmt.Errorf("%w（先取消当前生成再切分支）", ErrBusy)
	}
	st, id := s.st, s.id
	prevLeaf := s.leafLocked()
	s.mu.Unlock()
	if st == nil || id == "" {
		return SetLeafResult{}, errors.New("未启用持久化，无法切分支")
	}

	// 配对不变量：先用**落库后的路径**判一遍再动指针（判完不动就等于没发生）。
	// 这里读一次路径而不是用内存历史：seq 可能来自废弃分支，内存里根本没有它。
	if seq > 0 {
		msgs, err := s.pathMessagesTo(st, id, seq)
		if err != nil {
			return SetLeafResult{}, err
		}
		pairing := AnalyzeToolPairing(msgs)
		if !pairing.BalancedBefore(len(msgs)) {
			return SetLeafResult{}, errors.New(
				"这条消息后面还有未完成的工具调用（切在这里会让历史发不出去），请选它之后的节点")
		}
	}

	removed, leaf, err := st.SetLeaf(id, seq)
	if err != nil {
		return SetLeafResult{}, err
	}
	msgs, err := st.Load(id)
	if err != nil {
		return SetLeafResult{}, fmt.Errorf("切叶后重载历史失败: %w", err)
	}
	s.mu.Lock()
	s.history = msgs
	s.mu.Unlock()

	if _, err := s.afterLeafMove(st, id, leaf, prevLeaf, removed); err != nil {
		return SetLeafResult{}, err
	}
	return SetLeafResult{LeafSeq: leaf, Removed: removed}, nil
}

// pathMessagesTo 读「以 seq 结尾的那条路径」的消息（配对判定用）。
// 走 store 的公开读口：先记下当前叶、临时切过去读、再切回来——不引入第二套
// 路径查询（agent 层不该自己算 parent 链）。
//
// 代价是两次 UPDATE + 一次 Load；setLeaf 是低频人工操作，这个代价可接受，
// 换来的是「配对判定与真实回放同源」。
func (s *Session) pathMessagesTo(st Persistence, id string, seq int64) ([]llm.Message, error) {
	cur, err := st.LeafSeq(id)
	if err != nil {
		return nil, err
	}
	if cur == seq {
		return st.Load(id)
	}
	if _, _, err := st.SetLeaf(id, seq); err != nil {
		return nil, err
	}
	msgs, loadErr := st.Load(id)
	// 无论判定结果如何都要把叶还回去（判定失败时这次试探不该改变状态）。
	if _, _, err := st.SetLeaf(id, cur); err != nil && loadErr == nil {
		return nil, fmt.Errorf("恢复叶指针失败（会话停在 seq %d）: %w", seq, err)
	}
	if loadErr != nil {
		return nil, loadErr
	}
	return msgs, nil
}

// afterLeafMove 是叶移动之后的共同收尾：**重算占用测量**（不沿用落库那份——
// 那是切分支之前那条路径的测量）+ 落库 + 发事件。
//
// 为什么不能用 reanchoredUsage 的锚定算术（docs §E.4）：rewind 的新路径是旧路径的
// 前缀（只砍尾巴），锚定算术在数学上仍然成立；但 setLeaf 切到**另一条分支**时，
// 进入段与离开段毫无关系，锚定算术会算出假数字。两者都走「重估 + Estimated=true」
// ——一份实现，少一条分支就少一处漂移；精度由下一次 chat.done 的真实 prompt_tokens
// 校正回来。
func (s *Session) afterLeafMove(st Persistence, id string, leaf, prevLeaf int64, removed int) (ContextUsage, error) {
	s.mu.Lock()
	history := append([]llm.Message(nil), s.history...)
	agentID := s.agentID
	s.mu.Unlock()

	u := estimateContextUsage("", nil, history)
	if len(history) == 0 {
		// 空路径 = 两个来源都没有：返回**零值**（wire 上整键缺席、前端中性态），
		// 不编数（estimateContextUsage 对空历史也会给 4 个 token 的结构开销）。
		u = ContextUsage{}
	} else {
		// 窗口是**模型属性**不是路径属性：解析不出来时沿用旧值（沿用不是编数——
		// 它是上一次真实解析的结果）。解析得到就用新的（用户可能中途换了模型）。
		u.Window = s.contextWindowFor(agentID)
		if u.Window == 0 {
			s.mu.Lock()
			u.Window = s.context.Window
			s.mu.Unlock()
		}
		u.Estimated = true // 如实标注：这是估算，不是 provider 回报的真实用量
	}
	s.mu.Lock()
	s.context = u
	usage := projectedUsage(s.context, s.history)
	s.mu.Unlock()
	persistContextUsage(st, id, u)

	s.emit(LeafChangedEvent{
		LeafSeq: leaf, PrevLeafSeq: prevLeaf, Removed: removed, Context: usage,
	})
	return usage, nil
}

// leafLocked 返回内存视角的当前叶（= 历史最后一条的 Seq；空历史 = 0）。
// 只用于事件里的 prev_leaf_seq（客户端据此判断"是不是别人动的"）——权威值
// 永远从库里读（st.LeafSeq / SetLeaf 的返回值）。调用方持锁。
func (s *Session) leafLocked() int64 {
	if len(s.history) == 0 {
		return 0
	}
	return s.history[len(s.history)-1].Seq
}
