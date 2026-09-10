package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// openaiStream 发起 OpenAI SSE 流并交给后台解析。
func (c *Client) openaiStream(ctx context.Context, msgs []Message, o requestOpts) (<-chan StreamEvent, error) {
	payload := buildOpenAIRequest(c.cfg.Model, msgs, o, true)
	headers := map[string]string{}
	if c.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + c.cfg.APIKey
	}
	body, err := c.postStream(ctx, payload, headers)
	if err != nil {
		return nil, err
	}
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		c.parseOpenAIStream(ctx, body, ch)
		body.Close()
	}()
	return ch, nil
}

// parseOpenAIStream 解析 OpenAI SSE：data: 行 + [DONE]；
// tool_calls 按 Index 聚合；scanner 256KB buffer + Err() 显式检查
// （云端版 #7 教训：大行截断会静默返回残缺数据）。
// MYT-LLM 类端点（mytai.opencecs.com）的三层兼容（rawstream2/3 实测）：
//  1. data 载荷无 JSON 包裹，直接是纯文本 token；
//  2. 思考期持续发空 data 帧（心跳，420 帧/67s）；
//  3. token 内的换行把后续内容顶到**不带 data: 前缀的裸行**上——
//     裸行是正文的一部分，跳过它就是"回复被截断"的直接根因
//     （实测一次回复丢 3 个段落开头）。
//
// 裸行前的空行对应 token 内的换行，恢复为 "\n" 保住段落结构。
// 标准 SSE 字段行（event:/id:/retry:）与注释（:）仍然跳过，不误当正文。
func (c *Client) parseOpenAIStream(ctx context.Context, body io.Reader, ch chan<- StreamEvent) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 256*1024), 256*1024)
	var content, reasoning strings.Builder
	var toolCalls []ToolCall
	finish := ""
	var usageTok, promptTok int
	prevBlank := false

	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			prevBlank = false
			// 只剥 SSE 规范的单个前导空格，保留 token 自身的前导空格
			data := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if data == "[DONE]" {
				goto done
			}
			if data == "" {
				continue // 空 data 帧：思考期心跳
			}
			var delta struct {
				Choices []struct {
					Delta struct {
						Content          string     `json:"content"`
						ReasoningContent string     `json:"reasoning_content"`
						Reasoning        string     `json:"reasoning"`
						ToolCalls        []ToolCall `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens int `json:"prompt_tokens"`
					TotalTokens  int `json:"total_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &delta); err != nil {
				// 非标准端点：data: 后直接跟纯文本 token，无 JSON 包裹
				content.WriteString(data)
				if !emit(ctx, ch, StreamEvent{Type: EventText, TextDelta: data}) {
					return
				}
				continue
			}
			if len(delta.Choices) == 0 && delta.Usage == nil {
				// 合法 JSON 但对不上 OpenAI 结构，按原文当纯文本 token（防丢正文）
				content.WriteString(data)
				if !emit(ctx, ch, StreamEvent{Type: EventText, TextDelta: data}) {
					return
				}
				continue
			}
			if len(delta.Choices) > 0 {
				d := delta.Choices[0].Delta
				if d.ReasoningContent != "" {
					reasoning.WriteString(d.ReasoningContent)
					if !emit(ctx, ch, StreamEvent{Type: EventReasoning, TextDelta: d.ReasoningContent}) {
						return
					}
				}
				if d.Reasoning != "" {
					reasoning.WriteString(d.Reasoning)
					if !emit(ctx, ch, StreamEvent{Type: EventReasoning, TextDelta: d.Reasoning}) {
						return
					}
				}
				if d.Content != "" {
					content.WriteString(d.Content)
					if !emit(ctx, ch, StreamEvent{Type: EventText, TextDelta: d.Content}) {
						return
					}
				}
				if len(d.ToolCalls) > 0 {
					toolCalls = mergeToolCalls(toolCalls, d.ToolCalls)
				}
				if delta.Choices[0].FinishReason != "" {
					finish = delta.Choices[0].FinishReason
				}
			}
			if delta.Usage != nil {
				usageTok = delta.Usage.TotalTokens
				promptTok = delta.Usage.PromptTokens
			}
		case line == "":
			prevBlank = true
		case strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") ||
			strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:"):
			// 标准 SSE 注释/字段行，不是内容
			prevBlank = false
		default:
			// MYT-LLM 类端点的续行：token 内换行把后续内容顶到裸行——
			// 裸行是正文的一部分，跳过它就是截断。
			wasBlank := prevBlank
			prevBlank = false
			text := line
			if wasBlank && content.Len() > 0 {
				text = "\n" + line // 恢复被 SSE 帧分隔吃掉的换行
			}
			content.WriteString(text)
			if !emit(ctx, ch, StreamEvent{Type: EventText, TextDelta: text}) {
				return
			}
		}
	}
done:
	// 读取错误（断流/取消）：error 事件 + 已生成部分，aborted 与故障分开
	if err := sc.Err(); err != nil {
		res := openAIStreamResult(content.String(), reasoning.String(), toolCalls, finish, usageTok, promptTok)
		if ctx.Err() != nil {
			res.FinishReason = FinishAborted
		} else {
			res.FinishReason = FinishError
		}
		emitFinal(ch, StreamEvent{Type: EventError, Result: res, Err: fmt.Errorf("流中断: %w", err)})
		return
	}
	// 干净 EOF（无 [DONE]）：宽容收尾——llama.cpp/vLLM 偶发漏发终止标记，
	// 已有内容就当正常完成（云端版同款处理）；一个字都没有才是故障。
	if content.Len() == 0 && reasoning.Len() == 0 && len(toolCalls) == 0 && finish == "" {
		emitFinal(ch, StreamEvent{Type: EventError, Err: fmt.Errorf("流为空（端点没有返回任何内容）")})
		return
	}
	if finish == "" {
		finish = FinishStop
	}
	// 聚合完成的工具调用在收尾前投递（OpenAI 协议没有块级 stop 信号，
	// finish_reason 到达即全部参数已齐）
	if len(toolCalls) > 0 {
		for _, tc := range toolCalls {
			if !emit(ctx, ch, StreamEvent{Type: EventToolCall, ToolCall: tc}) {
				return
			}
		}
	}
	emitFinal(ch, StreamEvent{Type: EventDone,
		Result: openAIStreamResult(content.String(), reasoning.String(), toolCalls, finish, usageTok, promptTok)})
}

// mergeToolCalls 按 Index 聚合流式 tool_call 片段：
// 同 index 后续 chunk 追加 arguments、name 空不覆盖、id 后到补上（云端版已验证）。
func mergeToolCalls(acc []ToolCall, incoming []ToolCall) []ToolCall {
	for _, tc := range incoming {
		found := false
		for i := range acc {
			if acc[i].Index == tc.Index {
				found = true
				if tc.Function.Name != "" {
					acc[i].Function.Name = tc.Function.Name
				}
				acc[i].Function.Arguments += tc.Function.Arguments
				if tc.ID != "" {
					acc[i].ID = tc.ID
				}
				break
			}
		}
		if !found {
			acc = append(acc, tc)
		}
	}
	return acc
}
