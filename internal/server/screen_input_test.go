// screen_input_test.go —— 投屏控制（screen.input）的安全门 / 队列 / 长轮询测试。
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// inputReq 构造一条 screen.input 请求。
func inputReq(id string, body string) *protocol.Request {
	return &protocol.Request{ID: json.RawMessage(id), Method: protocol.MethodScreenInput,
		Params: json.RawMessage(body)}
}

// attachAllowControl 写一份允许控制的 remote.json（复用 remote_test.go 的
// attachRemote/writeRemote 辅助）。
func attachAllowControl(t *testing.T, s *Server, allow bool) {
	t.Helper()
	writeRemote(t, attachRemote(t, s), config.RemoteAccess{AllowControl: allow})
}

// TestScreenInputRequiresControlSwitch 开关关（默认态）：一律回
// CodeScreenControlDisabled——「允许控制」未显式授权前手机只能看。
func TestScreenInputRequiresControlSwitch(t *testing.T) {
	s := newScreenTestServer(t)
	s.shell.report(fakeShellState(true))
	c := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	s.shell.setReceiver(c)

	// 未装配 remote.json（默认态）→ 拒绝
	resp := s.dispatch(c, inputReq(`1`, `{"type":"click","x":0.5,"y":0.5}`))
	if resp.Error == nil || resp.Error.Code != protocol.CodeScreenControlDisabled {
		t.Fatalf("默认态应回 CodeScreenControlDisabled: %+v", resp.Error)
	}

	// 显式关 → 拒绝
	attachAllowControl(t, s, false)
	resp = s.dispatch(c, inputReq(`2`, `{"type":"click","x":0.5,"y":0.5}`))
	if resp.Error == nil || resp.Error.Code != protocol.CodeScreenControlDisabled {
		t.Fatalf("显式关闭应回 CodeScreenControlDisabled: %+v", resp.Error)
	}
}

// TestScreenInputReceiverOnly 安全门：只有当前接收者可入队；接收者停止投屏后
// 立即失权。
func TestScreenInputReceiverOnly(t *testing.T) {
	s := newScreenTestServer(t)
	attachAllowControl(t, s, true)
	s.shell.report(fakeShellState(true))

	rx := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	other := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[rx] = struct{}{}
	s.clients[other] = struct{}{}
	s.mu.Unlock()
	s.shell.setReceiver(rx)

	// 非接收者 → 拒绝
	resp := s.dispatch(other, inputReq(`1`, `{"type":"click","x":0.5,"y":0.5}`))
	if resp.Error == nil {
		t.Fatal("非接收者应被拒绝")
	}
	// 接收者 → 入队成功
	resp = s.dispatch(rx, inputReq(`2`, `{"type":"click","x":0.5,"y":0.5}`))
	if resp.Error != nil {
		t.Fatalf("接收者应入队成功: %+v", resp.Error)
	}
	if got := s.shell.takeInputs(0); len(got) != 1 || got[0].Type != "click" {
		t.Fatalf("队列内容不对: %+v", got)
	}
	// 停止投屏（清接收者）→ 立即失权
	resp = s.dispatch(rx, inputReq(`3`, `{"type":"click","x":0.5,"y":0.5}`))
	if resp.Error != nil {
		t.Fatalf("此时接收者仍在: %+v", resp.Error)
	}
	s.shell.clearReceiver(rx)
	resp = s.dispatch(rx, inputReq(`4`, `{"type":"click","x":0.5,"y":0.5}`))
	if resp.Error == nil {
		t.Fatal("接收者清除后应失权")
	}
}

// TestScreenInputValidation 畸形参数在入口拒掉（不进队列）。
func TestScreenInputValidation(t *testing.T) {
	s := newScreenTestServer(t)
	attachAllowControl(t, s, true)
	s.shell.report(fakeShellState(true))
	c := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	s.shell.setReceiver(c)

	for _, body := range []string{
		`{"type":"hover","x":0.5}`,               // 未知类型
		`{"type":"click","x":1.5,"y":0.5}`,       // x 越界
		`{"type":"move","x":0.5,"y":-0.1}`,       // y 越界
		`{"type":"click","x":0.5,"y":0.5,"button":"mega"}`, // 未知按键
		`{"type":"scroll"}`,                      // dx/dy 全 0
		`{"type":"key"}`,                         // key 空
		`{"type":"key","key":"a","modifiers":["super"]}`, // 未知修饰键
		`{"type":"char"}`,                        // text 空
	} {
		resp := s.dispatch(c, inputReq(`1`, body))
		if resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
			t.Fatalf("畸形参数应回 CodeInvalidParams: %s → %+v", body, resp.Error)
		}
	}
	if got := s.shell.takeInputs(0); len(got) != 0 {
		t.Fatalf("畸形事件不应入队: %+v", got)
	}
}

// TestScreenInputQueueDropOldest 队列满丢最旧（move 时序语义）。
func TestScreenInputQueueDropOldest(t *testing.T) {
	s := newScreenTestServer(t)
	attachAllowControl(t, s, true)
	c := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	s.shell.setReceiver(c)
	for i := 0; i < inputQueueCap+10; i++ {
		if !s.shell.enqueueInput(clientKey(c), protocol.ScreenInputParams{Type: "move", X: float64(i) / 100}) {
			t.Fatalf("第 %d 条入队失败", i)
		}
	}
	got := s.shell.takeInputs(0)
	if len(got) != inputQueueCap {
		t.Fatalf("应恰好 inputQueueCap 条: %d", len(got))
	}
	// 丢的是最旧：首条应是第 10 条（i=10）
	if got[0].X != 0.10 {
		t.Fatalf("首条应是丢最旧后的第 10 条: %v", got[0].X)
	}
}

// TestScreenInputReceiverChangeDropsQueue 换接收者/清接收者时清队列（防错投）。
func TestScreenInputReceiverChangeDropsQueue(t *testing.T) {
	s := newScreenTestServer(t)
	attachAllowControl(t, s, true)
	rx := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[rx] = struct{}{}
	s.mu.Unlock()
	s.shell.setReceiver(rx)
	s.shell.enqueueInput(clientKey(rx), protocol.ScreenInputParams{Type: "move", X: 0.5})
	s.shell.setReceiver(nil) // 换人
	if got := s.shell.takeInputs(0); len(got) != 0 {
		t.Fatalf("换接收者应清队列: %+v", got)
	}
}

// TestScreenInputLongPoll 长轮询：无事件 hold 到超时空回；有事件立即返回并取空队列。
func TestScreenInputLongPoll(t *testing.T) {
	s := newScreenTestServer(t)
	attachAllowControl(t, s, true)

	// 无事件 + wait=1：hold 上限 ~900ms（不能永久阻塞）
	start := time.Now()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/screen/input?wait=1", nil)
	req.RemoteAddr = "127.0.0.1:51000" // httptest 默认 192.0.2.1（非回环）——screenGate 403
	s.handleScreenInput(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("空回应 200: %d", w.Code)
	}
	if el := time.Since(start); el < 800*time.Millisecond {
		t.Fatalf("无事件应 hold 到超时（~900ms）: %v", el)
	}
	var out struct {
		Events []protocol.ScreenInputParams `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("空响应不是合法 JSON: %v", err)
	}
	if len(out.Events) != 0 {
		t.Fatalf("空响应应含空数组: %+v", out.Events)
	}

	// 入队后：立即返回（不等满 hold）且队列取空
	c := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	s.shell.setReceiver(c)
	s.shell.enqueueInput(clientKey(c), protocol.ScreenInputParams{Type: "click", X: 0.25, Y: 0.75})
	start = time.Now()
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/screen/input?wait=1", nil)
	req.RemoteAddr = "127.0.0.1:51000"
	s.handleScreenInput(w, req)
	if el := time.Since(start); el > 200*time.Millisecond {
		t.Fatalf("有事件应立即返回: %v", el)
	}
	out = struct {
		Events []protocol.ScreenInputParams `json:"events"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Events) != 1 || out.Events[0].Type != "click" || out.Events[0].X != 0.25 {
		t.Fatalf("事件内容不对: %+v", out.Events)
	}
	// 再取：已空
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/screen/input", nil)
	req.RemoteAddr = "127.0.0.1:51000"
	s.handleScreenInput(w, req)
	out = struct {
		Events []protocol.ScreenInputParams `json:"events"`
	}{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Events) != 0 {
		t.Fatalf("取过一次后应空: %+v", out.Events)
	}
}

// TestScreenInputEndpointGate /screen/input 端点同样过 screenGate（未开远程门时
// 回环放行——与 /screen/ctl 同语义）。
func TestScreenInputEndpointGate(t *testing.T) {
	s := newScreenTestServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/screen/input", nil)
	req.RemoteAddr = "127.0.0.1:51000"
	s.handleScreenInput(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("未开远程门时回环请求不应 401: %d", w.Code)
	}
}
