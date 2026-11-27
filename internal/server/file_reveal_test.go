// file.reveal 的路径白名单（A4 文件卡跳编辑器）：resolveWorkspacePath 的
// 纯函数用例 + 协议方法级的越界拒绝用例。
package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func TestResolveWorkspacePath(t *testing.T) {
	base := t.TempDir()

	// 相对路径：join 工作目录
	abs, err := resolveWorkspacePath(base, "src/main.go")
	if err != nil {
		t.Fatalf("相对路径应放行: %v", err)
	}
	if abs != filepath.Join(base, "src", "main.go") {
		t.Fatalf("相对路径应解析到工作目录内: %q", abs)
	}

	// 工作目录自身（打开文件夹场景）
	abs, err = resolveWorkspacePath(base, ".")
	if err != nil || abs != filepath.Clean(base) {
		t.Fatalf("工作目录自身应放行: abs=%q err=%v", abs, err)
	}

	// 绝对路径但仍在工作目录内：放行
	abs, err = resolveWorkspacePath(base, filepath.Join(base, "a.txt"))
	if err != nil {
		t.Fatalf("目录内绝对路径应放行: %v", err)
	}
	if abs != filepath.Join(base, "a.txt") {
		t.Fatalf("绝对路径应保持: %q", abs)
	}

	// 穿越：`..` 按元素拒绝
	if _, err := resolveWorkspacePath(base, "../escape.txt"); err == nil {
		t.Fatal("../ 应拒绝")
	}
	if _, err := resolveWorkspacePath(base, "sub/../../escape.txt"); err == nil {
		t.Fatal("sub/../../ 应拒绝")
	}

	// 绝对路径但越出工作目录：拒绝
	outside := filepath.Join(filepath.Dir(base), "outside.txt")
	if _, err := resolveWorkspacePath(base, outside); err == nil {
		t.Fatalf("目录外绝对路径应拒绝: %q", outside)
	}

	// 空路径：拒绝
	if _, err := resolveWorkspacePath(base, " "); err == nil {
		t.Fatal("空路径应拒绝")
	}

	// 相似前缀不误放行：base-2 与 base 前缀相近但是不同目录
	sibling := base + "-2"
	if _, err := resolveWorkspacePath(base, filepath.Join(sibling, "x.txt")); err == nil {
		t.Fatal("兄弟目录（前缀相似）应拒绝")
	}
}

// TestFileRevealProtocol：方法级——合法路径回绝对路径；越界路径报参数错误。
func TestFileRevealProtocol(t *testing.T) {
	srv, client, _ := newTestServer(t, immediateStream)
	id := newSessionForTest(t, client)
	// 会话还没有绑定项目（未分组）→ 工作目录 = 后端进程目录。进程目录下
	// 建一个真实文件供放行用例（不落盘就打开也是合法应答——reveal 不读文件，
	// 但用真实文件让 abs 断言有据）。
	sess := srv.sessions[id]
	if sess == nil {
		t.Fatalf("会话 %s 未注册", id)
	}
	wd := sess.WorkDir()
	if wd == "" {
		wd = mustGetwd(t)
	}
	name := "reveal-probe.txt"
	if err := writeProbeFile(wd, name); err != nil {
		t.Fatalf("写探针文件: %v", err)
	}

	resp := client.call(protocol.MethodFileReveal, protocol.FileRevealParams{SessionID: id, Path: name})
	if resp == nil || resp.Error != nil {
		t.Fatalf("工作目录内文件应放行: %+v", resp)
	}
	var r protocol.FileRevealResult
	if err := unmarshalResult(resp, &r); err != nil {
		t.Fatalf("解析应答: %v", err)
	}
	if !strings.HasSuffix(r.Abs, name) {
		t.Fatalf("应答绝对路径应指向该文件: %q", r.Abs)
	}

	// 越界：报参数错误（不回路径）
	resp = client.call(protocol.MethodFileReveal, protocol.FileRevealParams{SessionID: id, Path: "../escape.txt"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("穿越路径应拒绝: %+v", resp)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

func writeProbeFile(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte("probe"), 0o600)
}
