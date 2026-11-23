// screen_params_test.go —— 帧率/清晰度上限扩展的参数校验测试（fps 白名单、
// ultra 档、fps 随命令电平透传、壳上报 fps 透传到 ScreenState）。
package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestNormalizeFPSWhitelist 白名单精确命中才生效，未知/缺席回落默认 30。
func TestNormalizeFPSWhitelist(t *testing.T) {
	for _, f := range []int{30, 60, 120, 144} {
		if got := protocol.NormalizeFPS(f); got != f {
			t.Fatalf("fps %d 应原样通过，得 %d", f, got)
		}
	}
	for _, f := range []int{0, 24, 90, 240, -1} {
		if got := protocol.NormalizeFPS(f); got != protocol.ScreenFPSDefault {
			t.Fatalf("fps %d 应回落默认 %d，得 %d", f, protocol.ScreenFPSDefault, got)
		}
	}
}

// TestScreenStartFpsParams screen.start 的 fps 参数链：白名单值进命令电平，
// 未知值回落默认；quality 的 ultra 档被接受。
func TestScreenStartFpsParams(t *testing.T) {
	s := newScreenTestServer(t)
	s.shell.report(fakeShellState(false))

	start := func(params string) {
		req := &protocol.Request{ID: json.RawMessage(`1`), Method: protocol.MethodScreenStart,
			Params: json.RawMessage(params)}
		resp := s.dispatch(&wsClient{conn: nil, out: make(chan []byte, 4), bin: make(chan []byte, 4), done: make(chan struct{})}, req)
		if resp.Error != nil {
			t.Fatalf("start(%s) 应成功: %v", params, resp.Error)
		}
	}

	start(`{"monitor":0,"quality":"ultra","fps":144}`)
	cmd := s.shell.currentCommand()
	if cmd.Fps != 144 {
		t.Fatalf("fps 144 应进命令电平: %+v", cmd)
	}
	if cmd.Quality != "ultra" {
		t.Fatalf("ultra 档应被接受: %+v", cmd)
	}

	start(`{"monitor":0,"fps":90}`)
	if got := s.shell.currentCommand().Fps; got != protocol.ScreenFPSDefault {
		t.Fatalf("fps 90（白名单外）应回落默认 %d，得 %d", protocol.ScreenFPSDefault, got)
	}

	start(`{}`)
	if got := s.shell.currentCommand().Fps; got != protocol.ScreenFPSDefault {
		t.Fatalf("fps 缺席应回落默认 %d，得 %d", protocol.ScreenFPSDefault, got)
	}

	// 帧率默认不变：老客户端不带 fps 时行为与扩展前一致（30）。
	if s.shell.currentCommand().Quality != "medium" {
		t.Fatalf("quality 缺席应回落 medium: %+v", s.shell.currentCommand())
	}
}

// TestScreenStateFpsPassthrough 壳上报的 fps 随 screenState 透传；老壳
// （无 fps 键）时整键缺席（omitempty），不编造 30。
func TestScreenStateFpsPassthrough(t *testing.T) {
	s := newScreenTestServer(t)
	sh := fakeShellState(true)
	sh.Quality = "ultra"
	sh.Fps = 144
	s.shell.report(sh)
	st := s.screenState()
	if st.Fps != 144 {
		t.Fatalf("fps 应透传壳上报值: %+v", st)
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"fps":144`) {
		t.Fatalf("wire 形状缺 fps: %s", b)
	}
	s2 := newScreenTestServer(t)
	s2.shell.report(fakeShellState(true)) // 老壳：无 fps 键
	b2, _ := json.Marshal(s2.screenState())
	if strings.Contains(string(b2), "fps") {
		t.Fatalf("壳未上报时 fps 整键缺席（不编造 30）: %s", b2)
	}
}

// TestBinaryQueueDepthFpsAdaptive 队列深度按 fps 自适应：30 档不变（8），
// 144 档加深到 24（167ms 深度）；未知 fps 回落基准。
func TestBinaryQueueDepthFpsAdaptive(t *testing.T) {
	cases := map[int]int{0: 8, 30: 8, 60: 10, 120: 20, 144: 24, 90: 15}
	for fps, want := range cases {
		if got := binaryQueueDepth(fps); got != want {
			t.Fatalf("binaryQueueDepth(%d) = %d, want %d", fps, got, want)
		}
	}
}
