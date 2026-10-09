// Package agent 实现会话运行时：内存历史 + agent 工具循环 + 确认门 +
// todo 状态。这是内核的核心循环——上层（CLI、桌面壳）通过 Send 发起一轮，
// 经 Emitter 收到类型化事件流（流式增量、工具调用、确认请求……），
// 经 Confirm 裁决高危操作。内核不依赖任何 UI 框架与网络协议。
//
// 本文件是 Session 的**外壳**：类型定义、生命周期、公开访问器与状态查询。
// 一轮生成的四个阶段与它们的支撑函数分在：turn.go（回合循环）、tools.go
// （工具执行与权限门）、confirm.go（确认门）、resolve.go（Agent/模型解析）、
// persist.go（落库与附着）、switch.go（会话切换）。拆分的理由是这些职责的
// 读者与改动者完全不同——改确认门的人不需要读压缩触发逻辑。

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

const (
	// maxToolResultBytes 工具结果进入对话历史的上限（执行层 32KB 全文上限，
	// 历史层再收口——上下文有限）。
	//
	// 这里**刻意没有**"工具循环轮数上限"（原 maxToolRounds=16，2026-09-29 用户拍板
	// 去掉）：轮数区分不了「卡住」与「任务本来就长」——通读仓库的 researcher 16 轮
	// 每轮参数都不同，却被误杀在半路。防死循环改用**同参数重复调用**判据，见 repeat.go。
	maxToolResultBytes = 8 * 1024
)

// StreamFn 是 LLM 调用的抽象缝（单测注入假实现，不碰网络）。
type StreamFn func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error)

// AgentResolver 按名单 id 解析 Agent 装配载荷（四层组合的输入）。
// 消费方定义的接口（server 从 store 注入——agent 不 import store，
// 分层规则同 Persistence）。返回 ok=false 表示名单里没有该 id。
type AgentResolver interface {
	Resolve(agentID string) (*sessiondata.AgentContext, bool)
	// ResolveByName 按名字解析（容错：模型把名字当 id 传时兜底）。
	ResolveByName(name string) (*sessiondata.AgentContext, bool)
}

// 哨兵错误：服务端按类别映射协议错误码（errors.Is 判断，不做字符串匹配）。
var (
	// ErrBusy 会话正在生成中（Send/切换会话被拒）。
	ErrBusy = errors.New("会话正在生成中")
	// ErrPendingConfirm 有挂起的确认未处理（切换会话被拒）。
	ErrPendingConfirm = errors.New("有挂起的确认，先处理确认再切换会话")
	// ErrNoDefaultModel default 角色未绑定模型。
	ErrNoDefaultModel = errors.New("default 模型未配置")
	// ErrModelDisabled default 模型已停用。
	ErrModelDisabled = errors.New("default 模型已停用")
	// ErrAgentNotFound 指定的 Agent 不在名单中。
	ErrAgentNotFound = errors.New("Agent 不存在")
	// ErrAgentDisabled 指定的 Agent 已停用（不可选用）。
	ErrAgentDisabled = errors.New("Agent 已停用")
)

// Emitter 是事件出口：内核把类型化事件推给宿主（CLI 打印 / 壳渲染）。
type Emitter func(ev Event)

// Session 是单会话 agent 运行时：内存历史 + 串行工具循环 + 确认门 + todo。
// 会话级别的并发语义：同一时刻只有一轮生成（busy），Send 忙时拒绝。
type Session struct {
	reg    *config.Registry
	tools  *tools.Registry
	stream StreamFn
	emit   Emitter

	mu      sync.Mutex
	history []llm.Message
	busy    bool
	cancel  context.CancelFunc
	pending *ConfirmRequest
	// confirm 是确认门的裁决通道。ack 是「裁决结果」而不再是裸 bool：二元确认
	// 只填 Allow；ask_user 提问由 Answer 投入文本答案（见 confirm.go 的
	// ConfirmOutcome）。ask 与确认共用同一个槽位与同一条通道——全应用只有
	// 「同时一个挂起确认」这条不变式不因新形态而多出出口。
	confirm chan ConfirmOutcome
	todos   []tools.TodoItem
	// approval 是本会话**当前**的权限档（会话级实时状态，不是一轮的快照）：
	// 用户中途改档要立刻作用于正在跑的那一轮（runTools 每个工具调用现读
	// LiveApproval）。空 = confirm（与 tools.ApprovalFrom 的缺省语义一致）。
	approval string
	// approvalSource 非 nil = 本会话是子会话：档位不自己说了算，而是
	// 「父会话**此刻**的档位」与 approvalDefault 取严（子执行面不大于请求方）。
	// 为什么是回调而不是派发那一刻算好的死值：父会话中途改档必须传得到
	// **正在跑的**子会话——用户实测「跑 dev 的是子 Agent」正是这条。
	approvalSource func() string
	// approvalDefault 是子 Agent 自己的权限默认（取严用；空 = 未声明，按
	// 「继承父会话」处理）。只在 approvalSource 非 nil 时有意义。
	approvalDefault string
	// notices 是待投递的自动通告队列（后台任务唤醒）：Notify 忙时排队，
	// runTurn 在**轮边界**并入历史（见 notify.go）。与 history 同一把锁——
	// 否则 injectNotices 与 append 会交错。
	notices []llm.Message
	// wakeGate 是「能不能开新一轮」的判定（server 侧的连续唤醒预算；
	// nil = 不限制）。只约束空闲开新轮，不管轮边界注入。
	wakeGate func() bool

	// 持久化（st 为 nil = 纯内存模式，兼容不接存储的调用方/单测）。
	// SQLite 版无句柄概念：会话 = sessions 表一行，按 s.id 追加写。
	st Persistence
	id string // 当前会话 id（空 = 尚未创建行）
	// pendingWorkspace 是「下一个新会话」的归属项目（session.new 时设置，
	// 会话行首条消息懒建时落库并清空）。单会话架构下的过渡设计——
	// 多会话并发时归属直接挂在会话对象上。
	pendingWorkspace string
	// workDir 是当前会话的工作目录（归属项目的根目录；空 = 后端进程
	// 目录）。工具的相对路径、bash 默认目录与系统提示词的工作目录说明
	// 都以它为准——每轮开始时快照进 ctx（tools.WithWorkDir）。
	workDir string
	// worktreeBranch/worktreeBase 是本会话 Git 工作树的分支与基线提交
	// （server 在 prepareWorktree 成功后设置；子会话从父会话继承）。空 =
	// 未分组会话/没有 worktree——提示词对 Git 工作树零注入。与 workDir
	// 的分工：workDir 是「工具在哪跑」，这两个是「模型该怎么看待提交」。
	worktreeBranch string
	worktreeBase   string
	// dispatchRoot 是本轮主 Agent 载荷（dispatch 委派名单校验的依据——
	// runDispatch 在工具执行位读它；busy 期间与 ac 同生命周期）。
	dispatchRoot *sessiondata.AgentContext
	// agents 是名单解析器（M2 上下文组装——nil = 无 Agent 语境，走
	// 全局默认提示词；server 装配时从 store 注入）。
	agents AgentResolver
	// projectDocs 是项目守则读取器（项目根 AGENTS.md——server 装配时注入；
	// nil = 不注入）。agent 不碰文件系统：读取实现由消费方提供。
	projectDocs ProjectDocsFunc
	// context 是最近一次主轮请求的上下文占用（P1 计账）：Used 优先取
	// provider 回报的真实 prompt_tokens，拿不到才用估算。压缩触发与 UI
	// 指示器都读它——单一事实源，避免两处各算一遍而互相矛盾。
	context ContextUsage
	// turnDone 是本轮的收尾信号（SendWait 用）：Send 时新建，runTurn 的
	// defer 关闭。派发要等子会话给出结论——异步 Send 满足不了这个需求。
	turnDone chan struct{}
	// confirmProxy 非空时本会话的确认请求交给它裁决（子会话把确认门代理给
	// 父会话）：全应用只有"同时一个挂起确认"这条不变式，子会话自己持
	// pending 的话服务端的 tool.confirm 找不到它。
	confirmProxy func(ctx context.Context, req *ConfirmRequest) (ConfirmOutcome, bool)
	// confirmMu 串行化确认门本身：上面那条不变式（同时只有一个挂起确认）原先靠
	// "工具循环是串行的"顺带成立；并行 dispatch（runTools 阶段二）之后多个子会话
	// 会同时来要确认，不串行化的话后到的会顶掉 s.pending/s.confirm，先到的那次
	// 永远等不到裁决——子会话卡死在 busy。用户一次只裁决一个，排队符合交互语义。
	confirmMu sync.Mutex
	// protectHead 为真时压缩不碰历史第 0 条——子会话的任务说明书（派发时那条
	// user 任务消息，子 Agent 的全部依据）。它一旦被压进摘要，续跑/长任务里
	// 子 Agent 就再也看不到原始任务，只能看到一份自己越写越远的转述。
	// 主会话为假（没有这条头部，压缩前缀从 0 开始）；由 dispatch.openChildSession
	// 置位（新建与续跑同一入口）。
	protectHead bool
	// ownerID 是本会话的**时间线归属**（顶层会话 id）。子 Agent 是独立会话，它起的
	// 后台任务要挂在父会话的时间线上——唤醒通告投给父会话才有人能行动（子会话不进
	// 侧栏）。空 = 自己就是顶层（OwnerSessionID 回落自身 id）。
	// 由 dispatch.openChildSession 置位。
	ownerID string
	// agentID 是本会话运行的 Agent 名单 id（子会话 = 它自己的 Agent；主会话 = main）。
	// 空 = 旧语境/未指定（resolveAgent 把空当主 Agent）。
	//
	// 为什么要记它：会话页要显示"这个会话用的是哪个模型"，而子 Agent 可以绑自己的
	// 模型（AgentDef.Model）——不记归属 Agent 就只能回落主 Agent 的模型，把子会话的
	// 模型显示错。来源有三处：Send 时解析出的 ac、dispatch 开子会话时显式置位、
	// 服务端按会话 id 从库里读回（刷新后重新附着同一会话）。
	agentID string
}

// New 创建会话；emit 为 nil 时事件被丢弃（单测可只调方法）。
func New(reg *config.Registry, toolReg *tools.Registry, emit Emitter) *Session {
	if emit == nil {
		emit = func(Event) {}
	}
	s := &Session{reg: reg, tools: toolReg, stream: streamWithLLM, emit: emit}
	// 注意：todo 清单的写回口与技能目录都**不进注册表**（进程级单例），
	// 而是每轮经 ctx 注入（runTurn 里 WithTodoSink / WithSkillSource）——
	// 子 Agent 是独立会话后，注册表级全局态会让父子互相踩（见 tools/sessionstate.go）。
	return s
}

// SetAgentResolver 挂载名单解析器（M2 上下文组装；server 装配时注入，
// nil 已挂时是 no-op——幂等）。挂载后 Send 默认走主 Agent 语境。
func (s *Session) SetAgentResolver(r AgentResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agents == nil {
		s.agents = r
	}
}

// SetProjectDocs 挂载项目守则读取器（项目根 AGENTS.md；server 装配时注入）。
// 每轮组装提示词时调用一次——守则改了立刻生效（长命会话里「我刚改了
// AGENTS.md 它却不知道」是更糟的体验）。
func (s *Session) SetProjectDocs(fn ProjectDocsFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectDocs = fn
}

// SetProtectHead 声明本会话压缩时是否保护历史第 0 条（子会话的任务说明书）。
// 只该在会话开始跑之前调用一次——dispatch 开子会话时置位，主会话保持默认 false。
func (s *Session) SetProtectHead(protect bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.protectHead = protect
}

// projectDocsFor 取会话工作目录对应的项目守则（未注入或未分组 → 空）。
func (s *Session) projectDocsFor(workDir string) ProjectDocs {
	s.mu.Lock()
	fn := s.projectDocs
	s.mu.Unlock()
	if fn == nil || workDir == "" {
		// 空 workDir = 未分组会话（无项目）：严格项目级——不注入，
		// 也绝不退化成读后端进程目录的 AGENTS.md（那会把 lxcode 自己的
		// 仓库守则塞进用户无关的对话）
		return ProjectDocs{}
	}
	return fn(workDir)
}

// SetEmitter 替换事件出口（宿主构造晚于 Session 时接线用）。
// 并发安全：emit 只在事件 goroutine 里读——替换发生在任何 Send 之前是
// 期望用法；startEvents 的锁由宿主自己保证。
func (s *Session) SetEmitter(emit Emitter) {
	if emit == nil {
		emit = func(Event) {}
	}
	s.mu.Lock()
	s.emit = emit
	s.mu.Unlock()
}

// SetStream 替换 LLM 调用实现（测试注入假实现，不碰网络；须在 Send 前调用）。
func (s *Session) SetStream(fn StreamFn) {
	s.mu.Lock()
	s.stream = fn
	s.mu.Unlock()
}

// SetApprovalSource 声明本会话的权限档来源（子会话 = 父会话的实时档位）。
// 传回调而不是值：父会话中途改档必须传得到正在跑的子会话。只该在会话开始跑
// 之前调用一次——dispatch 开子会话时置位，主会话保持 nil（自己说了算）。
func (s *Session) SetApprovalSource(src func() string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvalSource = src
}

// SetApprovalDefault 声明子 Agent 自己的权限默认（取严用；空 = 未声明）。
// 与 SetApprovalSource 配套，只该在会话开始跑之前调用一次。
func (s *Session) SetApprovalDefault(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvalDefault = mode
}

// LiveApproval 返回本会话**此刻**的权限档（每个工具调用现读，不是开轮时的快照）。
//
// 为什么要有它（2026-09-29 用户实测）：权限档原先在 Send 里快照进这一轮的 ctx、
// runTools 每轮读那份快照——用户跑到一半把权限放开，正在跑的那一轮完全不知道，
// 该弹确认还是弹。改成会话级实时状态后，中途改档立刻作用于运行中的一轮。
//
// 子会话：stricterApproval(父会话此刻的档位, 子 Agent 自己的默认)——取严语义
// （子执行面不大于请求方）保持不变，只是从「派发那一刻」改成「每次现算」。
//
// 锁纪律：approvalSource 会回调父会话（父会话要拿自己的 s.mu），**绝不能在持
// s.mu 时调它**——同锁重入会死锁。所以先把三个字段取出来、解锁，再调。
func (s *Session) LiveApproval() string {
	s.mu.Lock()
	src, def, cur := s.approvalSource, s.approvalDefault, s.approval
	s.mu.Unlock()
	if src != nil {
		return stricterApproval(src(), def)
	}
	// 顶层会话：空 = 未指定 → confirm（沿用 effectiveApproval 的缺省单点）。
	return effectiveApproval(cur, "")
}

// SetApproval 设置本会话当前的权限档（中途改档立刻生效于运行中的一轮）。
// 空串 = 未指定（回落 confirm）。返回规范化后的档位。
//
// 切到 auto 时顺带放行挂起的确认：用户已经说了「别问我」，留一张卡堵着等于
// 「说了没用」——那正是这次要修的东西。裁决走**既有的确认通道**（往 s.confirm
// 投递，与 Confirm 完全同一条路径）：在锁里直接写 channel 会死锁，而绕开通道
// 另造一条放行路径会让「同时一个挂起确认」这条不变式多出一个出口。
//
// **ask 提问不放行**：auto 豁免的是「高危工具的确认打扰」，而 ask_user 是模型
// 主动向用户要决策——自动替用户编一个回答等于冒充用户表态（合并进程里可能就是
// 「冲突保哪边」这种不可撤回的取舍）。所以 auto 只放行 Kind=="" 的确认，
// ask 保持挂起等用户回来。
func (s *Session) SetApproval(mode string) string {
	norm := mode
	if norm == "" {
		norm = string(tools.ApprovalConfirm)
	}
	s.mu.Lock()
	s.approval = norm
	// 只在切到 auto 且确实有挂起**确认**（非 ask 提问）时取通道；非阻塞投递
	//（缓冲已满 = 这次挂起已被裁决过，重复投递没有意义）。
	var ch chan ConfirmOutcome
	if norm == string(tools.ApprovalAuto) && s.pending != nil && s.pending.Kind == "" {
		ch = s.confirm
	}
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- ConfirmOutcome{Allow: true}:
		default:
		}
	}
	return norm
}

// Send 发起一轮对话（异步）：校验模型 → 入历史 → 后台跑工具循环。
// 忙时返回 ErrBusy；模型未绑定/停用返回对应哨兵（服务端按类别映射错误码）。
func (s *Session) Send(text string, opts ...SendOpt) error {
	if text == "" {
		return errors.New("text 不能为空")
	}
	var cfg sendConfig
	for _, o := range opts {
		o(&cfg)
	}
	ac, err := s.resolveAgent(cfg.agentID)
	if err != nil {
		return err
	}
	if _, err := s.modelFor(ac); err != nil {
		return err // 快速失败（模型缺失在入历史前拒绝——不留半截轮次）
	}

	// 图片张数上限在入历史前校验（快失败——不留半截轮次）。
	if len(cfg.images) > llm.MaxImagesPerMessage {
		return fmt.Errorf("一条消息最多带 %d 张图片（收到 %d 张）", llm.MaxImagesPerMessage, len(cfg.images))
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy = true
	// 先落盘拿序号再入历史：chat.userMessage 实时事件要带 seq（前端拿它当撤回
	// 锚点），只让"刷新后的历史"有序号等于这条消息当场就撤不了——用户点撤回时
	// 前端手里没有锚点。
	// Notice 是**注入的提示条**标记（后台任务通告走 flushNotices → Send）：
	// 它随消息落库，会话统计的轮数按它把注入消息排除在外（见 store.foldSessionStats）。
	// Images 是图片**文件引用**（视觉请求）：随消息落库与回放，base64 绝不经此路径。
	userMsg := llm.Message{Role: "user", Content: text, Notice: cfg.notice, Images: cfg.images}
	userMsg.Seq = s.persistLocked(userMsg)
	s.history = append(s.history, userMsg)
	// 记下本轮归属的 Agent（会话页显示的模型按它解析）：主会话空 id 解析出的是
	// 主 Agent，这里存解析后的 id，之后 ModelID() 走同一条解析路径。
	if ac != nil {
		s.agentID = ac.Def.ID
	}
	// 权限模式**存进会话**（会话级实时状态，runTools 每次现读——见 LiveApproval）：
	// 显式给了就按请求级覆盖（chat.send 的参数语义不变，请求级优先），没给则
	// 只在还没定过时回落 Agent 默认——CLI 路径不带参数，别把用户中途选的档位
	// 重置掉。Agent 载荷与白名单仍进 runTurn（每轮快照，busy 期间不可变）。
	if cfg.approval != "" {
		s.approval = effectiveApproval(cfg.approval, agentDefaultOf(ac))
	} else if s.approval == "" {
		s.approval = effectiveApproval("", agentDefaultOf(ac))
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()
	// ctx 里这份是「开轮时的请求级档位」——判定来源已改成 LiveApproval（实时），
	// 这份保留给仍按 ctx 取档的调用方（tools.ApprovalFrom，见契约 E）。
	ctx = tools.WithApproval(ctx, tools.Approval(s.LiveApproval()))

	s.emit(UserMsgEvent{Message: userMsg})
	s.emit(BusyEvent{Busy: true})
	go s.runTurn(ctx, cfg, ac)
	return nil
}

// Cancel 取消当前生成（aborted 语义：已生成部分保留入历史）。
func (s *Session) Cancel() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// SendWait 跑一轮并**等它结束**（派发给子会话时用：主 Agent 要拿子会话的结论）。
// ctx 取消 → 取消这一轮并等它真正收尾（取消是异步的，不等会读到半截历史）。
// 与 Send 的差别只有"等"：事件流、历史写入、压缩触发全部走同一条路径。
func (s *Session) SendWait(ctx context.Context, text string, opts ...SendOpt) error {
	done := make(chan struct{})
	s.mu.Lock()
	s.turnDone = done
	s.mu.Unlock()

	if err := s.Send(text, opts...); err != nil {
		s.mu.Lock()
		s.turnDone = nil
		s.mu.Unlock()
		return err
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.Cancel()
		<-done // 等它真正收尾（取消是异步的）——否则父会话会读到半截历史
		return ctx.Err()
	}
}

// Busy 返回当前忙闲状态。
func (s *Session) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

// History 返回会话快照（消息 + 忙闲 + 挂起的确认 + todo），供宿主初始化视图。
func (s *Session) History() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := append([]llm.Message(nil), s.history...)
	var pending *ConfirmRequest
	if s.pending != nil {
		p := *s.pending
		pending = &p
	}
	return Snapshot{
		Messages: msgs, Busy: s.busy, Pending: pending,
		SessionID: s.id, Todos: append([]tools.TodoItem(nil), s.todos...),
		Context: projectedUsage(s.context, msgs), Checkpoints: checkpointIndexes(msgs),
	}
}

// projectedContext 是**展示用**的占用：真实测量 + 测量之后历史的变化量
// （projectedUsage，见那里的注释）。调用方必须持锁（它读 s.history）。
//
// 与 ContextUsage() 的分工：那个是**判定用**的真实压力值（maybeCompact 拿它比阈值），
// 这个是给 UI 看的投影值。两个不能混——判定用投影会在"其实还没到"的时候触发压缩。
func (s *Session) projectedContext() ContextUsage {
	return projectedUsage(s.context, s.history)
}

// Todos 返回当前任务清单（UI 渲染用）。
func (s *Session) Todos() []tools.TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tools.TodoItem(nil), s.todos...)
}

// ContextUsage 返回最近一次主轮请求的上下文占用（零值 = 本会话还没跑过主轮，
// 或刚切过会话——那时真实用量未知，UI 应显示中性态而不是编一个数）。
//
// **这是判定用的真实值**（压缩压力阈值比的就是它）。要展示给用户请用
// ProjectedContextUsage——那个把"测量之后历史又长了多少"折进去了。
func (s *Session) ContextUsage() ContextUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.context
}

// ProjectedContextUsage 返回**展示用**的占用：真实测量 + 测量之后历史的变化量。
// 事件（chat.done 的 context）与快照都走这个——用户看到的数字要跟着历史走，
// 不能停在最后一次请求那一刻（工具跑得越久偏得越多）。
func (s *Session) ProjectedContextUsage() ContextUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.projectedContext()
}

// SetAgentID 声明本会话运行的 Agent 名单 id（空 = 旧语境/主 Agent）。
// dispatch 开子会话时置位（子会话 = 它自己的 Agent），服务端在按 id 重建运行时
// 从库里读回（刷新后打开子会话页仍要显示它自己的模型）。只该在会话开始跑之前
// 或附着之后调用——它决定 ModelID() 的解析路径。
func (s *Session) SetAgentID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentID = id
}

// ModelID 返回本会话**实际使用**的模型注册表 id（走 resolve.go 那一套：Agent 绑定
// AgentDef.Model 优先，否则 default 角色）。
//
// 空串 = 未知（名单里没这个 Agent / 模型被删或被停用 / 没有 default 角色）——wire 上
// 整键缺席，前端显示中性态。**绝不**回落成"编一个模型名"：那会让子会话页显示一个
// 它其实没在用的模型。
func (s *Session) ModelID() string {
	s.mu.Lock()
	agentID := s.agentID
	s.mu.Unlock()
	// 锁外解析：resolveAgent/modelFor 读注册表，不碰会话锁（避免与注册表热加载
	// 的写锁交叉持有）。
	ac, err := s.resolveAgent(agentID)
	if err != nil {
		return ""
	}
	m, err := s.modelFor(ac)
	if err != nil {
		return ""
	}
	return m.ID
}

// WorkDir 返回当前会话的工作目录（空 = 后端进程目录）。
func (s *Session) WorkDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workDir
}

// SetWorkDir 覆盖当前会话的工具工作目录；运行中的轮次不能改目录。
// server 在挂载独立 Git worktree 后调用，子 Agent 也用它继承父目录。
func (s *Session) SetWorkDir(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	s.workDir = dir
	return nil
}

// SetWorktreeInfo 记录本会话 Git 工作树的分支与基线提交（server 在 prepareWorktree
// 成功后调用；子会话从父会话继承）。空 = 未分组会话/没有 worktree。
func (s *Session) SetWorktreeInfo(branch, base string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.worktreeBranch = branch
	s.worktreeBase = base
}

// worktreeInfo 返回本会话的 Git 工作树信息与「是否子会话」标志。子会话由
// approvalSource 非 nil 判定（与权限来源同一判据，不另造一个开关）。
func (s *Session) worktreeInfo() (WorktreeInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return WorktreeInfo{Branch: s.worktreeBranch, Base: s.worktreeBase}, s.approvalSource != nil
}

// SessionID 返回当前会话 id（无存储模式为空串）。
func (s *Session) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// SetOwner 声明本会话的时间线归属（顶层会话 id）。只该在会话开始跑之前调用一次——
// dispatch 开子会话时置位，主会话保持空（自己就是顶层）。
func (s *Session) SetOwner(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownerID = id
}

// OwnerSessionID 返回时间线归属：未设（本会话就是顶层）时回落自身 id。
func (s *Session) OwnerSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ownerID != "" {
		return s.ownerID
	}
	return s.id
}

// Close 关闭会话资源（进程退出/测试清理时调用；幂等）。
// SQLite 版句柄概念消失，连接由 store 持有——这里保留空实现维持
// 宿主的 defer Close() 调用面不变。
func (s *Session) Close() {}
