// CurrentBranch / ProjectStatus 在两种空仓库状态下的行为（零提交仓库建会话 bug 的
// 钉子）：① git init 后零提交（HEAD 未出生）——分支解析必须回退 symbolic-ref 拿到
// 分支名而不是炸出「ambiguous argument 'HEAD'」；② 目录不是 git 仓库——如实报错
//（降级与否由调用方决定，本层不吞）。
package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initUnbornRepo 建一个零提交仓库（git init + 用户配置，无任何提交）。
func initUnbornRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitTest(t, dir, "init")
	gitTest(t, dir, "config", "user.name", "Test")
	gitTest(t, dir, "config", "user.email", "test@example.invalid")
	return dir
}

// TestCurrentBranchOnUnbornRepo：零提交仓库上 CurrentBranch 回退 symbolic-ref，
// 返回未出生分支的名字（如 master/main），绝不报 ambiguous argument。
func TestCurrentBranchOnUnbornRepo(t *testing.T) {
	dir := initUnbornRepo(t)
	branch, err := CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("零提交仓库的分支解析不应失败: %v", err)
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		t.Fatalf("分支名异常: %q", branch)
	}
	// 与 git 自己报的未出生分支名一致（不猜默认分支叫什么）。
	want := strings.TrimSpace(gitTest(t, dir, "symbolic-ref", "--short", "HEAD"))
	if branch != want {
		t.Fatalf("分支名 = %q, want %q", branch, want)
	}
}

// TestProjectStatusOnUnbornRepo：workspace_status / git 面板共用的 ProjectStatus
// 在零提交仓库上同样不炸（分支名拿到、未跟踪文件照常列出）。
func TestProjectStatusOnUnbornRepo(t *testing.T) {
	dir := initUnbornRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := ProjectStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("零提交仓库的 ProjectStatus 不应失败: %v", err)
	}
	if st.Branch == "" || st.Branch == "HEAD" {
		t.Fatalf("分支名异常: %q", st.Branch)
	}
	found := false
	for _, line := range st.Files {
		if strings.Contains(line, "draft.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未跟踪文件应列出: %+v", st.Files)
	}
}

// TestCurrentBranchOnNonRepo：目录不是 git 仓库 → 如实报错（报错信息要能看出
// 根因是 git 层的，由调用方决定降级）。
func TestCurrentBranchOnNonRepo(t *testing.T) {
	dir := t.TempDir()
	_, err := CurrentBranch(context.Background(), dir)
	if err == nil {
		t.Fatal("非 git 目录应报错")
	}
	if !strings.Contains(err.Error(), "git") {
		t.Fatalf("错误信息应来自 git 层: %v", err)
	}
}
