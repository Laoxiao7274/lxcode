// 会话轮次域：chat.*（发送 / 取消 / 历史 / 压缩 / 工具确认）。

package server

import (
	"encoding/json"
	"errors"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func (s *Server) dispatchChat(c *wsClient, req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodChatSend:
		var p protocol.ChatSendParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if !protocol.ValidateEffort(p.Effort) {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"effort 必须是 minimal/low/medium/high 之一（空 = 模型默认）")
		}
		if !protocol.ValidateApproval(p.Approval) {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"approval 必须是 auto/confirm/strict 之一（空 = confirm）")
		}
		id := p.SessionID
		if id == "" {
			id = c.sessionID
		}
		if id == "" {
			var sess *agent.Session
			var err error
			id, sess, err = s.createSession("")
			if err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
			c.sessionID = id
			if err := s.sendSession(id, sess, p.Text, chatSendOptions(p)...); err != nil {
				return protocol.NewError(req.ID, errorCode(err), err.Error())
			}
			// 用户消息重置连续唤醒预算（契约 §5：用户交互本身就是交互，
			// 不该被自己的操作耗掉预算）
			s.resetWakes(id)
			return protocol.NewResult(req.ID, map[string]any{"accepted": true, "session_id": id})
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		if err := s.sendSession(id, sess, p.Text, chatSendOptions(p)...); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		// 用户消息重置连续唤醒预算（契约 §5）
		s.resetWakes(id)
		return protocol.NewResult(req.ID, map[string]any{"accepted": true, "session_id": id})

	case protocol.MethodChatCancel:
		var p protocol.ChatSessionParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id := p.SessionID
		if id == "" {
			id = c.sessionID
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		sess.Cancel()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodChatHistory:
		var p protocol.ChatHistoryParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id := p.SessionID
		if id == "" {
			id = c.sessionID
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		if len(sess.History().Messages) > 0 {
			if err := s.prepareWorktree(id, sess); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
		}
		return protocol.NewResult(req.ID, s.history(id, sess))

	case protocol.MethodChatCompact:
		var p protocol.CompactParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id := p.SessionID
		if id == "" {
			id = c.sessionID
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		res, err := sess.Compact(p.Agent)
		if err != nil {
			if errors.Is(err, agent.ErrNothingToCompact) {
				return protocol.NewResult(req.ID, protocol.CompactResult{Compacted: false, Reason: err.Error()})
			}
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		s.broadcastSessionChanged(id, "compacted")
		return protocol.NewResult(req.ID, protocol.CompactResult{
			Compacted: true, Before: res.Before, After: res.After, Shadowed: res.Shadowed,
		})

	case protocol.MethodChatApproval:
		var p protocol.ChatApprovalParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if !protocol.ValidateApproval(p.Approval) {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"approval 必须是 auto/confirm/strict 之一（空 = confirm）")
		}
		id := p.SessionID
		if id == "" {
			id = c.sessionID
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		// SetApproval 立刻作用于**运行中的**一轮（内核每次工具调用现读），
		// 切到 auto 时还会放行挂起的那张确认卡。
		approval := sess.SetApproval(p.Approval)
		// 广播（含发请求的这个客户端）：多客户端同步——壳与浏览器同时开着时
		// 两边的档位必须一致，否则用户在一侧改了、另一侧还显示旧档。
		s.broadcast(protocol.EventApproval, protocol.ApprovalChangedParams{
			SessionID: id, Approval: approval,
		})
		return protocol.NewResult(req.ID, protocol.ChatApprovalResult{Approval: approval})

	case protocol.MethodToolConfirm:
		var p protocol.ToolConfirmParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id := p.SessionID
		if id == "" {
			id = c.sessionID
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		if err := sess.Confirm(p.ID, p.Allow); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		return protocol.NewResult(req.ID, map[string]any{})
	}
	return nil
}
