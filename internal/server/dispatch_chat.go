// 会话轮次域：chat.*（发送 / 取消 / 历史 / 压缩 / 工具确认）。

package server

import (
	"encoding/json"
	"errors"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/tools"
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
		decodedFiles, fileNames, fileRelPaths, err := validateChatFiles(p.Files)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		id := p.SessionID
		if id == "" {
			id = c.focus()
		}
		if id == "" {
			var sess *agent.Session
			var err error
			id, sess, err = s.createSession("")
			if err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
			c.setFocus(id)
			if err := s.sendAttachments(id, sess, p, decodedImgs, decodedFiles, fileNames, fileRelPaths); err != nil {
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
		if err := s.sendAttachments(id, sess, p, decodedImgs, decodedFiles, fileNames, fileRelPaths); err != nil {
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
			id = c.focus()
		}
		// 先在活跃会话的子会话登记里找：id 是某个**正在跑的**子会话时，它的
		// 运行时不在 s.sessions（生命周期跟着派发走）——直接 s.session(id) 会
		// newRuntime 建一个空对象，Cancel 是空操作、停不掉真正在跑的子轮。
		// 命中即真停；未命中（普通会话 / 子会话已结束 / 重启恢复场景）走下面
		// 的原路径，行为与原先一致。
		if s.cancelChildAnywhere(id) {
			return protocol.NewResult(req.ID, map[string]any{})
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
			id = c.focus()
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		if len(sess.History().Messages) > 0 {
			if err := s.prepareWorkspace(id, sess); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
		}
		// before_seq 分页（2026-12 安卓向上翻页）：>0 = 按 anchor 取前缀窗口，
		// tail 在这个语境里是「最多多少条」的窗口大小（与尾部窗口共用参数：
		// 客户端同一份 TAIL_WINDOW 常量，语义 = 窗口大小）。0 = 老语义（tail
		// 从尾部数 N 条）。
		if p.BeforeSeq > 0 && p.Tail <= 0 {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"before_seq 分页必须同时给 tail（窗口大小，正整数）")
		}
		res, err := s.history(id, sess, p.Tail, p.BeforeSeq)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, res)

	case protocol.MethodChatCompact:
		var p protocol.CompactParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id := p.SessionID
		if id == "" {
			id = c.focus()
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
			id = c.focus()
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

	case protocol.MethodChatModel:
		var p protocol.ChatModelParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if p.ModelID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"model_id 不能为空（要回默认就显式传 default 角色的模型 id）")
		}
		id := p.SessionID
		if id == "" {
			id = c.focus()
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		// 会话级状态：只改这一条会话（校验模型存在且启用，不存在不静默回落）。
		model, err := sess.SetModel(p.ModelID)
		if err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		// 广播（含发请求的这个客户端）：多客户端同步——壳与浏览器/手机同时看着
		// 同一条会话时两边的模型选择必须一致，否则用户在一侧改了、另一侧还显示旧模型。
		// 用 chat.modelChanged 而不是 model.changed：后者会让客户端重拉整个注册表
		// （全局语义），这里只是某条会话的选择变了。
		s.broadcast(protocol.EventModelChanged, protocol.ModelChangedParams{
			SessionID: id, ModelID: model,
		})
		return protocol.NewResult(req.ID, protocol.ChatModelResult{SessionID: id, ModelID: model})

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
			id = c.focus()
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
			id = c.focus()
		}
		if id == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id（没有当前会话）")
		}
		// 遗留：合并进程已退役（2026-10 架构修订——会话直连主检出，没有分支合并
		// 流程）。与 merge_request 工具共用同一条答复口径，不启动任何任务；
		// 以错误码回带说明（前端就地展示人话，而不是假装起了一条任务）。
		return protocol.NewError(req.ID, protocol.CodeInvalidParams, tools.MergeRequestLegacyMessage())

	case protocol.MethodToolConfirm:
		var p protocol.ToolConfirmParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id := p.SessionID
		if id == "" {
			id = c.focus()
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
