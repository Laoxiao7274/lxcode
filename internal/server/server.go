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
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"runtime/debug"
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
	// mergeJobProject 记录在跑合并任务归属的项目 id（任务 id → projectID）：
	// jobs.Spec 没有项目字段，合并进程的「按项目互斥」靠它（startMergeJob 在
	// mergeMu 内检查+登记，runMergeJob 收尾注销）。mergeMu 保护两个字段。
	mergeMu         sync.Mutex
	mergeJobProject map[string]string
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
	// 每次都走 prepareWorkspace——不记就每次读历史都重付一遍（见 prepareWorkspaceLocked）。
	worktreeReady map[string]bool

	// 轮末自动检查点提交已移除（2026-10 用户拍板，回滚方案后期重新设计）：原先
	// commitMu/commitLocks/commitWG 只为它服务，一并删除。提交现在只发生在用户
	// 显式要求的 workspace_sync 里（请求 goroutine 内同步执行，无需额外锁）。
	// mergeHookWG 已随轮末自动合并钩子（maybeAutoMerge）退役。

	upgrader websocket.Upgrader

	mu      sync.Mutex
	clients map[*wsClient]struct{}

	// 远程访问（internal/server/remote.go）：remote.json 路径 + 管理互斥。
	// 未装配（路径空）= 门不存在，行为与旧版一致。
	remoteMu   sync.Mutex
	remotePath string

	// 行为开关（internal/server/behavior.go）：behavior.json 路径 + 互斥 +
	// 热加载比对基线。未装配（路径空）= 恒默认档。
	behaviorMu   sync.Mutex
	behaviorPath string
	behaviorLast config.Behavior
}

// runtimeBuildSpan 仅供测试观测「运行时构建」的真实并发度（性能审计钉子：
// TestConcurrentResumeOfBigSessionsDoesNotSerialize——恢复路径若再被全局串行点
// 排队，构建并发度会恒为 1）。进入 newRuntime 时调用并 defer 其返回的收尾函数；
// 生产恒 nil。
var runtimeBuildSpan func() func()

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

	// 每客户端**有界发送队列 + 独立 writer goroutine**（性能审查 P1-2，2026-10）：
	// 原先 broadcast 在流式转发 goroutine 里同步逐客户端 WriteJSON，慢客户端
	//（页面挂起不读、TCP 窗口满）一次写满缓冲就要等满 5s deadline 才报错——
	// 这一个事件让生成流整整停 5s，两个慢客户端串行即 10s。现在广播只做入队
	//（O(1)），每客户端单 writer 按序写（事件保序不变）；队列满 = 该客户端
	// 跟不上 → 关闭这条连接交给重连（fast-fail，与 wsclient 的「缓冲满丢最旧/
	// 断连」同一哲学），绝不让慢客户端阻塞生成流与其他客户端。
	out       chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

// clientQueueSize 是每客户端发送队列的容量（条数）。256 条 delta 级事件足够吸收
// 正常抖动；真满了说明客户端已经掉队（不是抖动），留着也只是白占内存。
const clientQueueSize = 256

// close 关闭连接并通知 writer goroutine 收尾（幂等——多处失败路径都会调它）。
func (c *wsClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// writeLoop 是该客户端唯一的写者：按序把队列里的帧写到连接上（保序不变）。
// 写失败（含 5s deadline 到了——超时后 gorilla 连接内部状态可能已脏，一帧只写了
// 一半，唯一正确的处理就是 Close，绝不能复用这条连接继续写）→ 关闭并退出。
func (c *wsClient) writeLoop() {
	for {
		select {
		case b := <-c.out:
			_ = c.conn.SetWriteDeadline(time.Now().Add(broadcastWriteTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				c.close()
				return
			}
		case <-c.done:
			return
		}
	}
}

// enqueue 把一条消息排进该客户端的发送队列（非阻塞）。返回 false = 该客户端已被
// 放弃（队列满被 fast-fail，或连接已关）——调用方无需再对它做什么，读循环的
// defer 会把它清理出注册表。
func (c *wsClient) enqueue(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		// 序列化失败是服务端 bug（协议载荷必须可序列化）：丢这条消息并记日志，
		// 不关连接——断连解决不了序列化问题。
		log.Printf("消息序列化失败（丢弃）: %v", err)
		return true
	}
	return c.enqueueBytes(b)
}

func (c *wsClient) enqueueBytes(b []byte) bool {
	select {
	case c.out <- b:
		return true
	case <-c.done:
		return false
	default:
		// 队列满 = 该客户端跟不上：fast-fail 关闭（客户端会自动重连）。
		log.Printf("客户端发送队列已满（%d 条），关闭慢客户端", clientQueueSize)
		c.close()
		return false
	}
}

// 连接焦点（sessionID）的受锁访问器：请求并发化后（serve 每请求一个 goroutine），
// 多个请求可能同时读写焦点——mu 同时保护写帧与焦点。
func (c *wsClient) focus() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

func (c *wsClient) setFocus(id string) {
	c.mu.Lock()
	c.sessionID = id
	c.mu.Unlock()
}

// broadcastWriteTimeout 是单帧写的上限：超过即视为该客户端消费不动，
// 关闭连接（客户端会自动重连）。超时错误的清理走 writeLoop 的关闭路径。
const broadcastWriteTimeout = 5 * time.Second

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
	// 崩溃自愈（docs/session-tree.md §C.5）：上次「文件已还原但 leaf 未移」的
	// 回滚在这里补做（只移指针，不碰文件）。失败只记日志——自愈是缓解，不是启动前提。
	s.ResumePendingRestores()
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
		Publish:         s.workspacePublish,
		PublishConfirm:  s.publishConfirmText,
	})
	// agent_read / agent_write（Agent 配置读写，lxagent 的手脚）的 server 侧
	// 实现：与 SetWorkspaceOps 同款注入模式。Update 走 store.UpdateAgentFields
	// ——与前端编辑器共用一套校验与落库（user_modified=1，种子热更新不覆盖）；
	// 成功后广播 agent.changed，让在线的编辑器界面立即反映新配置。
	s.treg.SetAgentCatalogOps(tools.AgentCatalogOps{
		List: func() ([]sessiondata.AgentDef, error) {
			return st.ListAgents()
		},
		Update: func(id string, u sessiondata.AgentFieldUpdate) (string, error) {
			if err := st.UpdateAgentFields(id, u); err != nil {
				return "", err
			}
			s.broadcast(protocol.EventAgentChanged, protocol.AgentChangedParams{Reason: "update"})
			return fmt.Sprintf("已更新 Agent「%s」的配置并落库——该 Agent 下次被派发就按新配置运行（编辑器界面已同步刷新）。", id), nil
		},
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
	// 测试观测点（性能审计钉子 TestConcurrentResumeOfBigSessionsDoesNotSerialize）：
	// 记录运行时构建的真实并发度——生产为 nil，一次 nil 比较的开销可忽略。
	if runtimeBuildSpan != nil {
		defer runtimeBuildSpan()()
	}
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
		// 审批档恢复（会话级状态持久化）：AttachTo 已把**落库值**恢复进内存；
		// 空列（老会话/从没改过档）→ 回落 behavior 默认档——只有默认档不是
		// confirm 时才需要显式置位（confirm 是缺省语义），置位同时会把列补上，
		// 之后的恢复就不再走回落。
		if stored, err := s.st.ApprovalOf(id); err != nil {
			log.Printf("读会话审批档失败（回落默认档）: %v", err)
		} else if stored == "" {
			if def := s.behaviorConfig().Approval; def != "" && def != config.ApprovalConfirm {
				sess.SetApproval(def)
			}
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
		c := &wsClient{conn: conn, out: make(chan []byte, clientQueueSize), done: make(chan struct{})}
		s.mu.Lock()
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.clients, c)
			s.mu.Unlock()
			// close（不只是 conn.Close）：writer goroutine 可能正空闲等队列，
			// 只关连接不关 done 会让它永远等下去（goroutine 泄漏）。
			c.close()
		}()
		// 单 writer goroutine：本连接上所有帧（事件 + 应答）都经它的队列按序写。
		go c.writeLoop()
		// 连接建立即告知服务端身份与忙闲（客户端据此决定初始状态）
		_ = c.enqueue(protocol.NewEvent(protocol.EventReady, protocol.HelloResult{
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
		// **每请求一个 goroutine**（2026-12 性能审计钉子
		// TestSlowRequestDoesNotHeadOfLineBlockPing）：原来 dispatch 在读循环里
		// 同步执行，一个慢请求（大会话历史加载 / 压缩摘要调用）把同一连接上的
		// 后续请求全部队头阻塞——心跳 connection.ping（客户端 3s 超时、连续 2 次
		// 失败即误判断连）跟着遭殃，是「恢复会话失败: 请求超时」的放大器。
		// 应答按 id 配对，乱序到达是 JSON-RPC 语义允许的（前端按 id 路由）；
		// 事件走独立广播路径（broadcast），按 session 的保序不变。
		go func(req protocol.Request) {
			// panic 防护（健壮性加固，2026-10）：每个请求一个 goroutine，任何
			// panic 按 Go 语义打死整个进程。这里 recover → 记日志（stack 完整
			// 落盘）+ 回 JSON-RPC internal error 应答——该请求失败，进程与其他
			// 请求照常运行。
			defer func() {
				if r := recover(); r != nil {
					log.Printf("请求 %s 处理 panic（进程继续运行）: %v\n%s", req.Method, r, debug.Stack())
					if len(req.ID) != 0 && string(req.ID) != "null" {
						c.enqueue(protocol.NewError(req.ID, protocol.CodeInternal,
							"internal error: 请求处理发生内部错误（已记录日志）"))
					}
				}
			}()
			resp := s.dispatch(c, &req)
			if resp != nil {
				// 应答同样走队列（单写者纪律——gorilla 连接不允许两个并发写者）：
				// 队列满被 fast-fail 时连接已由 enqueue 关闭，读循环随即收尾。
				c.enqueue(resp)
			}
		}(req)
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
	// 序列化**一次**、每客户端只做入队（O(1)）：原先在这里同步逐客户端
	// WriteJSON，慢客户端会把生成流卡满 5s deadline（见 wsClient.out 的注释）。
	b, err := json.Marshal(ev)
	if err != nil {
		log.Printf("广播事件序列化失败（丢弃）: %v", err)
		return
	}
	s.mu.Lock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		c.enqueueBytes(b)
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
	// 与 /health 同一个 mux、同一个端口。**套远程访问的 token 门**（与 /rpc 同一份
	// checkRemoteToken）：监听切到 0.0.0.0 后这条 URL 无凭证也 reachable——不套门
	// 等于远程开着时会话附件对外裸奔（remote_test.go 有「无 token 拒绝」的钉子）。
	mux.Handle("/attachments/", s.attachmentsGate(s.handleAttachments))
	// 远程访问管理端点（loopback-only，见 remote.go）
	mux.HandleFunc("/remote-access", s.handleRemoteAccess)
	return mux
}

// Run 在 listener.go（监听生命周期 + remote.json 开关热切换）。

func (s *Server) closeAllClients() {
	s.mu.Lock()
	clients := make([]*wsClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		c.close()
	}
}
