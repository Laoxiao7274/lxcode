package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultTimeout 本地慢推理给足（云端版同款 300s）。
const defaultTimeout = 300 * time.Second

// retryBackoff 是非流式调用的指数退避间隔；测试改小以避免真等 3 秒。
var retryBackoff = []time.Duration{time.Second, 2 * time.Second}

// Client 是一次配置、多处复用的对话客户端（无状态，并发安全）。
type Client struct {
	cfg        Config
	httpClient *http.Client // 非流式：整体超时（含响应读取）
	streamHTTP *http.Client // 流式：无整体超时——慢端点长文会被截断（实测
	// mytai.opencecs.com：思考 67s + 生成，300s 超时会砍长回复的中段）；
	// 取消靠 ctx（TUI 的 esc），stall 场景由调用方决定何时放弃
	url string // 归一化后的端点
}

// New 构造客户端：校验必填、按格式归一化 URL、固化超时。
func New(cfg Config) (*Client, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("model 不能为空")
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("base_url 必须是带 http(s) scheme 的完整地址: %q", cfg.BaseURL)
	}
	if cfg.Format == "" {
		cfg.Format = FormatOpenAI
	}
	switch cfg.Format {
	case FormatOpenAI, FormatAnthropic:
	default:
		return nil, fmt.Errorf("format 必须是 %q 或 %q: %q", FormatOpenAI, FormatAnthropic, cfg.Format)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	base := strings.TrimSuffix(cfg.BaseURL, "/")
	var endpoint string
	if cfg.Format == FormatAnthropic {
		endpoint = anthropicMessagesURL(base)
	} else {
		endpoint = chatCompletionsURL(base)
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: timeout},
		streamHTTP: &http.Client{},
		url:        endpoint,
	}, nil
}

// chatCompletionsURL 归一化 OpenAI 端点：裸地址 / 带 /v1 / 完整路径统一
// （云端版 chatCompletionsURL 同款逻辑，llama.cpp 与 vLLM 都吃这三种写法）。
func chatCompletionsURL(base string) string {
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}

// anthropicMessagesURL 归一化 Anthropic 端点到 /v1/messages。
func anthropicMessagesURL(base string) string {
	if strings.HasSuffix(base, "/messages") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

// requestOpts 是请求级选项（Option 模式）。
type requestOpts struct {
	tools       []Tool
	temperature float64
	maxTokens   int
	thinking    *bool  // tri-state：nil = 不传（Qwen3 chat_template_kwargs，仅 openai/vLLM 生效）
	effort      string // 推理强度档位（空 = 不传）；openai → reasoning_effort，anthropic → thinking budget
}

// Option 是 Chat/ChatStream 的请求级选项。
type Option func(*requestOpts)

// WithTools 声明可用工具（function calling）。
func WithTools(tools []Tool) Option { return func(o *requestOpts) { o.tools = tools } }

// WithTemperature 采样温度。0 值 = 不传，让服务端用默认值——
// 云端版血泪教训：显式传 1.0 曾导致随机性过高、工具调用意愿下降。
func WithTemperature(v float64) Option { return func(o *requestOpts) { o.temperature = v } }

// WithMaxTokens 输出上限。openai 0 值不传；anthropic 必填，走保守默认。
func WithMaxTokens(n int) Option { return func(o *requestOpts) { o.maxTokens = n } }

// WithThinking 控制 Qwen3 类模型的思考链（vLLM chat_template_kwargs.enable_thinking，
// 仅 openai 格式生效；anthropic 侧忽略）。
func WithThinking(on bool) Option { return func(o *requestOpts) { o.thinking = &on } }

// WithEffort 推理强度（minimal/low/medium/high）。调用方负责能力门控——
// 只对声明了 reasoning 能力的模型附加（对不支持的端点传参会直接 400）。
// openai 格式 → reasoning_effort；anthropic 格式 → thinking budget_tokens
// （minimal=1024 … high=32768，budget ≥ max_tokens 时自动抬高 max_tokens）。
// 空串是 no-op。
func WithEffort(e string) Option { return func(o *requestOpts) { o.effort = e } }

func applyOpts(opts []Option) requestOpts {
	var o requestOpts
	for _, f := range opts {
		f(&o)
	}
	return o
}

// Chat 非流式对话：网络错误/5xx 指数退避重试（1s、2s，共 3 次），4xx 短路。
func (c *Client) Chat(ctx context.Context, msgs []Message, opts ...Option) (*ChatResult, error) {
	var lastErr error
	for attempt := 0; attempt <= len(retryBackoff); attempt++ {
		res, err := c.chatOnce(ctx, msgs, applyOpts(opts))
		if err == nil {
			return res, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, err
		}
		if attempt < len(retryBackoff) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryBackoff[attempt]):
			}
		}
	}
	return nil, lastErr
}

// retryable：5xx/网络错误重试；4xx（鉴权/参数）与 ctx 取消不重试。
func retryable(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode >= 500
	}
	return true
}

func (c *Client) chatOnce(ctx context.Context, msgs []Message, o requestOpts) (*ChatResult, error) {
	if c.cfg.Format == FormatAnthropic {
		return c.anthropicChat(ctx, msgs, o)
	}
	return c.openaiChat(ctx, msgs, o)
}

// post 发 JSON 请求并读回（状态码由调用方判断）。鉴权头按格式由适配器设置。
func (c *Client) post(ctx context.Context, payload any, headers map[string]string) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("序列化请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, fmt.Errorf("请求已取消: %w", ctx.Err())
		}
		return 0, nil, fmt.Errorf("请求失败（端点未启动?）: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8MB 上限防御
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// postStream 发起流式 POST，成功（2xx）返回响应体供 SSE 解析。
func (c *Client) postStream(ctx context.Context, payload any, headers map[string]string) (io.ReadCloser, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.streamHTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("请求已取消: %w", ctx.Err())
		}
		return nil, fmt.Errorf("请求失败（端点未启动?）: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, &APIError{StatusCode: resp.StatusCode, Body: truncateStr(string(raw), 512)}
	}
	return resp.Body, nil
}

// ChatStream 流式对话。连接与状态码错误同步返回；解析在后台 goroutine，
// 事件经通道投递，通道关闭即流结束。中断（断流/取消）以 error 事件收尾，
// Result 携带已生成的部分内容——不整体丢弃。流式不重试（云端版同款）。
func (c *Client) ChatStream(ctx context.Context, msgs []Message, opts ...Option) (<-chan StreamEvent, error) {
	o := applyOpts(opts)
	if c.cfg.Format == FormatAnthropic {
		return c.anthropicStream(ctx, msgs, o)
	}
	return c.openaiStream(ctx, msgs, o)
}

// ChatAuto 是"可靠优先"的统一对话入口：按格式分流——
//   - anthropic 格式 → 永远流式：其工具调用经显式事件下发
//     （content_block_start type=tool_use + input_json_delta），实测
//     mytai.opencecs.com 的 anthropic 侧完全规范（rawstream5），流式 +
//     工具共存，思考链也可见；
//   - openai 格式 + 声明了工具 → 非流式（结果转事件回放，形态与
//     ChatStream 一致，消费方无感）：部分端点的 openai 流式会丢
//     tool_calls（同端点实测：思考心跳后直接 [DONE]，工具调用被吞——
//     模型说"我来帮您查看"然后没了），而非流式的 tool_calls 完整。
func (c *Client) ChatAuto(ctx context.Context, msgs []Message, opts ...Option) (<-chan StreamEvent, error) {
	o := applyOpts(opts)
	if c.cfg.Format == FormatAnthropic || len(o.tools) == 0 {
		return c.ChatStream(ctx, msgs, opts...)
	}
	res, err := c.Chat(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan StreamEvent, 4)
	go func() {
		defer close(ch)
		if res.Message.Content != "" {
			ch <- StreamEvent{Type: EventText, TextDelta: res.Message.Content}
		}
		if res.Message.ReasoningContent != "" {
			ch <- StreamEvent{Type: EventReasoning, TextDelta: res.Message.ReasoningContent}
		}
		for _, tc := range res.Message.ToolCalls {
			ch <- StreamEvent{Type: EventToolCall, ToolCall: tc}
		}
		ch <- StreamEvent{Type: EventDone, Result: res}
	}()
	return ch, nil
}

// emit 投递增量事件；ctx 取消时放弃（终态事件用 emitFinal，必须送达）。
func emit(ctx context.Context, ch chan<- StreamEvent, ev StreamEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// emitFinal 投递终态事件（done/error）。消费端排空直至通道关闭，
// 直发不 select——避免取消后收尾事件丢失导致 UI 卡在生成中。
func emitFinal(ch chan<- StreamEvent, ev StreamEvent) {
	ch <- ev
}
