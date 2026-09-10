package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// anthStreamBlock 是流式内容块的聚合状态（按 content block index 组织）。
type anthStreamBlock struct {
	kind     string // text | thinking | tool_use
	text     strings.Builder
	thinking strings.Builder
	sig      strings.Builder
	toolID   string
	toolName string
	toolJSON strings.Builder
}

// anthropicStream 发起 Anthropic SSE 流并交给后台解析。
func (c *Client) anthropicStream(ctx context.Context, msgs []Message, o requestOpts) (<-chan StreamEvent, error) {
	payload, err := convertToAnthropic(c.cfg.Model, msgs, o, true)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"anthropic-version": anthropicVersion}
	if c.cfg.APIKey != "" {
		headers["x-api-key"] = c.cfg.APIKey
	}
	body, err := c.postStream(ctx, payload, headers)
	if err != nil {
		return nil, err
	}
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		parseAnthropicStream(ctx, body, ch)
		body.Close()
	}()
	return ch, nil
}

// parseAnthropicStream 解析 Anthropic 事件流。data 行内嵌 type 字段
// （message_start / content_block_start / content_block_delta / content_block_stop /
//
//	message_delta / message_stop / ping / error），event 行冗余可忽略。
func parseAnthropicStream(ctx context.Context, body io.Reader, ch chan<- StreamEvent) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 256*1024), 256*1024)

	blocks := map[int]*anthStreamBlock{}
	var order []int // 块顺序，收尾组装用
	stopReason := ""
	var inputTok, outputTok int
	stopped := false

	getBlock := func(idx int) *anthStreamBlock {
		b, ok := blocks[idx]
		if !ok {
			b = &anthStreamBlock{}
			blocks[idx] = b
			order = append(order, idx)
		}
		return b
	}

	for !stopped && sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		var hdr struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(data), &hdr) != nil || hdr.Type == "" {
			continue
		}
		switch hdr.Type {
		case "message_start":
			var ev struct {
				Message struct {
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				inputTok = ev.Message.Usage.InputTokens
				outputTok = ev.Message.Usage.OutputTokens
			}
		case "content_block_start":
			var ev struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				b := getBlock(ev.Index)
				b.kind = ev.ContentBlock.Type
				b.toolID = ev.ContentBlock.ID
				b.toolName = ev.ContentBlock.Name
			}
		case "content_block_delta":
			var ev struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					Signature   string `json:"signature"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			b := getBlock(ev.Index)
			switch ev.Delta.Type {
			case "text_delta":
				b.text.WriteString(ev.Delta.Text)
				if !emit(ctx, ch, StreamEvent{Type: EventText, TextDelta: ev.Delta.Text}) {
					return
				}
			case "thinking_delta":
				b.thinking.WriteString(ev.Delta.Thinking)
				if !emit(ctx, ch, StreamEvent{Type: EventReasoning, TextDelta: ev.Delta.Thinking}) {
					return
				}
			case "signature_delta":
				b.sig.WriteString(ev.Delta.Signature)
			case "input_json_delta":
				b.toolJSON.WriteString(ev.Delta.PartialJSON)
			}
		case "content_block_stop":
			var ev struct {
				Index int `json:"index"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if b := blocks[ev.Index]; b != nil && b.kind == "tool_use" {
				tc := ToolCall{ID: b.toolID, Type: "function"}
				tc.Function.Name = b.toolName
				tc.Function.Arguments = b.toolJSON.String()
				if strings.TrimSpace(tc.Function.Arguments) == "" {
					tc.Function.Arguments = "{}"
				}
				if !emit(ctx, ch, StreamEvent{Type: EventToolCall, ToolCall: tc}) {
					return
				}
			}
		case "message_delta":
			var ev struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage *struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				if ev.Delta.StopReason != "" {
					stopReason = ev.Delta.StopReason
				}
				if ev.Usage != nil {
					outputTok = ev.Usage.OutputTokens
				}
			}
		case "message_stop":
			stopped = true
		case "ping":
			// 心跳，忽略
		case "error":
			var ev struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			msg := "流式错误事件"
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			res := assembleAnthropic(blocks, order, stopReason, inputTok, outputTok)
			res.FinishReason = FinishError
			emitFinal(ch, StreamEvent{Type: EventError, Err: fmt.Errorf("%s", msg), Result: res})
			return
		}
	}
	// 读取错误（断流/取消）：error 事件 + 已生成部分
	if err := sc.Err(); err != nil {
		res := assembleAnthropic(blocks, order, stopReason, inputTok, outputTok)
		if ctx.Err() != nil {
			res.FinishReason = FinishAborted
		} else {
			res.FinishReason = FinishError
		}
		emitFinal(ch, StreamEvent{Type: EventError, Result: res, Err: fmt.Errorf("流中断: %w", err)})
		return
	}
	emitFinal(ch, StreamEvent{Type: EventDone,
		Result: assembleAnthropic(blocks, order, stopReason, inputTok, outputTok)})
}

// assembleAnthropic 把聚合状态组装为最终 ChatResult。
func assembleAnthropic(blocks map[int]*anthStreamBlock, order []int, stop string, inputTok, outputTok int) *ChatResult {
	msg := Message{Role: "assistant"}
	var texts []string
	for _, idx := range order {
		b := blocks[idx]
		switch b.kind {
		case "text":
			if b.text.Len() > 0 {
				texts = append(texts, b.text.String())
			}
		case "thinking":
			msg.ReasoningContent = b.thinking.String()
			msg.ReasoningSignature = b.sig.String()
		case "tool_use":
			tc := ToolCall{ID: b.toolID, Type: "function"}
			tc.Function.Name = b.toolName
			tc.Function.Arguments = b.toolJSON.String()
			if strings.TrimSpace(tc.Function.Arguments) == "" {
				tc.Function.Arguments = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	msg.Content = strings.Join(texts, "\n\n")
	if msg.Content == "" && msg.ReasoningContent != "" && len(msg.ToolCalls) == 0 {
		msg.Content, msg.ReasoningContent = msg.ReasoningContent, ""
	}
	return &ChatResult{
		Message:      msg,
		UsageTokens:  inputTok + outputTok,
		PromptTokens: inputTok,
		FinishReason: mapAnthropicStop(stop),
	}
}
