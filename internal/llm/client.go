package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// Chat 非流式对话：网络错误/5xx/429 指数退避重试（默认 1s、2s，共 3 次；
// 429 的 Retry-After 可解析时用它，钳到 10s），其余 4xx 短路。
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
			case <-time.After(retryDelay(err, attempt)):
			}
		}
	}
	return nil, lastErr
}

// retryable：5xx/网络错误/429 重试；其余 4xx（鉴权/参数）与 ctx 取消不重试。
// 429 单列：它是限流不是参数错，等一等就能过——等待时长按 Retry-After（见 retryDelay）。
func retryable(err error) bool {
	if apiErr, ok := err.(*APIError); ok {
		if apiErr.StatusCode == http.StatusTooManyRequests {
			return true
		}
		return apiErr.StatusCode >= 500
	}
	return true
}

// retryDelay 计算一次重试前的等待：端点给了可解析的 Retry-After 就用它
//（钳到 maxRetryAfter——限流窗口可能长达几十分钟，照等会把用户挂在界面上），
// 否则按固定退避表。Chat 与 ChatStream 共用一份，两条路径的节奏必须一致。
func retryDelay(err error, attempt int) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		if apiErr.RetryAfter > maxRetryAfter {
			return maxRetryAfter
		}
		return apiErr.RetryAfter
	}
	if attempt < len(retryBackoff) {
		return retryBackoff[attempt]
	}
	return retryBackoff[len(retryBackoff)-1]
}

func (c *Client) chatOnce(ctx context.Context, msgs []Message, o requestOpts) (*ChatResult, error) {
	if c.cfg.Format == FormatAnthropic {
		return c.anthropicChat(ctx, msgs, o)
	}
	return c.openaiChat(ctx, msgs, o)
}

// post 发 JSON 请求并读回（状态码由调用方判断）。鉴权头按格式由适配器设置。
// 第三返回值是响应头（Retry-After 的取数源——429 限流重试要用）。
func (c *Client) post(ctx context.Context, payload any, headers map[string]string) (int, []byte, http.Header, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("序列化请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, fmt.Errorf("请求已取消: %w", ctx.Err())
		}
		return 0, nil, nil, fmt.Errorf("请求失败（端点未启动?）: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8MB 上限防御
	if err != nil {
		return resp.StatusCode, nil, resp.Header, fmt.Errorf("读取响应: %w", err)
	}
	return resp.StatusCode, raw, resp.Header, nil
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
		// Retry-After 一并解析（429 限流的等待依据，Chat/ChatStream 共用）
		return nil, newAPIError(resp.StatusCode, string(raw), resp.Header)
	}
	return resp.Body, nil
}

// ChatStream 流式对话。连接与状态码错误同步返回；解析在后台 goroutine，
// 事件经通道投递，通道关闭即流结束。中断（断流/取消）以 error 事件收尾，
// Result 携带已生成的部分内容——不整体丢弃。
//
// 流式**受限重试**（2026-10-10：网关超时/5xx 不再一碰就断）：与 Chat 同一套
// 节奏（1s/2s，共 3 次尝试），安全边界只有一条——**已向调用方交付过任何事件
// （delta/tool_call）就不能重试**，否则同一段输出会被重复交给用户。所以：
//   - 连接期失败（非 2xx / 网络错误）：一个事件都没交付过，同步退避后重试；
//   - 首个事件就是 error 且此前零交付（首事件前断流）：pumpStream 丢弃它退避重试；
//   - 已交付过事件后的失败：原样转发错误（partial 语义不变，调用方按现状处理）。
//
// ctx 取消与 4xx（含 IsContextOverflow）不重试。重试对上层透明：交付 0 事件
// 意味着 turn 层还没 emit 过任何东西，重试不产生重复输出。
func (c *Client) ChatStream(ctx context.Context, msgs []Message, opts ...Option) (<-chan StreamEvent, error) {
	o := applyOpts(opts)
	var lastErr error
	for attempt := 0; attempt <= len(retryBackoff); attempt++ {
		if attempt > 0 {
			// 退避（Retry-After 优先）；ctx 取消就放弃
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay(lastErr, attempt-1)):
			}
		}
		ch, err := c.chatStreamOnce(ctx, msgs, o)
		if err == nil {
			// 连上了：剩余的重试预算交给搬运 goroutine（首事件前断流用它重试）
			return c.pumpStream(ctx, ch, msgs, o, len(retryBackoff)-attempt), nil
		}
		lastErr = err
		if ctx.Err() != nil || !retryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// chatStreamOnce 发起一次流式调用（不做重试——重试逻辑在 ChatStream/pumpStream）。
func (c *Client) chatStreamOnce(ctx context.Context, msgs []Message, o requestOpts) (<-chan StreamEvent, error) {
	if c.cfg.Format == FormatAnthropic {
		return c.anthropicStream(ctx, msgs, o)
	}
	return c.openaiStream(ctx, msgs, o)
}

// pumpStream 把底层流事件搬运给调用方，并对「一个事件都没交付就失败」的流
// 做受限重试（剩余预算 retriesLeft 由 ChatStream 按已消耗的尝试数递减）。
//
// 为什么在搬运层做：mid-stream 的失败以 EventError 事件到达，只有搬运方能
// 在把它交给调用方之前判断「之前交付过几个事件」。已交付 >0 时错误原样冒泡
//（partial 语义不变）；零交付时可安全重试——丢弃这次流，退避后重新发起。
func (c *Client) pumpStream(ctx context.Context, ch <-chan StreamEvent, msgs []Message, o requestOpts, retriesLeft int) <-chan StreamEvent {
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		delivered := 0 // 已向调用方交付的事件数（重试安全边界的唯一判据）
		var pendingErr error
		pumpAttempt := 0 // 泵内的重试序号（默认退避表的游标）
		for {
			// 退避后重新发起（pendingErr 非空 = 上一次流在零交付处失败）
			if pendingErr != nil {
				select {
				case <-ctx.Done():
					// 取消不重试：错误原样交给调用方（partial 语义由它处理）
					out <- StreamEvent{Type: EventError, Err: pendingErr}
					return
				case <-time.After(retryDelay(pendingErr, pumpAttempt-1)):
				}
				next, err := c.chatStreamOnce(ctx, msgs, o)
				if err != nil {
					if ctx.Err() != nil || !retryable(err) || retriesLeft <= 0 {
						out <- StreamEvent{Type: EventError, Err: err}
						return
					}
					retriesLeft--
					pumpAttempt++
					pendingErr = err
					continue
				}
				ch = next
				pendingErr = nil
			}
			retry := false
			for ev := range ch {
				if ev.Type == EventError && delivered == 0 && retriesLeft > 0 && ctx.Err() == nil && retryable(ev.Err) {
					// 首个事件就是错误且零交付：丢弃，退避重试
					pendingErr = ev.Err
					retriesLeft--
					pumpAttempt++
					retry = true
					break
				}
				delivered++
				out <- ev
			}
			if !retry {
				return // 正常结束（done/error 均已交付）
			}
		}
	}()
	return out
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
		// 全部事件打 Replay 标记：这不是流式产出，是"结果拿回来再按事件形态
		// 回放"。消费方据此知道本轮没有"首 token 到达"这个时刻——回放的增量
		// 与 done 同一瞬间到达，拿它当首字只会报出"首字延迟 == 整轮耗时"。
		if res.Message.Content != "" {
			ch <- StreamEvent{Type: EventText, TextDelta: res.Message.Content, Replay: true}
		}
		if res.Message.ReasoningContent != "" {
			ch <- StreamEvent{Type: EventReasoning, TextDelta: res.Message.ReasoningContent, Replay: true}
		}
		for _, tc := range res.Message.ToolCalls {
			ch <- StreamEvent{Type: EventToolCall, ToolCall: tc, Replay: true}
		}
		ch <- StreamEvent{Type: EventDone, Result: res, Replay: true}
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
