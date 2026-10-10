// Package llm 实现与本地推理端点（llama.cpp / vLLM）的对话客户端：
// OpenAI chat completions 与 Anthropic Messages 双 wire 格式，
// 非流式（指数退避重试 + 类型化错误）与 SSE 流式（StreamEvent 通道）。
// 协议收敛依据与设计决策见 docs/01-model-management.md §3.2 / §4。
package llm

import (
	"encoding/json"
	"time"
)

// wire 格式（取值与 modelmgr.ModelConfig.Format 一致）。
const (
	FormatOpenAI    = "openai"
	FormatAnthropic = "anthropic"
)

// StreamEvent.Type 取值。
const (
	EventText      = "text"
	EventReasoning = "reasoning"
	EventToolCall  = "tool_call"
	EventDone      = "done"
	EventError     = "error"
)

// ChatResult.FinishReason 取值（对齐 pi-ai stopReason：aborted 不算 error）。
const (
	FinishStop      = "stop"
	FinishToolCalls = "tool_calls"
	FinishLength    = "length"
	FinishError     = "error"
	FinishAborted   = "aborted"
)

// Config 是客户端配置：只吃原始参数、不依赖 modelmgr——
// agent 循环可脱离注册表直接构造使用。
type Config struct {
	BaseURL string        // 兼容多形态：裸地址 / 带 /v1 / 完整路径（构造时按格式归一化）
	APIKey  string        // 可空（本地端点通常不鉴权）
	Model   string        // API 请求的 model 字段
	Format  string        // "openai"（默认）| "anthropic"
	Timeout time.Duration // 单次请求超时；0 = 300s（本地慢推理，给足）
}

// Message 是统一消息模型：内部以 OpenAI 形态为规范格式，
// anthropic 适配器负责双向转换（system 顶层化 / tool 消息 → tool_result 块 /
// Arguments 字符串 ↔ tool_use.input 对象 / thinking 签名透传）。
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
	// 由 sanitizeMessagesForWire 清掉（见 toolargs.go）。
	Seq int64 `json:"seq,omitempty"`

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
	// base64 只在构造 LLM 请求的瞬间存在（见 images.go 的分层纪律）——
	// 不发库里的 base64、不进长期内存、不进日志。历史回放（chat.history /
	// chat.userMessage）给前端的是同一份引用（前端经 HTTP 附件端点取缩略图）。
	// 空 = 无图（omitempty：老消息 wire 上整键缺席，零影响）。
	Images []ImageRef `json:"images,omitempty"`

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

// Tool 是向模型声明的工具；Parameters 为 JSON Schema 透传（校验在工具执行层）。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ChatResult 是一次调用的结果汇总（流式的 done/error 事件也携带）。
//
// 用量三个字段的分工（DSH token-meter 的四桶口径，本仓库统一在这里归一——
// 适配器只负责"把 provider 报的字段翻成这套语义"，消费方不再各猜一遍）：
//   - PromptTokens：**prompt 侧总量** = 未缓存输入 + 缓存读 + 缓存写。上下文占用
//     （压力）用它——命中缓存的 prompt 在 provider 眼里仍是 prompt，漏掉缓存那两桶
//     会把真实上下文报小一个数量级（anthropic 的 input_tokens 就是不含缓存的）。
//   - UsageTokens：**输出** token 数（生成速度的分母口径）。
//   - Input/CacheRead/CacheWriteTokens：prompt 侧三桶的明细（计费与缓存命中率）。
//
// 全是 0 = provider 没回报用量（未知）——不估算，绝不编数字。
type ChatResult struct {
	Message          Message
	UsageTokens      int
	PromptTokens     int
	InputTokens      int
	CacheReadTokens  int
	CacheWriteTokens int
	FinishReason     string // stop | tool_calls | length | error | aborted
}

// usageBuckets 是解析中途的用量分桶（适配器填好，经 normalized 落到 ChatResult）。
// 单独一个类型是为了让"三处解析点（openai 非流式 / openai 流式 / anthropic 流式与非流式）
// 归一成同一套语义"这件事只有一份实现——各写一遍必然漂移，而漂移的表现是
// 「同一个端点的两条路径给出的用量不一样」。
type usageBuckets struct {
	input      int // 未缓存输入
	cacheRead  int // 命中缓存的输入
	cacheWrite int // 写入缓存的输入
	output     int // 输出
}

// openAIUsage 把 OpenAI 兼容端点的 usage 归一成四桶。
//
// 口径差异（必须在这里抹平）：OpenAI 的 prompt_tokens **包含**缓存命中的部分，
// 而 anthropic 的 input_tokens **不含**缓存——同一个字段名在两家的含义不同。
// 所以 OpenAI 侧要减去缓存：未缓存输入 = prompt − cached（钳到 0，端点偶尔会报
// cached > prompt 的脏数据）。
//
// completion_tokens 缺席（个别端点只报 total_tokens）时按 total − prompt 推，
// 推不出来就是 0（未知）。
func openAIUsage(promptTokens, completionTokens, totalTokens, cachedTokens int) usageBuckets {
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > promptTokens {
		cachedTokens = promptTokens
	}
	output := completionTokens
	if output <= 0 && totalTokens > 0 && totalTokens > promptTokens {
		output = totalTokens - promptTokens
	}
	return usageBuckets{
		input:     promptTokens - cachedTokens,
		cacheRead: cachedTokens,
		output:    output,
	}
}

// anthropicUsage 把 anthropic 的 usage 归一成四桶。
// anthropic 的三个输入字段天然互斥（input_tokens 不含缓存那两项），直接搬运。
func anthropicUsage(inputTokens, outputTokens, cacheRead, cacheWrite int) usageBuckets {
	return usageBuckets{input: inputTokens, cacheRead: cacheRead, cacheWrite: cacheWrite, output: outputTokens}
}

// promptTotal 是 prompt 侧总量（未缓存 + 缓存读 + 缓存写）——上下文压力用它。
func (u usageBuckets) promptTotal() int {
	return u.input + u.cacheRead + u.cacheWrite
}

// applyTo 把分桶落到结果与消息上（**唯一**的落点：四个适配器路径共用，避免
// 「有的路径写了桶、有的没写」这种半截状态）。
func (u usageBuckets) applyTo(res *ChatResult) {
	res.UsageTokens = u.output
	res.PromptTokens = u.promptTotal()
	res.InputTokens = u.input
	res.CacheReadTokens = u.cacheRead
	res.CacheWriteTokens = u.cacheWrite
}

// stampUsage 把结果里的用量簿记盖到消息上（落库与折叠都读消息上的这份）。
// 输出 token 复用 UsageTokens（wire 上就是 usage_tokens），输入侧三桶走 json:"-" 的字段。
//
// 导出是因为落点必须在**适配器之外**（agent 的轮收尾）：各适配器自己盖一份的话，
// 「有的路径盖了、有的没盖」就是半截状态——而会话统计的四桶正是读消息上的这份。
func StampUsage(m *Message, res *ChatResult) {
	m.UsageTokens = res.UsageTokens
	m.InputTokens = res.InputTokens
	m.CacheReadTokens = res.CacheReadTokens
	m.CacheWriteTokens = res.CacheWriteTokens
}

// StreamEvent 是流式事件：text/reasoning 增量、聚合完成的 tool_call、
// done（最终汇总）、error（携带已生成部分内容——本地慢推理下中断不丢字是刚需）。
type StreamEvent struct {
	Type      string
	TextDelta string
	ToolCall  ToolCall
	Result    *ChatResult
	Err       error
	// Replay 为真 = 本事件不是真流式产出，而是**非流式结果的回放**
	// （ChatAuto 在 openai 格式带工具时走的那条路：部分端点流式会丢
	// tool_calls，所以整份结果拿回来再按流事件形态回放）。
	//
	// 消费方（agent 的首 token 计时）据此知道"没有首 token 这个时刻"：
	// 回放的增量与 done 是同一瞬间一起到达的，拿它当首字会报出"首字延迟 ==
	// 整轮耗时"这种假数据。
	Replay bool
}
