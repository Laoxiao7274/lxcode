package websearch

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 抓取（web_fetch 工具的网络层）的限额与超时。
const (
	// FetchDefaultMaxChars 是正文返回的默认上限（字符）。
	FetchDefaultMaxChars = 20000
	// FetchMaxCharsLimit 是正文返回的硬上限——模型可以要更多，但不能无节制。
	FetchMaxCharsLimit = 80000
	// fetchMaxBodyBytes 是响应体读取上限：与搜索渠道同一个理由
	//（防一个 500MB 的页面把内存吃光），抓正文比拿 JSON 宽一些。
	fetchMaxBodyBytes = 8 << 20
	// FetchTimeout 是单次抓取的总超时。抓正文是交互路径上的调用（用户在等），
	// 比搜索渠道宽松一点（页面比 API 慢），但不能挂住整轮生成。
	FetchTimeout = 30 * time.Second
	// fetchMaxRedirects 是重定向跳数上限。
	fetchMaxRedirects = 5
	// fetchProvider 是错误分类用的来源标识（脱敏与报错都走它）。
	fetchProvider = "web_fetch"
)

// FetchOptions 是一次抓取的参数。
type FetchOptions struct {
	MaxChars int // 正文返回上限（字符）；<=0 取 FetchDefaultMaxChars
}

// FetchResult 是一次抓取的结果。
type FetchResult struct {
	URL         string // 最终地址（跟随重定向后——模型据此知道实际读的是哪一页）
	Status      int
	ContentType string
	Title       string // HTML 的 <title>；非 HTML 为空
	Text        string // 正文文本
	Truncated   bool   // 正文是否被 MaxChars 截断
	Bytes       int    // 原始响应体字节数
}

// checkFetchTarget 与 dialFetchTarget 是 SSRF 守卫的两个接缝（URL 层 / 拨号层）。
//
// 为什么要有接缝：httptest 服务器监听在 127.0.0.1 上——**正是生产必须拒绝的
// 地址**。不替换它们的话，「抓取正常路径」（HTML 转正文、截断、重定向、内容类型
// 分派）就只能打真站点来测，那是不可接受的测试依赖（网络 + 不可复现）。
// 它们是测试接缝而不是配置项：**生产代码永不替换**，也不导出给调用方。
var (
	checkFetchTarget = CheckFetchURL
	dialFetchTarget  = safeDialContext
)

// safeDialContext 在真正建连之前校验**解析出来的 IP**。
//
// 只在 URL 层查一次是不够的，两条路都能绕过：一是域名可以先解析到公网、
// 建连时再解析到内网（DNS rebinding）；二是直接用一个指向内网的域名，
// 字面量检查根本看不到 IP。
// 校验放在建连这一层才是权威的——它管的是「这个 TCP 连接到底连去哪」。
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("地址格式错误: %w", err)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("域名解析失败: %w", err)
	}
	var lastErr error
	for _, ip := range ips {
		if BlockedIP(ip.IP) {
			lastErr = fmt.Errorf("拒绝访问内网地址 %s（解析为 %s）", host, ip.IP)
			continue // 有公网解析结果时仍然放行——挡的是内网，不是这个域名
		}
		d := &net.Dialer{Timeout: 10 * time.Second}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("域名 %s 没有可用的解析结果", host)
	}
	return nil, lastErr
}

// BlockedIP 报告 ip 是否属于禁止访问的范围（环回/私有/链路本地/CGNAT 等）。
//
// 169.254.0.0/16 必须挡：云厂商的实例元数据服务（169.254.169.254）就在这一段，
// 它是 SSRF 最经典的目标——拿到实例凭据。
func BlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10（CGNAT）与 0.0.0.0/8 不在标准判定里，但同样不该访问。
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
		if v4[0] == 0 {
			return true
		}
	}
	return false
}

// fetchClient 是抓取专用客户端。
//
// 与搜索渠道共用的 httpClient 刻意分开：那个不带 SSRF 防护（渠道地址是
// 用户自己配的、可信），而抓取面对的是**任意 URL**——混用一个客户端
// 要么让渠道也背上 SSRF 检查（用户配的自建 SearXNG 在内网，会被误拒），
// 要么让抓取失去防护。
var fetchClient = &http.Client{
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialFetchTarget(ctx, network, addr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConns:          4,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= fetchMaxRedirects {
			return fmt.Errorf("重定向超过 %d 次", fetchMaxRedirects)
		}
		// 每一跳都校验：内网重定向（公网页面 302 到 127.0.0.1）也是 SSRF。
		return checkFetchTarget(req.URL)
	},
}

// CheckFetchURL 校验地址本身（字面量层面，不做 DNS 解析）。
// 解析后的校验在 safeDialContext——两处都要有，各管一层。
func CheckFetchURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("地址为空")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("只支持 http/https 地址，实际 %q", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("地址缺少主机名")
	}
	// 保留域名：localhost 是环回，.local 是 mDNS（本地网段），.internal 是保留 TLD。
	// 这三个先挡掉是为了给出**能看懂的错误**，而不是等 dial 阶段报一个超时。
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return fmt.Errorf("拒绝访问本机/内网地址 %s", host)
	}
	if ip := net.ParseIP(host); ip != nil && BlockedIP(ip) {
		return fmt.Errorf("拒绝访问内网地址 %s", host)
	}
	return nil
}

// ParseFetchURL 解析并校验用户给的地址（严格版，守卫不可替换）。
func ParseFetchURL(raw string) (*url.URL, error) {
	return parseFetchURL(raw, CheckFetchURL)
}

// parseFetchURL 是 ParseFetchURL 的实现，校验函数由调用方给——
// 生产传 CheckFetchURL，测试传放行版（见上面的接缝说明）。
func parseFetchURL(raw string, check func(*url.URL) error) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("url 不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("地址解析失败: %w", err)
	}
	if err := check(u); err != nil {
		return nil, err
	}
	return u, nil
}

// Fetch 抓取一个 URL 并返回正文文本。
//
// 只处理 HTML 与文本：二进制（图片/PDF/压缩包）如实报「不支持的内容类型」
// 而不是把乱码灌进上下文——模型对着一屏 `\x89PNG` 什么也做不了。
func Fetch(ctx context.Context, rawURL string, opts FetchOptions) (FetchResult, error) {
	u, err := parseFetchURL(rawURL, checkFetchTarget)
	if err != nil {
		return FetchResult{}, NewProviderError(fetchProvider, KindConfig, 0, err.Error(), "", err)
	}
	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = FetchDefaultMaxChars
	}
	if maxChars > FetchMaxCharsLimit {
		maxChars = FetchMaxCharsLimit
	}

	reqCtx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FetchResult{}, NewProviderError(fetchProvider, KindConfig, 0, err.Error(), "", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.5")

	resp, err := fetchClient.Do(req)
	if err != nil {
		// 分类用**调用方**的 ctx（与 requestJSON 同款纪律）：我们自己的超时
		// 不该被误判成用户取消。
		return FetchResult{}, NewProviderError(fetchProvider, ClassifyTransport(ctx, err), 0, err.Error(), "", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, fetchMaxBodyBytes))
	if readErr != nil {
		return FetchResult{}, NewProviderError(fetchProvider, KindInvalidResponse,
			resp.StatusCode, "读取响应失败: "+readErr.Error(), "", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return FetchResult{}, NewProviderError(fetchProvider, ClassifyStatus(resp.StatusCode),
			resp.StatusCode, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncateForError(body)), "", nil)
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	out := FetchResult{
		URL:         resp.Request.URL.String(),
		Status:      resp.StatusCode,
		ContentType: ct,
		Bytes:       len(body),
	}
	switch {
	case isHTMLType(ct):
		page := string(body)
		out.Title = HTMLTitle(page)
		out.Text = HTMLToText(page)
	case ct == "" || isTextType(ct):
		out.Text = string(body)
	default:
		// 二进制如实报类型（带 Content-Type 便于模型判断这是什么）。
		return out, NewProviderError(fetchProvider, KindUnsupported, resp.StatusCode,
			fmt.Sprintf("不支持的内容类型 %s（只处理 HTML 与文本）", ct), "", nil)
	}
	if r := []rune(out.Text); len(r) > maxChars {
		out.Text = string(r[:maxChars])
		out.Truncated = true
	}
	return out, nil
}

// isHTMLType 报告 Content-Type 是否是 HTML（含 xhtml）。
func isHTMLType(ct string) bool {
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
}

// isTextType 报告 Content-Type 是否是可直接展示的文本。
//
// 刻意**不含** xml/csv 之外的二进制形态；application/json 收进来是因为
// 抓 API 端点看原始响应是常见用法（模型自己会解析）。
func isTextType(ct string) bool {
	for _, p := range []string{"text/", "application/json", "application/xml", "application/javascript"} {
		if strings.Contains(ct, p) {
			return true
		}
	}
	return false
}
