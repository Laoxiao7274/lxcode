// typed 内核事件 → session-scoped 协议事件：事件归属（子会话带 dispatch_id）
// 与协议载荷转换都关在这里。
package server

import (
	"log"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

func (s *Server) emitEvent(sessionID string, ev agent.Event) {
	switch e := ev.(type) {
	case agent.UserMsgEvent:
		s.broadcast(protocol.EventUserMsg, protocol.UserMessageParams{SessionID: sessionID, Message: e.Message})
	case agent.DeltaEvent:
		s.broadcast(protocol.EventDelta, protocol.DeltaParams{SessionID: sessionID, Kind: e.Kind, Text: e.Text, DispatchID: e.DispatchID})
	case agent.ToolCallEvent:
		s.broadcast(protocol.EventToolCall, protocol.ToolCallParams{
			SessionID: sessionID, ID: e.ID, Name: e.Name, Arguments: e.Arguments, DispatchID: e.DispatchID,
		})
	case agent.ToolResultEvent:
		s.broadcast(protocol.EventToolRslt, protocol.ToolResultParams{
			SessionID: sessionID, ID: e.ID, Name: e.Name, Content: e.Content, IsError: e.IsError, DispatchID: e.DispatchID,
		})
	case agent.ConfirmRequestEvent:
		s.broadcast(protocol.EventConfirm, toProtocolConfirm(sessionID, e.Request))
	case agent.BusyEvent:
		s.broadcast(protocol.EventBusy, protocol.BusyParams{SessionID: sessionID, Busy: e.Busy})
	case agent.TurnDoneEvent:
		params := protocol.DoneParams{
			SessionID: sessionID, Message: e.Message, UsageTokens: e.UsageTokens, FinishReason: e.FinishReason,
			DispatchID: e.DispatchID, Context: toProtocolContext(e.Context),
			// 计时/模型从**同一条消息**取（不另算一遍）：chat.done 的这三个值必须与
			// 随后 chat.history 回放出来的一模一样——两条路径各算一遍就会分叉
			// （AGENTS.md §2.2 的教训）。零值原样带出去，wire 上整键缺席。
			FirstTokenMs: e.Message.FirstTokenMs, DurationMs: e.Message.DurationMs, Model: e.Message.Model,
		}
		// 会话统计按**本事件的 session_id** 折叠（主轮 = 主会话的，子轮 = 子会话自己的）：
		// 子会话是一个独立会话，它的统计挂在它自己的 id 上（AGENTS.md §2.3），
		// 所以子轮的 done 也要带——不带的话子会话页的「会话消耗」永远是空的
		//（2026-09-30 用户报「子Agent的会话里…会话信息这些展示没有」）。
		// 主会话的时间线不会被它碰到：前端按 session_id 路由（子事件另带 dispatch_id，
		// 进的是卡与子会话自己的 state）。
		params.Stats = s.sessionStatsOf(sessionID)
		s.broadcast(protocol.EventDone, params)
	case agent.TurnErrorEvent:
		s.broadcast(protocol.EventError, protocol.ErrorParams{
			SessionID: sessionID, Message: e.Message, Aborted: e.Aborted, Partial: e.Partial,
		})
	case agent.DispatchStartEvent:
		s.broadcast(protocol.EventDispatchStart, protocol.DispatchStartParams{
			OwnerSessionID: sessionID, DispatchID: e.DispatchID, SessionID: e.SessionID,
			AgentID: e.AgentID, AgentName: e.AgentName, AgentColor: e.AgentColor, Task: e.Task,
		})
	case agent.DispatchEndEvent:
		s.broadcast(protocol.EventDispatchEnd, protocol.DispatchEndParams{
			OwnerSessionID: sessionID, DispatchID: e.DispatchID, SessionID: e.SessionID,
			Result: e.Result, IsError: e.IsError, UsageTokens: e.UsageTokens,
		})
	case agent.CompactedEvent:
		s.broadcast(protocol.EventCompacted, protocol.CompactedParams{
			SessionID: sessionID, Before: e.Result.Before, After: e.Result.After, Shadowed: e.Result.Shadowed,
			Summary: e.Result.Summary, Manual: e.Manual, DispatchID: e.DispatchID,
		})
	case agent.RewoundEvent:
		// 撤回后前端据此截断时间线（seq 之前的保留、之后的丢弃），不必重拉历史
		s.broadcast(protocol.EventRewound, protocol.ChatRewoundParams{
			SessionID: sessionID, Seq: e.Seq, Removed: e.Removed,
			// toProtocolContext 在未知（Used <= 0）时返回 nil → 整键缺席
			Context: toProtocolContext(e.Context),
			// 统计也重算：撤回真删了行，步数/token 跟着变小才对（压缩不删行，
			// 所以压缩不发统计——整段日志折叠出来的数字本来就不变）
			Stats: s.sessionStatsOf(sessionID),
		})
	case agent.TodoUpdatedEvent:
		s.broadcast(protocol.EventTodo, protocol.TodoUpdatedParams{SessionID: sessionID, Items: e.Items})
	case agent.FilesChangedEvent:
		params := protocol.FilesChangedParams{SessionID: sessionID, Files: make([]protocol.FileChangeParams, len(e.Files))}
		for i, f := range e.Files {
			params.Files[i] = protocol.FileChangeParams{Path: f.Path, Added: f.Added, Deleted: f.Deleted, Diff: f.Diff}
		}
		s.broadcast(protocol.EventFiles, params)
	case agent.SessionStartedEvent:
		// 首条消息懒建时刷新列表；这不是服务端的全局焦点变更。
		s.broadcastSessionChanged(e.ID, "started")
	}
}

func toProtocolContext(u agent.ContextUsage) *protocol.ContextUsage {
	if u.Used <= 0 {
		return nil
	}
	return &protocol.ContextUsage{
		Used: u.Used, Window: u.Window, System: u.System, Tools: u.Tools,
		ToolResults: u.ToolResults, Messages: u.Messages, Reasoning: u.Reasoning,
		// 估算位跟着走：它是 UI 区分「真实用量」与「估算」的唯一依据，掉在这一层
		// 就等于前端把估算当真实用量展示
		Estimated: u.Estimated,
	}
}

// sessionStatsOf 取一条会话的整段统计（读库折叠，见 store.SessionStatsOf）。
//
// 三条纪律：
//   - **读不到就是 nil**（没有存储 / 会话行还没建 / 查询失败）：wire 上整键缺席，
//     前端不渲染统计胶囊。失败只记日志——统计是展示信息，不该让一轮对话失败；
//   - **零值也是 nil**（还没有任何一步）：发一排 0 等于告诉用户"跑了 0 轮"，
//     那是编出来的假事实；
//   - 折叠的是**整段日志**（含被压缩影子掉的消息），所以压缩不改变它。
func (s *Server) sessionStatsOf(sessionID string) *protocol.SessionStats {
	if s.st == nil || sessionID == "" {
		return nil
	}
	st, err := s.st.SessionStatsOf(sessionID)
	if err != nil {
		log.Printf("读会话统计失败（本轮不带统计）: %v", err)
		return nil
	}
	if st.Empty() {
		return nil
	}
	return &protocol.SessionStats{
		Turns: st.Turns, Steps: st.Steps, LLMMs: st.LLMMs, ToolMs: st.ToolMs,
		TTFTMs: st.TTFTMs, TTFTSteps: st.TTFTSteps,
		DecodeMs: st.DecodeMs, DecodeTokens: st.DecodeTokens,
		InputTokens: st.InputTokens, CacheReadTokens: st.CacheReadTokens,
		CacheWriteTokens: st.CacheWriteTokens, OutputTokens: st.OutputTokens,
		LegacyTokens: st.LegacyTokens,
	}
}

func toProtocolConfirm(sessionID string, r *agent.ConfirmRequest) *protocol.ConfirmRequest {
	if r == nil {
		return nil
	}
	return &protocol.ConfirmRequest{
		SessionID: sessionID, ID: r.ID, Name: r.Name, Arguments: r.Arguments, Prompt: r.Prompt,
		DispatchID: r.DispatchID,
	}
}

func toProtocolSessionList(metas []store.SessionMeta) []protocol.SessionMeta {
	out := make([]protocol.SessionMeta, len(metas))
	for i, m := range metas {
		out[i] = protocol.SessionMeta{
			ID: m.ID, Title: m.Title, UpdatedAt: m.UpdatedAt,
			Messages: m.Messages, Archived: m.Archived, Workspace: m.Workspace,
		}
	}
	return out
}
