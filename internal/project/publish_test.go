// workspace_publish 的 project 层复制封装用例：单文件复制 / 目录递归+exclude /
// 目标父目录自动创建 / 覆盖 / 穿越拒绝 / 文件数上限 / `.git` 排除。
// 全部用临时目录，不碰真实工作区。
package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile 是测试里的小工具：建父目录并写文件。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readFile 读文件内容（失败即 Fatal）。
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPublishSingleFile：单文件复制——内容一致、目标父目录不存在时自动创建。
func TestPublishSingleFile(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "dist", "app.exe"), "binary-bytes")

	published, skipped, err := PublishPaths(context.Background(), src, dst, filepath.Join("dist", "app.exe"), nil)
	if err != nil {
		t.Fatalf("PublishPaths 不该报错: %v", err)
	}
	if published != 1 || skipped != 0 {
		t.Fatalf("published/skipped = %d/%d, want 1/0", published, skipped)
	}
	if got := readFile(t, filepath.Join(dst, "dist", "app.exe")); got != "binary-bytes" {
		t.Fatalf("复制后的内容不一致: %q", got)
	}
}

// TestPublishDirRecursiveWithExclude：目录递归复制保留相对结构，exclude 按
// 路径段排除（node_modules 整棵跳过、同名的 my_node_modules 不误伤）。
func TestPublishDirRecursiveWithExclude(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "release", "index.html"), "<html></html>")
	writeFile(t, filepath.Join(src, "release", "assets", "a.js"), "console.log(1)")
	writeFile(t, filepath.Join(src, "release", "node_modules", "react", "index.js"), "react")
	writeFile(t, filepath.Join(src, "release", "my_node_modules", "x.js"), "not-excluded")

	published, skipped, err := PublishPaths(context.Background(), src, dst,
		filepath.Join("release"), []string{"node_modules"})
	if err != nil {
		t.Fatalf("PublishPaths 不该报错: %v", err)
	}
	// 3 个普通文件发布（index/a.js/my_node_modules/x.js），node_modules 里 1 个跳过。
	if published != 3 || skipped != 1 {
		t.Fatalf("published/skipped = %d/%d, want 3/1", published, skipped)
	}
	if got := readFile(t, filepath.Join(dst, "release", "assets", "a.js")); got != "console.log(1)" {
		t.Fatalf("相对结构没保留: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "release", "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("node_modules 应被整棵排除: err=%v", err)
	}
	if got := readFile(t, filepath.Join(dst, "release", "my_node_modules", "x.js")); got != "not-excluded" {
		t.Fatal("my_node_modules 不该被误伤（exclude 按路径段精确匹配）")
	}
}

// TestPublishOverwritesExisting：目标已有同名文件 → 覆盖；目录发布进已有目录
// 也照常覆盖其中的文件。
func TestPublishOverwritesExisting(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "out", "report.txt"), "new")
	writeFile(t, filepath.Join(dst, "out", "report.txt"), "old")

	published, _, err := PublishPaths(context.Background(), src, dst, "out", nil)
	if err != nil {
		t.Fatalf("PublishPaths 不该报错: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1", published)
	}
	if got := readFile(t, filepath.Join(dst, "out", "report.txt")); got != "new" {
		t.Fatalf("已有文件应被覆盖: %q", got)
	}
}

// TestPublishRejectsTraversal：绝对路径、`..` 穿越、`.`（整个工作树）全部拒绝。
func TestPublishRejectsTraversal(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	for _, bad := range []string{"..", "../leak.txt", ".", "a/../.."} {
		if _, _, err := PublishPaths(context.Background(), src, dst, bad, nil); err == nil {
			t.Fatalf("rel=%q 应被拒绝", bad)
		}
	}
	// 绝对路径：Windows 盘符与 POSIX 根都算。
	for _, bad := range []string{src, `C:\Windows\Temp`, "/etc/passwd"} {
		if _, _, err := PublishPaths(context.Background(), src, dst, bad, nil); err == nil {
			t.Fatalf("绝对路径 %q 应被拒绝", bad)
		}
	}
	// target 同样拒绝穿越。
	if _, _, err := PublishPathsTo(context.Background(), src, dst, "out", filepath.Join("..", "escape"), nil); err == nil {
		t.Fatal("target 含 .. 应被拒绝")
	}
	if _, _, err := PublishPathsTo(context.Background(), src, dst, "out", src, nil); err == nil {
		t.Fatal("target 绝对路径应被拒绝")
	}
}

// TestPublishGitAlwaysExcluded：目录同步时 `.git` 无条件排除（exclude 没写它也一样）。
func TestPublishGitAlwaysExcluded(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "build", "ok.txt"), "ok")
	writeFile(t, filepath.Join(src, "build", ".git", "config"), "[core]")

	published, _, err := PublishPaths(context.Background(), src, dst, "build", nil)
	if err != nil {
		t.Fatalf("PublishPaths 不该报错: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1（.git 应被默认排除）", published)
	}
	if _, err := os.Stat(filepath.Join(dst, "build", ".git")); !os.IsNotExist(err) {
		t.Fatal(".git 应被无条件排除")
	}
}

// TestPublishFileCountLimit：超过单次文件数上限报「产物过大」。
func TestPublishFileCountLimit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	for i := 0; i < PublishMaxFiles+1; i++ {
		writeFile(t, filepath.Join(src, "many", "f"+itoa(i)+".txt"), "x")
	}
	_, _, err := PublishPaths(context.Background(), src, dst, "many", nil)
	if err == nil || !strings.Contains(err.Error(), "产物过大") {
		t.Fatalf("应报产物过大，got: %v", err)
	}
	if !strings.Contains(err.Error(), "分批") {
		t.Fatalf("错误应提示分批: %v", err)
	}
}

// TestPublishMissingSource：源不存在时报自解释错误（server 层依赖它给模型指路）。
func TestPublishMissingSource(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	_, _, err := PublishPaths(context.Background(), src, dst, "nope", nil)
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("应报源不存在: %v", err)
	}
}

// TestPlanPublishOverwrites：计划要如实列出会被覆盖的文件（确认门文案的依据）。
func TestPlanPublishOverwrites(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "out", "a.txt"), "new-a")
	writeFile(t, filepath.Join(src, "out", "b.txt"), "new-b")
	writeFile(t, filepath.Join(dst, "out", "a.txt"), "old-a")

	plan, err := PlanPublish(context.Background(), src, dst, "out", "out", nil)
	if err != nil {
		t.Fatalf("PlanPublish 不该报错: %v", err)
	}
	if len(plan.Files) != 2 || len(plan.Overwrites) != 1 {
		t.Fatalf("Files/Overwrites = %d/%d, want 2/1", len(plan.Files), len(plan.Overwrites))
	}
	if plan.Overwrites[0] != filepath.Join("a.txt") && plan.Overwrites[0] != "a.txt" {
		t.Fatalf("覆盖清单应含 a.txt: %v", plan.Overwrites)
	}
	if plan.TotalBytes != 10 { // new-a(5) + new-b(5)
		t.Fatalf("TotalBytes = %d, want 10", plan.TotalBytes)
	}
}

// itoa 免引 strconv 的小工具（测试内用）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
