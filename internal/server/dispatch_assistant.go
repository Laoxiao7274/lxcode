// 助手域方法分发：assistant.*（new / send / list / tasks / history / cancel / confirm / compact）。
//
// 放在 dispatchChat **之前**（dispatch 链的顺序即优先级）：六方法命中即返回。
// 参数与应答**复用 chat 既有结构**（只换方法名——前端一份解析代码两域共用）；
// 事件由 emit.go 的 assistantEventName 按会话域自动换名，这里只管请求入口。
//
// 域隔离的硬约束：assistant.* 只寻址助手域运行时（assistantSessions）——
// 主会话 id 传进来会在 AttachTo/读行时报错（不是静默串域）；主域 chat.*
// 传助手会话 id 同样报错（session(id) 按主域解析，helper 不认助手行——
// 它的行 parent_id=” 且 space='assistant'，SessionParentID 返回 ”，主域
// s.session 会现建运行时——所以 assistant.* 是助手会话的**唯一**合法入口，
// 前端按域路由，不交叉）。
package server

import (
	"encoding/json"
	"errors"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func (s *Server) dispatchAssistant(c *wsClient, req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodAssistantNew:
		// 新助手会话：显式建行落 space=assistant（不能走主域 createSession——
		// 那建的是 main 域）。建行即注册运行时（助手域不做懒建——space 列
		// 在 INSERT 里落，懒建路径的 Create() 只有 main 域）。
		if s.st == nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "会话存储未启用")
		}
		id, err := s.st.CreateIn(sessiondata.SpaceAssistant)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		if _, err := s.assistantSession(id); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		s.broadcast(protocol.EventSessionChanged, protocol.SessionChangedParams{ID: id, Reason: "created"})
		return protocol.NewResult(req.ID, protocol.SessionResult{SessionID: id})

	case protocol.MethodAssistantSend:
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
		if p.Images != nil || p.Files != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"assistant.send 暂不支持附件（images/files）——P0 范围外")
		}
		if p.SessionID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id（助手会话不设连接焦点，先 assistant.new）")
		}
		// 正在派发的子会话安全拦截（与 chat.send 同一条不变量：两份运行时
		// 并发写库 = 静默历史污染）。run_task 的子会话登记在**助手会话**的
		// children 里（子会话运行时不在 assistantSessions——生命周期跟派发走）。
		if s.busyChildOfAssistant(p.SessionID) {
			return protocol.NewError(req.ID, protocol.CodeBusy, "该子会话正在派发中，请稍候或先停止")
		}
		sess, err := s.assistantSession(p.SessionID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		opts := chatSendOptions(p)
		// 助手会话的默认执行 Agent = 助手（助手域的「空 = 助手」；主域的
		// resolveAgent 空 = main——两域的缺省语义不同，这里显式钉住）。
		if p.Agent == "" {
			opts = append(opts, agent.WithAgent(assistantAgentDef.ID))
		}
		if err := sess.Send(p.Text, opts...); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		s.resetWakes(p.SessionID)
		return protocol.NewResult(req.ID, map[string]any{"accepted": true, "session_id": p.SessionID})

	case protocol.MethodAssistantList:
		// 助手域会话列表：wire 形状与 session.list 同源（[]SessionMeta +
		// running 位），busy 判定扫 assistantSessions（与主域 busySessions
		// 同一套「活跃运行时缓存」事实源——不活跃必不在生成，不查库）。
		if s.st == nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "会话存储未启用")
		}
		metas, err := s.st.AssistantSessions()
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		busy := map[string]bool{}
		for _, sess := range s.assistantRuntimeSnapshots() {
			if sess.Busy() {
				busy[sess.SessionID()] = true
			}
		}
		list := toProtocolSessionList(metas)
		for i := range list {
			list[i].Running = busy[list[i].ID]
		}
		return protocol.NewResult(req.ID, protocol.AssistantListResult{Sessions: list})

	case protocol.MethodAssistantTasks:
		// 项目模式侧栏「来自助手」小节（2026-12）：某项目的 run_task 任务子会话。
		// wire 形状与 assistant.list 同构（AssistantListResult + running 位）；
		// running 位合流 busyChildOfAssistant——任务子会话的运行时生命周期跟
		// 派发走（不在 assistantSessions），列表的「在跑」判定只能经助父会话的
		// 子会话登记（与 assistant.send 的安全拦截同一事实源）。
		var p protocol.AssistantTasksParams
		if err := json.Unmarshal(params, &p); err != nil || p.ProjectID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 project_id")
		}
		if s.st == nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "会话存储未启用")
		}
		metas, err := s.st.AssistantTasksOfProject(p.ProjectID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		list := toProtocolSessionList(metas)
		for i := range list {
			list[i].Running = s.busyChildOfAssistant(list[i].ID)
		}
		return protocol.NewResult(req.ID, protocol.AssistantListResult{Sessions: list})

	case protocol.MethodAssistantHistory:
		var p protocol.ChatHistoryParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if p.SessionID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id")
		}
		sess, err := s.assistantSession(p.SessionID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		if p.BeforeSeq > 0 && p.Tail <= 0 {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"before_seq 分页必须同时给 tail（窗口大小，正整数）")
		}
		// prepareWorkspace 不做：助手会话没有项目归属，工作目录就是后端默认目录。
		res, err := s.history(p.SessionID, sess, p.Tail, p.BeforeSeq)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		return protocol.NewResult(req.ID, res)

	case protocol.MethodAssistantCancel:
		var p protocol.ChatSessionParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if p.SessionID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id")
		}
		// 先找助手会话登记里的**正在跑的**子会话（run_task 派发中）——子会话
		// 运行时不在 assistantSessions（生命周期跟派发走），直接建运行时的
		// Cancel 是空操作。命中即真停；未命中（普通助手会话/子会话已结束）
		// 走原路径。
		if s.cancelChildOfAssistant(p.SessionID) {
			return protocol.NewResult(req.ID, map[string]any{})
		}
		sess, err := s.assistantSession(p.SessionID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		sess.Cancel()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodAssistantConfirm:
		var p protocol.ToolConfirmParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if p.SessionID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id")
		}
		sess, err := s.assistantSession(p.SessionID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
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

	case protocol.MethodAssistantCompact:
		var p protocol.CompactParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if p.SessionID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id")
		}
		sess, err := s.assistantSession(p.SessionID)
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
		s.broadcast(protocol.EventSessionChanged, protocol.SessionChangedParams{ID: p.SessionID, Reason: "compacted"})
		return protocol.NewResult(req.ID, protocol.CompactResult{
			Compacted: true, Before: res.Before, After: res.After, Shadowed: res.Shadowed,
		})

	case protocol.MethodAssistantApproval:
		// 助手会话的执行档位（chat.approval 同款，只换方法名与事件域）：
		// SetApproval 立刻作用于**运行中的**一轮（内核每次工具调用现读），
		// 切到 auto 时还会放行挂起的那张确认卡。必须有 session_id——助手域
		// 不设连接焦点，没有「空 = 当前会话」的语义。
		var p protocol.ChatApprovalParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if !protocol.ValidateApproval(p.Approval) {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"approval 必须是 auto/confirm/strict 之一（空 = confirm）")
		}
		if p.SessionID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id")
		}
		sess, err := s.assistantSession(p.SessionID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		approval := sess.SetApproval(p.Approval)
		s.broadcastAssistant(p.SessionID, protocol.EventApproval, protocol.ApprovalChangedParams{
			SessionID: p.SessionID, Approval: approval,
		})
		return protocol.NewResult(req.ID, protocol.ChatApprovalResult{Approval: approval})

	case protocol.MethodAssistantModel:
		// 助手会话的模型选择（chat.model 同款，只换方法名与事件域）。省略
		// 主域的 BindAgentModel 分支：助手是内置 Agent（不在 agents 库表），
		// 模型选择就是会话级状态，没有「给这个 Agent 换模型」的语义。
		var p protocol.ChatModelParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if p.ModelID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams,
				"model_id 不能为空（要回默认就显式传 default 角色的模型 id）")
		}
		if p.SessionID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "缺少 session_id")
		}
		sess, err := s.assistantSession(p.SessionID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		model, err := sess.SetModel(p.ModelID)
		if err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		s.broadcastAssistant(p.SessionID, protocol.EventModelChanged, protocol.ModelChangedParams{
			SessionID: p.SessionID, ModelID: model,
		})
		return protocol.NewResult(req.ID, protocol.ChatModelResult{SessionID: p.SessionID, ModelID: model})

	case protocol.MethodAssistantConfigGet, protocol.MethodAssistantConfigSet:
		// 助手性格与拓展配置：全局配置（不是会话级状态），处理在
		// assistant_config.go 的 handleAssistantConfig。
		return s.handleAssistantConfig(req, params)
	}
	return nil
}

// cancelChildOfAssistant 在助手会话的子会话登记里找正在跑的派发并取消
// （assistant.cancel 的寻址路径——与主域 cancelChildAnywhere 同构，但只扫
// assistantSessions，主域函数零改动）。
func (s *Server) cancelChildOfAssistant(childID string) bool {
	for _, sess := range s.assistantRuntimeSnapshots() {
		if sess.CancelChild(childID) {
			return true
		}
	}
	return false
}

// busyChildOfAssistant 判断 id 是否是某个助手会话**正在跑**的 run_task 子会话
// （与 busyChildAnywhere 同构，只扫助手域——两份运行时并发写库的拦截面）。
func (s *Server) busyChildOfAssistant(childID string) bool {
	if childID == "" {
		return false
	}
	for _, sess := range s.assistantRuntimeSnapshots() {
		if sess.RunningChild(childID) != nil {
			return true
		}
	}
	return false
}

// assistantRuntimeSnapshots 快照助手域全部运行时（锁内拷贝、锁外扫描——
// 与主域 busySessions/cancelChildAnywhere 的同一套纪律）。
func (s *Server) assistantRuntimeSnapshots() []*agent.Session {
	s.sessionsMu.RLock()
	out := make([]*agent.Session, 0, len(s.assistantSessions))
	for _, sess := range s.assistantSessions {
		out = append(out, sess)
	}
	s.sessionsMu.RUnlock()
	return out
}
