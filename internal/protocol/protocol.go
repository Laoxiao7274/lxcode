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
)

// Version 是协议版本（hello 握手交换；不兼容变更时递增）。
const Version = "1"

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

	// 会话管理（持久化 + 切换）
	MethodSessionList    = "session.list"
	MethodSessionNew     = "session.new"
	MethodSessionResume  = "session.resume"
	MethodSessionRename  = "session.rename"
	MethodSessionArchive = "session.archive"

	// 项目管理（workspace 分组）
	MethodProjectAdd  = "project.add"
	MethodProjectList = "project.list"

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
	EventModels   = "model.changed"
	EventTodo     = "todo.updated" // 任务清单变更（客户端渲染 TodoList）

	EventSessionChanged = "session.changed"    // 会话切换（new/resume）——客户端须重拉 chat.history
	EventFiles          = "files.changed"      // 一轮的文件改动汇总（产物卡——验收视图）
	EventProjectChanged = "project.changed"    // 项目增删——客户端重拉 project.list
	EventAgentChanged   = "agent.changed"      // Agent 名单变更——客户端重拉 agent.list
	EventCatalogChanged = "catalog.changed"    // 拓展目录变更——客户端重拉对应 kind 的 catalog.*.list
	EventDispatchStart  = "chat.dispatchStart" // 主 Agent 派发子 Agent——客户端渲染 dispatch 卡
	EventDispatchEnd    = "chat.dispatchEnd"   // 子 Agent 执行收尾——dispatch 卡定格带结果
)

// 错误码：JSON-RPC 标准码 + 本应用码。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603

	// 应用错误码
	CodeNoDefaultModel = 1001 // 未绑定 default 角色模型
	CodeModelDisabled  = 1002 // default 模型已停用
	CodeBusy           = 1003 // 会话正在生成中
	CodeNoPending      = 1004 // 没有待确认的工具调用
	CodeAgentNotFound  = 1005 // Agent 不在名单中
	CodeAgentDisabled  = 1006 // Agent 已停用
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
	Text     string `json:"text"`
	Effort   string `json:"effort,omitempty"`   // 推理强度（可选；模型须声明 reasoning 能力才生效）
	Approval string `json:"approval,omitempty"` // 权限模式（可选；空 = Agent 默认/confirm）
	Agent    string `json:"agent,omitempty"`    // 执行 Agent 的名单 id（可选；空 = 主 Agent/旧语境）
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
	ID    string `json:"id"`
	Allow bool   `json:"allow"`
}

// ChatHistoryResult 是客户端接入/重连时的会话同步载荷。
type ChatHistoryResult struct {
	Messages  []llm.Message    `json:"messages"`
	Busy      bool             `json:"busy"`
	Pending   *ConfirmRequest  `json:"pending,omitempty"`
	SessionID string           `json:"session_id,omitempty"` // 当前会话 id（session.changed 后重拉可拿到新值）
	Todos     []tools.TodoItem `json:"todos,omitempty"`      // 任务清单（客户端渲染 TodoList）
}

// 事件载荷。

type DeltaParams struct {
	Kind       string `json:"kind"` // text | reasoning
	Text       string `json:"text"`
	DispatchID string `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 的增量（归属 dispatch 卡）
}

type ToolCallParams struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	DispatchID string `json:"dispatch_id,omitempty"`
}

type ToolResultParams struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
	DispatchID string `json:"dispatch_id,omitempty"`
}

// ConfirmRequest 是需要人工确认的工具调用（确认门）；客户端须回 tool.confirm。
type ConfirmRequest struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	Prompt     string `json:"prompt"`
	DispatchID string `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 的确认（归属 dispatch 卡）
}

type DoneParams struct {
	Message      llm.Message `json:"message"`
	UsageTokens  int         `json:"usage_tokens"`
	FinishReason string      `json:"finish_reason"`
	DispatchID   string      `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 轮完成
}

type ErrorParams struct {
	Message string       `json:"message"`
	Aborted bool         `json:"aborted"`
	Partial *llm.Message `json:"partial,omitempty"` // 中断时已生成的部分内容（客户端应入历史）
}

type BusyParams struct {
	Busy bool `json:"busy"`
}

// TodoUpdatedParams 是 todo.updated 事件的载荷：完整清单（全量替换语义）。
type TodoUpdatedParams struct {
	Items []tools.TodoItem `json:"items"`
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

// SessionMeta 是 session.list 的条目（resume 选择器的数据源）。
type SessionMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`      // 第一条 user 消息截断（空会话为占位）
	UpdatedAt string `json:"updated_at"` // 最后修改时间
	Messages  int    `json:"messages"`
	Archived  bool   `json:"archived"`            // 归档态——侧栏不显示，设置归档区可恢复
	Workspace string `json:"workspace,omitempty"` // 归属项目 id（空 = 未分组）
}

// SessionChangedParams 是 session.changed 事件的载荷：客户端收到后重拉
// chat.history 完成视图同步（多客户端一致性）。
type SessionChangedParams struct {
	ID     string `json:"id"`
	Reason string `json:"reason"` // new | resumed
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
	Files []FileChangeParams `json:"files"`
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

// DispatchStartParams 是 chat.dispatchStart 的载荷（M3——主 Agent 派发
// 子 Agent；前端渲染 dispatch 卡，后续带 dispatch_id 的事件归属进卡）。
type DispatchStartParams struct {
	DispatchID string `json:"dispatch_id"`
	AgentID    string `json:"agent_id"`
	AgentName  string `json:"agent_name"`
	AgentColor string `json:"agent_color"`
	Task       string `json:"task"`
}

// DispatchEndParams 是 chat.dispatchEnd 的载荷（子 Agent 收尾——结果
// 是主 Agent 的验收输入）。
type DispatchEndParams struct {
	DispatchID  string `json:"dispatch_id"`
	Result      string `json:"result"`
	IsError     bool   `json:"is_error"`
	UsageTokens int    `json:"usage_tokens,omitempty"`
}
