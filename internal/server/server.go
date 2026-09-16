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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// Server 是 WebSocket JSON-RPC 服务端：持有会话与模型注册表，
// 把会话事件广播给所有已连接客户端，并分发客户端请求。
type Server struct {
	reg  *config.Registry
	sess *agent.Session
	treg *tools.Registry // 保存引用：AttachSessionStore 时给它接会话搜索
	st   *store.Store    // 保存引用：会话管理方法（rename/archive）直通存储

	upgrader websocket.Upgrader

	mu      sync.Mutex
	clients map[*wsClient]struct{}
}

// wsClient 是一条客户端连接（每个连接一个写锁：gorilla 不允许并发写）。
type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsClient) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

// New 创建服务端；会话事件经 Server 广播。
func NewServer(reg *config.Registry) *Server {
	s := &Server{
		reg:  reg,
		treg: tools.New(),
		upgrader: websocket.Upgrader{
			// 仅本机/局域网使用，不做 Origin 校验（单用户）
			CheckOrigin: func(*http.Request) bool { return true },
		},
		clients: map[*wsClient]struct{}{},
	}
	// 内核 typed 事件 → 协议事件广播（唯一出口接线）
	s.sess = agent.New(reg, s.treg, s.emitEvent)
	return s
}

// Session 暴露会话（单测/探针直接驱动）。
func (s *Server) Session() *agent.Session { return s.sess }

// AttachSessionStore 挂载会话存储并恢复最近会话（main 装配时、监听前调用）。
// 同时接线 session_search 工具——JSONL 格式归 store 包所有，工具层只拿函数。
func (s *Server) AttachSessionStore(st *store.Store) error {
	s.st = st
	s.sess.AttachSessionSearch()
	return s.sess.EnablePersistence(st)
}

// emitEvent 把内核 typed 事件映射成协议事件并广播。
func (s *Server) emitEvent(ev agent.Event) {
	switch e := ev.(type) {
	case agent.UserMsgEvent:
		s.broadcast(protocol.EventUserMsg, e.Message)
	case agent.DeltaEvent:
		s.broadcast(protocol.EventDelta, protocol.DeltaParams{Kind: e.Kind, Text: e.Text})
	case agent.ToolCallEvent:
		s.broadcast(protocol.EventToolCall, protocol.ToolCallParams{
			ID: e.ID, Name: e.Name, Arguments: e.Arguments,
		})
	case agent.ToolResultEvent:
		s.broadcast(protocol.EventToolRslt, protocol.ToolResultParams{
			ID: e.ID, Name: e.Name, Content: e.Content, IsError: e.IsError,
		})
	case agent.ConfirmRequestEvent:
		s.broadcast(protocol.EventConfirm, toProtocolConfirm(e.Request))
	case agent.BusyEvent:
		s.broadcast(protocol.EventBusy, protocol.BusyParams{Busy: e.Busy})
	case agent.TurnDoneEvent:
		s.broadcast(protocol.EventDone, protocol.DoneParams{
			Message: e.Message, UsageTokens: e.UsageTokens, FinishReason: e.FinishReason,
		})
	case agent.TurnErrorEvent:
		s.broadcast(protocol.EventError, protocol.ErrorParams{
			Message: e.Message, Aborted: e.Aborted, Partial: e.Partial,
		})
	case agent.TodoUpdatedEvent:
		s.broadcast(protocol.EventTodo, protocol.TodoUpdatedParams{Items: e.Items})
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
			Server: "lxcode", Version: protocol.Version, Busy: s.sess.Busy(),
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
		resp := s.dispatch(&req)
		if resp != nil {
			if err := c.send(resp); err != nil {
				return
			}
		}
	}
}

// dispatch 分发单个请求，返回应答（nil = 通知，不回）。
func (s *Server) dispatch(req *protocol.Request) *protocol.Response {
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
		return protocol.NewResult(req.ID, protocol.HelloResult{
			Server: "lxcode", Version: protocol.Version, Busy: s.sess.Busy(),
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
		if err := s.sess.Send(p.Text); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		return protocol.NewResult(req.ID, map[string]any{"accepted": true})

	case protocol.MethodChatCancel:
		s.sess.Cancel()
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodChatHistory:
		return protocol.NewResult(req.ID, s.history())

	case protocol.MethodToolConfirm:
		var p protocol.ToolConfirmParams
		if err := json.Unmarshal(params, &p); err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
		}
		if err := s.sess.Confirm(p.ID, p.Allow); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		return protocol.NewResult(req.ID, map[string]any{})

	case protocol.MethodSessionList:
		return protocol.NewResult(req.ID, toProtocolSessionList(s.sess.SessionList()))

	case protocol.MethodSessionNew:
		var p protocol.SessionNewParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
			}
		}
		if _, err := s.sess.SwitchNew(); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		// 会话归属项目（新会话 id 懒生成：首条消息才建行，归属先记在册）
		if p.Workspace != "" {
			s.sess.SetWorkspace(p.Workspace)
		}
		// 新会话 id 懒生成（首条消息才建文件）：应答里给当前值即可
		s.broadcastSessionChanged(s.sess.SessionID(), "new")
		return protocol.NewResult(req.ID, map[string]any{"id": s.sess.SessionID()})

	case protocol.MethodSessionResume:
		var p protocol.SessionResumeParams
		if err := json.Unmarshal(params, &p); err != nil || p.ID == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 缺少 id")
		}
		if err := s.sess.SwitchTo(p.ID); err != nil {
			return protocol.NewError(req.ID, errorCode(err), err.Error())
		}
		s.broadcastSessionChanged(p.ID, "resumed")
		return protocol.NewResult(req.ID, map[string]any{"id": p.ID})

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

	case protocol.MethodProjectAdd:
		var p protocol.ProjectAddParams
		if err := json.Unmarshal(params, &p); err != nil || p.Path == "" || strings.TrimSpace(p.Name) == "" {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: 需要 name 与 path")
		}
		meta, err := ensureProjectRepo(p.Name, p.Path)
		if err != nil {
			return protocol.NewError(req.ID, protocol.CodeInvalidParams, err.Error())
		}
		saved, err := s.st.AddProject(meta.Name, meta.Path)
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
	}

	return protocol.NewError(req.ID, protocol.CodeMethodNotFound, "未知方法: "+req.Method)
}

// errorCode 把 agent 哨兵错误映射到协议错误码（结构化判断，不做字符串匹配）。
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
	default:
		return protocol.CodeInvalidParams
	}
}

// history 组装 chat.history 应答（消息 + 忙闲 + 挂起确认 + todo）。
func (s *Server) history() protocol.ChatHistoryResult {
	snap := s.sess.History()
	var pending *protocol.ConfirmRequest
	if snap.Pending != nil {
		pending = toProtocolConfirm(snap.Pending)
	}
	return protocol.ChatHistoryResult{
		Messages: snap.Messages, Busy: snap.Busy, Pending: pending,
		SessionID: snap.SessionID, Todos: snap.Todos,
	}
}

// toProtocolConfirm 把内核确认请求转成协议载荷（字段一一对应，
// 但类型分属两层——内核不 import protocol，转换归服务端）。
func toProtocolConfirm(r *agent.ConfirmRequest) *protocol.ConfirmRequest {
	if r == nil {
		return nil
	}
	return &protocol.ConfirmRequest{
		ID: r.ID, Name: r.Name, Arguments: r.Arguments, Prompt: r.Prompt,
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

// ensureProjectRepo 保证项目目录是 git 仓库：已存在 .git 直接用；
// 目录存在但不是仓库 → git init；目录不存在 → 报错。用户规则：
// 「本地仓库的创建——如果有了那就不用管」。
func ensureProjectRepo(name, path string) (store.ProjectMeta, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return store.ProjectMeta{}, fmt.Errorf("路径无效: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return store.ProjectMeta{}, fmt.Errorf("目录不存在或不是文件夹: %s", abs)
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
		return store.ProjectMeta{Name: name, Path: abs}, nil // 已是仓库，不用管
	}
	// 不是仓库 → git init（exec git，参数数组传递避免引号问题）
	cmd := exec.Command("git", "init")
	cmd.Dir = abs
	if out, err := cmd.CombinedOutput(); err != nil {
		return store.ProjectMeta{}, fmt.Errorf("git init 失败（git 未安装?）: %v: %s", err, truncate(string(out), 200))
	}
	return store.ProjectMeta{Name: name, Path: abs}, nil
}

// truncate 截断错误信息（自解释但不淹没）。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// broadcastSessionChanged 会话切换广播：所有客户端重拉 chat.history 同步视图。
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
