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
	"time"
)

// sseServer 起一个按行写 SSE 的测试服务器。
func sseServer(t *testing.T, lines []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, l := range lines {
			fmt.Fprintln(w, l)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// drain 收集流事件直到通道关闭。
func drain(ch <-chan StreamEvent) []StreamEvent {
	var out []StreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func mustClient(t *testing.T, url string, format string) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: url, Model: "m1", Format: format})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestOpenAIStreamText(t *testing.T) {
	lines := []string{
		`data: {"choices":[{"delta":{"content":"你"}}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"好"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":3,"total_tokens":8}}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	events := drain(ch)
	var text strings.Builder
	var done *ChatResult
	for _, ev := range events {
		switch ev.Type {
		case EventText:
			text.WriteString(ev.TextDelta)
		case EventDone:
			done = ev.Result
		case EventError:
			t.Fatalf("意外错误: %v", ev.Err)
		}
	}
	if text.String() != "你好" {
		t.Fatalf("流式文本不符: %q", text.String())
	}
	if done == nil || done.Message.Content != "你好" || done.FinishReason != "stop" || done.UsageTokens != 8 {
		t.Fatalf("done 不符: %+v", done)
	}
}

func TestOpenAIStreamUnicodeAcrossChunks(t *testing.T) {
	// 多字节 CJK 字符分布在多个 chunk（pi-ai 测试清单项）
	lines := []string{
		`data: {"choices":[{"delta":{"content":"设"}}]}`,
		`data: {"choices":[{"delta":{"content":"备"}}]}`,
		`data: {"choices":[{"delta":{"content":"状"}}]}`,
		`data: {"choices":[{"delta":{"content":"态"}}]}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, _ := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	events := drain(ch)
	var text strings.Builder
	for _, ev := range events {
		if ev.Type == EventText {
			text.WriteString(ev.TextDelta)
		}
	}
	if text.String() != "设备状态" {
		t.Fatalf("拼接不符: %q", text.String())
	}
}

func TestOpenAIStreamToolAggregation(t *testing.T) {
	// 工具调用跨 chunk：id 只在首个 chunk、arguments 分片、finish 后到
	lines := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"echo","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, _ := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	events := drain(ch)
	var tool ToolCall
	var done *ChatResult
	for _, ev := range events {
		switch ev.Type {
		case EventToolCall:
			tool = ev.ToolCall
		case EventDone:
			done = ev.Result
		}
	}
	if tool.Function.Name != "echo" || tool.ID != "call_1" || tool.Function.Arguments != `{"a":1}` {
		t.Fatalf("聚合不符: %+v", tool)
	}
	if done == nil || done.FinishReason != "tool_calls" || len(done.Message.ToolCalls) != 1 {
		t.Fatalf("done 不符: %+v", done)
	}
	if done.Message.ToolCalls[0].Function.Arguments != `{"a":1}` {
		t.Fatalf("done 里的工具参数不符: %+v", done.Message.ToolCalls[0])
	}
}

func TestOpenAIStreamEarlyClose(t *testing.T) {
	// 无 [DONE] 直接关连接（llama.cpp/vLLM 偶发漏发终止标记）：
	// 宽容收尾——已有内容当正常完成，不丢字（云端版同款处理）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"部分"}}]}`)
		fl.Flush()
	}))
	defer srv.Close()
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(ch)
	var done *ChatResult
	for _, ev := range events {
		if ev.Type == EventDone {
			done = ev.Result
		}
	}
	if done == nil {
		t.Fatalf("宽容收尾应产生 done: %+v", events)
	}
	if done.Message.Content != "部分" || done.FinishReason != "stop" {
		t.Fatalf("部分内容应保留且 finish 归一为 stop: %+v", done)
	}
}

func TestOpenAIStreamCancel(t *testing.T) {
	// 用户取消（ctx）：aborted 事件 + 已生成部分（pi-ai 语义：取消≠故障）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"部分"}}]}`)
		fl.Flush()
		<-r.Context().Done() // 挂住连接直到客户端取消
	}))
	defer srv.Close()
	c := mustClient(t, srv.URL, FormatOpenAI)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := c.ChatStream(ctx, []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	// 收到首个事件后取消
	select {
	case ev, ok := <-ch:
		if !ok || ev.Type != EventText {
			t.Fatalf("首事件应是 text: %+v ok=%v", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待首事件超时")
	}
	cancel()
	var last StreamEvent
	for ev := range ch {
		if ev.Type == EventError {
			last = ev
		}
	}
	if last.Type != EventError || last.Result == nil || last.Result.FinishReason != FinishAborted {
		t.Fatalf("取消应以 aborted 收尾并带部分内容: %+v", last)
	}
	if last.Result.Message.Content != "部分" {
		t.Fatalf("部分内容不符: %q", last.Result.Message.Content)
	}
}

func TestAnthropicStream(t *testing.T) {
	lines := []string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"想一想"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"你"}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"好"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"echo"}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`,
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"1}"}}`,
		`data: {"type":"content_block_stop","index":2}`,
		``,
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		`data: {"type":"message_stop"}`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatAnthropic)
	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	events := drain(ch)
	var text, reasoning strings.Builder
	var tool ToolCall
	var done *ChatResult
	for _, ev := range events {
		switch ev.Type {
		case EventText:
			text.WriteString(ev.TextDelta)
		case EventReasoning:
			reasoning.WriteString(ev.TextDelta)
		case EventToolCall:
			tool = ev.ToolCall
		case EventDone:
			done = ev.Result
		case EventError:
			t.Fatalf("意外错误: %v", ev.Err)
		}
	}
	if text.String() != "你好" || reasoning.String() != "想一想" {
		t.Fatalf("流式内容不符: text=%q reasoning=%q", text.String(), reasoning.String())
	}
	if tool.ID != "toolu_1" || tool.Function.Name != "echo" || tool.Function.Arguments != `{"a":1}` {
		t.Fatalf("tool_call 聚合不符: %+v", tool)
	}
	if done == nil {
		t.Fatal("缺少 done 事件")
	}
	if done.Message.Content != "你好" || done.Message.ReasoningContent != "想一想" {
		t.Fatalf("done 消息不符: %+v", done.Message)
	}
	if len(done.Message.ToolCalls) != 1 || done.Message.ToolCalls[0].Function.Arguments != `{"a":1}` {
		t.Fatalf("done 工具不符: %+v", done.Message.ToolCalls)
	}
	if done.FinishReason != "tool_calls" {
		t.Fatalf("stop_reason 映射不符: %s", done.FinishReason)
	}
	if done.UsageTokens != 17 || done.PromptTokens != 10 { // input 10 + output 7
		t.Fatalf("usage 不符: %+v", done)
	}
}

func TestOpenAIStreamPlainTextTokens(t *testing.T) {
	// MYT-LLM 类端点的非标准流：data: 后直接跟纯文本 token（无 JSON 包裹），
	// 思考期发空 data 行。实测 mytai.opencecs.com（2026-09-10 诊断）。
	lines := []string{
		`data: `,
		``,
		`data: `,
		`data: 收到`,
		`data: `,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(ch)
	var text strings.Builder
	var done *ChatResult
	for _, ev := range events {
		switch ev.Type {
		case EventText:
			text.WriteString(ev.TextDelta)
		case EventDone:
			done = ev.Result
		case EventError:
			t.Fatalf("意外错误: %v", ev.Err)
		}
	}
	if text.String() != "收到" {
		t.Fatalf("纯文本 token 拼接不符: %q", text.String())
	}
	if done == nil || done.Message.Content != "收到" {
		t.Fatalf("done 不符: %+v", done)
	}
}

func TestOpenAIStreamPlainTextJSONishToken(t *testing.T) {
	// 纯文本 token 恰好是合法 JSON（数字/对象）也不应误走 delta 路径
	lines := []string{
		`data: 123`,
		`data: {"a":1}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, _ := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	events := drain(ch)
	var text strings.Builder
	for _, ev := range events {
		if ev.Type == EventText {
			text.WriteString(ev.TextDelta)
		}
	}
	if text.String() != `123{"a":1}` {
		t.Fatalf("JSON 形态的纯文本 token 应按原文拼接: %q", text.String())
	}
}

func TestOpenAIStreamBareLineContinuation(t *testing.T) {
	// MYT-LLM 类端点（rawstream3 实测）：token 内的换行把后续内容顶到
	// 不带 data: 前缀的裸行——裸行是正文的一部分，跳过就是"回复被截断"
	// （实测一次丢 3 个段落开头）。裸行前的空行恢复为换行。
	lines := []string{
		`data: **云手机服务器`,
		`data: 简介**`,
		``,
		`云手机服务器是`,
		``,
		`data: 部署在数据中心的高性能计算`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(ch)
	var text strings.Builder
	var done *ChatResult
	for _, ev := range events {
		switch ev.Type {
		case EventText:
			text.WriteString(ev.TextDelta)
		case EventDone:
			done = ev.Result
		case EventError:
			t.Fatalf("意外错误: %v", ev.Err)
		}
	}
	want := "**云手机服务器简介**\n云手机服务器是部署在数据中心的高性能计算"
	if text.String() != want {
		t.Fatalf("裸行续行不符:\n got %q\nwant %q", text.String(), want)
	}
	if done == nil || done.Message.Content != want {
		t.Fatalf("done 不符: %+v", done)
	}
}

func TestOpenAIStreamSSEFieldsNotContent(t *testing.T) {
	// 标准 SSE 字段行（event:/id:/retry:）与注释（:）不是内容，不能误吞
	lines := []string{
		`event: message`,
		`id: 42`,
		`retry: 1000`,
		`: keep-alive comment`,
		`data: 你`,
		`data: 好`,
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, _ := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	var text strings.Builder
	for ev := range ch {
		if ev.Type == EventText {
			text.WriteString(ev.TextDelta)
		}
	}
	if text.String() != "你好" {
		t.Fatalf("SSE 字段行被误当内容: %q", text.String())
	}
}

func TestOpenAIStreamLeadingSpacePreserved(t *testing.T) {
	// data: 后只剥一个规范空格——token 自身的前导空格要保留
	// （英文名 token 常带前导空格，TrimSpace 会把它们吃掉）
	lines := []string{
		`data: hello`,
		`data:  world`, // 两个空格 = 规范空格 + token 前导空格
		`data: [DONE]`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, _ := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	var text strings.Builder
	for ev := range ch {
		if ev.Type == EventText {
			text.WriteString(ev.TextDelta)
		}
	}
	if text.String() != "hello world" {
		t.Fatalf("前导空格被吃: %q", text.String())
	}
}

// TestChatAutoToolsGoNonStream：声明工具 → 非流式请求（服务端应看到
// stream 缺省/False 的请求体），结果转事件回放（text + tool_call + done）。
func TestChatAutoToolsGoNonStream(t *testing.T) {
	requests := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, string(body))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"我来查看：","tool_calls":[{"id":"c1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"uname\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"total_tokens":9}}`))
	}))
	defer srv.Close()
	c := mustClient(t, srv.URL, FormatOpenAI)
	tool := Tool{Name: "bash", Description: "执行命令", Parameters: json.RawMessage(`{"type":"object"}`)}
	ch, err := c.ChatAuto(context.Background(), []Message{{Role: "user", Content: "看设备信息"}},
		WithTools([]Tool{tool}))
	if err != nil {
		t.Fatal(err)
	}
	// 请求体必须不带 stream:true（非流式）
	if strings.Contains(requests[0], `"stream":true`) {
		t.Fatalf("带工具应走非流式, body: %s", requests[0])
	}
	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 3 { // text + tool_call + done
		t.Fatalf("事件数不符: %+v", events)
	}
	if events[0].Type != EventText || events[0].TextDelta != "我来查看：" {
		t.Fatalf("text 事件不符: %+v", events[0])
	}
	if events[1].Type != EventToolCall || events[1].ToolCall.Function.Name != "bash" {
		t.Fatalf("tool_call 事件不符: %+v", events[1])
	}
	if events[2].Type != EventDone || events[2].Result.FinishReason != "tool_calls" ||
		len(events[2].Result.Message.ToolCalls) != 1 {
		t.Fatalf("done 事件不符: %+v", events[2])
	}
}

// TestChatAutoNoToolsStream：未声明工具 → 流式透传（打字机保留）。
func TestChatAutoNoToolsStream(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fl, _ := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"你好"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()
	c := mustClient(t, srv.URL, FormatOpenAI)
	ch, err := c.ChatAuto(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for ev := range ch {
		if ev.Type == EventText {
			text.WriteString(ev.TextDelta)
		}
	}
	if text.String() != "你好" {
		t.Fatalf("流式透传不符: %q", text.String())
	}
	if requests != 1 {
		t.Fatalf("应只有一次请求, got %d", requests)
	}
}

// TestChatAutoAnthropicToolsGoStream：anthropic 格式带工具也走流式——
// 其工具调用经显式事件下发，不依赖 openai 侧的 delta.tool_calls
// （MYT 端点 anthropic 侧实测规范，rawstream5）。
func TestChatAutoAnthropicToolsGoStream(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fl, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"type":"message_start","message":{"usage":{"input_tokens":3,"output_tokens":0}}}`)
		fmt.Fprintln(w, `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"echo"}}`)
		fmt.Fprintln(w, `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"text\":\"hi\"}"}}`)
		fmt.Fprintln(w, `data: {"type":"content_block_stop","index":0}`)
		fmt.Fprintln(w, `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`)
		fmt.Fprintln(w, `data: {"type":"message_stop"}`)
		fmt.Fprintln(w, `data: [DONE]`)
		if fl != nil {
			fl.Flush()
		}
	}))
	defer srv.Close()
	c := mustClient(t, srv.URL, FormatAnthropic)
	echoTool := Tool{Name: "echo", Description: "测试", Parameters: json.RawMessage(`{"type":"object"}`)}
	ch, err := c.ChatAuto(context.Background(), []Message{{Role: "user", Content: "x"}},
		WithTools([]Tool{echoTool}))
	if err != nil {
		t.Fatal(err)
	}
	var gotCall ToolCall
	var done *ChatResult
	for ev := range ch {
		switch ev.Type {
		case EventToolCall:
			gotCall = ev.ToolCall
		case EventDone:
			done = ev.Result
		case EventError:
			t.Fatalf("意外错误: %v", ev.Err)
		}
	}
	if gotCall.Function.Name != "echo" || gotCall.Function.Arguments != `{"text":"hi"}` {
		t.Fatalf("流式工具调用不符: %+v", gotCall)
	}
	if done == nil || done.FinishReason != "tool_calls" {
		t.Fatalf("done 不符: %+v", done)
	}
	if requests != 1 {
		t.Fatalf("应单次请求, got %d", requests)
	}
}

func TestAnthropicStreamErrorEvent(t *testing.T) {
	lines := []string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"部分"}}`,
		`data: {"type":"error","error":{"message":"overloaded"}}`,
	}
	srv := sseServer(t, lines)
	c := mustClient(t, srv.URL, FormatAnthropic)
	ch, _ := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "x"}})
	events := drain(ch)
	var last StreamEvent
	for _, ev := range events {
		if ev.Type == EventError {
			last = ev
		}
	}
	if last.Type != EventError {
		t.Fatal("应以 error 收尾")
	}
	if last.Err == nil || !strings.Contains(last.Err.Error(), "overloaded") {
		t.Fatalf("错误信息不符: %v", last.Err)
	}
	if last.Result == nil || last.Result.Message.Content != "部分" {
		t.Fatalf("error 应携带部分内容: %+v", last.Result)
	}
}

func TestAnthropicStreamRequestShape(t *testing.T) {
	// 请求形态：stream: true、max_tokens、system 顶层化
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		_, _ = w.Write([]byte{})
	}))
	defer srv.Close()
	c := mustClient(t, srv.URL, FormatAnthropic)
	_, err := c.ChatStream(context.Background(), []Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}})
	if err != nil {
		t.Fatal(err)
	}
	var req anthropicRequest
	if err2 := json.Unmarshal(body, &req); err2 != nil {
		t.Fatal(err2)
	}
	if !req.Stream || req.MaxTokens != 4096 || req.System != "s" {
		t.Fatalf("请求形态不符: stream=%v max_tokens=%d system=%q", req.Stream, req.MaxTokens, req.System)
	}
}
