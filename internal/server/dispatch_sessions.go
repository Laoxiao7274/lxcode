// 会话管理域：session.*（列表 / 新建 / 恢复 / 改名 / 归档）。

package server

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
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
		// running 位：**此刻**在生成的会话（与 hello 的 busy_sessions 同一份判定
		// ——busySessions()）。新连上的客户端错过 busy=true 广播时，列表据此直接
		// 亮运行指示，不用先点进会话（chat.history 带 busy）才补上。
		busy := map[string]bool{}
		for _, id := range s.busySessions() {
			busy[id] = true
		}
		list := toProtocolSessionList(metas)
		for i := range list {
			list[i].Running = busy[list[i].ID]
		}
		return protocol.NewResult(req.ID, list)

	case protocol.MethodSessionNew:
		var p protocol.SessionNewParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		id, _, err := s.createSession(p.Workspace)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		c.setFocus(id)
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
		// 有无历史用轻量计数（HasMessages）——见 dispatch_chat.go 的同款注释：
		// 窗口态会话在这两个入口不触发全量补载。
		if sess.HasMessages() {
			if err := s.prepareWorkspace(p.ID, sess); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
			}
		}
		c.setFocus(p.ID)
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

	case protocol.MethodSessionFulltext:
		if s.st == nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "会话存储未启用")
		}
		var p protocol.SessionFulltextParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if strings.TrimSpace(p.Query) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 query")
		}
		// 子串匹配：Query 原样进正则前先转义。UI 用户输入的是字面文本
		//（「(未命名)」这类括号字面量不该被当正则），正则语义保留给 agent 工具。
		hits, total, timedOut, err := s.st.Search(sessiondata.SearchQuery{
			Pattern: regexp.QuoteMeta(p.Query),
			Max:     p.Max,
		})
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		return protocol.NewResult(req.ID, protocol.SessionFulltextResult{
			Hits: hits, Total: total, TimedOut: timedOut,
		})

	case protocol.MethodFileReveal:
		var p protocol.FileRevealParams
		if err := json.Unmarshal(params, &p); err != nil || p.SessionID == "" || strings.TrimSpace(p.Path) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 session_id 与 path")
		}
		sess, err := s.session(p.SessionID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		// 白名单：路径净化后必须仍在该会话工作目录之内（share_file 同判据）。
		abs, err := resolveWorkspacePath(sess.WorkDir(), p.Path)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "路径越出会话工作目录，拒绝打开: "+p.Path)
		}
		return protocol.NewResult(req.ID, protocol.FileRevealResult{Abs: abs})

	case protocol.MethodSessionArchive:
		var p protocol.SessionArchiveParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.st.Archive(p.ID, p.Archived); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventSessionChanged, protocol.SessionChangedParams{ID: p.ID, Reason: "archived"})
		return protocol.NewResult(req.ID, protocol.SessionArchiveResult{Archived: p.Archived})
	}
	return nil
}
