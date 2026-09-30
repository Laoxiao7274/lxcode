// Package protocol 定义智能体后端与客户端（CLI / 桌面壳 / 未来 Web UI）之间的
// WebSocket JSON-RPC 2.0 协议：帧结构、方法名、事件名与各方法的参数结构。
// 客户端与服务端共享此包——协议只有一处定义，避免两端漂移。
// 形态移植自 local-myt-agent（同作者的项目），扩展了 todo 事件。
package protocol

import (
	"encoding/json"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
	"github.com/moyunteng/lxcode/internal/websearch"
)

// Version 是协议版本（hello 握手交换；不兼容变更时递增）。
const Version = "2"

// Path 是 WS 端点路径。
const Path = "/rpc"

// 方法名（客户端 → 服务端）。
const (
	MethodHello       = "connection.hello"
	MethodModelList   = "model.list"
	MethodModelAdd    = "model.add"
	MethodModelUpdate = "model.update"
	MethodModelRemove = "model.remove"
	MethodModelEnable = "model.enable"
	MethodRoleSet     = "role.set"
	MethodChatSend    = "chat.send"
	MethodChatCancel  = "chat.cancel"
	MethodChatHistory = "chat.history"
	MethodToolConfirm = "tool.confirm"
	// MethodChatCompact 手动压缩历史（空闲才允许——服务端返回 ErrBusy 映射的
	// 错误码）。参数可带 agent（与 chat.send 同语义：空 = 主 Agent）。
	MethodChatCompact = "chat.compact"
	// MethodChatApproval 中途改本会话的权限档（立刻生效于**运行中的**一轮）。
	// 与 chat.send 的 approval 参数的区别：那个是「这一轮用哪一档」，这个是
	// 「从现在起这个会话用哪一档」——用户的当前意图是会话级实时状态。
	MethodChatApproval = "chat.approval"
	// MethodChatRewind 撤回（rewind）：把 seq 那条消息**及其之后的全部历史**
	// 从会话里删掉（那条消息的正文由客户端自己留在输入框里，用户改完重发）。
	// 空闲才允许——正在跑的一轮手里握着历史快照，抽掉它等于让模型按一份
	// 已经不存在的上下文继续（与 chat.compact 同款纪律，服务端回 ErrBusy）。
	MethodChatRewind = "chat.rewind"

	// 会话管理（持久化 + 切换）
	MethodSessionList            = "session.list"
	MethodSessionNew             = "session.new"
	MethodSessionResume          = "session.resume"
	MethodSessionRename          = "session.rename"
	MethodSessionArchive         = "session.archive"
	MethodSessionWorktreeRelease = "session.worktree.release"

	// 项目管理（workspace 分组）
	MethodProjectAdd  = "project.add"
	MethodProjectList = "project.list"
	// 项目守则（项目根 AGENTS.md）读写：**只按项目 id 寻址**，客户端不传路径——
	// 越权面在结构上为零（固定文件名 + 项目根由服务端解析）。
	MethodProjectInstructionsGet  = "project.instructions.get"
	MethodProjectInstructionsSave = "project.instructions.save"

	// ---- Agent 注册表与拓展目录（M1——docs/backend-roadmap.md）----

	MethodAgentList   = "agent.list"
	MethodAgentAdd    = "agent.add"
	MethodAgentUpdate = "agent.update"
	MethodAgentRemove = "agent.remove"

	MethodCatalogModuleList   = "catalog.modules.list"
	MethodCatalogModuleAdd    = "catalog.modules.add"
	MethodCatalogModuleUpdate = "catalog.modules.update"
	MethodCatalogModuleRemove = "catalog.modules.remove"

	MethodCatalogToolList   = "catalog.tools.list"
	MethodCatalogToolAdd    = "catalog.tools.add"
	MethodCatalogToolUpdate = "catalog.tools.update"
	MethodCatalogToolRemove = "catalog.tools.remove"

	MethodCatalogMcpList   = "catalog.mcp.list"
	MethodCatalogMcpAdd    = "catalog.mcp.add"
	MethodCatalogMcpUpdate = "catalog.mcp.update"
	MethodCatalogMcpRemove = "catalog.mcp.remove"

	// ---- 网页搜索渠道（M4——渠道配置存 config/search.json，见 docs/backend-roadmap.md）----
	//
	// 渠道是**环境配置**不是拓展目录：它描述这台机器能连哪些搜索服务，
	// 与「有哪些工具」是两件事（拍板记录见路线图 M4）。所以方法名用 search.*
	// 而不是 catalog.search.*。
	MethodSearchChannelsList = "search.channels.list"
	MethodSearchChannelSave  = "search.channel.save"
	// MethodSearchChannelRemove 删除渠道配置（回到未配置状态）。
	MethodSearchChannelRemove = "search.channel.remove"
	// MethodSearchPrimarySet 设主渠道（降级链的第一个）。
	MethodSearchPrimarySet = "search.primary.set"
	// MethodSearchTest 只测一个渠道、不降级——用户点「测试」就是想验证这一个，
	// 降级会把「这个渠道坏了」测成「搜索正常」。
	MethodSearchTest = "search.test"

	// ---- 后台任务（jobs——docs/jobs.md §4）----
	//
	// 三个方法 + 两个事件。job.kill 就是**用户点「结束」**：后端走
	// Manager.Kill(id, EndedUser)，与 agent 的 job_kill 工具是同一条路径，
	// 只有 by 不同——两条路径的行为永远一致，不会出现「工具杀不唤醒、
	// 按钮杀唤醒」这种分叉。
	MethodJobList = "job.list"
	MethodJobKill = "job.kill"
	MethodJobLog  = "job.log"
)

// 事件名（服务端 → 全部客户端广播；无 id 的 JSON-RPC 消息）。
const (
	EventReady    = "connection.ready" // 连接建立时单发
	EventUserMsg  = "chat.userMessage" // 用户消息已被后端接受（多客户端同步）
	EventDelta    = "chat.delta"       // 流式增量：kind=text|reasoning
	EventToolCall = "chat.toolCall"    // 模型发起工具调用
	EventToolRslt = "chat.toolResult"  // 工具执行完成（结果已入历史）
	EventConfirm  = "chat.confirmRequest"
	EventDone     = "chat.done"  // 一轮 assistant 消息完成
	EventError    = "chat.error" // 出错或中断（aborted=true 表示用户取消）
	EventBusy     = "chat.busy"  // 忙闲状态变化（多客户端同步）
	// EventApproval 权限档变化（多客户端同步，与 chat.busy 同款）：壳与浏览器
	// 同时开着时两边的档位必须一致，否则用户在一侧改成 auto、另一侧还显示 confirm。
	EventApproval = "chat.approvalChanged"
	EventModels   = "model.changed"
	EventTodo     = "todo.updated" // 任务清单变更（客户端渲染 TodoList）

	EventSessionChanged = "session.changed"    // 会话元数据变化（created/started/renamed/archived/compacted）
	EventFiles          = "files.changed"      // 一轮的文件改动汇总（产物卡——验收视图）
	EventProjectChanged = "project.changed"    // 项目增删——客户端重拉 project.list
	EventAgentChanged   = "agent.changed"      // Agent 名单变更——客户端重拉 agent.list
	EventCatalogChanged = "catalog.changed"    // 拓展目录变更——客户端重拉对应 kind 的 catalog.*.list
	EventDispatchStart  = "chat.dispatchStart" // 主 Agent 派发子 Agent——客户端渲染 dispatch 卡
	EventDispatchEnd    = "chat.dispatchEnd"   // 子 Agent 执行收尾——dispatch 卡定格带结果
	EventCompacted      = "chat.compacted"     // 历史被压缩（前缀替换成摘要检查点）——客户端插标记块
	EventRewound        = "chat.rewound"       // 会话被撤回（seq 及其之后的历史已删除）——客户端截断时间线
	EventSearchChanged  = "search.changed"     // 搜索渠道配置变更——客户端重拉 search.channels.list
	// EventJobStarted / EventJobSettled 是后台任务的状态广播（载荷 = JobInfo）：
	// settled 带 EndedBy，前端据此显示「你停的 / 它挂了 / 超时 / 后端重启中断」。
	EventJobStarted = "job.started"
	EventJobSettled = "job.settled"
)

// 错误码：JSON-RPC 标准码 + 本应用码。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603

	// 应用错误码
	CodeNoDefaultModel  = 1001 // 未绑定 default 角色模型
	CodeModelDisabled   = 1002 // default 模型已停用
	CodeBusy            = 1003 // 会话正在生成中
	CodeNoPending       = 1004 // 没有待确认的工具调用
	CodeAgentNotFound   = 1005 // Agent 不在名单中
	CodeAgentDisabled   = 1006 // Agent 已停用
	CodeVersionMismatch = 1007 // 客户端与服务端协议版本不兼容
)

// Request / Response 是 JSON-RPC 2.0 帧。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 是 JSON-RPC 2.0 帧：请求的应答（带 ID + Result/Error），
// 或服务端事件（带 Method + Params、无 ID —— 即通知）。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// NewEvent 构造事件帧（无 id → 客户端按通知处理）。
func NewEvent(method string, params any) *Response {
	return &Response{JSONRPC: "2.0", Method: method, Params: params}
}

// NewResult / NewError 构造请求应答。
func NewResult(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Result: result}
}

func NewError(id json.RawMessage, code int, message string) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Error: &Error{Code: code, Message: message}}
}

// 以下为各方法的参数 / 结果结构。

type HelloParams struct {
	Client  string `json:"client"`  // 客户端标识：cli / desktop / web / probe
	Version string `json:"version"` // 客户端协议版本
}

type HelloResult struct {
	Server  string `json:"server"`
	Version string `json:"version"`
	Busy    bool   `json:"busy"`
}

// ModelListResult 是注册表快照（模型 + 角色绑定），也是 model.changed 事件载荷。
type ModelListResult struct {
	Models []config.ModelConfig `json:"models"`
	Roles  map[string]string    `json:"roles"`
}

type ModelRemoveParams struct {
	ID string `json:"id"`
}

type ModelEnableParams struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type RoleSetParams struct {
	Role    string `json:"role"`
	ModelID string `json:"model_id"` // 空串 = 解绑
}

// 推理强度档位（chat.send 可选参数；空 = 模型默认）。
// 值域对齐 OpenAI reasoning_effort（minimal/low/medium/high）；Anthropic 侧
// 由 llm 层映射为 thinking budget（见 llm.WithEffort）。
const (
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
)

// 权限模式（chat.send 可选参数；空 = confirm）。
// 工具执行的三档策略——auto 高危自动执行（仅隔离环境）、confirm 低危自动
// + 高危确认（默认，AGENTS.md §3 的现行语义）、strict 只读（变更类工具直接
// 拒绝，错误回填模型）。
const (
	ApprovalAuto    = "auto"
	ApprovalConfirm = "confirm"
	ApprovalStrict  = "strict"
)

type ChatSendParams struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Effort    string `json:"effort,omitempty"`   // 推理强度（可选；模型须声明 reasoning 能力才生效）
	Approval  string `json:"approval,omitempty"` // 权限模式（可选；空 = Agent 默认/confirm）
	Agent     string `json:"agent,omitempty"`    // 执行 Agent 的名单 id（可选；空 = 主 Agent/旧语境）
}

type ChatSessionParams struct {
	SessionID string `json:"session_id"`
}

type SessionResult struct {
	SessionID string `json:"session_id"`
}

// ValidateEffort 校验 effort 值域（空串合法 = 不指定）。
func ValidateEffort(e string) bool {
	switch e {
	case "", EffortMinimal, EffortLow, EffortMedium, EffortHigh:
		return true
	}
	return false
}

// ValidateApproval 校验 approval 值域（空串合法 = 默认 confirm）。
func ValidateApproval(a string) bool {
	switch a {
	case "", ApprovalAuto, ApprovalConfirm, ApprovalStrict:
		return true
	}
	return false
}

type ToolConfirmParams struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id"`
	Allow     bool   `json:"allow"`
}

// ChatHistoryParams 指定要读取的会话；客户端焦点不属于服务端全局状态。
type ChatHistoryParams struct {
	SessionID string `json:"session_id"`
}

// ChatHistoryResult 是指定会话的同步载荷。
type ChatHistoryResult struct {
	Messages  []llm.Message    `json:"messages"`
	Busy      bool             `json:"busy"`
	Pending   *ConfirmRequest  `json:"pending,omitempty"`
	SessionID string           `json:"session_id,omitempty"` // 当前会话 id（session.changed 后重拉可拿到新值）
	Todos     []tools.TodoItem `json:"todos,omitempty"`      // 任务清单（客户端渲染 TodoList）
	Context   *ContextUsage    `json:"context,omitempty"`    // 上下文占用（无 = 未知——刚切会话/后端刚重启）
	// Checkpoints 是压缩检查点在 Messages 里的下标：这些消息要渲染成
	// 「已压缩历史」块，而不是用户气泡（内容是摘要正文，不是用户说的话）。
	Checkpoints []int `json:"checkpoints,omitempty"`
	// Model 是该会话**实际使用**的模型注册表 id（子会话 = 它自己 Agent 的模型：
	// AgentDef.Model 优先，否则 default 角色）。空 = 未知（Agent 没了/没绑模型/
	// 没有 default）——整键缺席，前端显示中性态，**不编一个模型名**。
	// 与 Messages[].model 的区别：那是**每轮**实际用的（历史事实），这是**此刻**
	// 解析出来的（会话还没跑过任何一轮时也有值）。
	Model string `json:"model,omitempty"`
}

// ContextUsage 是上下文占用的 wire 形态（agent.ContextUsage 的映射——内核类型
// 不过协议边界）。used/window 是压力与环形依据（used 优先真实 prompt_tokens），
// 四个分类是估算拆分（已归一：分类之和 == used）。
type ContextUsage struct {
	Used        int `json:"used"`
	Window      int `json:"window,omitempty"`
	System      int `json:"system,omitempty"`
	ToolResults int `json:"tool_results,omitempty"`
	Messages    int `json:"messages,omitempty"`
	Reasoning   int `json:"reasoning,omitempty"`
}

// 事件载荷。

type UserMessageParams struct {
	SessionID string      `json:"session_id"`
	Message   llm.Message `json:"message"`
}

type DeltaParams struct {
	SessionID  string `json:"session_id"`
	Kind       string `json:"kind"` // text | reasoning
	Text       string `json:"text"`
	DispatchID string `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 的增量（归属 dispatch 卡）
}

type ToolCallParams struct {
	SessionID  string `json:"session_id"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	DispatchID string `json:"dispatch_id,omitempty"`
}

type ToolResultParams struct {
	SessionID  string `json:"session_id"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
	DispatchID string `json:"dispatch_id,omitempty"`
}

// ConfirmRequest 是需要人工确认的工具调用（确认门）；客户端须回 tool.confirm。
type ConfirmRequest struct {
	SessionID  string `json:"session_id"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	Prompt     string `json:"prompt"`
	DispatchID string `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 的确认（归属 dispatch 卡）
}

type DoneParams struct {
	SessionID    string      `json:"session_id"`
	Message      llm.Message `json:"message"`
	UsageTokens  int         `json:"usage_tokens"`
	FinishReason string      `json:"finish_reason"`
	DispatchID   string      `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 轮完成
	// Context 是本轮之后的上下文占用（仅主轮携带——子轮的占用不进主指示器）。
	Context *ContextUsage `json:"context,omitempty"`
	// 本轮计时（口径见 llm.Message 的同名字段）。与 message 里那份是**同一组数字**
	//（emit 时从同一条消息取，不另算一遍）：单独列出来只是让 chat.done 自解释——
	// 前端不必从 message 里挖。零值 = 未知（工具轮没有首 token / provider 不回报
	// 用量），wire 上整键缺席。usage_tokens 已在上面（那一版就有）。
	FirstTokenMs int64  `json:"first_token_ms,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	Model        string `json:"model,omitempty"`
}

type ErrorParams struct {
	SessionID string       `json:"session_id"`
	Message   string       `json:"message"`
	Aborted   bool         `json:"aborted"`
	Partial   *llm.Message `json:"partial,omitempty"` // 中断时已生成的部分内容（客户端应入历史）
}

type BusyParams struct {
	SessionID string `json:"session_id"`
	Busy      bool   `json:"busy"`
}

// CompactParams 是 chat.compact 的参数（与 chat.send 同语义：agent 空 = 主 Agent）。
type CompactParams struct {
	SessionID string `json:"session_id"`
	Agent     string `json:"agent,omitempty"`
}

// CompactResult 是 chat.compact 的结果：Compacted=false 表示没有可压的收益
// （历史太短，或摘要并不比被压缩段更小）——不是错误，Reason 说明为什么没压，
// 客户端直接显示给人看。
type CompactResult struct {
	Compacted bool   `json:"compacted"`
	Reason    string `json:"reason,omitempty"`
	Before    int    `json:"before,omitempty"`   // 压缩前上下文占用（估算）
	After     int    `json:"after,omitempty"`    // 压缩后
	Shadowed  int    `json:"shadowed,omitempty"` // 被替换的历史条数
}

// CompactedParams 是 chat.compacted 事件的载荷：宿主据此插一条「已压缩历史」
// 标记块（Summary 供展开查看），并刷新上下文指示器。DispatchID 非空 = 子会话
// 自己的压缩（归属进 dispatch 卡，不进主时间线）。
type CompactedParams struct {
	SessionID  string `json:"session_id"`
	Before     int    `json:"before"`
	After      int    `json:"after"`
	Shadowed   int    `json:"shadowed"`
	Summary    string `json:"summary"`
	Manual     bool   `json:"manual,omitempty"`
	DispatchID string `json:"dispatch_id,omitempty"`
}

// ChatRewindParams 是 chat.rewind 的参数：seq 是要撤回的那条消息的序号
// （= chat.history 的 messages[].seq，也是 chat.userMessage 的 params.message.seq）。
// 前端把自己那条消息的 seq 当锚点发回来——服务端不需要正文（撤回不改内容，
// 那条消息的文本本来就在用户的输入框里）。
type ChatRewindParams struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
}

// ChatRewindResult 是 chat.rewind 的结果：Removed = 从会话历史里删掉的条数
// （那条消息及其之后的全部历史）。重复撤回同一条返回 removed=0——那是幂等空操作，
// 不是错误（客户端重试/两个客户端同时点撤回都不该报错）。
type ChatRewindResult struct {
	Removed int `json:"removed"`
}

// ChatRewoundParams 是 chat.rewound 事件的载荷：宿主据此**截断时间线**
// （seq 之前的保留、seq 及其之后的丢弃）并刷新上下文指示器。
//
// 为什么是广播事件而不是「让客户端收到结果后自己重拉历史」：撤回是一个
// **多客户端可见的状态变更**（壳与浏览器同时开着时两边必须一致），广播让每个
// 客户端都在同一时刻收到同一份事实；而「重拉」要求每个客户端自己知道去拉、
// 并且在与流式增量交错时自己算清该丢哪一段（重拉与增量并发时会闪回旧内容）。
// 载荷带 seq/removed 而不是只给一个"变了"的信号：客户端本地就能精确截断，
// 一次网络往返都不需要。
type ChatRewoundParams struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
	Removed   int    `json:"removed"`
	// Context 是**重算后**的上下文占用，与 chat.done / ChatHistoryResult.context
	// 同一个结构与同一套语义（撤回删了一段历史，撤回前的数字一定是错的——
	// 客户端拿它直接刷新指示器，不必等下一轮，也不必自己按 -removed 猜）。
	// 未知（纯内存模式 / 本会话还没跑过主轮）时整键缺席：发零值等于显示 0%，
	// 那是编出来的假信息（本仓库既有纪律：未知就显示中性态）。
	Context *ContextUsage `json:"context,omitempty"`
}

// TodoUpdatedParams 是 todo.updated 事件的载荷：完整清单（全量替换语义）。
type TodoUpdatedParams struct {
	SessionID string           `json:"session_id"`
	Items     []tools.TodoItem `json:"items"`
}

// ChatApprovalParams 是 chat.approval 的参数（中途改权限档）。
// Approval 空 = 未指定（服务端按 confirm 规范化后回填）。
type ChatApprovalParams struct {
	SessionID string `json:"session_id"`
	Approval  string `json:"approval"` // auto/confirm/strict（空 = 回落 confirm）
}

// ChatApprovalResult 是 chat.approval 的结果：回**规范化后**的档位，客户端据此
// 对齐本地设置（不自己再猜一遍空值该落哪一档）。
type ChatApprovalResult struct {
	Approval string `json:"approval"`
}

// ApprovalChangedParams 是 chat.approvalChanged 事件的载荷（多客户端同步）。
type ApprovalChangedParams struct {
	SessionID string `json:"session_id"`
	Approval  string `json:"approval"`
}

// ---- 会话管理（持久化 + 切换）----

// SessionResumeParams 是 session.resume 的参数。
type SessionResumeParams struct {
	ID string `json:"id"`
}

// SessionNewParams 是 session.new 的可选参数（会话归属项目）。
type SessionNewParams struct {
	Workspace string `json:"workspace,omitempty"` // 归属项目 id（空 = 未分组）
}

// SessionRenameParams 是 session.rename 的参数。
type SessionRenameParams struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SessionArchiveParams 是 session.archive 的参数。
type SessionArchiveParams struct {
	ID       string `json:"id"`
	Archived bool   `json:"archived"`
}

// SessionWorktreeReleaseParams 是 session.worktree.release 的参数。
type SessionWorktreeReleaseParams struct {
	ID string `json:"id"`
}

// SessionWorktreeReleaseResult 表明工作区目录是否已释放。
type SessionWorktreeReleaseResult struct {
	Released bool `json:"released"`
}

// ProjectAddParams 是 project.add 的参数（path 为本地目录绝对路径）。
type ProjectAddParams struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ProjectMeta 是 project.list 的条目。
type ProjectMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// ProjectInstructionsParams 是 project.instructions.get/save 的参数：只给项目 id，
// 文件由服务端定位（项目根 + 固定文件名 AGENTS.md）。
type ProjectInstructionsParams struct {
	ProjectID string `json:"project_id"`
	// Content 只在 save 时使用（get 忽略）。
	Content string `json:"content,omitempty"`
}

// ProjectInstructionsResult 是 project.instructions.get 的结果。
// Exists=false = 该项目还没有守则文件（正常态，不是错误）；Note 非空 = 读取异常说明。
type ProjectInstructionsResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Exists  bool   `json:"exists"`
	Note    string `json:"note,omitempty"`
}

// SessionMeta 是 session.list 的条目（resume 选择器的数据源）。
type SessionMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`      // 第一条 user 消息截断（空会话为占位）
	UpdatedAt string `json:"updated_at"` // 最后修改时间
	Messages  int    `json:"messages"`
	Archived  bool   `json:"archived"`            // 归档态——侧栏不显示，设置归档区可恢复
	Workspace string `json:"workspace,omitempty"` // 归属项目 id（空 = 未分组）
}

// SessionChangedParams 是会话元数据变化通知；它不代表客户端焦点变化，
// 也不要求重载某个会话的生成状态。
type SessionChangedParams struct {
	ID     string `json:"id"`
	Reason string `json:"reason"` // created | started | renamed | archived | compacted | rewound
}

// FileChangeParams 是 files.changed 事件的载荷：一轮的文件改动汇总
// （产物卡——验收视图。前端 FileChange 结构 1:1 对应）。
type FileChangeParams struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Diff    string `json:"diff"`
}

// FilesChangedParams 是 files.changed 事件的载荷（文件改动数组）。
type FilesChangedParams struct {
	SessionID string             `json:"session_id"`
	Files     []FileChangeParams `json:"files"`
}

// ---- Agent 注册表与拓展目录（M1）----
// 协议载荷是 store/sessiondata 类型的 wire 形态（snake_case JSON tag；
// 映射在 server 层——store 类型过协议边界必须经映射，历史 bug 两次踩过）。

// AgentEntry 是 agent.list 的条目（AgentDef 的 wire 形态）。
type AgentEntry struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Desc      string   `json:"desc"`
	Color     string   `json:"color"`
	Model     string   `json:"model"`
	Tools     []string `json:"tools"`
	Workflow  string   `json:"workflow"`
	Skills    []string `json:"skills"`
	Delegates []string `json:"delegates"`
	Approval  string   `json:"approval"`
	Enabled   bool     `json:"enabled"`
	IsMain    bool     `json:"is_main,omitempty"`
	Prompt    string   `json:"prompt"`
	Protocol  string   `json:"protocol"`
	Custom    bool     `json:"custom"`
}

// AgentAddParams 是 agent.add 的载荷（也是 agent.update——全量替换语义）。
type AgentAddParams struct {
	Agent AgentEntry `json:"agent"`
}

// AgentRemoveParams 是 agent.remove 的参数。
type AgentRemoveParams struct {
	ID string `json:"id"`
}

// ModuleEntry 是 catalog.modules.list 的条目。
type ModuleEntry struct {
	ID     string `json:"id"`
	Desc   string `json:"desc"`
	Kind   string `json:"kind"` // process（模板） | skill（技能）
	Body   string `json:"body"`
	Custom bool   `json:"custom"`
}

// ModuleAddParams 是 catalog.modules.add/update 的载荷。
type ModuleAddParams struct {
	Module ModuleEntry `json:"module"`
}

// ModuleRemoveParams 是 catalog.modules.remove 的参数。
type ModuleRemoveParams struct {
	ID string `json:"id"`
}

// ToolParamEntry 是工具参数的 wire 形态。
type ToolParamEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Desc     string `json:"desc,omitempty"`
}

// ToolEntry 是 catalog.tools.list 的条目。
type ToolEntry struct {
	ID          string           `json:"id"`
	Desc        string           `json:"desc"`
	Risk        string           `json:"risk"`   // low | high
	Source      string           `json:"source"` // builtin | binary | mcp
	Params      []ToolParamEntry `json:"params,omitempty"`
	Doc         string           `json:"doc,omitempty"`
	Server      string           `json:"server,omitempty"`  // source=mcp 的来源服务器
	Command     string           `json:"command,omitempty"` // binary：{param} 占位模板
	Example     string           `json:"example,omitempty"`
	PackageFile string           `json:"package_file,omitempty"`
	Custom      bool             `json:"custom"`
}

// ToolAddParams 是 catalog.tools.add/update 的载荷。
type ToolAddParams struct {
	Tool ToolEntry `json:"tool"`
}

// ToolRemoveParams 是 catalog.tools.remove 的参数。
type ToolRemoveParams struct {
	ID string `json:"id"`
}

// McServerEntry 是 catalog.mcp.list 的条目。
//
// Status/ToolCount/LastError/Stderr 是**运行期**信息（由服务端在应答时合成，
// 不是磁盘形状）：界面据此显示「已连接 / 连接失败 + 原因 / 已停止」。
// 新增字段是加法变更，不递增协议 Version。
type McServerEntry struct {
	ID        string            `json:"id"`
	Desc      string            `json:"desc"`
	Transport string            `json:"transport"` // stdio | sse
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Enabled   bool              `json:"enabled"`
	Custom    bool              `json:"custom"`

	// Status：connected | error | stopped（停用或未对账到）。
	Status string `json:"status,omitempty"`
	// ToolCount 是已列举到的工具数（未连接时为 0）。
	ToolCount int `json:"tool_count,omitempty"`
	// LastError 是最近一次连接/列举失败的原因（成功时为空）。
	LastError string `json:"last_error,omitempty"`
	// Stderr 是 stdio 子进程 stderr 的尾部（诊断用，可能为空）。
	Stderr string `json:"stderr,omitempty"`
}

// McServerAddParams 是 catalog.mcp.add/update 的载荷。
type McServerAddParams struct {
	Server McServerEntry `json:"server"`
}

// McServerRemoveParams 是 catalog.mcp.remove 的参数。
type McServerRemoveParams struct {
	ID string `json:"id"`
}

// AgentChangedParams 是 agent.changed 事件的载荷（客户端重拉 agent.list）。
type AgentChangedParams struct {
	Reason string `json:"reason"` // add | update | remove
}

// CatalogChangedParams 是 catalog.changed 事件的载荷：kind 标明哪个目录
// 变了（modules/tools/mcp），客户端只重拉对应列表。
type CatalogChangedParams struct {
	Kind   string `json:"kind"`   // modules | tools | mcp
	Reason string `json:"reason"` // add | update | remove
}

// DispatchStartParams 是 chat.dispatchStart 的载荷。OwnerSessionID 是派发者
// （时间线持有者），SessionID 是子 Agent 的独立会话 id（可续跑、可回放）。
type DispatchStartParams struct {
	OwnerSessionID string `json:"owner_session_id"`
	DispatchID     string `json:"dispatch_id"`
	SessionID      string `json:"session_id,omitempty"` // 子会话 id
	AgentID        string `json:"agent_id"`
	AgentName      string `json:"agent_name"`
	AgentColor     string `json:"agent_color"`
	Task           string `json:"task"`
}

// DispatchEndParams 是 chat.dispatchEnd 的载荷。OwnerSessionID 标明结果归属
// 的父会话；SessionID 标明子会话（主 Agent 可据此续跑）。
type DispatchEndParams struct {
	OwnerSessionID string `json:"owner_session_id"`
	DispatchID     string `json:"dispatch_id"`
	SessionID      string `json:"session_id,omitempty"` // 子会话 id
	Result         string `json:"result"`
	IsError        bool   `json:"is_error"`
	UsageTokens    int    `json:"usage_tokens,omitempty"`
}

// ---- 网页搜索渠道 ----

// SearchChannelsResult 是渠道快照（也是 search.changed 事件的载荷，
// 与 ModelListResult 同款：一次拉全，客户端不需要增量合并）。
//
// Channels 含**全部内置渠道**（含未配置的）——前端要能列出可配置的渠道，
// 只回已配置的话用户永远看不到「还能配什么」。
type SearchChannelsResult struct {
	Channels []websearch.Channel `json:"channels"`
	// Primary 是主渠道 id（空 = 未指定，按预设顺序降级）。
	Primary string `json:"primary,omitempty"`
	// Ready 报告是否存在至少一个就绪渠道——前端据此决定要不要提示去配置。
	Ready bool `json:"ready"`
}

// SearchChannelSaveParams 保存（upsert）一个渠道的配置。
//
// APIKey 为空表示**清空**该渠道的 key（用户主动删 key 的场景）；
// 想「只改 base_url 不动 key」请先把现有 key 回填（前端本来就拿着快照）。
//
// Options 是渠道私有设置（键名见该渠道的 option_specs）；整体覆盖而非增量合并
// ——与 APIKey/BaseURL 同款语义：提交的载荷就是该渠道配置的完整新状态。
type SearchChannelSaveParams struct {
	ID      string            `json:"id"`
	APIKey  string            `json:"api_key"`
	BaseURL string            `json:"base_url"`
	Options map[string]string `json:"options,omitempty"`
	Enabled bool              `json:"enabled"`
}

// SearchChannelRefParams 按 id 定位一个渠道（删除用）。
type SearchChannelRefParams struct {
	ID string `json:"id"`
}

// SearchPrimarySetParams 设主渠道（空 id = 清空，回落预设顺序第一个就绪渠道）。
type SearchPrimarySetParams struct {
	ID string `json:"id"`
}

// SearchTestParams 测试单个渠道（不降级）。
type SearchTestParams struct {
	ID string `json:"id"`
	// Query 测试查询词；空 = 用一个默认词（用户点测试时通常不想先想关键词）。
	Query string `json:"query,omitempty"`
}

// SearchTestResult 是 search.test 的结果：把渠道真实返回的摘要回给前端，
// 让用户当场看到「这个 key 到底能不能用」。
type SearchTestResult struct {
	Provider string             `json:"provider"`
	Answer   string             `json:"answer,omitempty"`
	Results  []websearch.Result `json:"results"`
	// ElapsedMS 是耗时（用户判断渠道快慢的依据）。
	ElapsedMS int `json:"elapsed_ms"`
}

// ---- 后台任务（jobs——docs/jobs.md §4）----
//
// JobNoticePrefix 标注「这条消息是后台任务通告，不是用户说的」。
//
// 唤醒投递往归属会话发一条消息（契约 §5），前端据此把气泡渲染成通告而不是
// 用户气泡——没有这个标记，用户会以为那句话是自己说的。常量放协议包而不是
// agent 包：agent 不 import protocol（分层守卫），服务端组装文本时拼上它。
const JobNoticePrefix = "[后台任务通告] "

// JobInfo 是后台任务的对外快照（job.list 的条目与 job.started/job.settled
// 事件的载荷同款——一份形状，前端只需一套解析）。
//
// 时间字段是 RFC3339 字符串；FinishedAt 为空 = 未结束。
type JobInfo struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	EndedBy   string `json:"ended_by,omitempty"`
	Detail    string `json:"detail,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// OwnerSessionID 是**时间线归属**（顶层会话）：子 Agent 起的任务挂在父会话上，
	// 前端据此把它放进父会话的时间线、后端据此把唤醒通告投给父会话。
	OwnerSessionID string `json:"owner_session_id,omitempty"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at,omitempty"`
	// OutputTail 是最近 64KB 输出（面板直接显示，不必再拉一次 job.log）。
	OutputTail string `json:"output_tail,omitempty"`
	// OutputPath 是落盘日志路径（「查看输出」用它；降级为纯内存时为空）。
	OutputPath string `json:"output_path,omitempty"`
}

// JobListParams 是 job.list 的参数：SessionID 为空 = 全部会话。
//
// 与 job_list 工具的 session_only 同语义：默认列全部——跨会话常驻的
// dev server 在别的会话里也该看得见。
type JobListParams struct {
	SessionID string `json:"session_id,omitempty"`
}

// JobListResult 是任务列表（新的在前）。
type JobListResult struct {
	Jobs []JobInfo `json:"jobs"`
}

// JobKillParams 是 job.kill 的参数。
type JobKillParams struct {
	ID string `json:"id"`
}

// JobKillResult 回取消请求后的快照（状态通常是 stopping——真正的 settle
// 由 producer 收尾时定稿，随后的 job.settled 事件才是终态）。
type JobKillResult struct {
	Job JobInfo `json:"job"`
}

// JobLogParams 是 job.log 的参数（读全量落盘日志）。
type JobLogParams struct {
	ID string `json:"id"`
}

// JobLogResult 是全量日志：超上限时回**尾部**并置 Truncated
// （日志的尾部信息量最大——用户想知道的通常是"它最后报了什么"）。
type JobLogResult struct {
	Data      string `json:"data"`
	Truncated bool   `json:"truncated"`
}
