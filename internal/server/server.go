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
	"time"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/mcp"
	"github.com/moyunteng/lxcode/internal/modelcatalog"
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
	// catalog 是可选模型目录服务（AttachModelCatalog 装配；nil = 未装配，
	// 目录与探测方法返回「未装配」）。与 search 不同，它是**无状态查询**：
	// 不写配置、不广播事件，所以没有热加载与变更通知。
	catalog *modelcatalog.Service
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

	// commitMu/commitLocks 是每会话的自动提交锁（轮结束的检查点提交）：同一会话的
	// 提交必须串行，避免并发轮次互相打断 git index。与 worktreeLocks 分开——提交
	// 在事件路径的 goroutine 上跑，而 worktreeLocks 在 prepare/release 里被持有且
	// 可能跑较久的 git 校验，共用会互相阻塞（见 autocommit.go）。
	commitMu    sync.Mutex
	commitLocks map[string]*sync.Mutex
	// commitWG 追踪在途的自动提交（测试等它们结束用；生产路径不读它）。
	commitWG sync.WaitGroup

	upgrader websocket.Upgrader

	mu      sync.Mutex
	clients map[*wsClient]struct{}

	// 远程访问（internal/server/remote.go）：remote.json 路径 + 管理互斥。
	// 未装配（路径空）= 门不存在，行为与旧版一致。
	remoteMu   sync.Mutex
	remotePath string
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

// broadcastWriteTimeout 是单次广播写的上限：超过即视为该客户端消费不动，
// 关闭连接（客户端会自动重连）。写超时错误的清理走 broadcast/serve 既有路径。
const broadcastWriteTimeout = 5 * time.Second

func (c *wsClient) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 写超时防护：慢客户端（TCP 窗口满、页面被挂起不读）不能拖住广播循环把
	// 其他客户端的事件路径也卡住（head-of-line blocking）。deadline 到了
	// WriteJSON 返回错误，调用方关闭这条连接（客户端会自动重连）。
	// 注意 gorilla 的 WriteJSON 超时后连接内部状态可能已脏（一帧只写了一半），
	// 唯一正确的处理就是 Close，绝不能复用这条连接继续写。
	_ = c.conn.SetWriteDeadline(time.Now().Add(broadcastWriteTimeout))
	err := c.conn.WriteJSON(v)
	if err != nil {
		return err
	}
	_ = c.conn.SetWriteDeadline(time.Time{}) // 写成功清掉 deadline，不影响后续写
	return nil
}

func NewServer(reg *config.Registry) *Server {
	return &Server{
		reg:           reg,
		treg:          tools.New(),
		mcpMgr:        mcp.NewManager(),
		sessions:      map[string]*agent.Session{},
		worktreeLocks: map[string]*sync.Mutex{},
		worktreeReady: map[string]bool{},
		commitLocks:   map[string]*sync.Mutex{},
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
	// 图片附件：注入 llm 层的图片读取函数（请求构造瞬间按引用读文件 → base64，
	// 分层纪律见 internal/llm/images.go）。
	s.bindAttachmentLoader(st)
	s.treg.SetSessionSearch(func(_ context.Context, q sessiondata.SearchQuery) (string, error) {
		hits, total, err := st.Search(q)
		if err != nil {
			return "", err
		}
		return sessiondata.FormatSearchHits(hits, total), nil
	})
	// 工作区三件套（workspace_status / workspace_sync / workspace_rollback）的
	// server 侧实现：与 SetSessionSearch 同款注入模式（tools 是叶子包不 import
	// server，回调在装配期写一次）。
	s.treg.SetWorkspaceOps(tools.WorkspaceOps{
		Status:          s.workspaceStatus,
		Sync:            s.workspaceSync,
		SyncConfirm:     s.syncConfirmText,
		Rollback:        s.workspaceRollback,
		RollbackConfirm: s.rollbackConfirmText,
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
		// 归属 Agent 记在库里（子会话 = 它自己的 Agent）：按 id 重建运行时（刷新后
		// 打开会话页）时读回来，否则 ModelID() 只能回落主 Agent 的模型——子会话页
		// 会显示一个它没用过的模型。读不到不打断建运行时（显示中性态即可）。
		//
		// **先置位再附着**：附着时要把上下文占用恢复进内存（AttachTo →
		// restoredUsage），而"库里没有占用"那条回落路要按**这个会话的**模型解析窗口
		//（子 Agent 可以绑自己的模型）——顺序反了窗口就按主 Agent 算。
		agentID, err := s.st.SessionAgentID(id)
		if err != nil {
			log.Printf("读会话归属 Agent 失败（模型显示回落主 Agent）: %v", err)
		}
		sess.SetAgentID(agentID)
		if err := sess.AttachTo(s.st, id); err != nil {
			return nil, err
		}
	}
	return sess, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 远程访问的门：Enabled 时所有升级必须携带匹配 token（含回环——
		// 公网隧道从本机回环进来）。失败直接 401，不进入 ws 升级。
		if err := s.checkRemoteToken(r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
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

// routes 装配 HTTP 路由（WS 端点 + /health + 附件只读端点 + 远程访问管理）。
// 抽出来是为了测试能对**同一份路由表**起 httptest 服务（Run 与测试零漂移）。
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(protocol.Path, s.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	// 图片附件只读端点（GET /attachments/<sessionID>/<file>）：前端显示缩略图用。
	// 与 /health 同一个 mux、同一个端口（7789，只绑 127.0.0.1）。
	mux.HandleFunc("/attachments/", s.handleAttachments)
	// 远程访问管理端点（loopback-only，见 remote.go）
	mux.HandleFunc("/remote-access", s.handleRemoteAccess)
	return mux
}

func (s *Server) Run(ctx context.Context, addr string) error {
	s.setBaseCtx(ctx)
	srv := &http.Server{Addr: addr, Handler: s.routes()}
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
