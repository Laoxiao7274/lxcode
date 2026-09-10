package wsclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/myt-harness/internal/config"
	"github.com/moyunteng/myt-harness/internal/protocol"
	"github.com/moyunteng/myt-harness/internal/server"
)

// wsConn 是测试用的服务端连接封装：读请求、发响应/事件。
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsConn) readRequest() (protocol.Request, bool) {
	var req protocol.Request
	if err := c.conn.ReadJSON(&req); err != nil {
		return req, false
	}
	return req, true
}

func (c *wsConn) send(v any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v) == nil
}

// startFakeServer 起一个进程内 WS 服务端并返回地址（host:port），
// handler 拿到达的连接自行编排。
func startFakeServer(t *testing.T, handler func(*wsConn)) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		handler(&wsConn{conn: conn})
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// startRealBackend 起真实的 server.Server（临时注册表 + 一个模型），返回地址。
func startRealBackend(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "models.json")
	reg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		ContextWindow: 8192, MaxOutputTokens: 1024,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(protocol.Path, server.NewServer(reg).Handler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "http://")
}

func dialTest(t *testing.T, addr string) Backend {
	t.Helper()
	be, err := Dial(addr)
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	t.Cleanup(be.Close)
	return be
}

// waitForEvent 在超时内等待某个事件名，期间的其它事件（如 ready）跳过。
func waitForEvent(t *testing.T, be Backend, name string, timeout time.Duration) (protocol.Response, bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-be.Events():
			if !ok {
				return protocol.Response{}, false
			}
			if ev.Method == name {
				return ev, true
			}
		case <-deadline:
			return protocol.Response{}, false
		}
	}
}

// drainUntilClosed 等到事件 channel 关闭（连接断开被 readLoop 感知）。
func drainUntilClosed(be Backend, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-be.Events():
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// TestCallOverRealBackend：与真实 server.Server 对接——Dial 的真实路径
// （连接建立、ready 事件、请求-应答都在这里跑通）。
func TestCallOverRealBackend(t *testing.T) {
	be := dialTest(t, startRealBackend(t))

	ev, ok := waitForEvent(t, be, protocol.EventReady, 3*time.Second)
	if !ok {
		t.Fatal("未收到 connection.ready")
	}
	var hello protocol.HelloResult
	if b, err := json.Marshal(ev.Params); err != nil {
		t.Fatalf("ready 载荷序列化失败: %v", err)
	} else if err := json.Unmarshal(b, &hello); err != nil {
		t.Fatalf("ready 载荷解析失败: %v", err)
	}
	if hello.Server != "myt-harness" || hello.Version != protocol.Version {
		t.Fatalf("ready 载荷不符: %+v", hello)
	}

	var list protocol.ModelListResult
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := be.Call(ctx, protocol.MethodModelList, nil, &list); err != nil {
		t.Fatalf("model.list 失败: %v", err)
	}
	if len(list.Models) == 0 {
		t.Fatalf("模型列表为空（桩注册表应有一个模型）: %+v", list)
	}
	if list.Roles[config.RoleDefault] != "m1" {
		t.Fatalf("角色绑定未同步: %+v", list.Roles)
	}
}

// TestDialAndCallPairing：请求按 id 与应答配对，params 完整送达服务端。
func TestDialAndCallPairing(t *testing.T) {
	var gotMu sync.Mutex
	var gotReq protocol.Request
	addr := startFakeServer(t, func(c *wsConn) {
		req, ok := c.readRequest()
		if !ok {
			return
		}
		gotMu.Lock()
		gotReq = req
		gotMu.Unlock()
		c.send(protocol.NewResult(req.ID, map[string]any{"pong": true}))
		for {
			if _, ok := c.readRequest(); !ok {
				return
			}
		}
	})
	be := dialTest(t, addr)

	var out struct {
		Pong bool `json:"pong"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := be.Call(ctx, protocol.MethodHello, protocol.HelloParams{Client: "cli", Version: protocol.Version}, &out); err != nil {
		t.Fatalf("Call 失败: %v", err)
	}
	if !out.Pong {
		t.Fatalf("应答未解到 result: %+v", out)
	}
	gotMu.Lock()
	defer gotMu.Unlock()
	if gotReq.Method != protocol.MethodHello || gotReq.JSONRPC != "2.0" {
		t.Fatalf("服务端收到的请求不符: %+v", gotReq)
	}
	var p protocol.HelloParams
	if err := json.Unmarshal(gotReq.Params, &p); err != nil || p.Client != "cli" {
		t.Fatalf("params 未送达: %s (%v)", gotReq.Params, err)
	}
}

// TestCallErrorResponse：服务端 error 帧要变成 Call 的错误返回，且保留错误码。
func TestCallErrorResponse(t *testing.T) {
	addr := startFakeServer(t, func(c *wsConn) {
		req, ok := c.readRequest()
		if !ok {
			return
		}
		c.send(protocol.NewError(req.ID, protocol.CodeBusy, "会话正在生成中"))
	})
	be := dialTest(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := be.Call(ctx, protocol.MethodChatSend, protocol.ChatSendParams{Text: "x"}, nil)
	if err == nil {
		t.Fatal("错误应答应返回 error")
	}
	if !strings.Contains(err.Error(), "会话正在生成中") {
		t.Fatalf("错误信息未透传: %v", err)
	}
	// 调用方要能按错误码分支（CLI 用 1003 判断"忙"）
	rpcErr, ok := err.(*protocol.Error)
	if !ok {
		t.Fatalf("应返回 *protocol.Error 以便调用方按码分支, got %T", err)
	}
	if rpcErr.Code != protocol.CodeBusy {
		t.Fatalf("错误码未透传: %d", rpcErr.Code)
	}
}

// TestCallFailsFastOnClosedConnection：连接断开后 Call 必须立即失败，
// 而不是把客户端永久挂住（后端崩溃/被重启时就是这个路径）。
func TestCallFailsFastOnClosedConnection(t *testing.T) {
	addr := startFakeServer(t, func(c *wsConn) {
		c.conn.Close() // 一上来就断
	})
	be := dialTest(t, addr)

	if !drainUntilClosed(be, 3*time.Second) {
		t.Fatal("连接断开后事件 channel 未关闭")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := be.Call(ctx, protocol.MethodModelList, nil, nil)
	if err == nil {
		t.Fatal("连接断开后 Call 应返回错误")
	}
	if !strings.Contains(err.Error(), "断开") {
		t.Fatalf("断连错误信息应可读: %v", err)
	}
}

// TestCallContextCancel：ctx 取消要能打断等待（CLI 的取消语义）。
func TestCallContextCancel(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	addr := startFakeServer(t, func(c *wsConn) {
		if _, ok := c.readRequest(); !ok {
			return
		}
		<-release // 故意不应答
		c.conn.Close()
	})
	be := dialTest(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := be.Call(ctx, protocol.MethodChatSend, protocol.ChatSendParams{Text: "x"}, nil); err == nil {
		t.Fatal("ctx 取消应返回错误")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("ctx 取消未及时生效，耗时 %v", time.Since(start))
	}
}

// TestEventsRouteByIdlessFrames：无 id 的帧才是事件；带 id 的帧不得混进事件流。
// 混淆这两者会让客户端把应答当事件渲染，或把事件当应答吞掉。
func TestEventsRouteByIdlessFrames(t *testing.T) {
	addr := startFakeServer(t, func(c *wsConn) {
		req, ok := c.readRequest()
		if !ok {
			return
		}
		c.send(protocol.NewEvent(protocol.EventBusy, protocol.BusyParams{Busy: true}))
		c.send(protocol.NewResult(req.ID, map[string]any{}))
		c.send(protocol.NewEvent(protocol.EventBusy, protocol.BusyParams{Busy: false}))
		for {
			if _, ok := c.readRequest(); !ok {
				return
			}
		}
	})
	be := dialTest(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := be.Call(ctx, protocol.MethodChatCancel, nil, nil); err != nil {
		t.Fatalf("Call 失败: %v", err)
	}

	seen := 0
	deadline := time.After(2 * time.Second)
	for seen < 2 {
		select {
		case ev, ok := <-be.Events():
			if !ok {
				t.Fatal("事件流提前关闭")
			}
			if ev.Method == "" {
				t.Fatalf("应答帧混进了事件流: %+v", ev)
			}
			seen++
		case <-deadline:
			t.Fatalf("只收到 %d 个事件（期望 2）", seen)
		}
	}
}

// TestEventWithNonNumericIDIsIgnored：id 不是数字的应答帧要跳过而不是崩溃
// （协议保留 id 为任意 JSON 值，服务端只发数字）。
func TestEventWithNonNumericIDIsIgnored(t *testing.T) {
	addr := startFakeServer(t, func(c *wsConn) {
		req, ok := c.readRequest()
		if !ok {
			return
		}
		// 怪异帧：id 是字符串（不是数字）
		c.send(map[string]any{"jsonrpc": "2.0", "id": "not-a-number", "result": map[string]any{}})
		// 真正的应答必须仍能正确配对
		c.send(protocol.NewResult(req.ID, map[string]any{"ok": true}))
		for {
			if _, ok := c.readRequest(); !ok {
				return
			}
		}
	})
	be := dialTest(t, addr)

	var out struct {
		OK bool `json:"ok"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := be.Call(ctx, protocol.MethodModelList, nil, &out); err != nil {
		t.Fatalf("怪异帧不应影响正常应答配对: %v", err)
	}
	if !out.OK {
		t.Fatalf("应答未解到: %+v", out)
	}
}

// TestEventsDropOldestWhenFull：事件缓冲满时丢最旧而不是阻塞读循环——
// 渲染慢不能反过来卡住后端连接（否则一次长回复就能把客户端拖死）。
func TestEventsDropOldestWhenFull(t *testing.T) {
	const burst = 200 // > events channel 容量 64
	addr := startFakeServer(t, func(c *wsConn) {
		// 注意：不能先 readRequest 等客户端发消息——本测试的客户端只连不发，
		// 服务端一上来就得灌事件，否则什么都不会发生
		for i := 0; i < burst; i++ {
			if !c.send(protocol.NewEvent(protocol.EventDelta, protocol.DeltaParams{Kind: "text", Text: "x"})) {
				return
			}
		}
		c.send(protocol.NewEvent(protocol.EventDone, protocol.DoneParams{FinishReason: "stop"}))
		for {
			if _, ok := c.readRequest(); !ok {
				return
			}
		}
	})
	be := dialTest(t, addr)

	// 故意不读事件，让缓冲先被灌满
	time.Sleep(300 * time.Millisecond)

	// 之后仍能收到新事件（done 在最后）——说明读循环没被写阻塞住
	if _, ok := waitForEvent(t, be, protocol.EventDone, 3*time.Second); !ok {
		t.Fatal("缓冲满后读循环卡死，后续事件丢失")
	}
}

// TestDialFailure：后端未监听时给出可读错误（客户端据此提示用户怎么启动服务）。
func TestDialFailure(t *testing.T) {
	if _, err := Dial("127.0.0.1:1"); err == nil {
		t.Fatal("连接未监听的地址应返回错误")
	} else if !strings.Contains(err.Error(), "连接后端") {
		t.Fatalf("错误信息应说明是连接后端失败: %v", err)
	}
}

// TestCloseIdempotent：Close 可被多次调用（readLoop 的 defer 与外部调用会撞上）。
func TestCloseIdempotent(t *testing.T) {
	addr := startFakeServer(t, func(c *wsConn) {
		for {
			if _, ok := c.readRequest(); !ok {
				return
			}
		}
	})
	be := dialTest(t, addr)
	be.Close()
	be.Close() // 不应 panic（close of closed channel）
}
