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
		// 图片先纯校验（mime 白名单 / 张数 / 大小）再谈会话——参数不合法时
		// 连会话行都不该建（入历史前拒绝，不留半截状态）。文件附件同批：
		// 个数 / 大小 / 名字净化同一道闸。
		decodedImgs, err := validateChatImages(p.Images)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		decodedFiles, fileNames, err := validateChatFiles(p.Files)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
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
			if err := s.sendAttachments(id, sess, p, decodedImgs, decodedFiles, fileNames); err != nil {
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
		if err := s.sendAttachments(id, sess, p, decodedImgs, decodedFiles, fileNames); err != nil {
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

	case protocol.MethodChatRewind:
		var p protocol.ChatRewindParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if p.Seq <= 0 {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "seq 必须是消息序号（正整数）")
		}
		id := p.SessionID
		if id == "" {
			id = c.sessionID
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		res, err := sess.Rewind(p.Seq)
		if err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		// 事件由内核发（RewoundEvent → chat.rewound，见 emit.go）：撤回改变了
		// 时间线，所有客户端都要在同一时刻收到同一份事实。这里只回答请求方。
		//
		// 另外广播一次会话元数据变化（与 chat.compact 同款）：撤回让消息数变小了，
		// 侧栏的条数得跟着刷新——客户端收到 session.changed 一律重拉会话列表。
		if res.Removed > 0 {
			s.broadcastSessionChanged(id, "rewound")
		}
		return protocol.NewResult(req.ID, protocol.ChatRewindResult{Removed: res.Removed})

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

	case protocol.MethodChatMergeRequest:
		var p protocol.ChatMergeRequestParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id := p.SessionID
		if id == "" {
			id = c.sessionID
		}
		if id == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id（没有当前会话）")
		}
		// 与 merge_request 工具同一条路径（startMergeJob）：前置校验（项目会话 /
		// 已有分支 / 同一会话不重复起）与错误文案都在那里，这里只透传。
		jobID, err := s.startMergeJob(id, p.TargetBranch, false)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, protocol.MergeRequestResult{JobID: jobID})

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
		// ask 提问（tool.confirm 带 answer）走 Answer 路径：文本答案投进与二元
		// 确认同一个等待通道（Session.Answer 校验挂起请求存在、id 匹配且是
		// ask 形态）；answer 为空 = 现状语义（二元批准/拒绝）。
		if p.Answer != "" {
			if err := sess.Answer(p.ID, p.Answer); err != nil {
				return protocol.NewError(req.ID, errorCode(err), err.Error())
			}
			return protocol.NewResult(req.ID, map[string]any{})
		}
		if err := sess.Confirm(p.ID, p.Allow); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		return protocol.NewResult(req.ID, map[string]any{})
	}
	return nil
}
