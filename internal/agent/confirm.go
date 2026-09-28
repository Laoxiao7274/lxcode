// 确认门：高危工具执行前问用户。全应用只有「同时一个挂起确认」——
// 并行 dispatch 下多个子会话会同时来要确认，所以这里必须串行化（confirmMu）。

package agent

import (
	"context"
	"errors"
	"fmt"
)

// SetConfirmProxy 挂确认门代理（子会话 → 父会话；见 confirmProxy 字段注释）。
func (s *Session) SetConfirmProxy(fn func(ctx context.Context, req *ConfirmRequest) (bool, bool)) {
	s.mu.Lock()
	s.confirmProxy = fn
	s.mu.Unlock()
}

// awaitConfirm 挂起等宿主裁决；取消返回 ok=false。
// 挂了确认门代理（子会话）时交给代理——全应用只有"同时一个挂起确认"，
// 子会话自己持 pending 的话服务端的 tool.confirm 找不到它（会话卡死）。
// 代理路径由代理方发事件（这里不再发，否则确认卡会重复出现）。
func (s *Session) awaitConfirm(ctx context.Context, req *ConfirmRequest) (allow bool, ok bool) {
	s.mu.Lock()
	proxy := s.confirmProxy
	s.mu.Unlock()
	if proxy != nil {
		return proxy(ctx, req)
	}

	// 串行化（见 confirmMu 的注释）：并行 dispatch 下多个子会话可能同时来要确认，
	// 而确认槽只有一个（s.pending/s.confirm）——后到的会顶掉先到的。
	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()

	s.mu.Lock()
	s.pending = req
	s.confirm = make(chan bool, 1)
	ch := s.confirm
	s.mu.Unlock()

	s.emit(ConfirmRequestEvent{Request: req})
	select {
	case a := <-ch:
		s.mu.Lock()
		s.pending, s.confirm = nil, nil
		s.mu.Unlock()
		return a, true
	case <-ctx.Done():
		s.mu.Lock()
		s.pending, s.confirm = nil, nil
		s.mu.Unlock()
		return false, false
	}
}

// Confirm 是确认门裁决；id 必须匹配当前挂起的调用。
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
	case ch <- allow:
	default: // 已投递过（重复确认）
	}
	return nil
}
