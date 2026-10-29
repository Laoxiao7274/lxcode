package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 流式 tool_calls 聚合的单测（stream=true + tools 的 openai 路径）。
// 场景依据 2026-10-13 GLM-5.3-Flash 真机探测（temp/stream-tools-probe）：
// 流式 delta 的 tool_calls 按 index 增量下发（首帧带 id/name，后续帧只带
// arguments 增量），finish_reason=tool_calls，usage 在最后一个 chunk 照报。

// TestOpenAIStreamToolCallsSingle：单工具——id/name 首帧、arguments 分片跨
// chunk 拼接、finish_reason=tool_calls、usage 照报。
func TestOpenAIStreamToolCallsSingle(t *testing.T) {
	lines := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":" \"北京\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":162,"completion_tokens":12,"total_tokens":174}}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	tool := Tool{Name: "get_weather", Description: "查天气", Parameters: json.RawMessage(`{"type":"object"}`)}
	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "北京天气"}},
		WithTools([]Tool{tool}))
	if err != nil {
		t.Fatal(err)
	}
	var call ToolCall
	var done *ChatResult
	for ev := range ch {
		switch ev.Type {
		case EventToolCall:
			call = ev.ToolCall
		case EventDone:
			done = ev.Result
		case EventError:
			t.Fatalf("意外 error 事件: %v", ev.Err)
		}
	}
	if call.ID != "call_1" || call.Function.Name != "get_weather" {
		t.Fatalf("tool_call 不符: %+v", call)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments 拼接后不是合法 JSON: %q", call.Function.Arguments)
	}
	if args["city"] != "北京" {
		t.Fatalf("arguments 内容不符: %v", args)
	}
	if done == nil || done.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason 应为 tool_calls: %+v", done)
	}
	if done.PromptTokens != 162 || done.UsageTokens != 12 {
		t.Fatalf("usage 不符: prompt=%d output=%d", done.PromptTokens, done.UsageTokens)
	}
}

// TestOpenAIStreamToolCallsParallel：并行多工具——两个 index 各自聚合、
// id/name 各自齐全（GLM 实测：每个 index 独立 id）。
func TestOpenAIStreamToolCallsParallel(t *testing.T) {
	lines := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"北京\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"city\":\"上海\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	tool := Tool{Name: "get_weather", Description: "查天气", Parameters: json.RawMessage(`{"type":"object"}`)}
	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "北京和上海"}},
		WithTools([]Tool{tool}))
	if err != nil {
		t.Fatal(err)
	}
	var calls []ToolCall
	for ev := range ch {
		if ev.Type == EventToolCall {
			calls = append(calls, ev.ToolCall)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("应有 2 个 tool_call, got %d", len(calls))
	}
	if calls[0].ID != "call_a" || calls[1].ID != "call_b" {
		t.Fatalf("两个 index 的 id 各自聚合不符: %+v", calls)
	}
	if !strings.Contains(calls[0].Function.Arguments, "北京") ||
		!strings.Contains(calls[1].Function.Arguments, "上海") {
		t.Fatalf("并行 arguments 不符: %q / %q", calls[0].Function.Arguments, calls[1].Function.Arguments)
	}
}

// TestChatAutoStreamToolsOpen：cfg.StreamTools 开启 + openai 带工具 → 走流式
// （服务端应看到 "stream":true），事件为真流式产出（Replay=false）。
func TestChatAutoStreamToolsOpen(t *testing.T) {
	var sawStream bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sawStream = strings.Contains(string(body), `"stream":true`)
		fl, _ := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c9","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Model: "m1", Format: FormatOpenAI, StreamTools: true})
	if err != nil {
		t.Fatal(err)
	}
	tool := Tool{Name: "bash", Description: "执行命令", Parameters: json.RawMessage(`{"type":"object"}`)}
	ch, err := c.ChatAuto(context.Background(), []Message{{Role: "user", Content: "看设备信息"}},
		WithTools([]Tool{tool}))
	if err != nil {
		t.Fatal(err)
	}
	var call ToolCall
	var done *ChatResult
	for ev := range ch {
		switch ev.Type {
		case EventToolCall:
			call = ev.ToolCall
		case EventDone:
			done = ev.Result
		}
	}
	if !sawStream {
		t.Fatal("StreamTools 开启时应发 stream:true 请求")
	}
	if call.Function.Name != "bash" {
		t.Fatalf("tool_call 不符: %+v", call)
	}
	if done == nil || done.FinishReason != "tool_calls" {
		t.Fatalf("done 不符: %+v", done)
	}
}

// TestChatAutoStreamToolsClosedDefaultsOld：StreamTools 关闭（缺省）+ 带工具
// → 维持旧行为（非流式回放）。
func TestChatAutoStreamToolsClosedDefaultsOld(t *testing.T) {
	requests := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"我来查看：","tool_calls":[{"id":"c1","type":"function","function":{"name":"bash","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"total_tokens":9}}`))
	}))
	defer srv.Close()
	c := mustClient(t, srv.URL, FormatOpenAI) // StreamTools 缺省 false
	tool := Tool{Name: "bash", Description: "执行命令", Parameters: json.RawMessage(`{"type":"object"}`)}
	ch, err := c.ChatAuto(context.Background(), []Message{{Role: "user", Content: "看设备信息"}},
		WithTools([]Tool{tool}))
	if err != nil {
		t.Fatal(err)
	}
	events := []StreamEvent{}
	for ev := range ch {
		events = append(events, ev)
	}
	if len(requests) == 0 || strings.Contains(requests[0], `"stream":true`) {
		t.Fatal("StreamTools 缺省关闭时应走非流式")
	}
	for _, ev := range events {
		if !ev.Replay {
			t.Fatalf("非流式回放事件应带 Replay 标记: %+v", ev)
		}
	}
}
