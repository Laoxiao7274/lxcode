package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 三形态归一化：用户从注册表复制的常是完整路径，直接拼会得到必然 404 的地址。
func TestModelsURLNormalizes(t *testing.T) {
	cases := []struct{ base, want string }{
		{"https://api.deepseek.com", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com/", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com/v1", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com/v1/", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com/v1/chat/completions", "https://api.deepseek.com/v1/models"},
		{"https://api.deepseek.com/chat/completions", "https://api.deepseek.com/v1/models"},
		{"https://api.anthropic.com/v1/messages", "https://api.anthropic.com/v1/models"},
		{"https://gw.example.com/openai/v1", "https://gw.example.com/openai/v1/models"},
		{"https://gw.example.com/v1/models", "https://gw.example.com/v1/models"},
		{"http://127.0.0.1:8000", "http://127.0.0.1:8000/v1/models"},
	}
	for _, c := range cases {
		if got := modelsURL(c.base); got != c.want {
			t.Errorf("modelsURL(%q) = %q，期望 %q", c.base, got, c.want)
		}
	}
}

func TestDiscoverOpenAIFormat(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"m-b","object":"model"},{"id":"m-a","name":"A 模型"},
			{"id":"m-a","object":"model"},{"id":"  "}]}`))
	}))
	defer srv.Close()

	s := testService(nil)
	res, err := s.Discover(context.Background(), DiscoverInput{BaseURL: srv.URL, APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Errorf("请求路径 = %q，期望 /v1/models", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if res.Endpoint != srv.URL+"/v1/models" || res.Format != "openai" {
		t.Errorf("回显不对: %+v", res)
	}
	// 去重 + 排序 + 空 id 丢弃。
	if len(res.Models) != 2 || res.Models[0].ID != "m-a" || res.Models[1].ID != "m-b" {
		t.Fatalf("模型清单不对: %+v", res.Models)
	}
	if res.Models[0].Name != "A 模型" {
		t.Errorf("name 未解析: %+v", res.Models[0])
	}
}

func TestDiscoverAnthropicFormatUsesAPIKeyHeader(t *testing.T) {
	var gotKey, gotVersion, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotVersion, gotAuth = r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-x","display_name":"Claude X"}]}`))
	}))
	defer srv.Close()

	s := testService(nil)
	res, err := s.Discover(context.Background(), DiscoverInput{BaseURL: srv.URL, APIKey: "ak", Format: "anthropic"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if gotKey != "ak" || gotVersion != anthropicVersion {
		t.Errorf("anthropic 头不对: x-api-key=%q anthropic-version=%q", gotKey, gotVersion)
	}
	if gotAuth != "" {
		t.Errorf("anthropic 格式不该带 Authorization: %q", gotAuth)
	}
	if len(res.Models) != 1 || res.Models[0].Name != "Claude X" {
		t.Errorf("display_name 未回落成 name: %+v", res.Models)
	}
}

// 自建端点常不鉴权——无 key 时挂了头反而可能被拒。
func TestDiscoverWithoutKeySendsNoAuth(t *testing.T) {
	var auth, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, key = r.Header.Get("Authorization"), r.Header.Get("x-api-key")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	s := testService(nil)
	if _, err := s.Discover(context.Background(), DiscoverInput{BaseURL: srv.URL}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if auth != "" || key != "" {
		t.Errorf("无 key 不该带鉴权头: Authorization=%q x-api-key=%q", auth, key)
	}
}

func TestDiscoverAcceptsModelsKeyFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"id":"llama3"},{"id":"qwen2"}]}`))
	}))
	defer srv.Close()

	s := testService(nil)
	res, err := s.Discover(context.Background(), DiscoverInput{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Models) != 2 {
		t.Fatalf("models 形状未兼容: %+v", res.Models)
	}
}

// 错误必须自解释：用户该看到「检查 API Key」，不是光秃秃的状态码。
func TestDiscoverStatusErrorsAreSelfExplaining(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "检查 API Key"},
		{http.StatusForbidden, "检查 API Key"},
		{http.StatusNotFound, "/v1/models"},
		{http.StatusInternalServerError, "HTTP 500"},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte("upstream boom"))
		}))
		s := testService(nil)
		_, err := s.Discover(context.Background(), DiscoverInput{BaseURL: srv.URL})
		srv.Close()
		if err == nil {
			t.Fatalf("HTTP %d 必须报错", c.status)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("HTTP %d 的错误 %q 不含 %q", c.status, err.Error(), c.want)
		}
	}
}

func TestDiscoverRejectsBadInput(t *testing.T) {
	s := testService(nil)
	cases := []DiscoverInput{
		{BaseURL: ""},
		{BaseURL: "api.deepseek.com"},    // 缺 scheme
		{BaseURL: "ftp://x.example.com"}, // 非 http(s)
		{BaseURL: "https://x.example.com", Format: "google"},
	}
	for _, in := range cases {
		if _, err := s.Discover(context.Background(), in); err == nil {
			t.Errorf("输入 %+v 必须被拒", in)
		}
	}
}

func TestDiscoverRejectsNonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><html>登录页</html>"))
	}))
	defer srv.Close()

	s := testService(nil)
	if _, err := s.Discover(context.Background(), DiscoverInput{BaseURL: srv.URL}); err == nil {
		t.Fatal("非 JSON 响应必须报错（否则会静默得到空清单）")
	}
}
