// 管理站会话的 API 层测试：登录 / 失败 / 限流 / 过期 / 登出失效 / cookie 与 Bearer 双通道。
//
// 为什么这些必须在 API 层测：会话与限流是「谁能改线上数据」的唯一入口，
// 只有从 HTTP 打进去才能同时钉住 cookie 属性、状态码、以及两条鉴权通道的一致性。
package api

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode-site-backend/internal/seed"
	"github.com/moyunteng/lxcode-site-backend/internal/store"
)

func newSessionServer(t *testing.T, cfg Config) *httptest.Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开库失败: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := seed.Run(st, log.New(io.Discard, "", 0)); err != nil {
		t.Fatalf("导入种子失败: %v", err)
	}
	if cfg.Token == "" {
		cfg.Token = testToken
	}
	return httptest.NewServer(New(st, cfg, log.New(io.Discard, "", 0)).Handler())
}

// loginWith 用密码登录，返回 Set-Cookie 里的会话 cookie（没有则 nil）。
func loginWith(t *testing.T, srv *httptest.Server, password string) (*http.Response, *http.Cookie, []byte) {
	t.Helper()
	body := `{"password":"` + password + `"}`
	res, out := sendJSON(t, http.MethodPost, srv, "/api/admin/login", "", body)
	for _, cookie := range res.Cookies() {
		if cookie.Name == sessionCookieName {
			return res, cookie, out
		}
	}
	return res, nil, out
}

func postWithCookie(t *testing.T, srv *httptest.Server, path string, cookie *http.Cookie) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("造请求失败: %v", err)
	}
	req.AddCookie(cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res, out
}

func withCookie(t *testing.T, srv *httptest.Server, path string, cookie *http.Cookie) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("造请求失败: %v", err)
	}
	req.AddCookie(cookie)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res, out
}

func TestLoginIssuesSessionCookie(t *testing.T) {
	srv := newSessionServer(t, Config{})
	res, cookie, body := loginWith(t, srv, testToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("正确密码登录状态 %d，body=%s", res.StatusCode, string(body))
	}
	if cookie == nil {
		t.Fatalf("登录成功必须发会话 cookie")
	}
	if !cookie.HttpOnly {
		t.Errorf("会话 cookie 必须 HttpOnly（JS 读不到才偷不走）")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("会话 cookie SameSite = %v，want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("会话 cookie Path = %q，want /", cookie.Path)
	}
	if len(cookie.Value) != 64 {
		t.Errorf("会话 id 应是 32 字节 hex（64 字符），got %d 字符", len(cookie.Value))
	}

	// cookie 通道：me 200，管理列表 200 且含草稿
	res, out := withCookie(t, srv, "/api/admin/me", cookie)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(out), "authenticated") {
		t.Errorf("带 cookie 的 me = %d %s", res.StatusCode, string(out))
	}
	res, out = withCookie(t, srv, "/api/admin/releases", cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("带 cookie 的管理列表 = %d %s", res.StatusCode, string(out))
	}
	// 含草稿能力：种子里 0.2.0 是草稿，公开接口看不到，管理接口必须看到
	if !strings.Contains(string(out), "0.2.0") {
		t.Errorf("管理列表应含草稿 0.2.0（cookie 通道也要有草稿能力）")
	}

	// Bearer 通道仍然通（脚本 / CI 兼容）
	res, out = get(t, srv, "/api/admin/releases", testToken)
	if res.StatusCode != http.StatusOK {
		t.Errorf("Bearer 通道 = %d %s", res.StatusCode, string(out))
	}

	// 登出 → 会话失效（浏览器会自动带上 cookie，这里手动带）
	res, out = postWithCookie(t, srv, "/api/admin/logout", cookie)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("登出状态 %d %s", res.StatusCode, string(out))
	}
	if res, _ := withCookie(t, srv, "/api/admin/me", cookie); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("登出后 me 应 401，got %d", res.StatusCode)
	}
	if res, _ := withCookie(t, srv, "/api/admin/releases", cookie); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("登出后管理列表应 401，got %d", res.StatusCode)
	}
}

func TestLoginRejections(t *testing.T) {
	srv := newSessionServer(t, Config{})

	if res, _, body := loginWith(t, srv, "wrong-password"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("错误密码应 401，got %d %s", res.StatusCode, string(body))
	}
	// 没有 cookie 的会话通道
	if res, _ := get(t, srv, "/api/admin/me", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("未登录 me 应 401，got %d", res.StatusCode)
	}
	if res, _ := get(t, srv, "/api/admin/releases", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("未登录管理列表应 401，got %d", res.StatusCode)
	}
	// 伪造的会话 id 也 401
	fake := &http.Cookie{Name: sessionCookieName, Value: strings.Repeat("0", 64)}
	if res, _ := withCookie(t, srv, "/api/admin/me", fake); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("伪造会话应 401，got %d", res.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	srv := newSessionServer(t, Config{})
	for i := 1; i <= 10; i++ {
		res, _, _ := loginWith(t, srv, "wrong-password")
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应是 401，got %d", i, res.StatusCode)
		}
	}
	res, _, body := loginWith(t, srv, "wrong-password")
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("第 11 次失败应 429，got %d %s", res.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "尝试过多") {
		t.Errorf("429 文案应说明「尝试过多」，got %s", string(body))
	}
	// 被限流时**正确密码也不放行**（否则限流形同虚设）
	if res, _, _ := loginWith(t, srv, testToken); res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("限流期间正确密码也应 429，got %d", res.StatusCode)
	}
}

func TestSessionExpiry(t *testing.T) {
	srv := newSessionServer(t, Config{SessionTTL: 60 * time.Millisecond})
	_, cookie, _ := loginWith(t, srv, testToken)
	if cookie == nil {
		t.Fatalf("登录应发 cookie")
	}
	if res, _ := withCookie(t, srv, "/api/admin/me", cookie); res.StatusCode != http.StatusOK {
		t.Fatalf("刚登录应 200，got %d", res.StatusCode)
	}
	time.Sleep(120 * time.Millisecond)
	if res, _ := withCookie(t, srv, "/api/admin/me", cookie); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("会话过期后应 401，got %d", res.StatusCode)
	}
}

func TestSessionRenewedOnUse(t *testing.T) {
	// 有效期 200ms：用一次就续期，所以在 120ms 的间隔里连续用不会被踢
	srv := newSessionServer(t, Config{SessionTTL: 200 * time.Millisecond})
	_, cookie, _ := loginWith(t, srv, testToken)
	if cookie == nil {
		t.Fatalf("登录应发 cookie")
	}
	for i := 0; i < 3; i++ {
		time.Sleep(120 * time.Millisecond)
		if res, _ := withCookie(t, srv, "/api/admin/me", cookie); res.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次续期检查应 200，got %d", i+1, res.StatusCode)
		}
	}
}

func TestAdminEndpointsNeedConfiguredCredential(t *testing.T) {
	// 直接造一个「没配凭据」的服务（Token 为空）
	st := newStore(t)
	if err := seed.Run(st, log.New(io.Discard, "", 0)); err != nil {
		t.Fatalf("导入种子失败: %v", err)
	}
	srv := httptest.NewServer(New(st, Config{}, log.New(io.Discard, "", 0)).Handler())
	t.Cleanup(srv.Close)
	if res, _, _ := loginWith(t, srv, "anything"); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("未配置凭据时登录应 503，got %d", res.StatusCode)
	}
	if res, _ := get(t, srv, "/api/admin/me", ""); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("未配置凭据时 me 应 503，got %d", res.StatusCode)
	}
}
