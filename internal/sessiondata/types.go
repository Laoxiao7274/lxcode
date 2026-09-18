// Package sessiondata 定义会话与存储共享的业务数据，不依赖数据库或传输层。
package sessiondata

// SessionMeta 是会话列表摘要。
type SessionMeta struct {
	ID        string
	Title     string
	UpdatedAt string
	Messages  int
	Archived  bool
	Workspace string
}

// ProjectMeta 是注册项目的身份与根目录。
type ProjectMeta struct {
	ID   string
	Name string
	Path string
}

// SearchHit 是历史消息检索结果。
type SearchHit struct {
	SessionID string
	Index     int
	Role      string
	Content   string
}

// ---- 可组装 Agent 域（M1 注册表与目录——类型对齐前端 agent-types.ts，
// 后端为事实源；M2 上下文组装与 M3 dispatch 消费这些结构） ----

// AgentDef 是组装出的 Agent 名单条目（两类制：主 = 唯一调度者不可被
// 委派，子 = 纯执行者不可委派——委派深度恒 1）。
type AgentDef struct {
	ID        string
	Name      string
	Desc      string
	Color     string   // 标识色（名单卡与选择器圆点）
	Model     string   // 绑定模型（注册表 id；空 = 未绑定回落 default 角色）
	Tools     []string // 工具白名单（目录 id；主 Agent 恒只含 agent.dispatch——M3 落地，M1 仅存）
	Workflow  string   // 流程模块 id（单选；空 = 无）
	Skills    []string // 技能模块 id（多选注入）
	Delegates []string // 主 Agent 的默认委派名单（子 Agent 恒空）
	Approval  string   // 权限默认档（auto/confirm/strict；会话级请求可覆盖）
	Enabled   bool
	IsMain    bool   // 主 Agent 唯一（删除被拒）
	Prompt    string // 自定义上下文段（四层组合的第三层）
	Protocol  string // 定制协议（空 = 内置默认——第一层）
	Custom    bool   // 用户自建（可删）；主 Agent 是结构成员（custom=0）
}

// ModuleSpec 是上下文模块目录条目（模板/技能——纯 markdown 内容，
// 模板单选注入、技能多选注入；不授予工具权限）。
type ModuleSpec struct {
	ID     string
	Desc   string
	Kind   string // process（模板） | skill（技能）
	Body   string // markdown 正文（实际注入 Agent 上下文的内容）
	Custom bool   // 用户自建可删；内置种子只读
}

// ToolParam 是自定义工具的参数描述（模型提示词与详情层展示用）。
type ToolParam struct {
	Name     string
	Type     string
	Required bool
	Desc     string
}

// ToolSpec 是工具目录条目（内置与第三方统一形状；M1 只存元数据，
// 自定义工具的执行面在 M4 落地）。
type ToolSpec struct {
	ID          string
	Desc        string
	Risk        string // low | high
	Source      string // builtin | binary | mcp
	Params      []ToolParam
	Doc         string
	Server      string // source=mcp 时的来源服务器 id
	Command     string // binary：运行命令（{param} 占位模板）
	Example     string // 命令示例
	PackageFile string // 程序包文件名（声明）
	Custom      bool   // 用户自建可删；内置种子只读
}

// McServerSpec 是 MCP 服务器（接入单元）：stdio = command+args+env
// 进程直起 / sse = url 端点（建模对齐 mcpServers 事实标准）。停用 =
// 能力挂起（工具保留目录条目）。
type McServerSpec struct {
	ID        string
	Desc      string
	Transport string // stdio | sse
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Enabled   bool
	Custom    bool
}

// AgentContext 是一次对话的 Agent 装配载荷（M2 上下文组装的输入）：
// 四层组合所需的全部数据由消费方（server 从 store）解析好传入——
// agent 内核不 import store（分层规则）。
type AgentContext struct {
	Def      AgentDef     // 名单条目（含模型绑定/工具白名单/审批默认/提示词）
	Workflow *ModuleSpec  // 流程模块（单选；nil = 无）
	Skills   []ModuleSpec // 技能模块（多选注入）
	// Delegates 是有效委派名单（主 Agent 的动态注入层：会话覆盖 ?? 默认
	// ∩ 启用——取严逻辑在服务端解析，内核只拿结果）。
	Delegates []AgentDef
}
