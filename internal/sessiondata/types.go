// Package sessiondata 定义会话与存储共享的业务数据，不依赖数据库或传输层。
package sessiondata

// SessionMeta 是会话列表摘要。ParentID/AgentID 非空 = 子会话（派发给子 Agent 开的
// 独立会话）：自己的消息历史与压缩检查点，挂在父会话下；不进侧栏列表，但可被
// 续跑（附着同一个 id 继续）与按父查询。
type SessionMeta struct {
	ID        string
	Title     string
	UpdatedAt string
	Messages  int
	Archived  bool
	Workspace string
	ParentID  string // 父会话 id（空 = 顶层会话）
	AgentID   string // 该会话运行的 Agent（子会话续跑时按同一套四层组合组装）
}

// ContextUsage 是一次上下文测量的**共享形状**：agent 测量、store 落库、server 转 wire
// 三处用同一份定义（agent 侧是类型别名，见 internal/agent/context_usage.go）。
//
// 为什么定义在 sessiondata 而不是 agent：占用要**落库**才能在后端重启后仍然显示
// （用户实测：重启前跑过的会话，打开时指示器是空的），而分层规则禁止 store import agent
// （AGENTS.md §4）。sessiondata 正是这种"两侧共享的业务数据"的归处（SessionMeta 同理）。
//
// Used/Window 是压力判定与 UI 环形的依据（Used 优先取 provider 回报的真实 prompt 总量），
// 五个分类是估算拆分（已按 Used 归一，所以分类之和恒等于 Used）。
type ContextUsage struct {
	Used        int `json:"used"`                   // 已用 token（真实用量优先）
	Window      int `json:"window,omitempty"`       // 模型上下文窗口（0 = 未知）
	System      int `json:"system,omitempty"`       // 系统提示词
	Tools       int `json:"tools,omitempty"`        // 工具声明（wire 上的 JSON Schema）
	ToolResults int `json:"tool_results,omitempty"` // 工具结果
	Messages    int `json:"messages,omitempty"`     // 用户/助手正文与工具调用声明
	Reasoning   int `json:"reasoning,omitempty"`    // 思考链
	// Estimated 为真 = 这个数字是**估算**（按固定密度折算），不是 provider 回报的真实用量。
	// 两种来源都会标：① 本轮端点没回报 usage；② 库里没有真实测量，按已加载的历史回落估算
	//（老会话/重启前的会话）。UI 必须把它和真实用量区分开——用户看不到区别就会拿它做预算判断。
	Estimated bool `json:"estimated,omitempty"`
	// SampledTokens 是**采样基线**：那次测量发生时，历史部分（不含 system 与工具声明）
	// 的估算 token 数。有了它才能算出「测量之后历史又长了多少」并把增量折进展示值
	//（DSH 的 projectedTokens = pressureTokens + surfaceTokens − sampledSurfaceTokens）。
	//
	// 为什么是"历史部分"而不是总量：system 与工具声明每轮现组装、且**测量时未必在手里**
	//（展示路径拿不到 prompt/tools），两边都只算历史才能相减——system 那部分在差值里抵消。
	//
	// 0 = 未知（老库里的测量没有这个字段/纯估算的回落值）→ **不投影**：没有基线就算不出
	// 增量，编一个"大概长了一点"是编数字。
	SampledTokens int `json:"sampled_tokens,omitempty"`
}

// SessionStats 是**整段会话**的统计（对齐 DSH 的 sessionStats + tokenUsage 两个投影）。
//
// 为什么要有它（DSH 的原始理由，逐字适用）：这是"这条会话一共花了多少"的答案，
// 而它必须**不随历史被改写而变**——压缩把一段历史换成摘要、翻页只加载一段窗口，
// 都不该让「跑了多少步、花了多少 token」跟着变。所以折叠的输入是**整段日志**
// （store 里的全部消息行，含被压缩检查点影子掉的那些），而不是当前可见的历史。
// 撤回是唯一的例外：它真的把行删了，统计跟着变小才是对的。
//
// 零值 = 未知/空会话（还没有任何一步）——wire 上整键缺席，前端不渲染统计胶囊
// （DSH 同款：steps == 0 且没有 token 时不渲染，**不显示一排 0**）。
type SessionStats struct {
	// 轮数与步数：turns = 用户发起的轮数（一条用户消息开一轮，与右栏「轮次」面板
	// 同一口径），steps = 模型调用次数（每条 assistant 消息 = 一次调用）。
	Turns int `json:"turns"`
	Steps int `json:"steps"`
	// 墙钟时间（毫秒）：llmMs = 各步「请求发出 → 收尾」之和；toolMs = 工具执行之和
	//（tool 消息的 DurationMs；未执行的调用——拒绝/取消——不计，那是"未知"不是"0ms"）。
	LLMMs  int64 `json:"llm_ms"`
	ToolMs int64 `json:"tool_ms"`
	// 首字延迟：TTFTMs/TTFTSteps = 有首字可测的步数之和与计数（均值 = 两者相除）。
	// 工具轮与非流式回放没有"首字"这个时刻（见 llm.Message.FirstTokenMs）——不计入。
	TTFTMs    int64 `json:"ttft_ms"`
	TTFTSteps int   `json:"ttft_steps"`
	// 解码窗口与输出：DecodeMs = 首字 → 收尾的纯生成耗时之和，DecodeTokens = 同期
	// 的输出 token 之和。生成速度 = DecodeTokens / DecodeMs（扣掉 prefill 才是"吐字速度"，
	// 口径与 internal/agent/timing.go 的单轮 tok/s 一致）。
	DecodeMs     int64 `json:"decode_ms"`
	DecodeTokens int   `json:"decode_tokens"`
	// 计费侧四桶（provider 回报；未回报 = 0）：未缓存输入 / 缓存读 / 缓存写 / 输出。
	// 合计 = 这条会话一共过了多少 token（每一步的 prompt 都算一次，与 DSH 的
	// tokenUsage 投影同口径：它是"累计消耗"，不是"当前占用"）。
	InputTokens      int `json:"input_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	OutputTokens     int `json:"output_tokens"`
	// LegacyTokens 是**早期记录**的 token 之和：单独记账，**不混进上面四桶**。
	//
	// 为什么要有它：本功能上线（2026-09-30）之前，适配器把 provider 的 `total_tokens`
	//（输入+输出）写进了 `messages.usage_tokens` 那一列，而那时**没有**输入侧那三列。
	// 那些行今天的口径是「总量已知、拆分未知」——把它当输出累加会把生成速度报得离谱
	//（实测一条会话显示 687.8 tok/s，真值约 40），当输入累加又缺了输出。所以它们单独
	// 累加在这里，只作如实说明（UI 明细里写明"早期记录的口径是输入+输出"），
	// **不参与任何比值与速度**。
	//
	// 怎么认出来的（写侧启发式）：输入侧三列全 0 而 usage_tokens > 0。新代码写的行不会
	// 这样——provider 不回报 prompt 时 `PromptTokens` 为 0，但**输出**仍然写的是
	// completion_tokens（不会是总量）。代价如实说明：真有端点只报 completion_tokens、不报
	// prompt_tokens 时，它的行会被当成早期记录——少显示，不编数。
	LegacyTokens int `json:"legacy_tokens,omitempty"`
}

// Empty 判断统计是否还是零值（没有任何一步）——调用方据此决定 wire 上整键缺席。
func (s SessionStats) Empty() bool { return s == SessionStats{} }

// ProjectMeta 是注册项目的身份与根目录。
type ProjectMeta struct {
	ID   string
	Name string
	Path string
}

// SearchQuery 是历史检索的查询参数。
type SearchQuery struct {
	Pattern string // 正则（RE2）
	Max     int    // 最多返回条数
	Role    string // 只搜某个角色（user/assistant/tool）；空 = 全部
	Context int    // 每条命中前后各带 N 条相邻消息；0 = 只给命中本身
}

// SearchHit 是历史消息检索结果。
//
// SessionTitle/UpdatedAt 是**给模型的定位信息**：只有会话 id 的话，模型
// 无法回答「这是哪个会话里的事」——标题才是人（与模型）认得的东西。
type SearchHit struct {
	SessionID    string
	SessionTitle string
	UpdatedAt    string
	Index        int
	Role         string
	Content      string
	// Context 是命中前后各 N 条消息（含命中自身，按时间顺序）。
	// 为什么要它：命中那一行常常只说明「问过什么」，「怎么修的」在它后面
	// 几条——只给一行的话模型还得再搜一次才能拼出前因后果。
	Context []SearchLine
}

// SearchLine 是命中上下文里的一条相邻消息。
type SearchLine struct {
	Index   int
	Role    string
	Content string
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
	Tools     []string // 工具白名单（目录 id；主 Agent 恒只含 agent_dispatch——M3 落地，M1 仅存）
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
