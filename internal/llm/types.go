// Package llm 实现与本地推理端点（llama.cpp / vLLM）的对话客户端：
// OpenAI chat completions 与 Anthropic Messages 双 wire 格式，
// 非流式（指数退避重试 + 类型化错误）与 SSE 流式（StreamEvent 通道）。
// 协议收敛依据与设计决策见 docs/01-model-management.md §3.2 / §4。
package llm

import (
	"encoding/json"
	"time"

	"github.com/moyunteng/lxcode/internal/chatmsg"
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
	// StreamTools 显式开启「openai 格式流式带工具」（per-model 能力位，注册表
	// models.json 的 capabilities.stream_tools 透传而来）。缺省 false = 维持
	// 现状（openai 带工具走非流式回放）——只有实测确认端点流式 tool_calls
	// 完整的模型才应置 true。
	StreamTools bool
}

// Message 是统一消息模型（**类型别名 = chatmsg.Message**）：消息事实模型住在
// internal/chatmsg（叶包，store/tools/agent/protocol 共用，不必依赖本传输包）；
// 本包保留 wire 客户端与适配逻辑，经别名过渡——既有引用与 JSON 形态零变化。
type Message = chatmsg.Message

// ToolCall 是 assistant 消息携带的工具调用（别名 = chatmsg.ToolCall，理由同上）。
type ToolCall = chatmsg.ToolCall

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
