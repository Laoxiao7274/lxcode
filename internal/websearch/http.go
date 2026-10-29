package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RequestTimeout 是单次渠道请求的超时上限。
//
// 上游各适配器自带 SEARCH_TIMEOUT_MS；我们统一一处。搜索是交互路径上的
// 调用（用户在等），不能让它挂住整轮生成——超时归 KindNetwork（可降级）。
const RequestTimeout = 20 * time.Second

// maxResponseBytes 是响应体读取上限。搜索结果 JSON 正常远小于它；
// 设上限是为了防"某个渠道返回一个 500MB 的 HTML 错误页"把内存吃光。
const maxResponseBytes = 4 << 20

// maxErrorBodyChars 是错误消息里回显的响应体长度上限。
// 回显是为了可诊断（很多渠道把真实原因放在 body 里），截断是为了不让
// 一次失败把上下文撑爆（对齐 bash 工具的输出上限纪律）。
const maxErrorBodyChars = 512

// userAgent 是本包请求的 UA。有的渠道（SearXNG 实例、Cloudflare 前置的
// 自建服务）会对空 UA 直接 403，所以固定一个明确标识。
const userAgent = "lxcode-websearch/1.0"

// httpClient 是共享客户端。超时由 ctx 控制（见 withTimeout），
// 所以这里不设 Client.Timeout——否则两者叠加会得到难以解释的实际超时。
var httpClient = &http.Client{}

// withTimeout 给请求加超时。超时值可由 WithRequestTimeout 覆盖——
// 这样渠道能调整自己的超时（Exa 带 contents 的搜索上游给到 60s），
// 而不必让每个适配器的调用签名都多一个参数。
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	d := RequestTimeout
	if v, ok := ctx.Value(timeoutKey{}).(time.Duration); ok && v > 0 {
		d = v
	}
	return context.WithTimeout(ctx, d)
}

// timeoutKey 是超时覆盖在 ctx 里的键（私有类型，避免与其它包的键相撞）。
type timeoutKey struct{}

// WithRequestTimeout 覆盖本次请求的超时（渠道适配器在需要时调用）。
// 传 ≤0 表示不改。
func WithRequestTimeout(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, timeoutKey{}, d)
}

// requestJSON 是各适配器的统一出口：发送请求 → 分类错误 → 解码响应。
//
// 统一在这里做的理由与上游一致：错误分类是降级链的判据，必须只有一份实现；
// 各适配器只负责"请求长什么样、响应怎么映射"（上游分散在每个 provider 里，
// 我们集中一处，新增渠道不必重复写状态码判定）。
//
// key 用于错误脱敏——渠道常在错误体里回显请求内容（含 key）。
func requestJSON(ctx context.Context, provider, key string, req *http.Request, out any) error {
	reqCtx, cancel := withTimeout(ctx)
	defer cancel()
	req = req.WithContext(reqCtx)
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// 传**调用方**的 ctx（不是 reqCtx）：只有调用方结束才算取消，
		// 否则我们自己的请求超时会被误判成用户取消（详见 ClassifyTransport）。
		kind := ClassifyTransport(ctx, err)
		return NewProviderError(provider, kind, 0, err.Error(), key, err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if readErr != nil {
		// 读失败与"响应不是合法 JSON"同类：换渠道有可能好。
		return NewProviderError(provider, KindInvalidResponse,
			resp.StatusCode, "读取响应失败: "+readErr.Error(), key, readErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return NewProviderError(provider, ClassifyStatus(resp.StatusCode),
			resp.StatusCode, truncateForError(body), key, nil)
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return NewProviderError(provider, KindInvalidResponse, resp.StatusCode,
			"响应不是合法 JSON: "+err.Error(), key, err)
	}
	return nil
}

// requestText 发起请求并把响应体当文本返回（HTML 刮取类渠道用）。
// 与 requestJSON 同一套错误分类与脱敏，只是不做 JSON 解码。
func requestText(ctx context.Context, provider, key string, req *http.Request, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = maxResponseBytes
	}
	reqCtx, cancel := withTimeout(ctx)
	defer cancel()
	req = req.WithContext(reqCtx)
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		// 同上：分类用调用方 ctx，避免把渠道超时误判成用户取消。
		kind := ClassifyTransport(ctx, err)
		return "", NewProviderError(provider, kind, 0, err.Error(), key, err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if readErr != nil {
		return "", NewProviderError(provider, KindInvalidResponse,
			resp.StatusCode, "读取响应失败: "+readErr.Error(), key, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", NewProviderError(provider, ClassifyStatus(resp.StatusCode),
			resp.StatusCode, truncateForError(body), key, nil)
	}
	return string(body), nil
}

// newJSONRequest 构造 JSON POST 请求（body 为 nil 时无请求体）。
func newJSONRequest(ctx context.Context, method, url string, headers map[string]string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("构造请求体失败: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// truncateForError 截断错误回显（超出部分丢弃，避免把整个响应体灌进会话历史）。
func truncateForError(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "(空响应体)"
	}
	r := []rune(s)
	if len(r) > maxErrorBodyChars {
		return string(r[:maxErrorBodyChars]) + "…(已截断)"
	}
	return s
}

// bearer 返回 Authorization: Bearer <key> 头（多数渠道同款）。
func bearer(key string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + key}
}
