// assistant.* 协议域（助手模式 P0）：助手会话的独立方法域与事件域。
//
// 设计（用户拍板）：payload **复用 chat 既有结构**只换事件名——前端按事件名
// 前缀路由到助手时间线，字段形状与主域完全一致（一份渲染代码两域共用）。
// dispatchAssistant 域处理器放在 dispatchChat 之前（六方法都命中即返回）。
//
// 附件（2026-12）：assistant.send 与 chat.send 一样接受 images/files（同款
// ChatSendImage/ChatSendFile wire 形状与校验）——payload 复用的自然延伸。
package protocol

// assistant 方法名（客户端 → 服务端）。与 chat.* 一一对应：
//
//	assistant.new       = chat.send 的建会话位（新助手会话，space=assistant）
//	assistant.send      = chat.send（含 images/files 附件——2026-12 用户拍板，wire 形状
//	                      与校验/落盘/视觉门全部同款）
//	assistant.history   = chat.history
//	assistant.cancel    = chat.cancel
//	assistant.confirm   = tool.confirm（助手会话的确认裁决）
//	assistant.compact   = chat.compact
//	assistant.approval  = chat.approval（助手会话的执行档位）
//	assistant.model     = chat.model（助手会话的模型选择，省略 BindAgentModel
//	                      分支——助手内置 Agent 不在库名单，模型选择仍是会话级状态）
const (
	MethodAssistantNew      = "assistant.new"
	MethodAssistantSend     = "assistant.send"
	MethodAssistantList     = "assistant.list"
	MethodAssistantHistory  = "assistant.history"
	MethodAssistantCancel   = "assistant.cancel"
	MethodAssistantConfirm  = "assistant.confirm"
	MethodAssistantCompact  = "assistant.compact"
	MethodAssistantApproval = "assistant.approval"
	MethodAssistantModel    = "assistant.model"
	// 助手性格与拓展配置（config/assistant.json——全局配置，不是会话级状态）：
	// get 应答与 set 应答同形状（AssistantConfigResult），set 后广播
	// assistant.configChanged（多客户端同步，与 settings.todoPrompt.changed 同款语义）。
	MethodAssistantConfigGet = "assistant.config.get"
	MethodAssistantConfigSet = "assistant.config.set"
	// 项目模式侧栏「来自助手」小节（2026-12）：列出 run_task 派给某项目的任务
	// 子会话（space='assistant' AND parent_id!='' AND workspace=项目）。
	// 应答与 assistant.list 同构（AssistantListResult + running 位）。
	MethodAssistantTasks = "assistant.tasks"
)

// AssistantTasksParams 是 assistant.tasks 的参数：项目 id 必带（小节按项目
// 分组，没有「全部项目」的语义）。
type AssistantTasksParams struct {
	ProjectID string `json:"project_id"`
}

// AssistantListResult 是 assistant.list 的应答：助手域顶层会话列表。
// wire 形状与 session.list 同源（[]SessionMeta + running 位）——前端一份
// 解析代码两域共用；running 位语义同源（此刻在生成的助手会话）。
type AssistantListResult struct {
	Sessions []SessionMeta `json:"sessions"`
}

// assistant 事件名（服务端 → 全部客户端）。与 chat.* / todo.updated 一一对应
// （见 assistantEventName 的映射表）；dispatchStart/End 带 dispatch_id 归属
// （run_task 的子会话卡，与主域同一份前端归约）。
const (
	EventAssistantUserMsg         = "assistant.userMessage"
	EventAssistantDelta           = "assistant.delta"
	EventAssistantToolCall        = "assistant.toolCall"
	EventAssistantToolRslt        = "assistant.toolResult"
	EventAssistantConfirm         = "assistant.confirmRequest"
	EventAssistantDone            = "assistant.done"
	EventAssistantError           = "assistant.error"
	EventAssistantBusy            = "assistant.busy"
	EventAssistantDispatchStart   = "assistant.dispatchStart"
	EventAssistantDispatchEnd     = "assistant.dispatchEnd"
	EventAssistantCompacted       = "assistant.compacted"
	EventAssistantCompacting      = "assistant.compacting"
	EventAssistantTodo            = "assistant.todoUpdated"
	EventAssistantFilesChanged    = "assistant.filesChanged"
	EventAssistantApprovalChanged = "assistant.approvalChanged"
	EventAssistantModelChanged    = "assistant.modelChanged"
	// 助手配置变更（set 后 / 手改 assistant.json 被热加载检出）——载荷 =
	// AssistantConfigResult（get 应答同形状）。
	EventAssistantConfigChanged = "assistant.configChanged"
)

// AssistantConfigResult 是 assistant.config.get 的结果，也是
// assistant.configChanged 事件的载荷（一份形状——与 settings.todoPrompt 同款）。
// 各字段空 = 未设置（用内置值），不是错误。
type AssistantConfigResult struct {
	Name       string   `json:"name,omitempty"`
	Persona    string   `json:"persona,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Workflow   string   `json:"workflow,omitempty"`
	MCPServers []string `json:"mcpServers,omitempty"`
	// Tools 是旧的自定义命令工具绑定（条目粒度；**仅在 ToolWhitelist 为空时
	// 生效**——双字段正交过渡，见 AssistantConfig.ToolWhitelist 注释）。
	Tools []string `json:"tools,omitempty"`
	// ToolWhitelist 是助手工具白名单的完整覆盖（非空 = 助手的全部静态工具面）；
	// 空 = 用内置默认。get 应答总是同时带回 BuiltinDefaults——UI 预填与
	// 「恢复默认」的事实源是**服务端**，前端不写死内置 14 项（防漂移）。
	ToolWhitelist  []string `json:"toolWhitelist,omitempty"`
	BuiltinDefaults []string `json:"builtinDefaults,omitempty"`
}

// BuiltinAssistantToolIDs 是内置助手工具白名单默认 14 项（wire 事实源——
// assistant.config.get 的 builtinDefaults 恒带回，前端预填/恢复默认用它，
// 不自己写死）。与 internal/server 的 assistantAgentDef.Tools 是同一份清单：
// server 侧的常量是运行时事实源，这里是协议面投影——golden 样本用它设值，
// 两处漂移由 TestGoldenRoundTrip（样本 vs server 常量数组的 wire 快照）先爆。
// 这里不 import internal/server（protocol 是叶子包，server import protocol）。
var BuiltinAssistantToolIDs = []string{
	"read_file", "search", "read_image", "bash_readonly",
	"web_search", "web_fetch", "project_list", "session_search", "todo", "ask_user", "read_skill", "run_task",
	"update_assistant_config", "mcp_manage",
}

// AssistantConfigSetParams 是 assistant.config.set 的参数：**全量替换**——
// 带什么存什么（省略字段 = 清空绑定）；Skills/Workflow 的 id 有效性由服务端
// 现读 modules 表校验，MCPServers 现读 mcp_servers 表校验（validateMCPServerIDs），
// Tools 现读 tools 表校验（validateAssistantToolIDs——只放行 custom binary），
// ToolWhitelist 现读 tools 表校验（**只查存在性**——builtin/binary/custom/mcp
// 都放行，完全可配）。
// 工具 update_assistant_config 的部分更新在工具回调里做「读当前 → 合并 → 写盘」，
// 不经本方法；其 patch 形状**刻意不含 toolWhitelist/mcpServers/tools**（助手不可
// 自改工具白名单与 MCP/自定义工具绑定——防 auto 档自我提权）。
type AssistantConfigSetParams struct {
	Name          string   `json:"name,omitempty"`
	Persona       string   `json:"persona,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	Workflow      string   `json:"workflow,omitempty"`
	MCPServers    []string `json:"mcpServers,omitempty"`
	Tools         []string `json:"tools,omitempty"`
	ToolWhitelist []string `json:"toolWhitelist,omitempty"`
}
