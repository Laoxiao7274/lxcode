package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIError 是端点返回非 2xx 时的类型化错误：调用方按状态码分流
// （4xx 不重试、5xx 可重试），不做字符串匹配（云端版 is4xxError 教训）。
type APIError struct {
	StatusCode int
	Body       string // 截断后的响应体，人可读
	// RetryAfter 是端点 Retry-After 头的解析值（429 限流时重试等待的依据）。
	// 0 = 头缺席或解析失败——调用方回落默认退避节奏，不猜。
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// maxRetryAfter 是 Retry-After 的钳制上限：限流窗口可能是几十分钟，照等会把
// 用户挂在界面上——超过 10s 一律按 10s，超过部分下轮重试再说。
const maxRetryAfter = 10 * time.Second

// parseRetryAfter 解析 Retry-After 头：秒数（"3"）或 HTTP 日期（RFC1123 GMT）
// 两种合法形态；解析失败 / 负值 / 过去时刻返回 0（调用方回落默认退避）。
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// newAPIError 组装类型化错误并顺带解析 Retry-After（hdr 为 nil 时跳过——
// 构造方拿不到头的调用点保持原语义）。
func newAPIError(status int, body string, hdr http.Header) *APIError {
	e := &APIError{StatusCode: status, Body: truncateStr(body, 512)}
	if hdr != nil {
		e.RetryAfter = parseRetryAfter(hdr.Get("Retry-After"))
	}
	return e
}

// contextOverflowMarkers 是各端点在"请求超出上下文窗口"时的错误体特征串。
// 为什么在这里做字符串匹配：端点不提供结构化错误码，这段匹配属于 wire 格式
// 知识，归 llm 层；上层（agent）只消费 IsContextOverflow 这个结构化谓词。
var contextOverflowMarkers = []string{
	"context length", "context_length", "context window", "maximum context",
	"too many tokens", "reduce the length", "prompt is too long", "input is too long",
	"上下文长度", "超出上下文", "context limit", "max_tokens",
}

// IsContextOverflow 判断一次调用失败是否属于"请求超出模型上下文窗口"。
// 只认 4xx（5xx 是服务端问题，压缩救不了）；无状态码（连接层错误）不算。
func IsContextOverflow(err error) bool {
	var api *APIError
	if !errors.As(err, &api) {
		return false
	}
	if api.StatusCode < 400 || api.StatusCode >= 500 {
		return false
	}
	body := strings.ToLower(api.Body)
	for _, marker := range contextOverflowMarkers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// truncateStr 按字符数截断（错误信息里的响应体摘要用）。
func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
