package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// attachRemote 装配一个临时 remote.json 并返回其路径（测试里改开关用）。
func attachRemote(t *testing.T, srv *Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote.json")
	srv.AttachRemoteAccess(path)
	return path
}

func writeRemote(t *testing.T, path string, ra config.RemoteAccess) {
	t.Helper()
	if err := config.SaveRemoteAccess(path, ra); err != nil {
		t.Fatal(err)
	}
}

// dialUpgrade 直连升级 URL：成功返回连接（调用方负责关闭），失败返回 HTTP 响应。
func dialUpgrade(t *testing.T, srv *Server, query string) (*websocket.Conn, *http.Response) {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + protocol.Path + query
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		if resp == nil {
			t.Fatalf("升级失败且无 HTTP 响应: %v", err)
		}
		return nil, resp
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, nil
}

// Token 门：未启用时无凭证也放行；启用后无/错 token 401，对 token 放行。
func TestRemoteTokenGate(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	path := attachRemote(t, srv)

	// 未启用：无 token 也放行（旧版行为）
	conn, resp := dialUpgrade(t, srv, "")
	if conn == nil {
		t.Fatalf("未启用时不应拒绝连接: HTTP %d", resp.StatusCode)
	}

	// 启用但 token 为空：门对空 token 直接拒绝（自动生成发生在管理端点
	// 的开启路径——见 TestRemoteAccessLocalEndpoint）
	writeRemote(t, path, config.RemoteAccess{Enabled: true, Token: "tok-abc"})

	if conn, resp := dialUpgrade(t, srv, ""); conn != nil {
		t.Fatal("启用后无 token 必须拒绝")
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token 状态码 = %d，期望 401", resp.StatusCode)
	}
	if conn, resp := dialUpgrade(t, srv, "?token=wrong"); conn != nil {
		t.Fatal("启用后错误 token 必须拒绝")
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误 token 状态码 = %d，期望 401", resp.StatusCode)
	}
	if conn, _ := dialUpgrade(t, srv, "?token=tok-abc"); conn == nil {
		t.Fatal("正确 token 应放行")
	}
	// Bearer 头同效
	ts2 := httptest.NewServer(srv.Handler())
	t.Cleanup(ts2.Close)
	header := http.Header{"Authorization": {"Bearer tok-abc"}}
	conn2, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts2.URL, "http")+protocol.Path, header)
	if err != nil {
		t.Fatalf("Bearer 头应放行: %v", err)
	}
	_ = conn2.Close()
}

// 管理端点：GET/POST 回环可用；开启自动生成 token；轮换换新；关闭保留 token。
func TestRemoteAccessLocalEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	path := attachRemote(t, srv)
	ts := httptest.NewServer(http.HandlerFunc(srv.handleRemoteAccess))
	t.Cleanup(ts.Close)

	post := func(body string) config.RemoteAccess {
		t.Helper()
		resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		var ra config.RemoteAccess
		if err := json.Unmarshal(data, &ra); err != nil {
			t.Fatalf("响应不是合法 JSON: %s", string(data))
		}
		return ra
	}

	if ra := post(`{"enabled": true}`); !ra.Enabled || ra.Token == "" {
		t.Fatalf("开启应自动生成 token: %+v", ra)
	}
	ra := config.LoadRemoteAccess(path)
	if !ra.Enabled || ra.Token == "" {
		t.Fatalf("开启应落盘: %+v", ra)
	}

	if ra2 := post(`{"rotate": true}`); ra2.Token == "" || ra2.Token == ra.Token {
		t.Fatalf("轮换应产生新 token: old=%s new=%s", ra.Token, ra2.Token)
	}

	if ra3 := post(`{"enabled": false}`); ra3.Enabled {
		t.Fatal("关闭应落盘")
	}
	ra4 := config.LoadRemoteAccess(path)
	if ra4.Enabled || ra4.Token != config.LoadRemoteAccess(path).Token {
		t.Fatal("关闭应保留 token（再次开启不换新）")
	}
}

func TestIsLoopback(t *testing.T) {
	mk := func(remote string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		return r
	}
	if !isLoopback(mk("127.0.0.1:52341")) {
		t.Error("127.0.0.1 应视为回环")
	}
	if !isLoopback(mk("[::1]:52341")) {
		t.Error("::1 应视为回环")
	}
	if isLoopback(mk("192.168.1.10:52341")) {
		t.Error("局域网地址不应视为回环")
	}
	if isLoopback(mk("bogus")) {
		t.Error("坏地址不应视为回环")
	}
}
