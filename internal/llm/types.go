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
}
