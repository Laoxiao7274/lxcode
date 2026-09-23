// Package agent 实现会话运行时：内存历史 + agent 工具循环 + 确认门 +
// todo 状态。这是内核的核心循环——上层（CLI、桌面壳）通过 Send 发起一轮，
// 经 Emitter 收到类型化事件流（流式增量、工具调用、确认请求……），
// 经 Confirm 裁决高危操作。内核不依赖任何 UI 框架与网络协议。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/jsonrepair"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

const (
	// maxToolRounds 单次用户消息的工具循环轮数上限——防小模型工具死循环。
	maxToolRounds = 16
	// maxToolResultBytes 工具结果进入对话历史的上限（执行层 32KB 全文上限，
	// 历史层再收口——上下文有限）。编程 agent 的轮数上限比设备 agent 放宽
	// （8→16）：真实编码任务里"读→搜→改→验证"链路常态就是十几个来回。
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
	confirm chan bool
	todos   []tools.TodoItem

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
	confirmProxy func(ctx context.Context, req *ConfirmRequest) (allow bool, ok bool)
	// protectHead 为真时压缩不碰历史第 0 条——子会话的任务说明书（派发时那条
	// user 任务消息，子 Agent 的全部依据）。它一旦被压进摘要，续跑/长任务里
	// 子 Agent 就再也看不到原始任务，只能看到一份自己越写越远的转述。
	// 主会话为假（没有这条头部，压缩前缀从 0 开始）；由 dispatch.openChildSession
	// 置位（新建与续跑同一入口）。
	protectHead bool
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

// agentToolsOf 取 Agent 的工具白名单（nil = 无白名单语义——不过滤）。
func agentToolsOf(ac *sessiondata.AgentContext) []string {
	if ac == nil {
		return nil
	}
	return ac.Def.Tools
}

// llmToolsFiltered 按白名单过滤 wire 声明（nil = 全量——旧语境）。
func llmToolsFiltered(toolReg *tools.Registry, allow []string) []llm.Tool {
	all := toolReg.LLMTools()
	if allow == nil {
		return all
	}
	allowed := toolSet(allow)
	out := make([]llm.Tool, 0, len(allow))
	for _, t := range all {
		if allowed[t.Name] {
			out = append(out, t)
		}
	}
	return out
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

// streamWithLLM 默认 LLM 调用：按 default 角色配置建客户端，经 ChatAuto
// （anthropic 永远流式；openai 带工具走非流式回放，见 llm.ChatAuto 注释——
// 该策略来自真机端点实测：部分 openai 兼容端点的流式会丢 tool_calls）。
func streamWithLLM(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
	client, err := llm.New(llm.Config{
		BaseURL: m.BaseURL,
		APIKey:  m.APIKey,
		Model:   m.Model,
		Format:  m.EffectiveFormat(),
	})
	if err != nil {
		return nil, err
	}
	return client.ChatAuto(ctx, msgs, opts...)
}

// SendOpt 是 Send 的请求级选项（变参——现有调用点零改动）。
type SendOpt func(*sendConfig)

type sendConfig struct {
	effort   string // 推理强度（空 = 模型默认）
	approval string // 权限模式（空 = confirm）
	agentID  string // Agent 名单 id（空 = 主语境——无 resolver 时旧语义）
}

// WithEffort 指定本轮推理强度（模型须声明 reasoning 能力才真正生效）。
func WithEffort(e string) SendOpt { return func(c *sendConfig) { c.effort = e } }

// WithApproval 指定本轮工具执行的权限模式（空/未指定 = confirm）。
func WithApproval(a string) SendOpt { return func(c *sendConfig) { c.approval = a } }

// WithAgent 指定本轮的执行 Agent（名单 id；空 = 旧语境——全局默认
// 提示词与 default 角色模型，兼容不接名单的调用方/单测）。
func WithAgent(id string) SendOpt { return func(c *sendConfig) { c.agentID = id } }

// resolveAgent 解析本轮 Agent 载荷。agentID 空 + 无 resolver = nil
// （旧语境）；agentID 空 + 有 resolver = 主 Agent（调度中枢——M3 后
// agent.dispatch 已注册，主语境完整生效：不带 agent 的消息默认走
// 主 Agent，它只派活不亲自执行）；agentID 非空但名单没有 = 哨兵
// 错误。停用的 Agent 不可选用。
func (s *Session) resolveAgent(agentID string) (*sessiondata.AgentContext, error) {
	if s.agents == nil {
		return nil, nil // 无名单语境（旧调用方/单测——全部旧语义）
	}
	if agentID == "" {
		agentID = "main" // 默认主 Agent（名单语境下空 = 主）
	}
	ac, ok := s.agents.Resolve(agentID)
	if !ok {
		// 容错：模型可能把名字当 id 传（提示词已列 id，但弱模型仍会
		// 拿名字填参数）——按名字再解析一次。
		ac, ok = s.agents.ResolveByName(agentID)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s（提示词「可委派名单」里括号前的就是 id）", ErrAgentNotFound, agentID)
	}
	if !ac.Def.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrAgentDisabled, ac.Def.Name)
	}
	return ac, nil
}

// modelFor 按 Agent 绑定取模型（绑定优先，回落 default 角色）；
// 无 Agent 语境走 default 角色（旧语义）。
func (s *Session) modelFor(ac *sessiondata.AgentContext) (config.ModelConfig, error) {
	if ac != nil && ac.Def.Model != "" {
		m, ok := s.reg.Get(ac.Def.Model)
		if !ok {
			return config.ModelConfig{}, fmt.Errorf("Agent 绑定的模型 %s 不存在（编辑该 Agent 换绑或先在设置里添加模型）", ac.Def.Model)
		}
		if !m.Enabled {
			return config.ModelConfig{}, fmt.Errorf("%w: %s（Agent 绑定）", ErrModelDisabled, m.ID)
		}
		return m, nil
	}
	m, err := s.reg.ModelForRole(config.RoleDefault)
	if err != nil {
		return config.ModelConfig{}, fmt.Errorf("%w: %w", ErrNoDefaultModel, err)
	}
	if !m.Enabled {
		return config.ModelConfig{}, fmt.Errorf("%w: %s", ErrModelDisabled, m.ID)
	}
	return m, nil
}

// effectiveApproval 权限取严：请求级 > Agent 默认 > confirm。
// 注意它只解决"这一轮用哪一档"：请求级是用户的显式选择（UI 里选了 auto 就
// 是这一轮不要确认），所以它优先，不在这里取严——取严发生在派发子 Agent 那
// 一层（见 stricterApproval 与 runDispatch）。
func effectiveApproval(requested, agentDefault string) string {
	if requested != "" {
		return requested
	}
	if agentDefault != "" {
		return agentDefault
	}
	return "confirm"
}

// stricterApproval 取两档权限中更严的一档（auto < confirm < strict）——派发子
// Agent 时用父轮的授权面与子 Agent 自己的默认取严：子执行面不大于请求方。
//
// 为什么要单独做这一步：effectiveApproval 是"请求级优先"，而派发时请求级就是
// 父轮的审批（runDispatch 透传），于是子 Agent 自己更严的默认（如调研 Agent 的
// strict、测试 Agent 的 confirm）会被父轮的 auto 放大——代码注释写着"取严"而实现
// 不是，是自相矛盾的。
//
// 空值 = 未声明该档：按"继承请求方"处理（返回另一档），与 effectiveApproval
// 的空值语义一致。
func stricterApproval(requested, agentDefault string) string {
	if agentDefault == "" {
		return requested
	}
	if requested == "" {
		return agentDefault
	}
	if approvalRank(agentDefault) > approvalRank(requested) {
		return agentDefault
	}
	return requested
}

// approvalRank 是权限档位的严格度（越大越严）。未知值按 confirm 同档——与运行期
// 行为一致：只有精确等于 strict 才走"只读拒绝"，其余值都走确认门。
func approvalRank(a string) int {
	switch tools.Approval(a) {
	case tools.ApprovalAuto:
		return 0
	case tools.ApprovalStrict:
		return 2
	default:
		return 1
	}
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

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy = true
	userMsg := llm.Message{Role: "user", Content: text}
	s.history = append(s.history, userMsg)
	s.persistLocked(userMsg)
	// 权限模式随轮携带（请求级 > Agent 默认 > confirm——取严语义）；
	// Agent 载荷与白名单进 runTurn（每轮快照，busy 期间不可变）。
	approval := effectiveApproval(cfg.approval, agentDefaultOf(ac))
	ctx, cancel := context.WithCancel(context.Background())
	ctx = tools.WithApproval(ctx, tools.Approval(approval))
	s.cancel = cancel
	s.mu.Unlock()

	s.emit(UserMsgEvent{Message: userMsg})
	s.emit(BusyEvent{Busy: true})
	go s.runTurn(ctx, cfg, ac)
	return nil
}

// agentDefaultOf 取 Agent 的权限默认（nil 安全）。
func agentDefaultOf(ac *sessiondata.AgentContext) string {
	if ac == nil {
		return ""
	}
	return ac.Def.Approval
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

// SetConfirmProxy 挂确认门代理（子会话 → 父会话；见 confirmProxy 字段注释）。
func (s *Session) SetConfirmProxy(fn func(ctx context.Context, req *ConfirmRequest) (bool, bool)) {
	s.mu.Lock()
	s.confirmProxy = fn
	s.mu.Unlock()
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
		Context: s.context, Checkpoints: checkpointIndexes(msgs),
	}
}

// checkpointIndexes 标出历史里的压缩检查点下标（前端渲染成「已压缩历史」块）。
// 按内容标记识别：历史就是普通 llm.Message，没有类型字段可依赖。
func checkpointIndexes(msgs []llm.Message) []int {
	var out []int
	for i, m := range msgs {
		if isCheckpointContent(m.Content) {
			out = append(out, i)
		}
	}
	return out
}

// Todos 返回当前任务清单（UI 渲染用）。
func (s *Session) Todos() []tools.TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tools.TodoItem(nil), s.todos...)
}

// ContextUsage 返回最近一次主轮请求的上下文占用（零值 = 本会话还没跑过主轮，
// 或刚切过会话——那时真实用量未知，UI 应显示中性态而不是编一个数）。
func (s *Session) ContextUsage() ContextUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.context
}

// recordContextUsage 记录一次主轮请求的占用：res 带 prompt_tokens 就用真实值
// 锚定（分类等比归一），否则保留估算值。
func (s *Session) recordContextUsage(window int, est ContextUsage, res *llm.ChatResult) {
	used := 0
	if res != nil {
		used = res.PromptTokens
	}
	u := est.anchoredTo(used)
	u.Window = window
	s.mu.Lock()
	s.context = u
	s.mu.Unlock()
}

// runTurn 跑完整一轮：流式生成 → 工具调用 → 确认 → 执行 → 回填续轮。
// 文件改动（edit/write_file）被收集，轮末汇总发 FilesChangedEvent（产物视图）。
// ac 是本轮 Agent 载荷（nil = 旧语境——全局默认提示词与 default 模型）。
func (s *Session) runTurn(ctx context.Context, cfg sendConfig, ac *sessiondata.AgentContext) {
	// 快照工作目录并注入工具执行（busy 期间不可变——切换与归属入口
	// 都有 busy 守卫，这是单会话架构下的一致性来源）：项目会话的相对
	// 路径、bash 默认目录、确认门解析基准全部对齐项目根。
	s.mu.Lock()
	workDir := s.workDir
	s.dispatchRoot = ac // 主 Agent 载荷（dispatch 的委派名校验读它）
	s.mu.Unlock()
	ctx = tools.WithWorkDir(ctx, workDir)

	// 本轮技能目录注入 read_skill（渐进披露的取数源）：模型按 id 取
	// 完整正文，只暴露白名单内的——没在 ac.Skills 里的它当没有。
	// 经 ctx 注入（不是注册表）：父会话与子会话各有自己的白名单。
	if ac != nil {
		entries := make([]tools.SkillEntry, 0, len(ac.Skills))
		for i := range ac.Skills {
			entries = append(entries, tools.SkillEntry{ID: ac.Skills[i].ID, Desc: ac.Skills[i].Desc, Body: ac.Skills[i].Body})
		}
		ctx = tools.WithSkillSource(ctx, func(context.Context) []tools.SkillEntry { return entries })
	}
	// 本轮 todo 清单的写回口（清单状态归会话，UI 的 TodoList 数据源）
	ctx = tools.WithTodoSink(ctx, func(items []tools.TodoItem) {
		s.mu.Lock()
		s.todos = items
		s.mu.Unlock()
		s.emit(TodoUpdatedEvent{Items: items})
	})

	var fileChanges []FileChange
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.cancel = nil
		s.pending = nil
		s.confirm = nil
		s.dispatchRoot = nil
		done := s.turnDone
		s.turnDone = nil
		s.mu.Unlock()
		s.emit(BusyEvent{Busy: false})
		if len(fileChanges) > 0 {
			s.emit(FilesChangedEvent{Files: fileChanges})
		}
		// 阻塞式跑一轮（SendWait）的收尾信号——放在最后：宿主等到它就知道
		// 本轮（含事件广播）已经结束。
		if done != nil {
			close(done)
		}
	}()

	overflowRetried := false
	for round := 0; round < maxToolRounds; round++ {
		// 轮与轮之间是压缩的天然时机：上一轮的真实 prompt_tokens 已记录，
		// 超阈值就先压——否则下一轮请求可能直接撞窗口。
		if round > 0 {
			s.maybeCompact(ctx, ac, workDir)
		}
		// 历史快照（锁内取副本）：主轮写回 s.history（s.append）
		s.mu.Lock()
		histSnap := append([]llm.Message(nil), s.history...)
		s.mu.Unlock()
		res, err := s.streamRound(ctx, workDir, cfg.effort, ac, histSnap, "")
		if err != nil {
			// 端点报超长：强制压一次（保留预算归零）再重试同一轮——这是
			// 会话已经长到发不出去时的唯一出路。只重试一次（压不动就报错，
			// 不做无谓的循环）。
			if !overflowRetried && llm.IsContextOverflow(err) {
				overflowRetried = true
				if r, cerr := s.runCompaction(ctx, ac, workDir, 0); cerr == nil {
					log.Printf("端点报上下文超长：已压缩 %d 条历史（%d → %d tokens），重试本轮",
						r.Shadowed, r.Before, r.After)
					s.emit(CompactedEvent{Result: r})
					continue
				} else {
					log.Printf("端点报上下文超长，但压缩失败: %v", cerr)
				}
			}
			aborted := errors.Is(err, context.Canceled) || ctx.Err() != nil
			note := err.Error()
			if aborted {
				note = "已取消（保留已生成部分）"
			}
			// 中断也保留已生成的部分内容：入历史并随事件带给宿主
			var partial *llm.Message
			if res != nil {
				// 半截 JSON 的调用参数（流被打断、输出被截断）绝不能进历史——
				// 一条坏参数会让这个会话之后每次请求都发不出去，见 sanitizeToolCallArgs
				sanitizeToolCallArgs(res.Message.ToolCalls, "流中断保留的部分内容")
				s.append(res.Message)
				// 这条 assistant 消息可能已经声明了工具调用，而流在这里断了——
				// 那些调用永远不会有执行结果。补上配对结果，否则历史里留下没有
				// 回应的喊话：严格端点会 400，压缩的切点也会永久卡在它之前。
				if len(res.Message.ToolCalls) > 0 {
					skipNote := skippedCancelNote
					if !aborted {
						skipNote = skippedFailureNote
					}
					s.sinkSkippedToolResults(s.append, res.Message.ToolCalls, "", skipNote)
				}
				m := res.Message
				partial = &m
			}
			s.emit(TurnErrorEvent{Message: note, Aborted: aborted, Partial: partial})
			return
		}
		// 调用参数可能是半截 JSON（输出被 max_tokens 截断）：先修好再入历史，
		// 落库的参数与随后执行用的参数保持一致（tools 执行前修的是同一份实现）
		sanitizeToolCallArgs(res.Message.ToolCalls, "本轮工具调用")
		s.append(res.Message)
		s.emit(TurnDoneEvent{
			Message: res.Message, UsageTokens: res.UsageTokens, FinishReason: res.FinishReason,
			Context: s.ContextUsage(),
		})
		if len(res.Message.ToolCalls) == 0 {
			return
		}
		if !s.runTools(ctx, res.Message.ToolCalls, &fileChanges, ac, "", s.append) {
			return // 取消
		}
	}
	s.emit(TurnErrorEvent{
		Message: fmt.Sprintf("工具循环达上限（%d 轮），已停止", maxToolRounds),
	})
}

// streamRound 跑一轮流式生成，把增量事件转发给宿主，返回最终结果。
// workDir 是本轮快照的会话工作目录（系统提示词里的工作目录说明用它）。
// effort 是本轮请求的推理强度（模型须声明 reasoning 能力才转成 LLM 选项——
// 对不支持的端点传参会直接 400，能力门控是硬需求）。
// ac 是本轮 Agent 载荷（nil = 旧语境）：模型绑定优先 + 四层组合提示词
// + 工具白名单过滤。
// history 是这轮的消息序列（主轮 = s.history 快照；子轮 = 子上下文的
// 独立历史——隔离的核心）。dispatchID 非空 = 子 Agent 执行（事件带
// 归属标记，前端挂 dispatch 卡）。
func (s *Session) streamRound(ctx context.Context, workDir, effort string, ac *sessiondata.AgentContext, history []llm.Message, dispatchID string) (res *llm.ChatResult, err error) {
	m, err := s.modelFor(ac)
	if err != nil {
		return nil, err
	}
	var opts []llm.Option
	if m.MaxOutputTokens > 0 {
		opts = append(opts, llm.WithMaxTokens(m.MaxOutputTokens))
	}
	if effort != "" && m.Capabilities.Reasoning {
		opts = append(opts, llm.WithEffort(effort))
	}
	// 快照：快照后新消息（若有）不影响本轮请求。提示词按 Agent 四层
	// 组合（nil = 全局默认）；工具 wire 声明按白名单过滤。
	allow := agentToolsOf(ac)
	docs := s.projectDocsFor(workDir)
	var prompt string
	if ac != nil {
		prompt = ComposeSystemPrompt(s.tools, workDir, ac, allow, docs)
	} else {
		prompt = BuildSystemPrompt(s.tools, workDir, docs)
	}
	msgs := append([]llm.Message{{Role: "system", Content: prompt}}, history...)

	wireTools := llmToolsFiltered(s.tools, allow)
	// 本轮请求的上下文占用（估算）：provider 回报了真实用量就以它为准，
	// 估算只用于分类拆分（anchoredTo 归一）与「端点不回报 usage」的回落。
	est := estimateContextUsage(prompt, wireTools, history)
	// 记录统一放出口（done / error / 断流多个 return 点）——子轮的占用不是
	// 主会话的压力（子上下文有自己的窗口与预算），跳过。
	defer func() {
		if dispatchID == "" {
			s.recordContextUsage(m.ContextWindow, est, res)
		}
	}()

	opts = append([]llm.Option{llm.WithTools(wireTools)}, opts...)
	ch, err := s.stream(ctx, m, msgs, opts)
	if err != nil {
		return nil, err
	}
	var lastErr error
	var liveContent, liveReasoning strings.Builder
	var liveTools []llm.ToolCall
	for ev := range ch {
		switch ev.Type {
		case llm.EventText:
			liveContent.WriteString(ev.TextDelta)
			s.emit(DeltaEvent{Kind: "text", Text: ev.TextDelta, DispatchID: dispatchID})
		case llm.EventReasoning:
			liveReasoning.WriteString(ev.TextDelta)
			s.emit(DeltaEvent{Kind: "reasoning", Text: ev.TextDelta, DispatchID: dispatchID})
		case llm.EventToolCall:
			liveTools = append(liveTools, ev.ToolCall)
			tc := ev.ToolCall
			s.emit(ToolCallEvent{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments, DispatchID: dispatchID})
		case llm.EventDone:
			return ev.Result, nil
		case llm.EventError:
			if ev.Err != nil {
				lastErr = ev.Err
			} else {
				lastErr = errors.New("流式错误")
			}
			if ev.Result != nil {
				return ev.Result, lastErr
			}
			return nil, lastErr
		}
	}
	// 流结束无 done 事件：可能是取消（ctx 已断流）——把已生成的部分作为
	// partial 返回给 runTurn 的错误路径（保留部分内容的语义）。
	if liveContent.Len() > 0 || liveReasoning.Len() > 0 || len(liveTools) > 0 {
		partial := &llm.ChatResult{
			Message: llm.Message{
				Role: "assistant", Content: liveContent.String(),
				ReasoningContent: liveReasoning.String(), ToolCalls: liveTools,
			},
		}
		if lastErr == nil {
			lastErr = errors.New("流意外结束（无 done 事件）")
		}
		return partial, lastErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("流意外结束（无 done 事件）")
}

// runTools 执行本轮工具调用，按权限模式门控：
//   - strict 只读：变更类工具（IsMutating）直接拒绝，错误回填模型
//   - auto 完全访问：高危跳过确认门
//   - confirm（默认）：高危先确认（现行语义）
//
// Agent 白名单之外的调用直接拒绝（错误自解释——模型看到的工具清单
// 已按白名单过滤，正常不会越界；这里是防御层）。
// dispatchID 非空 = 子 Agent 执行（事件带归属）；写目标经 sink 抽象
// （主轮 = s.append 进会话历史并落库；子轮 = 写局部历史，隔离）。
// 返回 false 表示被取消。fileChanges 收集文件改动供轮末产物汇总。
func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall, fileChanges *[]FileChange, ac *sessiondata.AgentContext, dispatchID string, sink func(llm.Message)) bool {
	policy := tools.ApprovalFrom(ctx)
	allowed := toolSet(agentToolsOf(ac))
	for i, tc := range calls {
		if ctx.Err() != nil {
			// 取消：本批剩下的调用都不会执行了，但它们的声明已经进了历史——
			// 补上配对结果再走（见 sinkSkippedToolResults）
			s.sinkSkippedToolResults(sink, calls[i:], dispatchID, skippedCancelNote)
			return false
		}
		// 白名单防御：清单外的工具拒绝（ac 非 nil 才有白名单语义）。
		// agent.dispatch 在子语境天然被挡（子 Agent 白名单不含它——
		// 两类制深度恒 1 的运行时保证）。
		if allowed != nil && !allowed[tc.Function.Name] {
			reject := fmt.Sprintf(
				"错误: 工具 %s 不在本 Agent 的白名单内（可用: %s）。如需该能力，请让用户在 Agent 组装里勾选。",
				tc.Function.Name, strings.Join(agentToolsOf(ac), ", "))
			sink(llm.Message{Role: "tool", ToolCallID: tc.ID, Content: reject})
			s.emit(ToolResultEvent{ID: tc.ID, Name: tc.Function.Name, Content: reject, IsError: true, DispatchID: dispatchID})
			continue
		}
		// strict：变更类工具拒绝（不进确认门——只读模式没有"确认放行"语义；
		// 错误信息自解释，模型可换读取类工具或向用户说明）。
		if policy == tools.ApprovalStrict && s.tools.IsMutating(tc.Function.Name) {
			reject := fmt.Sprintf(
				"错误: 当前为只读模式（strict），已禁用 %s。请改用 read_file / search 等读取类工具，或提示用户切换权限模式。",
				tc.Function.Name)
			sink(llm.Message{Role: "tool", ToolCallID: tc.ID, Content: reject})
			s.emit(ToolResultEvent{ID: tc.ID, Name: tc.Function.Name, Content: reject, IsError: true, DispatchID: dispatchID})
			continue
		}
		if prompt := s.tools.Confirm(ctx, tc); prompt != "" && policy != tools.ApprovalAuto {
			req := &ConfirmRequest{
				ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments, Prompt: prompt,
				DispatchID: dispatchID, // 子 Agent 的确认归属（前端挂 dispatch 卡内）
			}
			allow, ok := s.awaitConfirm(ctx, req)
			if !ok {
				// 等待确认期间被取消：本调用与它后面的调用都没执行
				s.sinkSkippedToolResults(sink, calls[i:], dispatchID, skippedCancelNote)
				return false // 取消
			}
			if !allow {
				sink(llm.Message{Role: "tool", ToolCallID: tc.ID,
					Content: "用户拒绝了这次工具调用（未执行）。请改用其他方式完成任务，或向用户说明需要该操作的原因。"})
				s.emit(ToolResultEvent{
					ID: tc.ID, Name: tc.Function.Name, Content: "用户拒绝执行", IsError: true, DispatchID: dispatchID,
				})
				continue
			}
		}
		// agent.dispatch 走内核直连（工具声明的 Exec 是未接线兜底）：
		// 子循环需要 tc.ID 做事件归属 + 委派名单校验，Exec 的入参形状
		//（json.RawMessage）给不了——在调用位展开。
		var result string
		if tc.Function.Name == "agent.dispatch" {
			var p struct {
				Agent   string `json:"agent"`
				Task    string `json:"task"`
				Context string `json:"context"`
				Session string `json:"session"`
			}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &p); err != nil {
				result = fmt.Sprintf("错误: agent.dispatch 参数解析失败: %v", err)
			} else {
				res := s.runDispatch(ctx, tools.DispatchCall{
					DispatchID: tc.ID, Agent: p.Agent, Task: p.Task, Context: p.Context,
					Session: p.Session,
				})
				result = res.Output
				if res.SessionID != "" {
					// 把子会话 id 交给主 Agent：下一轮要接着它跑就填进 session 参数
					result += fmt.Sprintf("\n\n[子会话 id: %s —— 需要接着这次进度继续时，把它填进 session 参数重派]", res.SessionID)
				}
			}
		} else {
			result = s.tools.Execute(ctx, tc)
		}
		collectFileChange(fileChanges, tc, result)
		if r := []rune(result); len(r) > maxToolResultBytes {
			result = string(r[:maxToolResultBytes]) +
				fmt.Sprintf("\n…（结果过长已截断，全文共 %d 字符）", len(r))
		}
		sink(llm.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
		s.emit(ToolResultEvent{ID: tc.ID, Name: tc.Function.Name, Content: result, DispatchID: dispatchID})
	}
	return true
}

// 未执行调用的合成结果文案（取消与流式失败两条路径的口径）。写成常量是为了
// 单测能按语义断言，而不是按某处拼出来的字符串。
const (
	skippedCancelNote  = "已取消，未执行——用户中断了这次生成。需要时请重新发起该调用。"
	skippedFailureNote = "本轮生成失败，该调用未执行。需要时请重新发起。"
)

// sinkSkippedToolResults 给「不会执行」的调用补上合成结果。
//
// 为什么必须补：历史里 assistant 声明的每个 tool_call 都要有配对的 tool 结果。
// 缺配对不只是"不好看"——严格端点会直接 400 拒收整轮（assistant 的 tool_calls
// 必须有配对结果），而且 AnalyzeToolPairing 的游标在缺配对处之后再不平衡，
// 压缩的切点会永久卡在它之前：那个会话从此再也压不动，上下文一路涨到撞窗口。
// 取消（用户点停止）与流式失败两条路径共用这一份补齐逻辑——理由与「工具被拒绝
// 也要回填结果」完全相同（见上面白名单/strict/用户拒绝三处的同款写法）。
func (s *Session) sinkSkippedToolResults(sink func(llm.Message), calls []llm.ToolCall, dispatchID, note string) {
	for _, tc := range calls {
		sink(llm.Message{Role: "tool", ToolCallID: tc.ID, Content: note})
		s.emit(ToolResultEvent{
			ID: tc.ID, Name: tc.Function.Name, Content: note, IsError: true, DispatchID: dispatchID,
		})
	}
}

// sanitizeToolCallArgs 保证写进历史的工具调用参数是合法 JSON。
//
// 为什么必须在写边界做（2026-09-23 线上事故）：模型输出被 max_tokens 截断时，
// arguments 会是半截 JSON（字符串与括号都没闭合）。这种消息一旦落进历史，之后
// **每一次**请求都会在组装阶段被端点适配器拒绝（anthropic 适配器对此是硬校验：
// `工具 X 的 arguments 不是合法 JSON`）——整个会话永久发不出请求，用户只能新开
// 会话。与「取消时补配对」是同一类不变量：历史必须始终良构。
//
// 策略：先保守修复（internal/jsonrepair，与 tools / llm 适配器同一份实现），
// 修不动就降级成 "{}"。**不删调用**——删掉会让它的结果变成孤儿 tool 消息（配对
// 不变量更硬），降级成空参数至少保住结构合法，模型也能看出参数丢了。where 只用于
// 日志（说明是哪条路径修的）。
func sanitizeToolCallArgs(calls []llm.ToolCall, where string) {
	for i := range calls {
		args := calls[i].Function.Arguments
		if strings.TrimSpace(args) == "" || json.Valid([]byte(args)) {
			continue
		}
		next, how := "{}", "降级为空参数"
		if repaired, ok := jsonrepair.Repair(args); ok {
			next, how = repaired, "保守补全"
		}
		calls[i].Function.Arguments = next
		log.Printf("工具调用参数不是合法 JSON，写历史前已%s：%s（%s，原参数 %d 字节）",
			how, calls[i].Function.Name, where, len(args))
	}
}

// collectFileChange 从 edit/write_file 调用提取改动摘要（同文件多次改动
// 逐步合并——产物卡按文件聚合，行数累计，diff 追加最新块）。
func collectFileChange(out *[]FileChange, tc llm.ToolCall, _ string) {
	var path, oldStr, newStr string
	switch tc.Function.Name {
	case "edit":
		var a struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &a) != nil || a.Path == "" {
			return
		}
		path, oldStr, newStr = a.Path, a.OldString, a.NewString
	case "write_file":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &a) != nil || a.Path == "" {
			return
		}
		path, newStr = a.Path, a.Content
	default:
		return
	}
	added, deleted := 0, 0
	if oldStr != "" {
		deleted = len(strings.Split(strings.TrimSuffix(oldStr, "\n"), "\n"))
	}
	if newStr != "" {
		added = len(strings.Split(strings.TrimSuffix(newStr, "\n"), "\n"))
	}
	var diff strings.Builder
	diff.WriteString("@@ " + path + "\n")
	for _, l := range strings.Split(oldStr, "\n") {
		diff.WriteString("-" + l + "\n")
	}
	for _, l := range strings.Split(newStr, "\n") {
		diff.WriteString("+" + l + "\n")
	}
	// 同文件合并：行数累计 + diff 换块
	for i := range *out {
		if (*out)[i].Path == path {
			(*out)[i].Added += added
			(*out)[i].Deleted += deleted
			(*out)[i].Diff += "\n" + diff.String()
			return
		}
	}
	*out = append(*out, FileChange{Path: path, Added: added, Deleted: deleted, Diff: diff.String()})
}

// awaitConfirm 挂起等宿主裁决；取消返回 ok=false。
// 挂了确认门代理（子会话）时交给代理——全应用只有"同时一个挂起确认"，
// 子会话自己持 pending 的话服务端的 tool.confirm 找不到它（会话卡死）。
// 代理路径由代理方发事件（这里不再发，否则确认卡会重复出现）。
func (s *Session) awaitConfirm(ctx context.Context, req *ConfirmRequest) (allow bool, ok bool) {
	s.mu.Lock()
	proxy := s.confirmProxy
	s.mu.Unlock()
	if proxy != nil {
		return proxy(ctx, req)
	}

	s.mu.Lock()
	s.pending = req
	s.confirm = make(chan bool, 1)
	ch := s.confirm
	s.mu.Unlock()

	s.emit(ConfirmRequestEvent{Request: req})
	select {
	case a := <-ch:
		s.mu.Lock()
		s.pending, s.confirm = nil, nil
		s.mu.Unlock()
		return a, true
	case <-ctx.Done():
		s.mu.Lock()
		s.pending, s.confirm = nil, nil
		s.mu.Unlock()
		return false, false
	}
}

// Confirm 是确认门裁决；id 必须匹配当前挂起的调用。
func (s *Session) Confirm(id string, allow bool) error {
	s.mu.Lock()
	pending := s.pending
	ch := s.confirm
	s.mu.Unlock()
	if pending == nil || ch == nil {
		return errors.New("没有待确认的工具调用")
	}
	if pending.ID != id {
		return fmt.Errorf("确认 id 不匹配（当前挂起: %s）", pending.ID)
	}
	select {
	case ch <- allow:
	default: // 已投递过（重复确认）
	}
	return nil
}

func (s *Session) append(m llm.Message) {
	s.mu.Lock()
	s.history = append(s.history, m)
	s.persistLocked(m)
	s.mu.Unlock()
}

// persistLocked 落盘（调用方持锁；store 挂了才做，失败只记日志不回滚内存——
// 内存才是运行真源，磁盘落后最多丢"最后几条"，比让整轮对话因 IO 抖动失败好）。
func (s *Session) persistLocked(m llm.Message) {
	if s.st == nil {
		return
	}
	if s.ensureSessionLocked() == nil {
		if err := s.st.AppendMsg(s.id, m); err != nil {
			log.Printf("会话落盘失败（继续运行）: %v", err)
		}
	}
}

// ensureSessionLocked 懒创建当前会话行（首条消息才建，避免空会话记录）。调用方持锁。
func (s *Session) ensureSessionLocked() error {
	if s.id != "" || s.st == nil {
		return nil
	}
	id, err := s.st.Create()
	if err != nil {
		return err
	}
	s.id = id
	// 归属项目落库（session.new 时预约的）
	if s.pendingWorkspace != "" {
		if err := s.st.SessionWorkspace(id, s.pendingWorkspace); err != nil {
			log.Printf("会话归属落库失败（继续运行）: %v", err)
		}
		s.pendingWorkspace = ""
	}
	// 通知宿主：会话行已建（侧栏「发消息 → 新对话出现」的信号）
	s.emit(SessionStartedEvent{ID: id})
	return nil
}

// resolveWorkspaceDir 区分未分组与失效项目：只有未分组才允许默认目录。
func resolveWorkspaceDir(st Persistence, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if st == nil {
		return "", errors.New("未启用项目存储")
	}
	meta, found, err := st.ProjectByID(id)
	if err != nil {
		return "", fmt.Errorf("查询项目 %s: %w", id, err)
	}
	if !found {
		return "", fmt.Errorf("项目 %s 不存在", id)
	}
	return project.ValidateDirectory(meta.Path)
}

// WorkDir 返回当前会话的工作目录（空 = 后端进程目录）。
func (s *Session) WorkDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workDir
}

// EnablePersistence 挂载磁盘存储并恢复最近会话（启动时调用）。
// 库里没有任何会话时保持空历史（全新开始）。幂等：重复调用是 no-op。
func (s *Session) EnablePersistence(st Persistence) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil {
		return nil
	}
	if st == nil {
		return errors.New("持久化实现为空")
	}
	if err := s.switchGuardLocked(); err != nil {
		return err
	}
	id, msgs, err := st.Latest()
	if err != nil {
		return err
	}
	var dir string
	if id != "" {
		ws, err := st.WorkspaceOf(id)
		if err != nil {
			return err
		}
		dir, err = resolveWorkspaceDir(st, ws)
		if err != nil {
			return err
		}
	}
	// 全部读取和校验成功后才提交，失败可修复存储后重试。
	s.st, s.id, s.history, s.workDir = st, id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	s.context = ContextUsage{} // 占用随会话走：换了历史就得重新测量
	return nil
}

// SessionID 返回当前会话 id（无存储模式为空串）。
func (s *Session) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// AttachTo 把会话挂到指定存储与**既有会话 id**（子会话创建/续跑用）。
// 与 EnablePersistence 的区别：那个走"恢复最近会话"的路径（Latest），这个按 id
// 精确附着——派发开的子会话刚建好行（历史为空）或要续跑（历史在库里）。
// 校验全部成功后才提交，失败保留原状态。
func (s *Session) AttachTo(st Persistence, id string) error {
	if st == nil {
		return errors.New("持久化实现为空")
	}
	if id == "" {
		return errors.New("会话 id 不能为空")
	}
	msgs, err := st.Load(id) // 会话不存在时报错
	if err != nil {
		return err
	}
	ws, err := st.WorkspaceOf(id)
	if err != nil {
		return err
	}
	dir, err := resolveWorkspaceDir(st, ws)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return fmt.Errorf("%w（不能附着到别的会话）", ErrBusy)
	}
	s.st, s.id, s.history, s.workDir = st, id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	s.context = ContextUsage{} // 占用随会话走：换了历史就得重新测量
	return nil
}

// Close 关闭会话资源（进程退出/测试清理时调用；幂等）。
// SQLite 版句柄概念消失，连接由 store 持有——这里保留空实现维持
// 宿主的 defer Close() 调用面不变。
func (s *Session) Close() {}

// SwitchNew 结束当前会话（记录保留，可后续 resume），从空历史开始。
// 生成中/有挂起确认时拒绝——切换会撕裂运行中的工具循环。
func (s *Session) SwitchNew(workspace string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.switchGuardLocked(); err != nil {
		return "", err
	}
	dir, err := resolveWorkspaceDir(s.st, workspace)
	if err != nil {
		return "", err
	}
	// 历史、todo 和项目归属必须在同一锁内提交，Send 不能插入两步之间。
	s.id, s.history, s.todos = "", nil, nil
	s.pendingWorkspace, s.workDir = workspace, dir
	s.context = ContextUsage{} // 新会话：占用清零（下一轮重新测量）
	return "", nil
}

// SwitchTo 恢复指定会话：加载其历史并继续追加。当前会话记录保留。
func (s *Session) SwitchTo(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st == nil {
		return errors.New("未启用会话存储")
	}
	if err := s.switchGuardLocked(); err != nil {
		return err
	}
	msgs, err := s.st.Load(id)
	if err != nil {
		return err
	}
	ws, err := s.st.WorkspaceOf(id)
	if err != nil {
		return err
	}
	dir, err := resolveWorkspaceDir(s.st, ws)
	if err != nil {
		return err
	}
	// 校验完成再提交；todo 尚未持久化，不能沿用上一会话的内存清单。
	s.id, s.history, s.workDir = id, msgs, dir
	s.todos, s.pendingWorkspace = nil, ""
	s.context = ContextUsage{} // 换会话：占用重新测量（沿用旧值会误导压力判定）
	return nil
}

// SessionList 列出全部持久会话（无存储模式返回空）。
func (s *Session) SessionList() []sessiondata.SessionMeta {
	if s.st == nil {
		return nil
	}
	metas, err := s.st.List()
	if err != nil {
		log.Printf("列出会话失败: %v", err)
		return nil
	}
	return metas
}

// AttachSessionSearch 把会话搜索接进工具注册表（EnablePersistence 后调用）：
// JSONL 格式归 store 包所有，工具层只拿函数——格式单源不漂移。
func (s *Session) AttachSessionSearch() {
	s.tools.SetSessionSearch(func(ctx context.Context, pattern string, max int) (string, error) {
		if s.st == nil {
			return "", errors.New("会话存储未启用，无法搜索历史")
		}
		hits, err := s.st.Search(pattern, max)
		if err != nil {
			return "", err
		}
		// Search 的 max 已被钳制；total 用同值（Search 不超发）
		return sessiondata.FormatSearchHits(hits, len(hits)), nil
	})
}

// switchGuardLocked 是切换会话的前置检查（调用方持锁）：
// busy → ErrBusy；挂起确认 → ErrPendingConfirm。
func (s *Session) switchGuardLocked() error {
	if s.busy {
		return fmt.Errorf("%w（先取消当前生成）", ErrBusy)
	}
	if s.pending != nil {
		return ErrPendingConfirm
	}
	return nil
}
