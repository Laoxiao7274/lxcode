// logs.tail 的协议端到端测试（桌面错误日志查看器，2026-12）：真 WS 客户端
// 打真服务端（与 jobs_test 同款骨架），验证读尾 / 截断标记 / 未装配报错三条。
package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestLogsTailReadsFileEnd：路径已装配 → 读到文件尾；文件比请求大 →
// Truncated 且内容是尾部（不是头部）。
func TestLogsTailReadsFileEnd(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	logFile := filepath.Join(t.TempDir(), "lxcode.log")
	content := strings.Repeat("A", 32) + "\n" + "最近一条错误: 会话存储初始化失败\n"
	if err := os.WriteFile(logFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.SetLogPath(logFile)

	// 全量（请求字节数 >= 文件大小）：Truncated=false，内容完整。
	resp := client.call(protocol.MethodLogsTail, protocol.LogsTailParams{Bytes: 4096})
	if resp == nil || resp.Error != nil {
		t.Fatalf("logs.tail 失败: %+v", resp)
	}
	var full protocol.LogsTailResult
	decodeServerResult(t, resp.Result, &full)
	if full.Path != logFile {
		t.Fatalf("Path = %q, want %q", full.Path, logFile)
	}
	if full.Content != content {
		t.Fatalf("Content = %q, want %q", full.Content, content)
	}
	if full.Truncated {
		t.Fatal("文件小于请求字节数时不应置 Truncated")
	}

	// 尾部（请求 16 字节 < 文件大小）：Truncated=true，读到的是**末尾**16 字节。
	resp = client.call(protocol.MethodLogsTail, protocol.LogsTailParams{Bytes: 16})
	if resp == nil || resp.Error != nil {
		t.Fatalf("logs.tail(bytes=16) 失败: %+v", resp)
	}
	var tail protocol.LogsTailResult
	decodeServerResult(t, resp.Result, &tail)
	if !tail.Truncated {
		t.Fatal("文件大于请求字节数时应置 Truncated")
	}
	want := content[len(content)-16:]
	if tail.Content != want {
		t.Fatalf("尾部内容 = %q, want %q", tail.Content, want)
	}
	if !strings.Contains(want, "初始化失败") {
		t.Fatalf("尾部应包含末条错误，got %q", want)
	}
}

// TestLogsTailDefaultsAndClamps：bytes 缺省 = 16KB（读小文件等于全量）；
// 超上限钳到 maxLogsTailBytes（1MB——不照办 1GB 的请求）。
func TestLogsTailDefaultsAndClamps(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	logFile := filepath.Join(t.TempDir(), "lxcode.log")
	if err := os.WriteFile(logFile, []byte("短日志\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.SetLogPath(logFile)

	// 缺省（bytes 键缺席）。
	resp := client.call(protocol.MethodLogsTail, protocol.LogsTailParams{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("logs.tail(缺省) 失败: %+v", resp)
	}
	var def protocol.LogsTailResult
	decodeServerResult(t, resp.Result, &def)
	if def.Content != "短日志\n" || def.Truncated {
		t.Fatalf("缺省 16KB 读小文件应得全量: %+v", def)
	}

	// 超上限：钳到上限后仍能读出小文件。
	resp = client.call(protocol.MethodLogsTail, protocol.LogsTailParams{Bytes: 1 << 30})
	if resp == nil || resp.Error != nil {
		t.Fatalf("logs.tail(bytes=1GB) 失败: %+v", resp)
	}
	var clamped protocol.LogsTailResult
	decodeServerResult(t, resp.Result, &clamped)
	if clamped.Content != "短日志\n" {
		t.Fatalf("钳上限后应正常读取: %+v", clamped)
	}
}

// TestLogsTailNotConfigured：未装配（内存测试模式）→ 明确错误，不猜路径。
func TestLogsTailNotConfigured(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	resp := client.call(protocol.MethodLogsTail, protocol.LogsTailParams{})
	if resp == nil || resp.Error == nil {
		t.Fatalf("未装配时应报错: %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "日志不可用") {
		t.Fatalf("错误文案应说明日志不可用: %q", resp.Error.Message)
	}
}
