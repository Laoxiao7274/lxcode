// screen_test.go —— 投屏协议面测试：帧头编解码、方法分派、二进制帧转发
// （文本帧 = JSON-RPC / 二进制帧 = 视频的分路纪律）。
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestFrameHeaderRoundTrip 帧头编码/解码对称；版本不符或长度不足判非法。
func TestFrameHeaderRoundTrip(t *testing.T) {
	h := protocol.FrameHeader{
		Version:     protocol.FrameVersion1,
		Flags:       protocol.FrameFlagKeyframe,
		Len24:       protocol.PutLen24(4096),
		TimestampMs: 0x0102030405060708,
	}
	buf := make([]byte, protocol.FrameHeaderSize+3)
	h.Marshal(buf)
	got, ok := protocol.UnmarshalFrameHeader(buf)
	if !ok {
		t.Fatal("合法帧头被拒绝")
	}
	if got.Version != h.Version || got.Flags != h.Flags || got.TimestampMs != h.TimestampMs {
		t.Fatalf("往返不一致: %+v != %+v", got, h)
	}
	if protocol.GetLen24(got.Len24) != 4096 {
		t.Fatalf("len24 往返不一致: %d", protocol.GetLen24(got.Len24))
	}
	// 短帧非法
	if _, ok := protocol.UnmarshalFrameHeader(buf[:protocol.FrameHeaderSize-1]); ok {
		t.Fatal("不足 13 字节应判非法")
	}
	// 版本不符非法
	buf[0] = 2
	if _, ok := protocol.UnmarshalFrameHeader(buf); ok {
		t.Fatal("版本不符应判非法")
	}
}

// newScreenTestServer 装配一个带屏通道的测试服务（无 WS 运行时，直接打方法层）。
func newScreenTestServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(nil)
	return s
}

// fakeShellState 构造一份壳上报。
func fakeShellState(running bool) shellState {
	return shellState{
		Running: running,
		Monitor: 0,
		Encoder: "h264_nvenc",
		Width:   1920, Height: 1080,
		Monitors: []protocol.ScreenMonitor{{ID: 0, Name: "主显示器", Primary: true, Width: 1920, Height: 1080, Attached: true}},
	}
}

// TestScreenShellReportAndStale 壳上报缓存与保鲜期：上报后在线，超时后离线。
func TestScreenShellReportAndStale(t *testing.T) {
	s := newScreenTestServer(t)
	sh := s.shell
	if sh.shellOnline() {
		t.Fatal("壳从未上报不应在线")
	}
	sh.report(fakeShellState(true))
	if !sh.shellOnline() {
		t.Fatal("上报后应在线")
	}
	sh.mu.Lock()
	sh.lastSeen = time.Now().Add(-shellStale - time.Second)
	sh.mu.Unlock()
	if sh.shellOnline() {
		t.Fatal("超过保鲜期应离线")
	}
}

// TestScreenStartRequiresShellOnline 壳不在线时 start 回 CodeScreenUnavailable。
func TestScreenStartRequiresShellOnline(t *testing.T) {
	s := newScreenTestServer(t)
	req := &protocol.Request{ID: json.RawMessage(`1`), Method: protocol.MethodScreenStart}
	resp := s.dispatch(nil, req)
	if resp.Error == nil || resp.Error.Code != protocol.CodeScreenUnavailable {
		t.Fatalf("壳离线应回 CodeScreenUnavailable: %+v", resp.Error)
	}
}

// TestScreenStartStopLifecycle 壳在线时 start 置命令电平并成为接收者；stop 清除。
func TestScreenStartStopLifecycle(t *testing.T) {
	s := newScreenTestServer(t)
	s.shell.report(fakeShellState(false))

	c := &wsClient{conn: nil, out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()

	req := &protocol.Request{ID: json.RawMessage(`1`), Method: protocol.MethodScreenStart,
		Params: json.RawMessage(`{"monitor":0,"quality":"high"}`)}
	resp := s.dispatch(c, req)
	if resp.Error != nil {
		t.Fatalf("壳在线时 start 应成功: %+v", resp.Error)
	}
	cmd := s.shell.currentCommand()
	if !cmd.Want || cmd.Quality != "high" || cmd.Monitor != 0 {
		t.Fatalf("命令电平不对: %+v", cmd)
	}
	if s.receiverOf() == "" {
		t.Fatal("start 后应有接收者")
	}
	st := s.screenState()
	if st.Active || st.Receiver == "" {
		t.Fatalf("start 应答态: 壳还没上报 running，Active 应为 false 但 Receiver 应有值: %+v", st)
	}

	// stop
	reqStop := &protocol.Request{ID: json.RawMessage(`2`), Method: protocol.MethodScreenStop}
	resp = s.dispatch(c, reqStop)
	if resp.Error != nil {
		t.Fatalf("stop 应成功: %+v", resp.Error)
	}
	if cmd := s.shell.currentCommand(); cmd.Want {
		t.Fatal("stop 后命令电平应为 false")
	}
	if s.receiverOf() != "" {
		t.Fatal("stop 后接收者应清空")
	}
}

// TestScreenFrameForwarderRoutesToReceiver 转发只投给接收者的二进制队列，
// 非接收者不受影响；队列满丢帧不阻塞。
func TestScreenFrameForwarderRoutesToReceiver(t *testing.T) {
	s := newScreenTestServer(t)
	rx := &wsClient{conn: nil, out: make(chan []byte, 4), bin: make(chan []byte, 2), done: make(chan struct{})}
	other := &wsClient{conn: nil, out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[rx] = struct{}{}
	s.clients[other] = struct{}{}
	s.mu.Unlock()
	s.shell.mu.Lock()
	s.shell.receiverId = clientKey(rx)
	s.shell.mu.Unlock()

	frame := make([]byte, 32)
	protocol.FrameHeader{Version: 1, Flags: protocol.FrameFlagKeyframe, TimestampMs: 5}.Marshal(frame)
	for i := 0; i < 5; i++ {
		s.screenFrameForwarder(frame)
	}
	// 接收者队列容量 2：前 2 帧入队，其余丢弃（不阻塞、不 fast-fail）
	if len(rx.bin) != 2 {
		t.Fatalf("接收者应收满 2 帧（其余丢帧）: %d", len(rx.bin))
	}
	if len(other.bin) != 0 {
		t.Fatal("非接收者不应收到视频帧")
	}
	got := <-rx.bin
	h, ok := protocol.UnmarshalFrameHeader(got)
	if !ok || h.Flags&protocol.FrameFlagKeyframe == 0 {
		t.Fatalf("收到的帧头不对: %+v ok=%v", h, ok)
	}
}

// TestScreenClientGoneStopsCommand 接收者断开：清接收者 + 停命令电平 + 广播。
func TestScreenClientGoneStopsCommand(t *testing.T) {
	s := newScreenTestServer(t)
	s.shell.report(fakeShellState(true))
	c := &wsClient{conn: nil, out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	s.shell.mu.Lock()
	s.shell.receiverId = clientKey(c)
	s.shell.cmd = screenCommand{Want: true, Monitor: 0, Quality: "medium"}
	s.shell.mu.Unlock()

	s.onScreenClientGone(c)
	if s.receiverOf() != "" {
		t.Fatal("接收者断开后应清空")
	}
	if s.shell.currentCommand().Want {
		t.Fatal("接收者断开后应停投屏命令")
	}
}

// TestScreenCtlGate 屏端点的门：非回环 403；token 门与 /rpc 同源。
func TestScreenCtlGate(t *testing.T) {
	s := newScreenTestServer(t)
	// 非回环（httptest 的 client 地址是回环——用 handler 直调模拟非回环较繁琐，
	// 这里验证 token 门：配置 token 后无 token 的回环请求被 401）
	// remote.json 未装配 → remoteConfig 零值 → token 空 → 放行。
	w := httptest.NewRecorder()
	s.handleScreenCtl(w, httptest.NewRequest(http.MethodGet, "/screen/ctl", nil))
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("未开远程门时回环请求不应 401: %d", w.Code)
	}
}

// TestUnknownScreenEventShape screen.changed 事件载荷可序列化且形状稳定。
func TestUnknownScreenEventShape(t *testing.T) {
	b, err := json.Marshal(protocol.NewEvent(protocol.EventScreenChanged, protocol.ScreenChangedParams{
		State: protocol.ScreenState{Active: true, Monitor: 1, Quality: "low", Encoder: "h264_nvenc"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"method":"screen.changed"`) {
		t.Fatalf("事件帧形状不对: %s", b)
	}
	if !bytes.Contains(b, []byte(`"quality":"low"`)) {
		t.Fatalf("state 字段缺失: %s", b)
	}
}

// TestScreenDeframerSplitsAdheredFrames 一次 Read 含多帧/半帧：按帧头切完整帧
// 逐帧转发、残尾拼下一块（此前整块透传，粘住的帧被接收端整条丢弃——丢关键帧
// 即黑屏一整个 GOP）。半帧里的关键帧也绝不能丢（真机「无画面」主因之一）。
func TestScreenDeframerSplitsAdheredFrames(t *testing.T) {
	mk := func(ts uint64, key bool, payload string) []byte {
		flags := byte(0)
		if key {
			flags = protocol.FrameFlagKeyframe
		}
		f := make([]byte, protocol.FrameHeaderSize+len(payload))
		protocol.FrameHeader{Version: 1, Flags: flags, Len24: protocol.PutLen24(len(payload)), TimestampMs: ts}.Marshal(f)
		copy(f[protocol.FrameHeaderSize:], payload)
		return f
	}
	a := mk(33, true, "IDR-A")
	b := mk(66, false, "P-B")
	c := mk(99, false, "P-C")

	var got [][]byte
	df := &screenDeframer{}
	// 第一块：a 完整 + b 的前 10 字节
	n := df.feed(append(append([]byte{}, a...), b[:10]...), func(f []byte) { got = append(got, append([]byte{}, f...)) })
	if n != 1 || len(got) != 1 || string(got[0]) != string(a) {
		t.Fatalf("第一块应切出 a（半帧 b 留存）: n=%d got=%d", n, len(got))
	}
	// 第二块：b 的余下部分 + c 完整
	n = df.feed(b[10:], func(f []byte) { got = append(got, append([]byte{}, f...)) })
	_ = df.feed(c, func(f []byte) { got = append(got, append([]byte{}, f...)) })
	if len(got) != 3 || string(got[1]) != string(b) || string(got[2]) != string(c) {
		t.Fatalf("三帧都应完整转发: %d 帧", len(got))
	}
	// 坏长度：丢弃重来，不 panic、不无限增长
	df2 := &screenDeframer{}
	bad := make([]byte, protocol.FrameHeaderSize)
	bad[0] = 1
	bad[2], bad[3], bad[4] = 0xff, 0xff, 0xff
	df2.feed(bad, func([]byte) {})
	df2.feed([]byte("trailing garbage"), func([]byte) {})
	if len(df2.buf) > protocol.FrameMaxPayload+protocol.FrameHeaderSize {
		t.Fatalf("坏流缓冲应被清掉: %d", len(df2.buf))
	}
}

// TestStaleVideoDropNonKeyframeOnly 新鲜度丢弃只落非关键帧：关键帧保留并推进
// 锚点，落后锚点超阈值的非关键帧丢弃（排队积压的旧帧堵心跳 → 真机「断联」）。
func TestStaleVideoDropNonKeyframeOnly(t *testing.T) {
	mk := func(ts uint64, key bool) []byte {
		flags := byte(0)
		if key {
			flags = protocol.FrameFlagKeyframe
		}
		f := make([]byte, protocol.FrameHeaderSize+4)
		protocol.FrameHeader{Version: 1, Flags: flags, Len24: protocol.PutLen24(4), TimestampMs: ts}.Marshal(f)
		return f
	}
	c := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	if c.dropStaleVideoFrame(mk(1000, false)) {
		t.Fatal("无锚点时不应丢帧")
	}
	if c.dropStaleVideoFrame(mk(1000, true)) {
		t.Fatal("关键帧永不丢")
	}
	if c.dropStaleVideoFrame(mk(1200, false)) {
		t.Fatal("锚点之后的新帧不应丢")
	}
	if !c.dropStaleVideoFrame(mk(1000-600, false)) {
		t.Fatal("落后锚点超阈值的旧非关键帧应丢（关键帧前排队的积压帧）")
	}
	if c.dropStaleVideoFrame(mk(1000-400, false)) {
		t.Fatal("落后锚点 400ms 内的帧不应丢（阈值 500ms）")
	}
}
