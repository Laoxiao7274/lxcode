// 自动通告（后台任务唤醒）的投递与排队。
//
// 契约 docs/jobs.md §5：settle 之后把通告投递给**归属会话**——owner 忙则
// 注入下一步，空闲则开一轮。lxcode 原先没有任何"往运行中的一轮注入消息"的
// 机制（Send 忙时直接 ErrBusy），所以这里引入一个**待投递通告队列**：
//   - 忙：排队，等**轮边界**并入历史（injectNotices）；
//   - 空闲：直接 Send 开一轮（Notify），或只排队等下次（QueueNotice）。
//
// 为什么注入点必须是**轮边界**（runTurn 每轮循环的开头）：那是唯一安全的
// 历史插入点——上一轮所有工具结果此时已全部落进历史，插一条 user 消息不会把
// assistant 的 tool_calls 与它的结果拆开。拆开有两个后果（见 toolpair.go 与
// AGENTS.md §2.2 的 P2）：严格端点直接 400 拒收整轮，而且游标在缺配对处
// 之后再不平衡，压缩切点永久卡在它之前——那个会话从此再也压不动。
//
// 为什么通告是真的 user 角色消息：模型必须把它当成一次用户回合来回应
//（这是唤醒的全部意义）；「它不是用户说的」这件事由文本前缀标注
//（protocol.JobNoticePrefix），前端据此渲染成区别于用户气泡的提示条。

package agent

import (
	"errors"
	"log"
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
)

// Notify 投递一条自动通告：空闲则开一轮，忙则排队（轮边界注入）。
func (s *Session) Notify(text string) error {
	msg, err := noticeMessage(text)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.busy {
		s.notices = append(s.notices, msg)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if err := s.Send(msg.Content, WithNotice()); err != nil {
		if errors.Is(err, ErrBusy) {
			// 竞态：查忙闲与 Send 之间别人起了一轮——退回队列，
			// 那一轮会在它的轮边界把它并进历史（通告不丢）
			s.mu.Lock()
			s.notices = append(s.notices, msg)
			s.mu.Unlock()
			return nil
		}
		return err
	}
	return nil
}

// QueueNotice 只把通告放进待投递队列，**不开新一轮**。
//
// 唤醒预算耗尽时用它（契约 §5）：通告留在队列里，等下一次轮边界或用户下次
// 说话时投递——**绝不静默丢弃**（丢掉会让模型基于过期信息继续做，比如它还在
// 等一个已经被用户杀掉的 dev server）。
func (s *Session) QueueNotice(text string) error {
	msg, err := noticeMessage(text)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.notices = append(s.notices, msg)
	s.mu.Unlock()
	return nil
}

// QueueNoticePassive 把通告放进**被动队列**：只入队、且**永不自动开轮**——
// 它只会在下一轮真的开始时（用户说话 / 下一次派发）由轮边界的 injectNotices
// 并入历史。
//
// 与 QueueNotice 的分工：后台任务通告（notices）的语义是「尽快唤醒 agent」，
// runTurn 收尾的 flushNotices 会把它开成一轮；而「生成被用户中断」这类注记
// 走这条路会把中断变成模型的自言自语（用户刚点停止，又冒一段回复）——被动
// 队列保证注记只在有真实下一轮时才被模型看见。
func (s *Session) QueueNoticePassive(text string) error {
	msg, err := noticeMessage(text)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.passiveNotices = append(s.passiveNotices, msg)
	s.mu.Unlock()
	return nil
}

// SetWakeGate 注入「能不能开新一轮」的判定（server 侧的连续唤醒预算）。
// nil = 不限制（单测与未装配预算的调用方）。
//
// 它只约束**空闲 → 开一轮**这一条路（flushNotices）：轮边界注入不额外开轮，
// 不消耗预算——会话已经在跑，压着通告不给反而让模型基于过期信息继续做。
func (s *Session) SetWakeGate(fn func() bool) {
	s.mu.Lock()
	s.wakeGate = fn
	s.mu.Unlock()
}

func noticeMessage(text string) (llm.Message, error) {
	if strings.TrimSpace(text) == "" {
		return llm.Message{}, errors.New("通告内容不能为空")
	}
	// Notice=true：这不是用户说的话。它随消息落库，会话统计的轮数按它排除
	//（否则「后台任务结束」会被算成用户发了一轮）。
	return llm.Message{Role: "user", Content: text, Notice: true}, nil
}

// injectNotices 把排队通告并入历史（**轮边界**调用）。
//
// 为什么必须是轮边界：runTurn 每轮开头重新快照历史，此刻上一轮所有工具结果
// 已全部落进历史——插一条 user 消息不会把 assistant 的 tool_calls 与它的
// 结果拆开（配对不变量，见 toolpair.go）。插在别处（如工具执行中间）会让
// 严格端点 400 拒收整轮，而且压缩的平衡切点会永久卡在缺配对处之前。
func (s *Session) injectNotices() {
	s.mu.Lock()
	if len(s.notices) == 0 && len(s.passiveNotices) == 0 {
		s.mu.Unlock()
		return
	}
	msgs := s.notices
	s.notices = nil
	passive := s.passiveNotices
	s.passiveNotices = nil
	s.mu.Unlock()
	// 先投后台通告（它们等得最久），再投被动注记（用户中断这类——离当下最近）
	for _, m := range msgs {
		s.append(m)
		s.emit(UserMsgEvent{Message: m})
	}
	for _, m := range passive {
		s.append(m)
		s.emit(UserMsgEvent{Message: m})
	}
}

// flushNotices 在轮次收尾后把剩余通告开成新一轮（**空闲 → 开一轮**）。
//
// 受唤醒预算约束（wakeGate）：预算耗尽时通告**留在队列里**，等下一次轮边界
// 或用户下次说话时投递——绝不丢弃。投递失败（如模型未配置）同样放回队列。
func (s *Session) flushNotices() {
	s.mu.Lock()
	if s.busy || len(s.notices) == 0 {
		s.mu.Unlock()
		return
	}
	gate := s.wakeGate
	s.mu.Unlock()
	// 预算判定在锁外调：它可能回调到别处；判定只影响「开不开这一轮」，
	// 队列内容一律不动（预算耗尽 = 留着，不是丢）
	if gate != nil && !gate() {
		return
	}
	s.mu.Lock()
	if s.busy || len(s.notices) == 0 {
		s.mu.Unlock()
		return
	}
	msgs := s.notices
	s.notices = nil
	s.mu.Unlock()
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.Content)
	}
	if err := s.Send(b.String(), WithNotice()); err != nil {
		// 投不出去（模型缺失 / 又忙了）：放回队列等下一次唤醒
		s.mu.Lock()
		s.notices = append(msgs, s.notices...)
		s.mu.Unlock()
		log.Printf("自动通告投递失败（已放回队列，不丢）: %v", err)
	}
}
