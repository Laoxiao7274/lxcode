package websearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrorKind 是渠道错误的分类。降级链只认它，不做字符串匹配。
// 取值对齐上游 SearchProviderErrorKind（gemini-search.ts:54-65）。
type ErrorKind string

const (
	// KindTransient 服务端临时故障（5xx）——重试别的渠道有意义。
	KindTransient ErrorKind = "transient"
	// KindQuota 配额/限流（429）——换个渠道能绕开当前额度。
	KindQuota ErrorKind = "quota"
	// KindNetwork 连接层失败（DNS/超时/连接被拒）。
	KindNetwork ErrorKind = "network"
	// KindCredential 凭据缺失或无效（401/403）——不降级。
	KindCredential ErrorKind = "credential"
	// KindConfig 本地配置错（缺 base_url、值域非法）——不降级。
	KindConfig ErrorKind = "config"
	// KindAuth 鉴权被拒但凭据本身存在（如账号无该 API 权限）。
	KindAuth ErrorKind = "auth"
	// KindInvalidRequest 请求本身不合法（400/422）——不降级。
	KindInvalidRequest ErrorKind = "invalid-request"
	// KindInvalidResponse 响应体解析失败/结构不符——降级（换渠道可能好）。
	KindInvalidResponse ErrorKind = "invalid-response"
	// KindUnsupported 该渠道不支持此能力（404/405/501，如不支持域名过滤）。
	KindUnsupported ErrorKind = "unsupported"
	// KindAborted 调用方取消——直接上抛，不降级。
	KindAborted ErrorKind = "aborted"
	// KindUnknown 未分类。
	KindUnknown ErrorKind = "unknown"
)

// FallbackKinds 是允许触发降级的错误类型集合。
//
// 刻意排除 credential/config/auth/invalid-request：这三类都是「我们这边的问题」，
// 换渠道只会把配置错误掩盖成搜索成功。对齐上游 SearchRoutingConfig.fallbackOn
// （gemini-search.ts:67-71 只允许 transient/quota/network/invalid-response/unsupported）。
var FallbackKinds = map[ErrorKind]bool{
	KindTransient:       true,
	KindQuota:           true,
	KindNetwork:         true,
	KindInvalidResponse: true,
	KindUnsupported:     true,
}

// ProviderError 是带分类的渠道错误。
type ProviderError struct {
	Provider string    // 渠道 id
	Kind     ErrorKind // 分类（降级判定依据）
	Status   int       // HTTP 状态码（0 = 非 HTTP 层错误）
	Message  string    // 已脱敏的说明
	Err      error     // 底层错误（errors.Is/As 可穿透）
}

func (e *ProviderError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s 搜索失败(%s, HTTP %d): %s", e.Provider, e.Kind, e.Status, e.Message)
	}
	return fmt.Sprintf("%s 搜索失败(%s): %s", e.Provider, e.Kind, e.Message)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// NewProviderError 构造分类错误（Message 会先脱敏）。
func NewProviderError(provider string, kind ErrorKind, status int, msg, key string, cause error) *ProviderError {
	return &ProviderError{
		Provider: provider,
		Kind:     kind,
		Status:   status,
		Message:  Redact(msg, key),
		Err:      cause,
	}
}

// KindOf 取错误的分类；非 ProviderError 一律归 KindUnknown
// （调用方不该靠类型断言决定流程，但降级链必须有个判据）。
func KindOf(err error) ErrorKind {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return KindUnknown
}

// ShouldFallback 报告该错误是否应触发降级。
//
// 只有**调用方取消**永不降级（用户已经不要这次搜索了，换渠道只是白烧配额）。
// 这里刻意不拦 context.DeadlineExceeded：我们自己的渠道超时也表现为它，
// 而「渠道太慢」正是降级要处理的情况——调用方自己的 deadline 到点则由
// ClassifyTransport 归成 KindAborted（不在 FallbackKinds 里）挡住，
// 裸的 DeadlineExceeded 则因 KindOf 归 KindUnknown 而挡住。
func ShouldFallback(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	return FallbackKinds[KindOf(err)]
}

// ClassifyStatus 把 HTTP 状态码翻译成错误类型。
// 这张表就是「降级语义」的全部依据——上游各适配器分散地写，
// 我们集中一处，新增渠道不必各自重复判定。
func ClassifyStatus(status int) ErrorKind {
	switch {
	case status == 401 || status == 403:
		// 401/403 归凭据：key junk/权限不足都是「先别换渠道，去修配置」。
		return KindCredential
	case status == 429:
		return KindQuota
	case status == 404 || status == 405 || status == 501:
		// 端点不存在/方法不允许/未实现 = 该渠道没这个能力（如不支持域名过滤）。
		return KindUnsupported
	case status == 400 || status == 422:
		return KindInvalidRequest
	case status >= 500:
		return KindTransient
	case status >= 400:
		return KindInvalidRequest
	default:
		return KindUnknown
	}
}

// ClassifyTransport 把传输层错误翻译成错误类型。
//
// ctx 必须是**调用方**的 ctx，不是 withTimeout 派生出来的那份——判据是
// 「调用方是否已经结束」：
//   - 调用方结束了（用户取消 / 调用方自己的 deadline 到点）→ KindAborted，不降级；
//   - 调用方还活着而请求失败了 → 是渠道侧的问题（超时/连不上/DNS），归 KindNetwork。
//
// 踩过的坑（2026-09-27 修）：这里原本收到的是派生 ctx，于是**我们自己的
// 20s/60s 请求超时**在第一步就被判成 KindAborted——「渠道太慢」被当成
// 「用户取消」，主渠道一慢整次搜索就直接失败，降级链形同虚设（与 RequestTimeout
// 注释声明的「超时归 KindNetwork（可降级）」正好相反）。错误分类的输入必须与
// 「是谁的意图」对应，拿派生 ctx 判定等于把我们的决定算到调用方头上。
func ClassifyTransport(ctx context.Context, err error) ErrorKind {
	if err == nil {
		return KindUnknown
	}
	if ctx.Err() != nil {
		return KindAborted
	}
	if errors.Is(err, context.Canceled) {
		return KindAborted
	}
	// 其余传输层失败一律可降级：请求超时（渠道太慢）、连接被拒、DNS 失败、
	// 网络不可达——换一个渠道都有可能成功，这正是降级链存在的理由。
	return KindNetwork
}

// Redact 从文本里抹掉密钥。
//
// 错误消息常常回显请求体或响应体（上游为此专门写了 redactCredential，
// openai-search.ts:712/742 两处调用）——不脱敏的话，一次失败就会把用户的
// key 写进会话历史（会话是可搜索的、还会随导出外流）。
func Redact(text, key string) string {
	if key == "" || text == "" {
		return text
	}
	out := strings.ReplaceAll(text, key, "***")
	// 有的渠道回显的是去掉 Bearer 前缀的裸 key、有的是 URL 编码后的形态，
	// 这里只做最常见的两种补充；覆盖不到时由调用方限制回显长度兜底。
	if len(key) > 8 {
		out = strings.ReplaceAll(out, key[:8], "***")
	}
	return out
}
