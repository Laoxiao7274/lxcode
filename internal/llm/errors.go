package llm

import (
	"errors"
	"fmt"
	"strings"
)

// APIError 是端点返回非 2xx 时的类型化错误：调用方按状态码分流
// （4xx 不重试、5xx 可重试），不做字符串匹配（云端版 is4xxError 教训）。
type APIError struct {
	StatusCode int
	Body       string // 截断后的响应体，人可读
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
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
