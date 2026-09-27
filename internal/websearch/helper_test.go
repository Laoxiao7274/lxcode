package websearch

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// captured 是一次被捕获的请求（适配器测试用来断言「请求长什么样」）。
type captured struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// captureServer 起一个测试服务器：记录收到的请求，按给定状态码与响应体作答。
//
// 适配器测试的核心手段——不依赖真渠道（测试不能花钱、不能联网），
// 只钉住「我们发出去的请求」与「我们把响应映射成什么」这两件事。
func captureServer(t *testing.T, status int, body string) (*httptest.Server, *captured) {
	t.Helper()
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.Method = r.Method
		cap.Path = r.URL.Path
		cap.Query = r.URL.Query()
		cap.Header = r.Header.Clone()
		cap.Body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

// decodeBody 把捕获的请求体解成 map（断言字段用）。
func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v（原文: %s）", err, raw)
	}
	return m
}

// assertStatus 断言错误里带的 HTTP 状态码与分类。
func assertProviderError(t *testing.T, err error, wantKind ErrorKind, wantStatus int) *ProviderError {
	t.Helper()
	if err == nil {
		t.Fatalf("期望报错，实际成功")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("期望 *ProviderError，实际 %T: %v", err, err)
	}
	if pe.Kind != wantKind {
		t.Errorf("错误分类 = %q，期望 %q（%v）", pe.Kind, wantKind, err)
	}
	if wantStatus != 0 && pe.Status != wantStatus {
		t.Errorf("状态码 = %d，期望 %d", pe.Status, wantStatus)
	}
	return pe
}
