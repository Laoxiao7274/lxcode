// screen_repro_test.go —— 「收 1 关键帧后全停」回归钉子（2026-12 真机诊断）：
// 根因是 handleScreenStream 缺 EnableFullDuplex（Go http.Server 半双工回写死锁，
// 详见 screen_shell.go 修复注释）。本测试钉住整条链：真实后端 routes + 模拟壳
// （上报 running + chunked POST 连续推流）+ 模拟手机（WS 连接、screen.start、
// readLoop 计数）——600 帧推流必须 ≥100 帧到达手机；缺修复时 1 帧后全停。
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func mkReproFrame(ts uint64, key bool, payload string) []byte {
	flags := byte(0)
	if key {
		flags = protocol.FrameFlagKeyframe
	}
	f := make([]byte, protocol.FrameHeaderSize+len(payload))
	protocol.FrameHeader{Version: 1, Flags: flags, Len24: protocol.PutLen24(len(payload)), TimestampMs: ts}.Marshal(f)
	copy(f[protocol.FrameHeaderSize:], payload)
	return f
}

// TestReproOnlyFirstFrameArrives 复现「手机只收到首帧」：模拟壳以 ~60fps 推 600
// 帧，模拟手机全速读，期待后续帧也到达（≥100 帧即通过）。
func TestReproOnlyFirstFrameArrives(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(s.routes())
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + protocol.Path

	// 手机端：WS 连接 + screen.start
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	ws, _, err := dialer.Dial(url, http.Header{})
	if err != nil {
		t.Fatalf("WS dial: %v", err)
	}
	defer ws.Close()
	sendReq := func(id int, method string, params string) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": json.RawMessage(params)})
		_ = ws.WriteMessage(websocket.TextMessage, b)
	}
	// 模拟壳上报 running（后端才认为壳在线、允许 start）
	reportState := func(running bool) {
		body := fmt.Sprintf(`{"running":%v,"monitor":0,"encoder":"h264_amf","width":1920,"height":1080,"monitors":[]}`, running)
		resp, err := http.Post(srv.URL+"/screen/ctl", "application/json", strings.NewReader(body))
		if err != nil {
			t.Logf("ctl 上报失败（后台 goroutine，不 Fail）: %v", err)
			return
		}
		resp.Body.Close()
	}
	reportState(true)
	sendReq(1, protocol.MethodScreenStart, `{"monitor":0,"quality":"medium"}`)
	// 等 screen.start 应答落地（异步 dispatch；不等的话接收者可能还没挂上）
	time.Sleep(200 * time.Millisecond)
	if s.receiverOf() == "" {
		t.Fatal("start 应答后接收者未挂上（screen.start 失败或时序）")
	}

	// 读 start 应答/事件（非阻塞排空）
	// 定期重报壳状态（真实壳 1s 轮询持续上报——lastSeen 4s 保鲜期靠它维持；
	// 不重报的话长时间跑后 shellOnline 变 false，但已设的 receiverId 不受影响，
	// 这里保留是为了贴近真实壳行为）
	reportStop := make(chan struct{})
	defer close(reportStop)
	go func() {
		tk := time.NewTicker(1 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-tk.C:
				reportState(true)
			case <-reportStop:
				return
			}
		}
	}()

	// 读 start 应答（文本帧）
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		mt, p, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("读 start 应答前断: %v", err)
		}
		if mt == websocket.TextMessage && (strings.Contains(string(p), "id") || strings.Contains(string(p), "result") || strings.Contains(string(p), "error")) {
			t.Logf("start 应答原文: %s", p)
			break
		}
	}
	_ = ws.SetReadDeadline(time.Time{})

	// 壳：chunked POST 连续推流（真实壳形态：一次开路、逐帧 write）
	pr, pw := io.Pipe()
	go func() {
		for i := 0; i < 600; i++ {
			f := mkReproFrame(uint64(i*33), i%60 == 0, fmt.Sprintf("frame-%04d-payload-padpadpadpadpadpad", i))
			if _, err := pw.Write(f); err != nil {
				return
			}
			time.Sleep(16 * time.Millisecond)
		}
		pw.Close()
	}()
	go func() {
		resp, err := http.Post(srv.URL+"/screen/stream", "application/octet-stream", pr)
		if err != nil {
			t.Logf("推流 POST 失败: %v", err)
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	// 模拟手机收帧循环：数二进制帧（到 100 帧即通过）
	deadline := time.After(15 * time.Second)
	got, keys := 0, 0
	for {
		select {
		case <-deadline:
			t.Fatalf("15s 内只收到 %d 帧（关键帧 %d）——后续帧没有到达手机", got, keys)
		default:
		}
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		mt, p, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("收帧循环提前断（已收 %d 帧）: %v", got, err)
		}
		if mt == websocket.BinaryMessage {
			got++
			if h, ok := protocol.UnmarshalFrameHeader(p); ok && h.Flags&protocol.FrameFlagKeyframe != 0 {
				keys++
			}
			if got >= 100 {
				t.Logf("通过：收到 %d 帧（关键帧 %d）", got, keys)
				return
			}
		}
	}
}
