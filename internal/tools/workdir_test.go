package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- 会话工作目录（项目根）注入后的工具行为 ----------

func TestResolveToolPath(t *testing.T) {
	ctx := context.Background()
	// 未注入：原样（OS 按进程目录解析——旧行为）
	if got := resolveToolPath(ctx, "a/b.txt"); got != "a/b.txt" {
		t.Fatalf("未注入时应原样: %q", got)
	}
	if got := resolveToolPath(ctx, ""); got != "" {
		t.Fatalf("空路径应原样: %q", got)
	}
	wd := WithWorkDir(ctx, `C:\proj`)
	// 相对 + 注入 → join 工作目录
	if got := resolveToolPath(wd, "src/main.go"); got != filepath.Join(`C:\proj`, "src/main.go") {
		t.Fatalf("相对路径应按工作目录解析: %q", got)
	}
	// 绝对路径：原样
	if got := resolveToolPath(wd, `D:\elsewhere\x.txt`); got != `D:\elsewhere\x.txt` {
		t.Fatalf("绝对路径应原样: %q", got)
	}
}

// TestToolsRelativePathInWorkDir：read/edit/write 三个文件工具在注入
// 工作目录后，相对路径落在工作目录里（这是「项目会话在项目根干活」的核心）。
func TestToolsRelativePathInWorkDir(t *testing.T) {
	r := New()
	dir := t.TempDir()
	ctx := WithWorkDir(context.Background(), dir)
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("第一行\n第二行"), 0o644); err != nil {
		t.Fatal(err)
	}

	// read_file 相对路径
	got := r.Execute(ctx, call("read_file", `{"path":"hello.txt"}`))
	if !strings.Contains(got, "第一行") {
		t.Fatalf("read_file 应在工作目录读到: %q", got)
	}
	// edit 相对路径
	got = r.Execute(ctx, call("edit", `{"path":"hello.txt","old_string":"第二行","new_string":"改过的行"}`))
	if !strings.Contains(got, "已修改") {
		t.Fatalf("edit 应落在工作目录: %q", got)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if !strings.Contains(string(data), "改过的行") {
		t.Fatalf("工作目录里的文件应被改掉: %q", data)
	}
	// write_file 相对路径（含父目录）
	got = r.Execute(ctx, call("write_file", `{"path":"nested/out.txt","content":"新文件"}`))
	if !strings.Contains(got, "已写入") {
		t.Fatalf("write_file 应落在工作目录: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "nested", "out.txt")); err != nil {
		t.Fatalf("文件应写到工作目录下: %v", err)
	}
}

// TestWriteFileConfirmResolvesWorkDir：确认门与执行层解析同一个文件——
// 相对路径指向工作目录里的既有文件时，覆盖确认必须弹出（stat 错目录会
// 误判成新文件，项目里的文件被静默覆盖）。
func TestWriteFileConfirmResolvesWorkDir(t *testing.T) {
	r := New()
	dir := t.TempDir()
	ctx := WithWorkDir(context.Background(), dir)
	if err := os.WriteFile(filepath.Join(dir, "cfg.json"), []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := r.Confirm(ctx, call("write_file", `{"path":"cfg.json","content":"{}"}`))
	if !strings.Contains(prompt, "变短") || !strings.Contains(prompt, "cfg.json") {
		t.Fatalf("相对路径的覆盖确认应按工作目录解析: %q", prompt)
	}
}

// TestBashDefaultWorkDir：bash 未传 cwd 时默认在会话工作目录执行
// （用输出重定向落盘验证——pwd 输出是 POSIX 形式路径，不能直接字符串比对）。
func TestBashDefaultWorkDir(t *testing.T) {
	skipWithoutSh(t)
	r := New()
	dir := t.TempDir()
	ctx := WithWorkDir(context.Background(), dir)
	got := r.Execute(ctx, call("bash", `{"command":"echo hi > from-bash.txt"}`))
	if strings.Contains(got, "错误") {
		t.Fatalf("bash 执行失败: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "from-bash.txt")); err != nil {
		t.Fatalf("未传 cwd 时应在工作目录执行: %v", err)
	}
}

// TestBashRelativeCwdInWorkDir：显式相对 cwd 也按工作目录解析。
func TestBashRelativeCwdInWorkDir(t *testing.T) {
	skipWithoutSh(t)
	r := New()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := WithWorkDir(context.Background(), dir)
	got := r.Execute(ctx, call("bash", `{"command":"ls","cwd":"sub"}`))
	if !strings.Contains(got, "marker.txt") {
		t.Fatalf("相对 cwd 应按工作目录解析: %q", got)
	}
}

// TestSearchDefaultPathWorkDir：search 不传 path 时默认搜会话工作目录。
func TestSearchDefaultPathWorkDir(t *testing.T) {
	r := New()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "unique-key-file.txt"), []byte("needle-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := WithWorkDir(context.Background(), dir)
	got := r.Execute(ctx, call("search", `{"pattern":"unique-*.txt"}`))
	if !strings.Contains(got, "unique-key-file.txt") {
		t.Fatalf("默认根应为会话工作目录: %q", got)
	}
}
