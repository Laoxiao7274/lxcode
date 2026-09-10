package llm

import "fmt"

// APIError 是端点返回非 2xx 时的类型化错误：调用方按状态码分流
// （4xx 不重试、5xx 可重试），不做字符串匹配（云端版 is4xxError 教训）。
type APIError struct {
	StatusCode int
	Body       string // 截断后的响应体，人可读
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// truncateStr 按字符数截断（错误信息里的响应体摘要用）。
func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
