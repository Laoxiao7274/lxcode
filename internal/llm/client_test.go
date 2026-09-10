package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// captureBody 记录请求体供断言。
type captureBody struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

func newTestClient(t *testing.T, format, respJSON string, status int) (*Client, *captureBody) {
	t.Helper()
	cap := &captureBody{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.Method, cap.Path, cap.Header, cap.Body = r.Method, r.URL.Path, r.Header, body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respJSON))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, Model: "m1", Format: format})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, cap
}

func TestURLNormalization(t *testing.T) {
	cases := []struct {
		format string
		base   string
		want   string
	}{
		{FormatOpenAI, "http://x", "http://x/v1/chat/completions"},
		{FormatOpenAI, "http://x/", "http://x/v1/chat/completions"},
		{FormatOpenAI, "http://x/v1", "http://x/v1/chat/completions"},
		{FormatOpenAI, "http://x/v1/", "http://x/v1/chat/completions"},
		{FormatOpenAI, "http://x/v1/chat/completions", "http://x/v1/chat/completions"},
		{FormatAnthropic, "http://x", "http://x/v1/messages"},
		{FormatAnthropic, "http://x/v1", "http://x/v1/messages"},
		{FormatAnthropic, "http://x/v1/messages", "http://x/v1/messages"},
	}
	for _, c := range cases {
		cl, err := New(Config{BaseURL: c.base, Model: "m", Format: c.format})
		if err != nil {
			t.Fatalf("New(%s,%s): %v", c.format, c.base, err)
		}
		if cl.url != c.want {
			t.Errorf("%s %s → %s, want %s", c.format, c.base, cl.url, c.want)
		}
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://x", Model: ""}); err == nil {
		t.Error("空 model 应报错")
	}
	if _, err := New(Config{BaseURL: "127.0.0.1:8080", Model: "m"}); err == nil {
		t.Error("无 scheme 的 base_url 应报错")
	}
	if _, err := New(Config{BaseURL: "http://x", Model: "m", Format: "gemini"}); err == nil {
		t.Error("非法 format 应报错")
	}
	if _, err := New(Config{BaseURL: "http://x", Model: "m"}); err != nil {
		t.Errorf("format 缺省应为 openai: %v", err)
	}
}

func TestOpenAIChat(t *testing.T) {
	resp := `{"choices":[{"message":{"content":"你好"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	c, cap := newTestClient(t, FormatOpenAI, resp, 200)
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "嗨"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Message.Content != "你好" || res.FinishReason != "stop" || res.UsageTokens != 15 || res.PromptTokens != 10 {
		t.Fatalf("结果不符: %+v", res)
	}
	// 请求形态：路径 / 鉴权头 / 消息序列化
	if cap.Path != "/v1/chat/completions" || cap.Method != "POST" {
		t.Fatalf("请求路径不符: %s %s", cap.Method, cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "" {
		t.Fatalf("未配 key 不应带 Authorization: %q", got)
	}
	var req map[string]any
	if err := json.Unmarshal(cap.Body, &req); err != nil {
		t.Fatal(err)
	}
	if req["model"] != "m1" {
		t.Fatalf("model 字段不符: %v", req["model"])
	}
	msgs := req["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("messages 不符: %v", msgs)
	}
	if _, has := req["stream"]; has {
		t.Fatal("非流式不应带 stream 字段")
	}
}

func TestOpenAIChatAuthAndTools(t *testing.T) {
	cap := &captureBody{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.Header = r.Header
		cap.Body, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "sk-test", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	tool := Tool{Name: "echo", Description: "回声", Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)}
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, WithTools([]Tool{tool}))
	if err != nil || res.Message.Content != "ok" {
		t.Fatalf("Chat: %v %+v", err, res)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("鉴权头不符: %q", got)
	}
	var req map[string]any
	if err := json.Unmarshal(cap.Body, &req); err != nil {
		t.Fatal(err)
	}
	tools := req["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "echo" || fn["parameters"] == nil {
		t.Fatalf("tools 序列化不符: %v", tools)
	}
}

func TestOpenAIChatToolCalls(t *testing.T) {
	resp := `{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function",
		"function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}]}`
	c, _ := newTestClient(t, FormatOpenAI, resp, 200)
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "调用工具"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls 不符: %+v", res.Message.ToolCalls)
	}
	tc := res.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "echo" || tc.Function.Arguments != `{"text":"hi"}` {
		t.Fatalf("tool_call 不符: %+v", tc)
	}
	if res.FinishReason != "tool_calls" {
		t.Fatalf("finish 不符: %s", res.FinishReason)
	}
}

func TestOpenAIChatReasoningFallback(t *testing.T) {
	// vLLM thinking 怪癖：content 空、思考链有值 → 思考链当正文
	resp := `{"choices":[{"message":{"content":"","reasoning_content":"思考结果"},"finish_reason":"stop"}]}`
	c, _ := newTestClient(t, FormatOpenAI, resp, 200)
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.Content != "思考结果" || res.Message.ReasoningContent != "" {
		t.Fatalf("fallback 不符: %+v", res.Message)
	}
}

func TestOpenAIChatAPIError(t *testing.T) {
	c, _ := newTestClient(t, FormatOpenAI, `{"error":{"message":"bad key"}}`, 401)
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 401 {
		t.Fatalf("应报 401 APIError, got %v", err)
	}
	if !strings.Contains(apiErr.Error(), "bad key") {
		t.Fatalf("错误应含响应体: %v", apiErr)
	}
}

func TestOpenAIChatRetryOn5xx(t *testing.T) {
	// 把退避改小，避免测试真等 3 秒
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { retryBackoff = old }()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`boom`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL, Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil || res.Message.Content != "ok" {
		t.Fatalf("重试后应成功: %v %+v", err, res)
	}
	if calls != 3 {
		t.Fatalf("应重试到第 3 次, got %d", calls)
	}
}

func TestOpenAIChatNoRetryOn4xx(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`bad request`))
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL, Model: "m1"})
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("应报 400, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("4xx 不应重试, got %d calls", calls)
	}
}

func TestOpenAIChatNoChoices(t *testing.T) {
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { retryBackoff = old }()
	c, _ := newTestClient(t, FormatOpenAI, `{"choices":[]}`, 200)
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("空 choices 应报错")
	}
}

func TestAnthropicChat(t *testing.T) {
	resp := `{"content":[{"type":"text","text":"你好"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":8,"output_tokens":4}}`
	c, cap := newTestClient(t, FormatAnthropic, resp, 200)
	res, err := c.Chat(context.Background(), []Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "嗨"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Message.Content != "你好" || res.FinishReason != "stop" {
		t.Fatalf("结果不符: %+v", res)
	}
	if res.UsageTokens != 12 || res.PromptTokens != 8 {
		t.Fatalf("usage 不符: %+v", res)
	}
	// 请求形态：路径 / 鉴权头 / system 顶层化 / max_tokens 必填
	if cap.Path != "/v1/messages" {
		t.Fatalf("路径不符: %s", cap.Path)
	}
	if got := cap.Header.Get("x-api-key"); got != "" {
		t.Fatalf("未配 key 不应带 x-api-key: %q", got)
	}
	if got := cap.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version 缺失: %q", got)
	}
	var req map[string]any
	if err := json.Unmarshal(cap.Body, &req); err != nil {
		t.Fatal(err)
	}
	if req["system"] != "你是助手" {
		t.Fatalf("system 应顶层化: %v", req["system"])
	}
	if req["max_tokens"] != float64(4096) {
		t.Fatalf("max_tokens 应有默认值: %v", req["max_tokens"])
	}
	msgs := req["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("system 不应出现在 messages: %v", msgs)
	}
}

func TestAnthropicMessageConversion(t *testing.T) {
	// 历史：assistant 思考 + 工具调用 → tool 结果 → 完整往返转换
	c, cap := newTestClient(t, FormatAnthropic, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`, 200)
	assistantMsg := Message{Role: "assistant", Content: "我调用工具", ReasoningContent: "想想", ReasoningSignature: "sig123"}
	tc := ToolCall{ID: "toolu_1", Type: "function"}
	tc.Function.Name = "echo"
	tc.Function.Arguments = `{"text":"hi"}`
	assistantMsg.ToolCalls = []ToolCall{tc}
	_, err := c.Chat(context.Background(), []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "问"},
		assistantMsg,
		{Role: "tool", Content: "hi", ToolCallID: "toolu_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var req anthropicRequest
	if err := json.Unmarshal(cap.Body, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("消息数不符: %+v", req.Messages)
	}
	// assistant → thinking + text + tool_use 三块
	blocks := req.Messages[1].Content
	if len(blocks) != 3 {
		t.Fatalf("assistant 应 3 块: %+v", blocks)
	}
	if blocks[0].Type != "thinking" || blocks[0].Signature != "sig123" || blocks[0].Thinking != "想想" {
		t.Fatalf("thinking 块不符: %+v", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].Text != "我调用工具" {
		t.Fatalf("text 块不符: %+v", blocks[1])
	}
	if blocks[2].Type != "tool_use" || blocks[2].ID != "toolu_1" || string(blocks[2].Input) != `{"text":"hi"}` {
		t.Fatalf("tool_use 块不符: %+v", blocks[2])
	}
	// tool 消息 → user 的 tool_result 块
	tb := req.Messages[2].Content[0]
	if req.Messages[2].Role != "user" || tb.Type != "tool_result" || tb.ToolUseID != "toolu_1" || tb.Content != "hi" {
		t.Fatalf("tool_result 块不符: %+v", tb)
	}
}

func TestAnthropicChatToolUseResponse(t *testing.T) {
	resp := `{"content":[{"type":"tool_use","id":"toolu_1","name":"echo","input":{"text":"hi"}},
		{"type":"text","text":"调用中"}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`
	c, _ := newTestClient(t, FormatAnthropic, resp, 200)
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls: %+v", res.Message.ToolCalls)
	}
	tc := res.Message.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Function.Name != "echo" || tc.Function.Arguments != `{"text":"hi"}` {
		t.Fatalf("tool_call 不符: %+v", tc)
	}
	if res.Message.Content != "调用中" {
		t.Fatalf("text 块不符: %q", res.Message.Content)
	}
	if res.FinishReason != "tool_calls" {
		t.Fatalf("stop_reason 映射不符: %s", res.FinishReason)
	}
}

func TestAnthropicMaxTokensOption(t *testing.T) {
	c, cap := newTestClient(t, FormatAnthropic, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`, 200)
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}}, WithMaxTokens(512)); err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(cap.Body, &req); err != nil {
		t.Fatal(err)
	}
	if req["max_tokens"] != float64(512) {
		t.Fatalf("max_tokens 不符: %v", req["max_tokens"])
	}
}

func TestAnthropicStopReasonMapping(t *testing.T) {
	cases := map[string]string{"end_turn": "stop", "max_tokens": "length", "tool_use": "tool_calls", "stop_sequence": "stop"}
	for in, want := range cases {
		if got := mapAnthropicStop(in); got != want {
			t.Errorf("mapAnthropicStop(%q) = %q, want %q", in, got, want)
		}
	}
}
