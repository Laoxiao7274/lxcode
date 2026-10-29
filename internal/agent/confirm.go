// 确认门：高危工具执行前问用户；ask_user 提问等用户回答（同一扇门、两种形态）。
// 全应用只有「同时一个挂起确认」——并行 dispatch 下多个子会话会同时来要确认，
// 所以这里必须串行化（confirmMu）。

package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/moyunteng/lxcode/internal/tools"
)

// ConfirmKindAsk 标记一次「提问」形态的确认请求（ask_user 工具发起）：
// 用户以文本回答，而不是二元批准/拒绝。空 Kind = 现状语义（高危工具确认）——
// 老事件不带 kind 字段，消费方按空值回落到确认语义。
const ConfirmKindAsk = "ask"

// ConfirmOutcome 是确认门的一次裁决结果：二元确认只填 Allow；
// ask 提问在用户回答时填 Answer（Allow 恒 true——「回答」本身就是放行继续），
// 用户跳过（不回答）时 Answer 为空且 Allow=false，与「拒绝」同义。
type ConfirmOutcome struct {
	Allow  bool
	Answer string
}

// newConfirmID 生成一次挂起请求的唯一 id。ask_user 不经过工具调用循环
//（没有 tc.ID 可用），得自己造一个不与工具调用 id 冲突的。
func newConfirmID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 随机源失败（几乎不可能）：纳秒时间戳兜底——id 只要求会话内唯一
		return fmt.Sprintf("ask-%d", time.Now().UnixNano())
	}
	return "ask-" + hex.EncodeToString(b[:])
}

// SetConfirmProxy 挂确认门代理（子会话 → 父会话；见 confirmProxy 字段注释）。
func (s *Session) SetConfirmProxy(fn func(ctx context.Context, req *ConfirmRequest) (ConfirmOutcome, bool)) {
	s.mu.Lock()
	s.confirmProxy = fn
	s.mu.Unlock()
}

// awaitConfirm 挂起等宿主裁决；取消返回 ok=false。
// 挂了确认门代理（子会话）时交给代理——全应用只有"同时一个挂起确认"，
// 子会话自己持 pending 的话服务端的 tool.confirm 找不到它（会话卡死）。
// 代理路径由代理方发事件（这里不再发，否则确认卡会重复出现）。
func (s *Session) awaitConfirm(ctx context.Context, req *ConfirmRequest) (ConfirmOutcome, bool) {
	s.mu.Lock()
	proxy := s.confirmProxy
	s.mu.Unlock()
	if proxy != nil {
		return proxy(ctx, req)
	}

	// 串行化（见 confirmMu 的注释）：并行 dispatch 下多个子会话可能同时来要确认，
	// 而确认槽只有一个（s.pending/s.confirm）——后到的会顶掉先到的。
	// ask 提问与高危确认共用这一个槽位（不另开槽）：用户一次只裁决一件事。
	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()

	s.mu.Lock()
	s.pending = req
	s.confirm = make(chan ConfirmOutcome, 1)
	ch := s.confirm
	s.mu.Unlock()

	s.emit(ConfirmRequestEvent{Request: req})
	select {
	case out := <-ch:
		s.mu.Lock()
		s.pending, s.confirm = nil, nil
		s.mu.Unlock()
		return out, true
	case <-ctx.Done():
		s.mu.Lock()
		s.pending, s.confirm = nil, nil
		s.mu.Unlock()
		return ConfirmOutcome{}, false
	}
}

// Confirm 是确认门的二元裁决（高危工具：批准/拒绝）；id 必须匹配当前挂起的调用。
func (s *Session) Confirm(id string, allow bool) error {
	s.mu.Lock()
	pending := s.pending
	ch := s.confirm
	s.mu.Unlock()
	if pending == nil || ch == nil {
		return errors.New("没有待确认的工具调用")
	}
	if pending.ID != id {
		return fmt.Errorf("确认 id 不匹配（当前挂起: %s）", pending.ID)
	}
	select {
	case ch <- ConfirmOutcome{Allow: allow}:
	default: // 已投递过（重复确认）
	}
	return nil
}

// Answer 是 ask_user 提问的用户回答（文本）；id 必须匹配当前挂起的请求，
// 且它必须是提问形态（ask）——普通高危确认走 Confirm，不能拿文本答案冒充批准。
func (s *Session) Answer(id, text string) error {
	s.mu.Lock()
	pending := s.pending
	ch := s.confirm
	s.mu.Unlock()
	if pending == nil || ch == nil {
		return errors.New("没有待回答的提问")
	}
	if pending.ID != id {
		return fmt.Errorf("提问 id 不匹配（当前挂起: %s）", pending.ID)
	}
	if pending.Kind != ConfirmKindAsk {
		return fmt.Errorf("当前挂起的是工具确认（%s），不是提问——请用批准/拒绝裁决", pending.Name)
	}
	select {
	case ch <- ConfirmOutcome{Allow: true, Answer: text}:
	default: // 已投递过（重复回答）
	}
	return nil
}

// askUser 是 ask_user 工具的会话侧实现（每轮经 tools.WithAsker 注入 ctx）：
// 构造 Kind=ask 的确认请求走 awaitConfirm——与高危确认共用同一个挂起槽位、
// 同一张确认卡通道（confirmMu 串行化），前端零新页面。
//
// 注意：ask **不走** tools.Registry.Confirm 的高危确认文案路径——它不是高危
// 工具确认，由本函数自己发起（runTools 的确认门只管 Registry 判定要确认的调用）。
//
// 返回 answer=true 表示用户给了文本回答；false = 用户跳过（等价拒绝）或等待被取消。
func (s *Session) askUser(ctx context.Context, question string, options []string) (string, bool) {
	args, _ := json.Marshal(map[string]any{"question": question, "options": options})
	req := &ConfirmRequest{
		ID: newConfirmID(), Name: tools.AskUserToolName, Kind: ConfirmKindAsk,
		Arguments: string(args), Prompt: question, Options: options,
	}
	out, ok := s.awaitConfirm(ctx, req)
	if !ok || out.Answer == "" {
		return "", false
	}
	return out.Answer, true
}
