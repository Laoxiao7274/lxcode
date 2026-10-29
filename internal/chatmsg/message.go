package chatmsg

// Message 是统一消息模型：内部以 OpenAI 形态为规范格式，
// anthropic 适配器负责双向转换（system 顶层化 / tool 消息 → tool_result 块 /
// Arguments 字符串 ↔ tool_use.input 对象 / thinking 签名透传）。
// （从 internal/llm 搬入：它是全系统的消息事实模型，不只是 LLM wire 载荷。）
type Message struct {
	Role               string     `json:"role"` // system | user | assistant | tool
	Content            string     `json:"content"`
	ReasoningContent   string     `json:"reasoning_content,omitempty"` // thinking / reasoning 思考链
	ReasoningSignature string     `json:"-"`                           // anthropic thinking 块签名，透传不解释
	ToolCalls          []ToolCall `json:"tool_calls,omitempty"`        // assistant 发起
	ToolCallID         string     `json:"tool_call_id,omitempty"`      // tool 消息回填对应关系
	// Seq 是这条消息在会话里的**序号**（store 分配的 messages.seq，从 1 起；
	// 0 = 还没有落库：模型刚产出、或纯内存模式）。它随消息一起流动，因为
	// 「当前上下文」有三条路径要给前端同一份历史（chat.history 回放、
	// chat.userMessage 实时、重启后 Load），序号必须由同一个持有者（store）
	// 写在消息上——各算一遍必然漂移，而前端拿它当撤回锚点。
	//
	// 注意它是**我们自己的簿记**，不是模型该看见的字段：组装 provider 请求时
	// 由 sanitizeMessagesForWire 清掉（见 llm/toolargs.go）。
	Seq int64 `json:"seq,omitempty"`

	// ParentSeq 是这条消息在**会话树**里的父节点（messages.parent_seq，0 = root）。
	// 与 sessions.parent_id 的分工：那个是**会话级**父子（子 Agent = 独立会话），
	// 这个是**消息级**父指针（会话树的一条边，见 docs/session-tree.md §E.1）。
	//
	// 批次 2 起**上 wire**（omitempty：root 的 0 整键缺席）：前端按它建树/画分支，
	// 与 chat.tree 的 parent_seq 同一口径。它是**簿记字段**，模型不需要——组装
	// provider 请求时由 sanitizeMessagesForWire 清掉（与 Seq 同一批）。
	ParentSeq int64 `json:"parent_seq,omitempty"`

	// 以下字段是**每轮生成的簿记**（计时 + 用量 + 模型），与 Seq 同一性质与
	// 同一条纪律：随消息一起流动（chat.done 实时 / chat.history 回放 / 重启后
	// Load 三条路径必须给出同一份数字——各算一遍必然漂移），并在
	// sanitizeMessagesForWire 里清掉（模型不该看见，严格网关多一个未知字段就
	// 400 拒收整轮，见 AGENTS.md §5 坑 13）。
	//
	// 零值一律表示**未知**，wire 上整键缺席（omitempty），前端显示中性态：
	//   - FirstTokenMs：首 token 延迟（请求发出 → 第一个文字/思考增量到达）。
	//     0 = 本轮没有增量（工具轮/空回），或这轮是**非流式回放**（openai 带工具
	//     时 ChatAuto 走回放，根本没有"首字"这个时刻）——不填 0 冒充"0ms 首字"。
	//   - DurationMs：本轮从请求发出到收尾的总耗时。**tool 角色消息上它是工具执行
	//     耗时**（会话统计的「工具时间」按它折叠，口径见 sessiondata.SessionStats）。
	//   - Model：本轮实际使用的模型注册表 id（Agent 绑定优先、否则 default 角色）。
	//   - UsageTokens：provider 回报的**输出** token 数（DSH 的 outputTokens 同款：
	//     生成速度的分母口径）。拿不到就是 0（缺席），绝不用估算值冒充——估算的
	//     tok/s 是编数据。注意它**不含**输入侧：输入按下面三个桶分开记（缓存命中率
	//     与计费口径都靠它们，见 sessiondata.SessionStats）。
	FirstTokenMs int64  `json:"first_token_ms,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	Model        string `json:"model,omitempty"`
	UsageTokens  int    `json:"usage_tokens,omitempty"`

	// 输入侧用量分桶（provider 回报；未回报 = 0 = 未知）。json:"-"：它们是**落库
	// 簿记**（会话统计的折叠输入），不是模型该看的字段，也不是每条消息都要发给
	// 前端的载荷——会话统计由后端折叠好整份给前端，前端不必逐条重算。
	// 与 ReasoningSignature 同一条处理（有自己的库列，但不上 wire）。
	//
	// 语义与 DSH token-meter 的四桶一致（prompt 侧三桶 + 输出一桶）：
	//   - InputTokens：**未缓存**输入；
	//   - CacheReadTokens：命中缓存的输入（anthropic 的 cache_read / openai 的
	//     prompt_tokens_details.cached_tokens / deepseek 的 prompt_cache_hit_tokens）；
	//   - CacheWriteTokens：写入缓存的输入（anthropic 的 cache_creation；openai
	//     兼容端点不报，恒 0）。
	InputTokens      int `json:"-"`
	CacheReadTokens  int `json:"-"`
	CacheWriteTokens int `json:"-"`

	// Images 是这条 user 消息携带的图片**引用**（视觉请求）：恒为文件引用，
	// base64 只在构造 LLM 请求的瞬间存在（分层纪律）——
	// 不发库里的 base64、不进长期内存、不进日志。历史回放（chat.history /
	// chat.userMessage）给前端的是同一份引用（前端经 HTTP 附件端点取缩略图）。
	// 空 = 无图（omitempty：老消息 wire 上整键缺席，零影响）。
	Images []ImageRef `json:"images,omitempty"`

	// Attachments 是这条消息携带的附件**引用**（展示通道，第一版不进模型上下文）：
	// 用户发的图/文件（chat.send 落盘后的元数据）与 share_file 工具共享的文件。
	// 与 Images 的分工：Images 服务视觉请求（模型要看到图），Attachments 服务
	// **前端渲染**（缩略图卡 / 文件卡）——同一条消息可以两者都有（用户发的图既进
	// 视觉通道也渲染附件卡）。恒为引用 + 元数据（名字/大小），绝不带 base64；
	// 随消息落库与回放（chat.history / chat.userMessage / chat.toolResult 三条
	// wire 同一份形状），前端经 HTTP 附件端点取内容。
	// 空 = 无附件（omitempty：老消息 wire 上整键缺席，零影响）。
	Attachments []AttachmentRef `json:"attachments,omitempty"`

	// Notice 为真 = 这条 user 消息是**注入的提示条**（重复调用提醒 / 后台任务通告），
	// 不是用户说的话。前端按文本前缀渲染成提示条（见 agent.RepeatNoticePrefix），
	// 后端这个位是给**会话统计**用的：轮数只数真实用户消息。
	// 为什么要一个显式的位而不是在后端也做前缀匹配：两个前缀常量分别在 agent 与
	// protocol 包里，而折叠统计的 store 谁都不能 import（分层规则，AGENTS.md §4）——
	// 判定记在写边界（注入那两处），读侧就不必猜。
	Notice bool `json:"-"`
}

// ToolCall 是 assistant 消息携带的工具调用（OpenAI function calling 形态）。
// Arguments 是 JSON 字符串，执行前自行解析；Index 仅流式聚合对齐用。
type ToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Index    int    `json:"index,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
