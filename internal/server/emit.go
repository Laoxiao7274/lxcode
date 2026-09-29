// typed 内核事件 → session-scoped 协议事件：事件归属（子会话带 dispatch_id）
// 与协议载荷转换都关在这里。
package server

import (
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
		s.broadcast(protocol.EventDone, protocol.DoneParams{
			SessionID: sessionID, Message: e.Message, UsageTokens: e.UsageTokens, FinishReason: e.FinishReason,
			DispatchID: e.DispatchID, Context: toProtocolContext(e.Context),
		})
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
		Used: u.Used, Window: u.Window, System: u.System,
		ToolResults: u.ToolResults, Messages: u.Messages, Reasoning: u.Reasoning,
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
