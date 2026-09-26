// Package server 实现独立后端进程：WS JSON-RPC 服务端，包裹 agent.Session
// （方法分发 + typed 事件 → 协议事件广播）。CLI / 桌面壳 / 未来 Web UI
// 都经协议接入。后端是模型注册表与配置文件的唯一写者。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// Server 是 WebSocket JSON-RPC 服务端：持有会话与模型注册表，
// 把会话事件广播给所有已连接客户端，并分发客户端请求。
type Server struct {
	reg  *config.Registry
	treg *tools.Registry // 保存引用：AttachSessionStore 时接动态工具与会话搜索
	st   *store.Store    // 保存引用：会话管理方法（rename/archive）直通存储

	sessionsMu    sync.RWMutex
	sessions      map[string]*agent.Session
	lastSessionID string // 仅供测试诊断；协议请求绝不读取全局焦点
	memoryID      uint64
	stream        agent.StreamFn

	worktreeMu    sync.Mutex
	worktreeLocks map[string]*sync.Mutex

	upgrader websocket.Upgrader

	mu      sync.Mutex
	clients map[*wsClient]struct{}
}

// wsClient 是一条客户端连接（每个连接一个写锁：gorilla 不允许并发写）。
type wsClient struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	sessionID string // 连接本地焦点，仅兼容没有显式 session_id 的客户端
}

func (c *wsClient) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

// New 创建服务端；每个稳定 session_id 对应独立的 agent.Session 运行时。
func NewServer(reg *config.Registry) *Server {
	return &Server{
		reg:           reg,
		treg:          tools.New(),
		sessions:      map[string]*agent.Session{},
		worktreeLocks: map[string]*sync.Mutex{},
		upgrader: websocket.Upgrader{
			// 仅本机/局域网使用，不做 Origin 校验（单用户）
			CheckOrigin: func(*http.Request) bool { return true },
		},
		clients: map[*wsClient]struct{}{},
	}
}

// projectDocsReader 读项目根的守则文件并转成内核的注入载荷。
// 读不到/超大只体现在 Note 上，不打断生成（规则见 project.LoadInstructions）。
func projectDocsReader(workDir string) agent.ProjectDocs {
	f := project.LoadInstructions(workDir)
	return agent.ProjectDocs{Path: f.Path, Content: f.Content, Note: f.Note}
}

// Session 暴露最近创建的运行时，仅供包内测试观察；协议操作必须按 id 查表。
func (s *Server) Session() *agent.Session {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.sessions[s.lastSessionID]
}

// SetStream 注入 LLM 流实现；测试在创建运行时前调用。
func (s *Server) SetStream(stream agent.StreamFn) { s.stream = stream }

// CloseSessions 关闭全部运行时资源（目前 SQLite 连接由 Store 持有）。
func (s *Server) CloseSessions() {
	s.sessionsMu.RLock()
	runtimes := make([]*agent.Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		runtimes = append(runtimes, sess)
	}
	s.sessionsMu.RUnlock()
	for _, sess := range runtimes {
		sess.Close()
	}
}

// AttachSessionStore 挂载持久化与 Agent 解析器；不再恢复单个全局活跃会话。
func (s *Server) AttachSessionStore(st *store.Store) error {
	if st == nil {
		return errors.New("会话存储为空")
	}
	s.st = st
	s.treg.SetSessionSearch(func(ctx context.Context, pattern string, max int) (string, error) {
		hits, err := st.Search(pattern, max)
		if err != nil {
			return "", err
		}
		return sessiondata.FormatSearchHits(hits, len(hits)), nil
	})
	// M4：工具目录里的自定义工具（binary）注册进工具注册表——启动时就位，
	// 之后的目录变更由 catalog.tools.* 分支触发同步。
	s.syncDynamicTools()
	return nil
}

func (s *Server) newRuntime(id string) (*agent.Session, error) {
	sess := agent.New(s.reg, s.treg, func(ev agent.Event) { s.emitEvent(id, ev) })
	sess.SetProjectDocs(projectDocsReader)
	if s.st != nil {
		sess.SetAgentResolver(&storeAgentResolver{st: s.st})
	}
	if s.stream != nil {
		sess.SetStream(s.stream)
	}
	if s.st != nil {
		if err := sess.AttachTo(s.st, id); err != nil {
			return nil, err
		}
	}
	return sess, nil
}

func (s *Server) session(id string) (*agent.Session, error) {
	if id == "" {
		return nil, errors.New("session_id 不能为空")
	}
	s.sessionsMu.RLock()
	sess := s.sessions[id]
	s.sessionsMu.RUnlock()
	if sess != nil {
		return sess, nil
	}
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	if sess = s.sessions[id]; sess != nil {
		return sess, nil
	}
	if s.st == nil {
		return nil, errors.New("会话存储未启用")
	}
	var err error
	sess, err = s.newRuntime(id)
	if err != nil {
		return nil, err
	}
	s.sessions[id] = sess
	s.lastSessionID = id
	return sess, nil
}

func (s *Server) createSession(workspace string) (string, *agent.Session, error) {
	if s.st == nil {
		if workspace != "" {
			return "", nil, errors.New("项目会话需要持久化存储")
		}
		id := fmt.Sprintf("memory-%d", atomic.AddUint64(&s.memoryID, 1))
		sess, err := s.newRuntime(id)
		if err != nil {
			return "", nil, err
		}
		s.sessionsMu.Lock()
		s.sessions[id], s.lastSessionID = sess, id
		s.sessionsMu.Unlock()
		return id, sess, nil
	}
	if workspace != "" {
		meta, found, err := s.st.ProjectByID(workspace)
		if err != nil {
			return "", nil, err
		}
		if !found {
			return "", nil, fmt.Errorf("项目 %s 不存在", workspace)
		}
		if _, err := project.ValidateDirectory(meta.Path); err != nil {
			return "", nil, err
		}
	}
	id, err := s.st.Create()
	if err != nil {
		return "", nil, err
	}
	if workspace != "" {
		if err := s.st.SessionWorkspace(id, workspace); err != nil {
			return "", nil, err
		}
	}
	sess, err := s.newRuntime(id)
	if err != nil {
		return "", nil, err
	}
	s.sessionsMu.Lock()
	s.sessions[id], s.lastSessionID = sess, id
	s.sessionsMu.Unlock()
	return id, sess, nil
}

func (s *Server) sessionWorktreeLock(sessionID string) *sync.Mutex {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()
	lock := s.worktreeLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.worktreeLocks[sessionID] = lock
	}
	return lock
}

func (s *Server) prepareWorktree(sessionID string, sess *agent.Session) error {
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	return s.prepareWorktreeLocked(sessionID, sess)
}

func (s *Server) prepareWorktreeLocked(sessionID string, sess *agent.Session) error {
	if s.st == nil {
		return nil
	}
	workspace, err := s.st.WorkspaceOf(sessionID)
	if err != nil {
		return err
	}
	if workspace == "" {
		return nil
	}
	projectMeta, found, err := s.st.ProjectByID(workspace)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("项目 %s 不存在", workspace)
	}
	meta, err := s.st.WorktreeOf(sessionID)
	if err != nil {
		return err
	}
	if meta.Path == "" {
		if meta.Branch != "" || meta.BaseCommit != "" {
			return fmt.Errorf("会话 %s 的 worktree 元数据不完整", sessionID)
		}
		wt, err := project.CreateWorktree(projectMeta.Path, s.st.WorktreeRoot(), workspace, sessionID)
		if err != nil {
			return err
		}
		meta = store.WorktreeMeta{Path: wt.Path, Branch: wt.Branch, BaseCommit: wt.BaseCommit}
		if err := s.st.SetWorktree(sessionID, meta); err != nil {
			if rollbackErr := project.RemoveWorktree(projectMeta.Path, wt); rollbackErr != nil {
				return fmt.Errorf("保存 worktree 元数据失败: %v；回滚也失败: %w", err, rollbackErr)
			}
			return err
		}
	} else if err := project.RestoreWorktree(projectMeta.Path, meta.Path, meta.Branch); err != nil {
		return err
	}
	if sess.WorkDir() != meta.Path {
		if err := sess.SetWorkDir(meta.Path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) sendSession(sessionID string, sess *agent.Session, text string, opts ...agent.SendOpt) error {
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.prepareWorktreeLocked(sessionID, sess); err != nil {
		return err
	}
	return sess.Send(text, opts...)
}

func (s *Server) releaseSessionWorktree(sessionID string) error {
	if s.st == nil {
		return fmt.Errorf("会话存储未启用，无法释放工作区")
	}
	sess, err := s.session(sessionID)
	if err != nil {
		return err
	}
	lock := s.sessionWorktreeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()
	if sess.Busy() {
		return agent.ErrBusy
	}
	workspace, err := s.st.WorkspaceOf(sessionID)
	if err != nil {
		return err
	}
	if workspace == "" {
		return fmt.Errorf("未分组会话没有独立 worktree")
	}
	projectMeta, found, err := s.st.ProjectByID(workspace)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("项目 %s 不存在", workspace)
	}
	meta, err := s.st.WorktreeOf(sessionID)
	if err != nil {
		return err
	}
	if meta.Path == "" || meta.Branch == "" {
		return fmt.Errorf("会话尚未创建完整的 worktree")
	}
	return project.ReleaseWorktree(projectMeta.Path, project.Worktree{
		Path: meta.Path, Branch: meta.Branch, BaseCommit: meta.BaseCommit,
	})
}

// storeAgentResolver 实现 agent.AgentResolver：从 store 解析四层组合的
// 全部输入（分层规则：agent 不 import store——经接口注入）。
type storeAgentResolver struct {
	st *store.Store
}

func (r *storeAgentResolver) Resolve(agentID string) (*sessiondata.AgentContext, bool) {
	return r.resolve(func(a sessiondata.AgentDef) bool { return a.ID == agentID })
}

// ResolveByName 按名字解析（模型把名字当 id 传的容错兜底）。
func (r *storeAgentResolver) ResolveByName(name string) (*sessiondata.AgentContext, bool) {
	return r.resolve(func(a sessiondata.AgentDef) bool { return a.Name == name })
}

func (r *storeAgentResolver) resolve(match func(sessiondata.AgentDef) bool) (*sessiondata.AgentContext, bool) {
	agents, err := r.st.ListAgents()
	if err != nil {
		return nil, false
	}
	var def *sessiondata.AgentDef
	for i := range agents {
		if match(agents[i]) {
			def = &agents[i]
			break
		}
	}
	if def == nil {
		return nil, false
	}
	mods, _ := r.st.ListModules()
	ac := &sessiondata.AgentContext{Def: *def}
	for i := range mods {
		if def.Workflow == mods[i].ID {
			m := mods[i]
			ac.Workflow = &m
		}
		if containsStr(def.Skills, mods[i].ID) {
			ac.Skills = append(ac.Skills, mods[i])
		}
	}
	// 主 Agent 的默认委派名单（有效 = 默认 ∩ 启用；会话级覆盖是前端
	// UI 态——协议接入时覆盖名单随 chat.send 计算，M3 dispatch 前够用）
	if def.IsMain {
		for i := range agents {
			if containsStr(def.Delegates, agents[i].ID) && agents[i].Enabled {
				ac.Delegates = append(ac.Delegates, agents[i])
			}
		}
	}
	return ac, true
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// emitEvent 把某个运行时的 typed 事件映射成 session-scoped 协议事件。
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

// Handler 返回 WS 端点的 http.Handler（挂到 protocol.Path）。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws 升级失败: %v", err)
			return
		}
		c := &wsClient{conn: conn}
		s.mu.Lock()
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.clients, c)
			s.mu.Unlock()
			conn.Close()
		}()
		// 连接建立即告知服务端身份与忙闲（客户端据此决定初始状态）
		_ = c.send(protocol.NewEvent(protocol.EventReady, protocol.HelloResult{
			Server: "lxcode", Version: protocol.Version, Busy: false,
		}))
		s.serve(c)
	})
}

// serve 读循环：逐帧解析请求并回响应（事件走 broadcast 主动推送）。
func (s *Server) serve(c *wsClient) {
	for {
		var req protocol.Request
		if err := c.conn.ReadJSON(&req); err != nil {
			return // 连接关闭
		}
		resp := s.dispatch(c, &req)
		if resp != nil {
			if err := c.send(resp); err != nil {
				return
			}
		}
	}
}

// dispatch 分发单个请求，返回应答（nil = 通知，不回）。
func (s *Server) dispatch(c *wsClient, req *protocol.Request) *protocol.Response {
	params := req.Params
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	// 通知（无 id）不回应答
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	switch req.Method {
	case protocol.MethodHello:
		var p protocol.HelloParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		log.Printf("客户端接入: %s (协议 %s)", p.Client, p.Version)
		if isNotification {
			return nil
		}
		if p.Version != protocol.Version {
			return protocol.NewError(req.ID, protocol.CodeVersionMismatch,
				fmt.Sprintf("协议版本不兼容：客户端 %q，服务端 %q", p.Version, protocol.Version))
		}
		return protocol.NewResult(req.ID, protocol.HelloResult{
			Server: "lxcode", Version: protocol.Version, Busy: false,
		})

	case protocol.MethodModelList:
		return protocol.NewResult(req.ID, s.modelList())

	case protocol.MethodModelAdd, protocol.MethodModelUpdate:
		var m config.ModelConfig
		if err := json.Unmarshal(params, &m); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		var err error
		if req.Method == protocol.MethodModelAdd {
			err = s.reg.Add(m)
		} else {
			err = s.reg.Update(m)
		}
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcastModels()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodModelRemove:
		var p protocol.ModelRemoveParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if err := s.reg.Remove(p.ID); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcastModels()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodModelEnable:
		var p protocol.ModelEnableParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		m, ok := s.reg.Get(p.ID)
		if !ok {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "模型不存在: "+p.ID)
		}
		m.Enabled = p.Enabled
		if err := s.reg.Update(m); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcastModels()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodRoleSet:
		var p protocol.RoleSetParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if err := s.reg.SetRole(p.Role, p.ModelID); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcastModels()
		return protocol.NewResult(req.ID, map[string]any{})

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
			return protocol.NewResult(req.ID, map[string]any{"accepted": true, "session_id": id})
		}
		sess, err := s.session(id)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		if err := s.sendSession(id, sess, p.Text, chatSendOptions(p)...); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
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
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodSessionWorktreeRelease:
		var p protocol.SessionWorktreeReleaseParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.releaseSessionWorktree(p.ID); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		return protocol.NewResult(req.ID, protocol.SessionWorktreeReleaseResult{Released: true})

	case protocol.MethodProjectAdd:
		var p protocol.ProjectAddParams
		if err := json.Unmarshal(params, &p); err != nil || p.Path == "" || strings.TrimSpace(p.Name) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 name 与 path")
		}
		saved, err := project.Add(s.st, p.Name, p.Path)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		s.broadcast(protocol.EventProjectChanged, map[string]any{})
		// 协议映射（store 类型无 json tag——直接序列化会漏键名，session.list 踩过同款）
		return protocol.NewResult(req.ID, protocol.ProjectMeta{
			ID: saved.ID, Name: saved.Name, Path: saved.Path,
		})

	case protocol.MethodProjectList:
		metas, err := s.st.ListProjects()
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		out := make([]protocol.ProjectMeta, len(metas))
		for i, m := range metas {
			out[i] = protocol.ProjectMeta{ID: m.ID, Name: m.Name, Path: m.Path}
		}
		return protocol.NewResult(req.ID, out)

	// 项目守则（项目根 AGENTS.md）读写——「自定义指令」的项目级入口。
	// 只按项目 id 寻址：路径由服务端从项目根解析（客户端传不了任意路径）。
	case protocol.MethodProjectInstructionsGet:
		var p protocol.ProjectInstructionsParams
		if err := json.Unmarshal(params, &p); err != nil || p.ProjectID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 project_id")
		}
		meta, ok, err := s.st.ProjectByID(p.ProjectID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		if !ok {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "项目不存在: "+p.ProjectID)
		}
		f := project.LoadInstructions(meta.Path)
		return protocol.NewResult(req.ID, protocol.ProjectInstructionsResult{
			Path: f.Path, Content: f.Content, Exists: f.Exists, Note: f.Note,
		})

	case protocol.MethodProjectInstructionsSave:
		var p protocol.ProjectInstructionsParams
		if err := json.Unmarshal(params, &p); err != nil || p.ProjectID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 project_id")
		}
		meta, ok, err := s.st.ProjectByID(p.ProjectID)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
		}
		if !ok {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "项目不存在: "+p.ProjectID)
		}
		path, err := project.WriteInstructions(meta.Path, p.Content)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		// 无需广播：守则是每轮现读进提示词，不参与客户端间同步的状态
		return protocol.NewResult(req.ID, protocol.ProjectInstructionsResult{
			Path: path, Content: p.Content, Exists: true,
		})
	}

	// agent.*/catalog.*（M1——独立分发函数，未命中回落 unknown）
	if resp := s.dispatchAgentCatalog(req.ID, req.Method, params); resp != nil {
		return resp
	}

	return protocol.NewError(req.ID, protocol.CodeMethodNotFound, "未知方法: "+req.Method)
}

// errorCode 把 agent 哨兵错误映射到协议错误码（结构化判断，不做字符串匹配）。
func chatSendOptions(p protocol.ChatSendParams) []agent.SendOpt {
	var opts []agent.SendOpt
	if p.Effort != "" {
		opts = append(opts, agent.WithEffort(p.Effort))
	}
	if p.Approval != "" {
		opts = append(opts, agent.WithApproval(p.Approval))
	}
	if p.Agent != "" {
		opts = append(opts, agent.WithAgent(p.Agent))
	}
	return opts
}

func errorCode(err error) int {
	switch {
	case errors.Is(err, agent.ErrBusy):
		return protocol.CodeBusy
	case errors.Is(err, agent.ErrNoDefaultModel):
		return protocol.CodeNoDefaultModel
	case errors.Is(err, agent.ErrModelDisabled):
		return protocol.CodeModelDisabled
	case errors.Is(err, agent.ErrPendingConfirm):
		return protocol.CodeBusy // 挂起确认占着会话，语义同忙
	case errors.Is(err, agent.ErrAgentNotFound):
		return protocol.CodeAgentNotFound
	case errors.Is(err, agent.ErrAgentDisabled):
		return protocol.CodeAgentDisabled
	default:
		return protocol.CodeInvalidParams
	}
}

// history 组装指定会话的 chat.history 应答（消息 + 忙闲 + 挂起确认 + todo）。
func (s *Server) history(sessionID string, sess *agent.Session) protocol.ChatHistoryResult {
	snap := sess.History()
	var pending *protocol.ConfirmRequest
	if snap.Pending != nil {
		pending = toProtocolConfirm(sessionID, snap.Pending)
	}
	return protocol.ChatHistoryResult{
		Messages: snap.Messages, Busy: snap.Busy, Pending: pending,
		SessionID: sessionID, Todos: snap.Todos,
		Context:     toProtocolContext(snap.Context),
		Checkpoints: snap.Checkpoints,
	}
}

// toProtocolContext 把内核的上下文测量转成协议载荷；未知（Used=0）时返回 nil
// ——客户端据此显示中性态，而不是把 0 当成「用满了 0%」。
func toProtocolContext(u agent.ContextUsage) *protocol.ContextUsage {
	if u.Used <= 0 {
		return nil
	}
	return &protocol.ContextUsage{
		Used: u.Used, Window: u.Window, System: u.System,
		ToolResults: u.ToolResults, Messages: u.Messages, Reasoning: u.Reasoning,
	}
}

// toProtocolConfirm 把内核确认请求转成协议载荷（字段一一对应，
// 但类型分属两层——内核不 import protocol，转换归服务端）。
func toProtocolConfirm(sessionID string, r *agent.ConfirmRequest) *protocol.ConfirmRequest {
	if r == nil {
		return nil
	}
	return &protocol.ConfirmRequest{
		SessionID: sessionID, ID: r.ID, Name: r.Name, Arguments: r.Arguments, Prompt: r.Prompt,
		DispatchID: r.DispatchID,
	}
}

func (s *Server) modelList() protocol.ModelListResult {
	return protocol.ModelListResult{Models: s.reg.List(), Roles: s.reg.RoleBindings()}
}

// broadcastModels 注册表变更后通知全部客户端刷新。
func (s *Server) broadcastModels() {
	s.broadcast(protocol.EventModels, s.modelList())
}

// NotifyModels 供外部（注册表变更）触发 model.changed 广播。
func (s *Server) NotifyModels() { s.broadcastModels() }

// toProtocolSessionList 把 store 的会话列表映射成协议载荷——store 层不 import
// protocol（依赖单向），且 JSON 键名（id/title/updated_at/messages）只在协议
// 契约有定义；直接序列化 store 类型会漏字段名（历史 bug：前端拿到全大写键）。
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

// broadcastSessionChanged 通知元数据变化；焦点与生成事件按连接/会话单独路由。
func (s *Server) broadcastSessionChanged(id, reason string) {
	s.broadcast(protocol.EventSessionChanged, protocol.SessionChangedParams{ID: id, Reason: reason})
}

// broadcast 把事件推给所有客户端（写失败的连接交由读循环清理）。
func (s *Server) broadcast(method string, params any) {
	ev := protocol.NewEvent(method, params)
	s.mu.Lock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		if err := c.send(ev); err != nil {
			_ = c.conn.Close()
		}
	}
}

// Run 启动 HTTP 服务（阻塞）。addr 形如 127.0.0.1:7789。
// ctx 取消即优雅退出：先强制关闭全部 WS 连接（http.Shutdown 只关监听、
// 不打断长连接——WS 读循环会一直挂着，Shutdown 会无限等），
// 再 Shutdown 等存量请求收尾。
func (s *Server) Run(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle(protocol.Path, s.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		s.closeAllClients()
		_ = srv.Shutdown(context.Background())
	}()
	log.Printf("后端 WS 服务监听 %s%s", addr, protocol.Path)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// closeAllClients 强制断开全部连接（优雅停机/长连接不被 Shutdown 等待）。
func (s *Server) closeAllClients() {
	s.mu.Lock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		_ = c.conn.Close()
	}
}
