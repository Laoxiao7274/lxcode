// publish 计数与规模上限的补充用例（2026-11 修复的钉子）：
//   - 目录不计入 skipped（曾把每个目录都误计成跳过项）；
//   - 被排除目录的子目录计一次 skipped（排除树的锚点，根目录除外）；
//   - 非普通文件（符号链接）计入 skipped 且不发布（平台允许时）；
//   - PublishMaxBytes 超限中止（单文件路径与目录累计路径）。
package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPublishSkippedCountsExcludedDir：目录发布里被排除的整棵子树只让
// 「子树锚点目录」计一次 skipped——目录只是遍历结构，绝不能逐个计入。
func TestPublishSkippedCountsExcludedDir(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// release/ 下：2 个普通文件 + node_modules 子树（2 个文件）+ 嵌套的
	// node_modules/deep 子目录——修复前每个目录都 +1 skipped。
	writeFile(t, filepath.Join(src, "release", "index.html"), "h")
	writeFile(t, filepath.Join(src, "release", "app.js"), "j")
	writeFile(t, filepath.Join(src, "release", "node_modules", "react", "index.js"), "r")
	writeFile(t, filepath.Join(src, "release", "node_modules", "deep", "x.js"), "x")

	published, skipped, err := PublishPaths(context.Background(), src, dst, "release", []string{"node_modules"})
	if err != nil {
		t.Fatalf("PublishPaths 不该报错: %v", err)
	}
	if published != 2 {
		t.Fatalf("published = %d, want 2", published)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1（只计 node_modules 锚点目录本身）", skipped)
	}
}

// TestPublishDirItselfNotSkipped：不带 exclude 的目录发布，skipped 必须为 0
// （修复前遍历经过的每个目录都被误计）。
func TestPublishDirItselfNotSkipped(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "out", "sub", "a.txt"), "a")
	writeFile(t, filepath.Join(src, "out", "b.txt"), "b")

	published, skipped, err := PublishPaths(context.Background(), src, dst, "out", nil)
	if err != nil {
		t.Fatalf("PublishPaths 不该报错: %v", err)
	}
	if published != 2 || skipped != 0 {
		t.Fatalf("published/skipped = %d/%d, want 2/0", published, skipped)
	}
}

// TestPublishNonRegularCountedSkipped：符号链接（非普通文件）计入 skipped
// 且不被发布。Windows 上创建符号链接需要特权/开发者模式，创建失败就跳过
// （平台限制，不是用例失败）。
func TestPublishNonRegularCountedSkipped(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, filepath.Join(src, "out", "real.txt"), "real")
	if err := os.Symlink(filepath.Join(src, "out", "real.txt"), filepath.Join(src, "out", "link.txt")); err != nil {
		t.Skipf("平台不允许创建符号链接: %v", err)
	}

	published, skipped, err := PublishPaths(context.Background(), src, dst, "out", nil)
	if err != nil {
		t.Fatalf("PublishPaths 不该报错: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1（符号链接不发布）", published)
	}
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1（符号链接计入跳过）", skipped)
	}
	if _, err := os.Lstat(filepath.Join(dst, "out", "link.txt")); !os.IsNotExist(err) {
		t.Fatal("符号链接不应被发布到目标")
	}
}

// TestPublishSingleFileOverByteLimit：单文件超过 PublishMaxBytes 直接拒绝
// （复制开始之前就中止，不留半成品）。
func TestPublishSingleFileOverByteLimit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// 稀疏文件（Truncate 只打洞不落盘）：体积真实超过 1GB 上限，但磁盘上
	// 几乎不占空间。
	f, err := os.Create(filepath.Join(src, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(PublishMaxBytes + 1); err != nil {
		f.Close()
		t.Skipf("平台不支持稀疏文件: %v", err)
	}
	f.Close()

	_, _, err = PublishPaths(context.Background(), src, dst, "big.bin", nil)
	if err == nil || !strings.Contains(err.Error(), "产物过大") {
		t.Fatalf("应报产物过大，got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dst, "big.bin")); !os.IsNotExist(statErr) {
		t.Fatal("超限文件不应被复制到目标（不留半成品）")
	}
}

// TestPublishDirOverByteLimitAborts：目录发布累计字节数超限时报错中止，
// 不继续复制后续文件。用稀疏文件（Truncate 只打洞不落盘）真实超过 1GB
// 上限——磁盘上几乎不占空间，复制开始前就会命中累计检查。
func TestPublishDirOverByteLimitAborts(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	mkSparse := func(name string, size int64) {
		t.Helper()
		f, err := os.Create(filepath.Join(src, "out", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			f.Close()
			t.Skipf("平台不支持稀疏文件: %v", err)
		}
		f.Close()
	}
	writeFile(t, filepath.Join(src, "out", "small.txt"), "tiny") // 正常复制
	// 第二个文件名义上 1GB+1：累计检查在复制它之前命中（稀疏文件不占磁盘，
	// 也让测试不用真写 1GB 数据）。
	mkSparse("huge.bin", PublishMaxBytes+1)

	_, _, err := PublishPaths(context.Background(), src, dst, "out", nil)
	if err == nil || !strings.Contains(err.Error(), "产物过大") {
		t.Fatalf("应报产物过大，got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dst, "out", "huge.bin")); !os.IsNotExist(statErr) {
		t.Fatal("超限文件不应被复制（累计检查在复制它之前中止）")
	}
}
