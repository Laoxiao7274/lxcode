// screen_receiver_gate_test.go —— screen.start 调用者门（2026-12）：只有
// hello 自报 android 的连接能成为投屏接收者。probe 之类的探活客户端抢占接收者位
// 又断开 → onScreenClientGone 停投屏 → 手机端自动退回（真机「已连接就断开」根因）。
package server

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// gateTestClient 构造一个指定 clientID 的假连接（直打方法层，不起 WS）。
func gateTestClient(t *testing.T, clientID string) *wsClient {
	t.Helper()
	c := &wsClient{out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}
	c.clientID = clientID
	return c
}

// TestScreenStartGateProbeDenied probe（无观看能力）调 screen.start：被拒、
// 不占接收者位、不置投屏命令——探活连接断开不再停掉别人的投屏。
func TestScreenStartGateProbeDenied(t *testing.T) {
	s := newScreenTestServer(t)
	s.shell.report(fakeShellState(true))
	c := gateTestClient(t, "probe")
	req := &protocol.Request{Method: protocol.MethodScreenStart, Params: json.RawMessage(`{"monitor":0,"quality":"medium"}`)}
	resp := s.dispatchScreen(c, req, req.Params)
	if resp.Error == nil {
		t.Fatalf("probe 调 screen.start 应被拒绝: %+v", resp)
	}
	if resp.Error.Code != protocol.CodeScreenReceiverDenied {
		t.Fatalf("错误码应为 CodeScreenReceiverDenied(%d): %d", protocol.CodeScreenReceiverDenied, resp.Error.Code)
	}
	if s.receiverOf() != "" {
		t.Fatal("被拒的连接不应成为接收者")
	}
	if s.shell.currentCommand().Want {
		t.Fatal("被拒的 start 不应置投屏命令")
	}
}

// TestScreenStartGateUnidentifiedDenied 无 hello（clientID 空）的连接同样拒绝
// （fail-closed：放行未识别客户端等于没修）。
func TestScreenStartGateUnidentifiedDenied(t *testing.T) {
	s := newScreenTestServer(t)
	s.shell.report(fakeShellState(true))
	c := gateTestClient(t, "")
	resp := s.dispatchScreen(c, &protocol.Request{Method: protocol.MethodScreenStart, Params: json.RawMessage(`{}`)}, json.RawMessage(`{}`))
	if resp.Error == nil || resp.Error.Code != protocol.CodeScreenReceiverDenied {
		t.Fatalf("未识别客户端应被拒: %+v", resp)
	}
}

// TestScreenStartGateAndroidAllowed android 调 screen.start：成功并成为接收者
// （真机观看端，白名单内）。断开走既有 onScreenClientGone 路径清接收者。
func TestScreenStartGateAndroidAllowed(t *testing.T) {
	s := newScreenTestServer(t)
	s.shell.report(fakeShellState(true))
	c := gateTestClient(t, "android")
	req := &protocol.Request{Method: protocol.MethodScreenStart, Params: json.RawMessage(`{"monitor":0,"quality":"medium"}`)}
	resp := s.dispatchScreen(c, req, req.Params)
	if resp.Error != nil {
		t.Fatalf("android 调 screen.start 应成功: %+v", resp.Error)
	}
	if s.receiverOf() != clientKey(c) {
		t.Fatal("android 成功后应成为接收者")
	}
	if !s.shell.currentCommand().Want {
		t.Fatal("android 成功后投屏命令应置位")
	}
	// 接收者断开仍照常清位（对 android 本身的行为不变）。
	s.onScreenClientGone(c)
	if s.receiverOf() != "" || s.shell.currentCommand().Want {
		t.Fatal("android 接收者断开后应照常停止投屏命令")
	}
}

// TestScreenStartGateWebDenied web/桌面壳等其它 client 自报标识不在白名单
// （web 观看端加入时扩展 screenReceiverAllowed）。
func TestScreenStartGateWebDenied(t *testing.T) {
	s := newScreenTestServer(t)
	s.shell.report(fakeShellState(true))
	for _, id := range []string{"lxcode-web", "cli", "desktop"} {
		c := gateTestClient(t, id)
		resp := s.dispatchScreen(c, &protocol.Request{Method: protocol.MethodScreenStart, Params: json.RawMessage(`{}`)}, json.RawMessage(`{}`))
		if resp.Error == nil || resp.Error.Code != protocol.CodeScreenReceiverDenied {
			t.Fatalf("client=%q 应被拒: %+v", id, resp)
		}
		if s.receiverOf() != "" {
			t.Fatalf("client=%q 被拒后不应占接收者位", id)
		}
	}
}

// TestClientTagContainsClientID 日志定位：clientTag 带 client 标识（conn 为 nil
// 时远端地址回落 unknown），nil 连接整体回落 unknown。
func TestClientTagContainsClientID(t *testing.T) {
	if got := clientTag(nil); got != "unknown" {
		t.Fatalf("nil 连接应回落 unknown: %q", got)
	}
	c := gateTestClient(t, "android")
	if got := clientTag(c); got != "android/unknown" {
		t.Fatalf("clientTag 应含 client 标识: %q", got)
	}
	c2 := gateTestClient(t, "")
	if got := clientTag(c2); got != "unidentified/unknown" {
		t.Fatalf("未识别连接应标 unidentified: %q", got)
	}
}
