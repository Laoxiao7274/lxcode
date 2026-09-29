// Server 装配与协议分发：把内核（agent）与存储（store）接起来，对外只说
// JSON-RPC。这是**唯一**把三层拼在一起的地方（内核不 import 传输，存储不 import
// 内核——见 internal/architecture 的静态守卫）。
//
// 本文件是装配外壳与生命周期。各域的协议处理器分在：dispatch.go（方法分派）、
// session_ops.go（会话的建/查/发）、worktree.go（项目会话的 git 工作树）、
// emit.go（typed 事件 → 协议事件）、agent_resolver.go（Agent 名单解析）。
// 另有 agent_catalog.go / mcp.go / search.go / tools_sync.go 四个目录域。
package server

import (
	"context"
	"errors"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"sync"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/mcp"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
	"github.com/moyunteng/lxcode/internal/websearch"
)

type Server struct {
	reg  *config.Registry
	treg *tools.Registry // 保存引用：AttachSessionStore 时接动态工具与会话搜索
	st   *store.Store    // 保存引用：会话管理方法（rename/archive）直通存储
	// search 是网页搜索渠道服务（AttachSearch 装配；nil = 未装配）。
	search *websearch.Service
	// jobs 是后台任务管理器（AttachJobs 装配；nil = 未装配）。任务注册表是
	// 进程级单例——工具面与协议面共用同一个实例。
	jobsMu sync.Mutex
	jobs   *jobs.Manager
	// wakes 是连续唤醒预算表（契约 §5）：settle 投递在任务 goroutine 上，
	// 用户消息与 job.kill 在 WS 读循环上——两处并发读写同一张表。
	wakeMu sync.Mutex
	wakes  map[string]int
	// mcpMgr 是 MCP 客户端管理器（NewServer 时建；生命周期内不换）。
	// mcpMu 保护这一个字段（对账可能在协议请求路径上并发触发）。
	mcpMu  sync.Mutex
	mcpMgr *mcp.Manager

	// baseCtx 是服务运行期的基上下文（Run 时设置）。
	// 请求分发出在 WS 读循环里，没有请求级 ctx 可传——而搜索要打网络。
	// 没有它，停机时在途的搜索请求只能干等自己的超时（最长 20s）才收尾。
	ctxMu   sync.RWMutex
	baseCtx context.Context

	sessionsMu    sync.RWMutex
	sessions      map[string]*agent.Session
	lastSessionID string // 仅供测试诊断；协议请求绝不读取全局焦点
	memoryID      uint64
	stream        agent.StreamFn

	worktreeMu    sync.Mutex
	worktreeLocks map[string]*sync.Mutex
	// worktreeReady 记录「本进程内已校验过工作树」的会话（release 时删除）。
	// 校验要跑 4 个 git 子进程，实测 ~130ms，而 chat.history / session.resume
	// 每次都走 prepareWorktree——不记就每次读历史都重付一遍（见 prepareWorktreeLocked）。
	worktreeReady map[string]bool

	upgrader websocket.Upgrader

	mu      sync.Mutex
	clients map[*wsClient]struct{}
}

func (s *Server) Ctx() context.Context {
	s.ctxMu.RLock()
	defer s.ctxMu.RUnlock()
	if s.baseCtx == nil {
		return context.Background()
	}
	return s.baseCtx
}

func (s *Server) setBaseCtx(ctx context.Context) {
	s.ctxMu.Lock()
	s.baseCtx = ctx
	s.ctxMu.Unlock()
}

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

func NewServer(reg *config.Registry) *Server {
	return &Server{
		reg:           reg,
		treg:          tools.New(),
		mcpMgr:        mcp.NewManager(),
		sessions:      map[string]*agent.Session{},
		worktreeLocks: map[string]*sync.Mutex{},
		worktreeReady: map[string]bool{},
		upgrader: websocket.Upgrader{
			// 仅本机/局域网使用，不做 Origin 校验（单用户）
			CheckOrigin: func(*http.Request) bool { return true },
		},
		clients: map[*wsClient]struct{}{},
	}
}

func projectDocsReader(workDir string) agent.ProjectDocs {
	f := project.LoadInstructions(workDir)
	return agent.ProjectDocs{Path: f.Path, Content: f.Content, Note: f.Note}
}

func (s *Server) Session() *agent.Session {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.sessions[s.lastSessionID]
}

func (s *Server) SetStream(stream agent.StreamFn) { s.stream = stream }

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

func (s *Server) AttachSessionStore(st *store.Store) error {
	if st == nil {
		return errors.New("会话存储为空")
	}
	s.st = st
	s.treg.SetSessionSearch(func(_ context.Context, q sessiondata.SearchQuery) (string, error) {
		hits, total, err := st.Search(q)
		if err != nil {
			return "", err
		}
		return sessiondata.FormatSearchHits(hits, total), nil
	})
	// M4：工具目录里的自定义工具（binary）注册进工具注册表——启动时就位，
	// 之后的目录变更由 catalog.tools.* 分支触发同步。
	s.syncDynamicTools()
	// M4 后半段：MCP 服务器连接 + 工具物化（同样是启动就位）。
	// 放在 syncDynamicTools 之后：对账内部会再同步一次注册表（那时 MCP 工具
	// 才在目录里），所以这里的顺序只影响首次启动的日志顺序。
	s.syncMCPServers()
	return nil
}

func (s *Server) CloseMCP() {
	if mgr := s.mcpManager(); mgr != nil {
		mgr.Close()
	}
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

func (s *Server) modelList() protocol.ModelListResult {
	return protocol.ModelListResult{Models: s.reg.List(), Roles: s.reg.RoleBindings()}
}

func (s *Server) broadcastModels() {
	s.broadcast(protocol.EventModels, s.modelList())
}

func (s *Server) NotifyModels() { s.broadcastModels() }

func (s *Server) broadcastSessionChanged(id, reason string) {
	s.broadcast(protocol.EventSessionChanged, protocol.SessionChangedParams{ID: id, Reason: reason})
}

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

func (s *Server) Run(ctx context.Context, addr string) error {
	s.setBaseCtx(ctx)
	mux := http.NewServeMux()
	mux.Handle(protocol.Path, s.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		s.closeAllClients()
		// MCP stdio 服务器是**子进程**：不显式关就会留下孤儿进程
		//（进程退出不会自动带走它们——Windows 上尤其如此）。
		s.CloseMCP()
		_ = srv.Shutdown(context.Background())
	}()
	log.Printf("后端 WS 服务监听 %s%s", addr, protocol.Path)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

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
