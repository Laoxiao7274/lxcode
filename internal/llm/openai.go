package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// openaiRequest / openaiResponse：OpenAI chat completions wire 格式。
// 只声明用到的字段，端点私有扩展字段靠 json 忽略（宽容解析）。
// Message 本身就是 OpenAI 形态（统一模型的规范格式），直接序列化。
type openaiRequest struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	Tools         []openaiToolDef `json:"tools,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Temperature        *float64 `json:"temperature,omitempty"` // 0 值不传（云端版教训）
	MaxTokens          *int     `json:"max_tokens,omitempty"`
	ReasoningEffort    string   `json:"reasoning_effort,omitempty"` // 思考强度（仅推理模型支持——调用方负责能力门控）
	ChatTemplateKwargs *struct {
		EnableThinking bool `json:"enable_thinking"`
	} `json:"chat_template_kwargs,omitempty"` // vLLM Qwen3 thinking 开关
}

type openaiToolDef struct {
	Type     string `json:"type"` // 恒 "function"
	Function Tool   `json:"function"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			Reasoning        string     `json:"reasoning"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// openaiChat 非流式调用（openai 格式）。
func (c *Client) openaiChat(ctx context.Context, msgs []Message, o requestOpts) (*ChatResult, error) {
	payload := buildOpenAIRequest(c.cfg.Model, msgs, o, false)
	headers := map[string]string{}
	if c.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + c.cfg.APIKey
	}
	status, raw, err := c.post(ctx, payload, headers)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &APIError{StatusCode: status, Body: truncateStr(string(raw), 512)}
	}
	var or openaiResponse
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, fmt.Errorf("解析响应（端点返回的不是 JSON?）: %w", err)
	}
	if len(or.Choices) == 0 {
		return nil, fmt.Errorf("响应没有 choices（模型未生成内容）")
	}
	return openAIResult(&or), nil
}

// buildOpenAIRequest 组装请求体。
func buildOpenAIRequest(model string, msgs []Message, o requestOpts, stream bool) openaiRequest {
	req := openaiRequest{Model: model, Messages: msgs, Stream: stream}
	if stream {
		// 让 vLLM 等在最后一个 chunk 带回 usage
		req.StreamOptions = &struct {
			IncludeUsage bool `json:"include_usage"`
		}{IncludeUsage: true}
	}
	for _, t := range o.tools {
		req.Tools = append(req.Tools, openaiToolDef{Type: "function", Function: t})
	}
	if o.temperature > 0 {
		req.Temperature = &o.temperature
	}
	if o.maxTokens > 0 {
		req.MaxTokens = &o.maxTokens
	}
	if o.thinking != nil {
		req.ChatTemplateKwargs = &struct {
			EnableThinking bool `json:"enable_thinking"`
		}{EnableThinking: *o.thinking}
	}
	if o.effort != "" {
		// canonical 4 档直传；超集值（xhigh/max 等历史遗留）防御性收敛——
		// OpenAI 值域只有 minimal/low/medium/high，传别的直接 400。
		req.ReasoningEffort = o.effort
		if req.ReasoningEffort == "xhigh" || req.ReasoningEffort == "max" {
			req.ReasoningEffort = "high"
		}
	}
	return req
}

// openAIResult 归一响应：reasoning 双字段 + content 空 fallback
// （vLLM thinking 怪癖，云端版 llm.go:436-446 已验证场景）。
func openAIResult(or *openaiResponse) *ChatResult {
	ch := or.Choices[0]
	msg := Message{
		Role:      "assistant",
		Content:   ch.Message.Content,
		ToolCalls: ch.Message.ToolCalls,
	}
	reasoning := ch.Message.ReasoningContent
	if reasoning == "" {
		reasoning = ch.Message.Reasoning
	}
	msg.ReasoningContent = reasoning
	if msg.Content == "" && reasoning != "" && len(msg.ToolCalls) == 0 {
		msg.Content, msg.ReasoningContent = reasoning, ""
	}
	return &ChatResult{
		Message:      msg,
		UsageTokens:  or.Usage.TotalTokens,
		PromptTokens: or.Usage.PromptTokens,
		FinishReason: ch.FinishReason,
	}
}

// openAIStreamResult 由流式聚合状态组装最终结果（流式解析共用）。
func openAIStreamResult(content, reasoning string, toolCalls []ToolCall, finish string, usageTok, promptTok int) *ChatResult {
	msg := Message{Role: "assistant", Content: content, ReasoningContent: reasoning, ToolCalls: toolCalls}
	if msg.Content == "" && reasoning != "" && len(toolCalls) == 0 {
		msg.Content, msg.ReasoningContent = reasoning, ""
	}
	return &ChatResult{Message: msg, UsageTokens: usageTok, PromptTokens: promptTok, FinishReason: finish}
}
