package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// Anthropic Messages 适配：消息模型双向转换 + wire 格式。
// 关键差异（docs/01-model-management.md §4.3 约定）：
//   - system 消息 → 顶层 system 字段（Anthropic 无 system role）
//   - role=tool 消息 → user 消息里的 tool_result 块；**连续的 tool 结果合并进
//     同一条 user 消息**（一条 assistant 的全部 tool_use 必须在紧接着的下一条
//     消息里拿到结果，否则 400 拒收整轮——并行工具调用的轮次必踩）
//   - ToolCalls.Function.Arguments 字符串 ↔ tool_use.input 对象
//   - thinking 块签名原样透传（换模型即失效，不解释）
//   - max_tokens 必填：未配置时用保守默认

const (
	anthropicVersion          = "2023-06-01"
	anthropicDefaultMaxTokens = 4096
)

// effortBudget 档位 → thinking budget（token）。API 硬约束：≥1024 且
// < max_tokens；budget 抬高时 max_tokens 不足会自动补（见 convertToAnthropic）。
var effortBudget = map[string]int{
	"minimal": 1024,
	"low":     4096,
	"medium":  16384,
	"high":    32768,
}

// anthropicRequest 是 /v1/messages 请求体。
type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicToolDef `json:"tools,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	Thinking    *struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	} `json:"thinking,omitempty"` // 推理强度档位映射的思考预算（仅推理模型——调用方负责能力门控）
}

type anthropicMessage struct {
	Role    string                  `json:"role"` // user | assistant
	Content []anthropicContentBlock `json:"content"`
}

// anthropicContentBlock 是 content 数组里的块（text/thinking/tool_use/tool_result）。
// 不同块的专用字段靠 omitempty 共存。
type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// thinking 块
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// tool_use 块
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result 块
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicResponse 是 /v1/messages 非流式响应。
type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// anthropicChat 非流式调用（anthropic 格式）。
func (c *Client) anthropicChat(ctx context.Context, msgs []Message, o requestOpts) (*ChatResult, error) {
	payload, err := convertToAnthropic(c.cfg.Model, msgs, o, false)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"anthropic-version": anthropicVersion}
	if c.cfg.APIKey != "" {
		headers["x-api-key"] = c.cfg.APIKey
	}
	status, raw, err := c.post(ctx, payload, headers)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &APIError{StatusCode: status, Body: truncateStr(string(raw), 512)}
	}
	var ar anthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("解析响应: %w", err)
	}
	return anthropicResult(&ar), nil
}

// convertToAnthropic 把统一消息模型转为 Anthropic 请求形态。
//
// 两条 wire 硬约束（都由实测 400 换来的）：
//   - **一条 assistant 里的全部 tool_use 必须在紧接着的下一条消息里拿到对应
//     tool_result**——所以连续的 tool 结果要合并成一条 user 消息（多条独立消息
//     会被拒收整轮：`messages.N: tool_use ids were found without tool_result
//     blocks immediately after`）。并行工具调用（主 Agent 一次派两个子任务）
//     的轮次必踩这条，2026-09-23 实测；
//   - 配对缺失时补齐合成结果（见 backfillToolResults）——历史里缺一条结果的
//     畸形轮次曾经让整个会话再也发不出请求，读侧兜底比让会话变砖好。
func convertToAnthropic(model string, msgs []Message, o requestOpts, stream bool) (*anthropicRequest, error) {
	req := &anthropicRequest{Model: model, MaxTokens: anthropicDefaultMaxTokens, Stream: stream}
	if o.maxTokens > 0 {
		req.MaxTokens = o.maxTokens
	}
	if o.temperature > 0 {
		req.Temperature = &o.temperature
	}
	if o.effort != "" {
		// thinking 开启时 API 硬约束 budget < max_tokens：预算盖过输出上限时
		// 抬高 max_tokens（给正文留 4096）。模型真实上限不足会以 400 显式报错，
		// 不静默截断档位。
		budget := effortBudget[o.effort]
		if budget == 0 {
			budget = effortBudget["medium"] // 未知档位（防御）回落中档
		}
		if req.MaxTokens <= budget {
			req.MaxTokens = budget + 4096
		}
		req.Thinking = &struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		}{Type: "enabled", BudgetTokens: budget}
	}
	for _, t := range o.tools {
		req.Tools = append(req.Tools, anthropicToolDef{
			Name: t.Name, Description: t.Description, InputSchema: t.Parameters,
		})
	}
	var system []string
	// lastToolResults：上一条 user 消息如果"专收工具结果"，记住它的下标以便
	// 把后续结果并进去（Anthropic 要求一条 assistant 的全部 tool_use 在同一
	// 下一条消息里拿到结果）。-1 = 上一条不是这种消息。
	lastToolResults := -1
	for _, m := range msgs {
		switch m.Role {
		case "system":
			system = append(system, m.Content)
			lastToolResults = -1
		case "user":
			req.Messages = append(req.Messages, anthropicMessage{Role: "user",
				Content: []anthropicContentBlock{{Type: "text", Text: m.Content}}})
			lastToolResults = -1
		case "assistant":
			req.Messages = append(req.Messages, anthropicMessage{Role: "assistant", Content: assistantToBlocks(m)})
			lastToolResults = -1
		case "tool":
			block := anthropicContentBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}
			if lastToolResults >= 0 {
				req.Messages[lastToolResults].Content = append(req.Messages[lastToolResults].Content, block)
			} else {
				req.Messages = append(req.Messages, anthropicMessage{Role: "user",
					Content: []anthropicContentBlock{block}})
				lastToolResults = len(req.Messages) - 1
			}
		default:
			return nil, fmt.Errorf("不支持的消息角色: %q", m.Role)
		}
	}
	req.Messages = backfillToolResults(req.Messages)
	req.System = strings.Join(system, "\n\n")
	return req, nil
}

// backfillToolResults 给"声明了 tool_use 但没等到结果"的消息补合成 tool_result。
//
// 为什么要有：Anthropic 要求每条 tool_use 在下一条消息里都有对应 tool_result，
// 缺一条就 400 拒收**整轮**——也就是历史里一个畸形轮次能让这个会话此后再也发不出
// 请求（2026-09-23 用户真实事故：三条「继续」全部无响应）。我们的写边界已经保证
// 新建的历史不缺配对（P5 的 sinkSkippedToolResults），但**进程在两次 append 之间
// 被杀**这类中断，以及历史遗留数据，仍可能留下缺口；读侧兜底比让会话变砖好。
//
// 只在"下一条消息确实存在"时追加（不凭空插入新消息，避免改变历史顺序）；补齐时
// 记一条日志，不做静默篡改。
func backfillToolResults(msgs []anthropicMessage) []anthropicMessage {
	for i := 0; i < len(msgs)-1; i++ {
		if msgs[i].Role != "assistant" {
			continue
		}
		var want []string
		for _, b := range msgs[i].Content {
			if b.Type == "tool_use" && b.ID != "" {
				want = append(want, b.ID)
			}
		}
		if len(want) == 0 {
			continue
		}
		next := &msgs[i+1]
		got := map[string]bool{}
		for _, b := range next.Content {
			if b.Type == "tool_result" {
				got[b.ToolUseID] = true
			}
		}
		// 下一条必须已经是"承载工具结果的消息"才动手并进去——否则是历史顺序乱了
		//（结果与调用不相邻），凭空插一条消息改变语义比让端点报错更糟。
		if next.Role != "user" || len(got) == 0 {
			continue
		}
		for _, id := range want {
			if got[id] {
				continue
			}
			log.Printf("历史里工具调用 %s 没有结果记录，已补合成结果后发送（历史残缺；见 AGENTS.md §5 坑 12）", id)
			next.Content = append(next.Content, anthropicContentBlock{
				Type: "tool_result", ToolUseID: id, Content: "（该调用的结果未记录：历史残缺）",
			})
		}
	}
	return msgs
}

// assistantToBlocks：assistant 消息 → thinking/text/tool_use 块序列。
// 不返回错误：工具参数已经过读侧兜底（repairToolArgsForWire），不会因为一条坏
// 参数让整轮请求失败——那条路径曾经把会话永久锁死。
func assistantToBlocks(m Message) []anthropicContentBlock {
	var blocks []anthropicContentBlock
	if m.ReasoningContent != "" {
		// 签名原样透传（真实 Anthropic 校验签名；llama.cpp 本地端点宽松）
		blocks = append(blocks, anthropicContentBlock{
			Type: "thinking", Thinking: m.ReasoningContent, Signature: m.ReasoningSignature,
		})
	}
	if m.Content != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, anthropicContentBlock{
			Type: "tool_use", ID: tc.ID, Name: tc.Function.Name,
			Input: argumentsToInput(tc.Function.Arguments),
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, anthropicContentBlock{Type: "text"})
	}
	return blocks
}

// argumentsToInput：Arguments 字符串 → input 对象（空串 → 空对象）。
// 坏参数不报错而是兜底修复或降级成空对象（见 toolargs.go 的事故说明）。
func argumentsToInput(args string) json.RawMessage {
	if strings.TrimSpace(args) == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(repairToolArgsForWire(args))
}

// anthropicResult：内容块 → 统一消息；stop_reason 映射。
func anthropicResult(ar *anthropicResponse) *ChatResult {
	msg := Message{Role: "assistant"}
	var texts []string
	for _, b := range ar.Content {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "thinking":
			msg.ReasoningContent = b.Thinking
			msg.ReasoningSignature = b.Signature
		case "tool_use":
			args, _ := json.Marshal(b.Input) // RawMessage 字面量直通
			tc := ToolCall{ID: b.ID, Type: "function"}
			tc.Function.Name = b.Name
			tc.Function.Arguments = string(args)
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	msg.Content = strings.Join(texts, "\n\n")
	if msg.Content == "" && msg.ReasoningContent != "" && len(msg.ToolCalls) == 0 {
		msg.Content, msg.ReasoningContent = msg.ReasoningContent, ""
	}
	return &ChatResult{
		Message:      msg,
		UsageTokens:  ar.Usage.InputTokens + ar.Usage.OutputTokens,
		PromptTokens: ar.Usage.InputTokens,
		FinishReason: mapAnthropicStop(ar.StopReason),
	}
}

// mapAnthropicStop：end_turn→stop、max_tokens→length、tool_use→tool_calls。
func mapAnthropicStop(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence", "":
		return FinishStop
	case "max_tokens":
		return FinishLength
	case "tool_use":
		return FinishToolCalls
	default:
		return reason
	}
}
