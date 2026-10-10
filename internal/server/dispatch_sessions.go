// 会话管理域：session.*（列表 / 新建 / 恢复 / 改名 / 归档 / worktree 释放）。

package server

import (
	"encoding/json"
	"strings"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func (s *Server) dispatchSessions(c *wsClient, req *protocol.Request, params json.RawMessage) *protocol.Response {
	switch req.Method {
	case protocol.MethodSessionList:
		if s.st == nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "会话存储未启用")
		}
		metas, err := s.st.List()
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		return protocol.NewResult(req.ID, toProtocolSessionList(metas))

	case protocol.MethodSessionNew:
		var p protocol.SessionNewParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id, _, err := s.createSession(p.Workspace)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		c.sessionID = id
		s.broadcastSessionChanged(id, "created")
		return protocol.NewResult(req.ID, protocol.SessionResult{SessionID: id})

	case protocol.MethodSessionResume:
		var p protocol.SessionResumeParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		sess, err := s.session(p.ID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		if len(sess.History().Messages) > 0 {
			if err := s.prepareWorktree(p.ID, sess); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
		}
		c.sessionID = p.ID
		return protocol.NewResult(req.ID, protocol.SessionResult{SessionID: p.ID})

	case protocol.MethodSessionRename:
		var p protocol.SessionRenameParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" || strings.TrimSpace(p.Title) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 id 与非空 title")
		}
		if err := s.st.Rename(p.ID, p.Title); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		// 会话列表变了：广播让客户端刷新侧栏
		s.broadcast(protocol.EventSessionChanged, protocol.SessionChangedParams{ID: p.ID, Reason: "renamed"})
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodSessionArchive:
		var p protocol.SessionArchiveParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.st.Archive(p.ID, p.Archived); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventSessionChanged, protocol.SessionChangedParams{ID: p.ID, Reason: "archived"})
		result := protocol.SessionArchiveResult{Archived: p.Archived}
		// 释放只在归档时尝试（取消归档不碰工作区）。归档已经提交并广播——释放是
		// 尽力而为的附加动作：失败只把原因带回，绝不回滚归档。
		if p.Archived && p.ReleaseWorktree {
			result.ReleasedWorktree, result.ReleaseError = s.releaseArchivedWorktrees(p.ID)
		}
		return protocol.NewResult(req.ID, result)

	case protocol.MethodSessionWorktreeRelease:
		var p protocol.SessionWorktreeReleaseParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.releaseSessionWorktree(p.ID); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		return protocol.NewResult(req.ID, protocol.SessionWorktreeReleaseResult{Released: true})
	}
	return nil
}
