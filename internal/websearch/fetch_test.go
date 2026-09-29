package websearch

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// allowLoopbackForTest 把 SSRF 守卫换成放行版，并注册恢复。
//
// 为什么需要：httptest 服务器监听在 127.0.0.1 上，而生产守卫必须拒绝它——
// 不替换接缝的话，下面所有「抓取正常路径」的测试都只能打真站点。
// 守卫本身由 TestFetchBlocksPrivateTargets / TestBlockedIPCoversMetadataAndCGNAT
// 直接测（那两条**不**替换接缝，测的就是真守卫）。
func allowLoopbackForTest(t *testing.T) {
	t.Helper()
	oldCheck, oldDial := checkFetchTarget, dialFetchTarget
	checkFetchTarget = func(*url.URL) error { return nil }
	dialFetchTarget = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	t.Cleanup(func() {
		checkFetchTarget, dialFetchTarget = oldCheck, oldDial
	})
}

// TestFetchBlocksPrivateTargets：SSRF 守卫必须挡住本机与内网地址。
//
// 这是 web_fetch 与 web_search 最大的安全差异：搜索渠道地址是用户自己配的
// （可信，可以指向内网自建 SearXNG），而抓取面对的是模型给的**任意 URL**。
func TestFetchBlocksPrivateTargets(t *testing.T) {
	blocked := []string{
		"http://localhost/admin",
		"http://127.0.0.1:7789/rpc",
		"http://127.1.2.3/",
		"http://[::1]:8080/",
		"http://10.0.0.5/",
		"http://172.16.3.4/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/", // 云元数据端点：SSRF 最经典的目标
		"http://100.64.0.1/",                       // CGNAT
		"http://0.0.0.0/",
		"http://foo.localhost/",
		"http://printer.local/",
		"file:///C:/Windows/win.ini",
		"ftp://example.com/x",
	}
	for _, raw := range blocked {
		if _, err := ParseFetchURL(raw); err == nil {
			t.Errorf("地址 %s 必须被拒绝（SSRF 守卫失效）", raw)
		}
	}
}

// TestBlockedIPCoversMetadataAndCGNAT：逐条钉住分类边界。
//
// 169.254.169.254 单独测：它在 IsLinkLocalUnicast 里，但那是「顺带被挡住的」——
// 将来有人把判定改成只看 IsPrivate，这条会红，而它是 SSRF 里代价最高的一个。
func TestBlockedIPCoversMetadataAndCGNAT(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"100.127.255.255", true},
		{"100.128.0.1", false}, // CGNAT 段外
		{"10.1.1.1", true},
		{"172.31.255.255", true},
		{"172.32.0.1", false}, // 私有段外
		{"192.168.0.1", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
	}
	for _, c := range cases {
		got := BlockedIP(net.ParseIP(c.ip))
		if got != c.blocked {
			t.Errorf("BlockedIP(%s) = %v，期望 %v", c.ip, got, c.blocked)
		}
	}
}

// TestFetchReadsHTMLAsText：HTML 页面 → 正文（剥脚本样式、块级换行、解实体）。
func TestFetchReadsHTMLAsText(t *testing.T) {
	allowLoopbackForTest(t)
	page := `<html><head><title>标题 &amp; 实体</title>` +
		`<style>body{color:red}</style>` +
		`<script>var secret = "不该出现在正文里";</script></head>` +
		`<body><!-- 注释也不该出现 --><h1>大标题</h1><p>第一段</p><p>第二段 &lt;tag&gt;</p>` +
		`<noscript>无脚本提示</noscript></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	res, err := Fetch(context.Background(), srv.URL, FetchOptions{})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if res.Title != "标题 & 实体" {
		t.Errorf("标题 = %q，期望解实体后的 %q", res.Title, "标题 & 实体")
	}
	for _, bad := range []string{"var secret", "color:red", "注释也不该", "无脚本提示"} {
		if strings.Contains(res.Text, bad) {
			t.Errorf("正文不该含 %q（脚本/样式/注释/无脚本区必须整块丢掉）:\n%s", bad, res.Text)
		}
	}
	for _, want := range []string{"大标题", "第一段", "第二段 <tag>"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("正文缺 %q:\n%s", want, res.Text)
		}
	}
	// 块级元素必须换行：不换行整页挤成一行，模型读不出结构。
	// 断言用「不同行」而不是「恰好一个换行」——开闭标签各产生一个换行，
	// 块之间因此是一个空行（段落分隔），那是刻意的。
	if !strings.Contains(res.Text, "大标题\n\n第一段") {
		t.Errorf("块级元素之间应换行分隔:\n%q", res.Text)
	}
}

// TestFetchRejectsBinaryContentType：二进制如实报类型，不灌乱码进上下文。
func TestFetchRejectsBinaryContentType(t *testing.T) {
	allowLoopbackForTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n"))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.URL, FetchOptions{})
	if err == nil {
		t.Fatal("图片响应必须报「不支持的内容类型」，而不是把二进制灌进上下文")
	}
	if !strings.Contains(err.Error(), "image/png") {
		t.Errorf("错误消息应点明内容类型（模型据此判断这是什么）: %v", err)
	}
}

// TestFetchTruncatesText：超上限截断并置 Truncated（不注明的话模型会以为读到了全文）。
func TestFetchTruncatesText(t *testing.T) {
	allowLoopbackForTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("A", 500)))
	}))
	defer srv.Close()

	res, err := Fetch(context.Background(), srv.URL, FetchOptions{MaxChars: 100})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if !res.Truncated {
		t.Error("超过 max_chars 必须置 Truncated")
	}
	if len([]rune(res.Text)) != 100 {
		t.Errorf("正文应截到 100 字符，实际 %d", len([]rune(res.Text)))
	}
}

// TestFetchFollowsRedirectAndReportsFinalURL：最终地址要如实回报。
//
// 重定向可能换了站点——模型据「最终地址」判断出处可信度，报原始地址就是骗它。
func TestFetchFollowsRedirectAndReportsFinalURL(t *testing.T) {
	allowLoopbackForTest(t)
	var dst string
	mux := http.NewServeMux()
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("落地页正文"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	dst = srv.URL + "/final"
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dst, http.StatusFound)
	})

	res, err := Fetch(context.Background(), srv.URL+"/start", FetchOptions{})
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if !strings.HasSuffix(res.URL, "/final") {
		t.Errorf("URL 应是重定向后的最终地址，实际 %q", res.URL)
	}
	if !strings.Contains(res.Text, "落地页正文") {
		t.Errorf("正文应来自落地页，实际 %q", res.Text)
	}
}

// TestFetchReportsHTTPError：非 2xx 如实报状态码与响应片段。
func TestFetchReportsHTTPError(t *testing.T) {
	allowLoopbackForTest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no such page"))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.URL, FetchOptions{})
	if err == nil {
		t.Fatal("404 必须报错")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("错误应含状态码: %v", err)
	}
}
