// listener_test.go —— 监听地址热切换（remote.json Enabled 翻转 → 0.0.0.0 ↔ 回环）。
// 测试用本机非回环地址模拟「另一台电脑」：非回环目标连不上回环监听，连得上
// 0.0.0.0 监听——与跨机场景同构（同一份 TCP 语义），不依赖真实第二台机器。
package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// httptestServer 起 routes() 的测试服务（t.Cleanup 收尾）。
func httptestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

// freePort 找一个空闲回环端口（先听后关——有小概率被抢，测试端口自选足够）。
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// lanIP 返回本机第一个非回环 IPv4（没有就跳过测试——纯回环环境无从验证）。
func lanIP(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	t.Skip("本机无非回环 IPv4，跳过")
	return ""
}

// dialRaw 裸 TCP 拨号（含超时）：err == nil = 端口可达。
func dialRaw(addr string) error {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	_ = c.Close()
	return nil
}

// wsHello 走完整 WS 升级（带 query）：返回 HTTP 状态码（0 = 升级成功）。
func wsHello(t *testing.T, addr, query string) int {
	t.Helper()
	url := "ws://" + addr + protocol.Path + query
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		if resp == nil {
			return -1
		}
		return resp.StatusCode
	}
	_ = conn.Close()
	return 0
}

// desiredBindAddr：Enabled 决定通配与否；显式通配与坏地址原样返回。
func TestDesiredBindAddr(t *testing.T) {
	cases := []struct {
		addr    string
		enabled bool
		want    string
	}{
		{"127.0.0.1:7789", false, "127.0.0.1:7789"},
		{"127.0.0.1:7789", true, "0.0.0.0:7789"},
		{"0.0.0.0:7789", true, "0.0.0.0:7789"}, // 显式通配：尊重配置
		{":7789", true, ":7789"},               // 空 host 已是通配
		{"[::1]:7789", true, "0.0.0.0:7789"},   // IPv6 回环同样切通配
		{"no-port", true, "no-port"},           // 坏地址原样返回（启动时net.Listen 报错）
	}
	for _, c := range cases {
		if got := desiredBindAddr(c.addr, c.enabled); got != c.want {
			t.Errorf("desiredBindAddr(%q, %v) = %q, 期望 %q", c.addr, c.enabled, got, c.want)
		}
	}
}

// 热切换全链路：关闭 = 回环-only 且局域网拒连；开启 = 局域网可达且 token 门生效、
// 既有连接不断；再关闭 = 局域网立即拒连。回环全程可用。
func TestRunHotSwitchBind(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	path := attachRemote(t, srv)
	oldInterval := remoteWatchInterval
	remoteWatchInterval = 20 * time.Millisecond
	t.Cleanup(func() { remoteWatchInterval = oldInterval })

	lan := lanIP(t)
	port := freePort(t)
	loop := "127.0.0.1:" + port
	lanAddr := lan + ":" + port

	writeRemote(t, path, config.RemoteAccess{Enabled: false})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx, loop) }()

	// 等回环监听就绪
	deadline := time.Now().Add(5 * time.Second)
	for dialRaw(loop) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("回环监听未就绪: %v", <-runErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := dialRaw(lanAddr); err == nil {
		t.Fatal("未启用远程时局域网地址不应可达")
	}

	// 开启（写文件触发热加载）：局域网可达 + token 门生效
	writeRemote(t, path, config.RemoteAccess{Enabled: true, Token: "tok-x"})
	deadline = time.Now().Add(5 * time.Second)
	for {
		err := dialRaw(lanAddr)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("开启后局域网地址不可达: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code := wsHello(t, lanAddr, ""); code != http.StatusUnauthorized {
		t.Fatalf("开启后无 token 状态码 = %d，期望 401", code)
	}
	if code := wsHello(t, lanAddr, "?token=tok-x"); code != 0 {
		t.Fatalf("开启后带 token 应升级成功，状态码 = %d", code)
	}
	// 回环在开启后仍可用（token 门对回环一视同仁——隧道从回环进来）
	if code := wsHello(t, loop, "?token=tok-x"); code != 0 {
		t.Fatalf("开启后回环带 token 应升级成功，状态码 = %d", code)
	}

	// 既有连接跨翻转存活（关旧 listener 只停接受新连接）——此时远程已开启，
	// 升级按门的要求带 token。
	old, resp := dialUpgrade(t, srv, "?token=tok-x")
	if old == nil {
		t.Fatalf("httptest 升级失败: HTTP %d", resp.StatusCode)
	}
	_ = old.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))

	// 关闭：局域网立即拒连，回环仍可用
	writeRemote(t, path, config.RemoteAccess{Enabled: false, Token: "tok-x"})
	deadline = time.Now().Add(5 * time.Second)
	for {
		err := dialRaw(lanAddr)
		if err == nil {
			if time.Now().After(deadline) {
				t.Fatal("关闭后局域网地址仍可达")
			}
		} else {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := dialRaw(loop); err != nil {
		t.Fatalf("关闭后回环应仍可用: %v", err)
	}
	if code := wsHello(t, loop, ""); code != 0 {
		t.Fatalf("关闭后回环无 token 应升级成功，状态码 = %d", code)
	}

	// 翻转期间建立的 httptest 连接未被打断（两条监听互不干扰）
	_ = old.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("优雅退出应返回 nil: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后 Run 未返回")
	}
}

// 附件端点与 /rpc 同一门：启用后无 token 401、带 token 过门（到 404 的正常路径）。
func TestAttachmentsTokenGate(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	path := attachRemote(t, srv)
	ts := httptestServer(t, srv.routes())

	writeRemote(t, path, config.RemoteAccess{Enabled: false})
	resp, err := http.Get(ts.URL + "/attachments/00000000000000000000000000000000/x.png")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("未启用时附件端点不应要求 token")
	}

	writeRemote(t, path, config.RemoteAccess{Enabled: true, Token: "tok-a"})
	resp, err = http.Get(ts.URL + "/attachments/00000000000000000000000000000000/x.png")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("启用后无 token 状态码 = %d，期望 401", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/attachments/00000000000000000000000000000000/x.png", nil)
	req.Header.Set("Authorization", "Bearer tok-a")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("启用后带 token 应过门（文件不存在走 404 的正常路径）")
	}
}
