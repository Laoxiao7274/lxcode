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

	// 以下四个字段是**每轮生成的簿记**（计时 + 用量 + 模型），与 Seq 同一性质与
	// 同一条纪律：随消息一起流动（chat.done 实时 / chat.history 回放 / 重启后
	// Load 三条路径必须给出同一份数字——各算一遍必然漂移），并在
	// sanitizeMessagesForWire 里清掉（模型不该看见，严格网关多一个未知字段就
	// 400 拒收整轮，见 AGENTS.md §5 坑 13）。
	//
	// 零值一律表示**未知**，wire 上整键缺席（omitempty），前端显示中性态：
	//   - FirstTokenMs：首 token 延迟（请求发出 → 第一个文字/思考增量到达）。
	//     0 = 本轮没有增量（工具轮/空回），或这轮是**非流式回放**（openai 带工具
	//     时 ChatAuto 走回放，根本没有"首字"这个时刻）——不填 0 冒充"0ms 首字"。
	//   - DurationMs：本轮从请求发出到收尾的总耗时。
	//   - Model：本轮实际使用的模型注册表 id（Agent 绑定优先、否则 default 角色）。
	//   - UsageTokens：provider 回报的输出 token 数；拿不到就是 0（缺席），
	//     绝不用估算值冒充——估算的 tok/s 是编数据。
	FirstTokenMs int64  `json:"first_token_ms,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	Model        string `json:"model,omitempty"`
	UsageTokens  int    `json:"usage_tokens,omitempty"`
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
type ChatResult struct {
	Message      Message
	UsageTokens  int
	PromptTokens int
	FinishReason string // stop | tool_calls | length | error | aborted
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
