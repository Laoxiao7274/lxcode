package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// openaiRequest / openaiResponse：OpenAI chat completions wire 格式。
// 只声明用到的字段，端点私有扩展字段靠 json 忽略（宽容解析）。
// Message 本身就是 OpenAI 形态（统一模型的规范格式），经 sanitizeMessagesForWire
// 清簿记后转成 openaiWireMessage 序列化——中间多一层是因为带图 user 消息的
// content 要从 string 变数组（string/数组两种形态共用 any），而无图消息必须
// 逐字节保持旧形态（openaiWireMessage 的字段顺序与 json tag 与 Message 严格一致）。
type openaiRequest struct {
	Model         string              `json:"model"`
	Messages      []openaiWireMessage `json:"messages"`
	Tools         []openaiToolDef     `json:"tools,omitempty"`
	Stream        bool                `json:"stream,omitempty"`
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

// openaiWireMessage 是发给 OpenAI 端点的单条消息。Content 用 any：
// 无图消息持有 string（与旧形态逐字节一致），带图消息持有 content 分片数组
// （{"type":"text"} + {"type":"image_url"}——OpenAI 视觉请求的标准形态）。
// 字段顺序即序列化顺序，必须与 llm.Message 一致（无图消息逐字节不变的保证）。
type openaiWireMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

// openaiContentPart 是带图消息 content 数组里的分片（text / image_url 二选一）。
type openaiContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openaiImageURL `json:"image_url,omitempty"`
}

type openaiImageURL struct {
	URL string `json:"url"`
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
		// 缓存命中的 prompt 部分。OpenAI 官方放在 prompt_tokens_details.cached_tokens，
		// deepseek 另有一个扁平的 prompt_cache_hit_tokens（两家都见过，两个都认）。
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	} `json:"usage"`
}

// cachedPromptTokens 取缓存命中的 prompt 数：优先 OpenAI 官方的嵌套字段，
// 缺席时回落 deepseek 的扁平字段（0 = 端点不报缓存）。
func (u openaiResponse) cachedPromptTokens() int {
	if u.Usage.PromptTokensDetails.CachedTokens > 0 {
		return u.Usage.PromptTokensDetails.CachedTokens
	}
	return u.Usage.PromptCacheHitTokens
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
	// 消息里的工具调用参数过一遍读侧兜底（历史里的坏参数曾让端点 400 拒收整轮）
	req := openaiRequest{Model: model, Messages: openaiWireMessages(sanitizeMessagesForWire(msgs)), Stream: stream}
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
	res := &ChatResult{Message: msg, FinishReason: ch.FinishReason}
	openAIUsage(or.Usage.PromptTokens, or.Usage.CompletionTokens, or.Usage.TotalTokens,
		or.cachedPromptTokens()).applyTo(res)
	return res
}

// openAIStreamResult 由流式聚合状态组装最终结果（流式解析共用）。
func openAIStreamResult(content, reasoning string, toolCalls []ToolCall, finish string, u usageBuckets) *ChatResult {
	msg := Message{Role: "assistant", Content: content, ReasoningContent: reasoning, ToolCalls: toolCalls}
	if msg.Content == "" && reasoning != "" && len(toolCalls) == 0 {
		msg.Content, msg.ReasoningContent = reasoning, ""
	}
	res := &ChatResult{Message: msg, FinishReason: finish}
	u.applyTo(res)
	return res
}

// openaiWireMessages 把清过簿记的消息转成 wire 形态：无图消息原样搬运
// （Content 保持 string，序列化逐字节与旧形态一致）；带图 user 消息的 Content
// 变成 text + image_url 分片数组——base64 在这里（请求构造瞬间）由 loader
// 读文件产出，绝不写回调用方的消息（内存历史恒存引用）。
// 图片读取失败 fail-open：跳过该图，文本尾部追加提示（见 images.go loadImages）。
func openaiWireMessages(msgs []Message) []openaiWireMessage {
	out := make([]openaiWireMessage, len(msgs))
	for i, m := range msgs {
		wm := openaiWireMessage{
			Role:             m.Role,
			Content:          m.Content,
			ReasoningContent: m.ReasoningContent,
			ToolCalls:        m.ToolCalls,
			ToolCallID:       m.ToolCallID,
		}
		// 只有 user 消息带图（assistant/tool 的 Images 恒空——写边界保证，
		// 这里防御性不展开：万一带了也按无图发，不给端点编畸形结构）。
		if m.Role == "user" && len(m.Images) > 0 {
			text := m.Content
			imgs, note := loadImages(m.Images)
			if note != "" {
				text += note
			}
			parts := make([]openaiContentPart, 0, len(imgs)+1)
			parts = append(parts, openaiContentPart{Type: "text", Text: text})
			for _, im := range imgs {
				parts = append(parts, openaiContentPart{
					Type:     "image_url",
					ImageURL: &openaiImageURL{URL: "data:" + im.mime + ";base64," + im.b64},
				})
			}
			wm.Content = parts
		}
		out[i] = wm
	}
	return out
}
