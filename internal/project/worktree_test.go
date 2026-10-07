package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeFromHEADAndRestore(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	worktreeRoot := filepath.Join(root, "sessions", "worktrees")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, repo, "init")
	gitTest(t, repo, "config", "user.name", "Test")
	gitTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, repo, "add", "base.txt")
	gitTest(t, repo, "commit", "-m", "base")
	wantHead := strings.TrimSpace(gitTest(t, repo, "rev-parse", "HEAD"))

	wt, err := CreateWorktree(repo, worktreeRoot, "project-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if wt.BaseCommit != wantHead || wt.Branch != "lxcode/session-session-1" {
		t.Fatalf("unexpected worktree metadata: %+v", wt)
	}
	// 模拟创建工作树后、写入 SQLite 前进程中断：重复 ensure 应重用原分支。
	recovered, err := CreateWorktree(repo, worktreeRoot, "project-1", "session-1")
	if err != nil || recovered != wt {
		t.Fatalf("idempotent create = %+v, %v; want %+v", recovered, err, wt)
	}
	got, err := os.ReadFile(filepath.Join(wt.Path, "base.txt"))
	if err != nil || strings.TrimSpace(string(got)) != "committed" {
		t.Fatalf("worktree did not start from HEAD: %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "only-here.txt"), []byte("isolated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "only-here.txt")); !os.IsNotExist(err) {
		t.Fatalf("worktree write leaked into main checkout: %v", err)
	}
	if err := RestoreWorktree(repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("valid worktree failed restore validation: %v", err)
	}

	// 目录丢失时仅从既有分支重挂，不从新的 HEAD 重建。
	if err := os.RemoveAll(wt.Path); err != nil {
		t.Fatal(err)
	}
	if err := RestoreWorktree(repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("restore existing branch: %v", err)
	}
	if got := strings.TrimSpace(gitTest(t, wt.Path, "branch", "--show-current")); got != wt.Branch {
		t.Fatalf("restored branch = %q, want %q", got, wt.Branch)
	}
}

// validateExistingWorktree 的三条拒绝路径。它被重写成「一次 rev-parse 取三项」，
// 行序即字段序，所以必须钉住：目录不是工作树、工作树属于别的仓库、工作树在别的分支上。
func TestRestoreWorktreeRejectsForeignRepoBranchAndPlainDir(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	other := filepath.Join(root, "other")
	worktreeRoot := filepath.Join(root, "sessions", "worktrees")
	for _, dir := range []string{repo, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitTest(t, dir, "init")
		gitTest(t, dir, "config", "user.name", "Test")
		gitTest(t, dir, "config", "user.email", "test@example.invalid")
		if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitTest(t, dir, "add", "base.txt")
		gitTest(t, dir, "commit", "-m", "base")
	}
	// ① 工作树属于别的仓库：公共目录对不上 → 必须拒绝（否则会在别人的仓库里干活）
	foreign, err := CreateWorktree(other, worktreeRoot, "project-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreWorktree(repo, foreign.Path, foreign.Branch); err == nil || !strings.Contains(err.Error(), "不属于预期项目仓库") {
		t.Fatalf("foreign worktree accepted: %v", err)
	}
	// ② 本项目的工作树但分支被切走 → 必须拒绝（否则用户改动会落到别的分支上）
	mine, err := CreateWorktree(repo, worktreeRoot, "project-1", "session-2")
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, mine.Path, "checkout", "-b", "moved-away")
	if err := RestoreWorktree(repo, mine.Path, mine.Branch); err == nil || !strings.Contains(err.Error(), "分支不匹配") {
		t.Fatalf("branch mismatch accepted: %v", err)
	}
	// ③ 目录存在但不是 Git 工作树 → 必须拒绝
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RestoreWorktree(repo, plain, "lxcode/session-x"); err == nil || !strings.Contains(err.Error(), "不是有效 Git 工作树") {
		t.Fatalf("plain directory accepted: %v", err)
	}
}

func TestReleaseWorktreeKeepsBranchAndCanRestore(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	worktreeRoot := filepath.Join(root, "sessions", "worktrees")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTest(t, repo, "init")
	gitTest(t, repo, "config", "user.name", "Test")
	gitTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("cache/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, repo, "add", ".gitignore", "base.txt")
	gitTest(t, repo, "commit", "-m", "base")
	wt, err := CreateWorktree(repo, worktreeRoot, "project-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "result.txt"), []byte("saved on branch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, wt.Path, "add", "result.txt")
	gitTest(t, wt.Path, "commit", "-m", "session result")
	branchHead := strings.TrimSpace(gitTest(t, repo, "rev-parse", wt.Branch))
	ignoredPath := filepath.Join(wt.Path, "cache", "deps.bin")
	if err := os.MkdirAll(filepath.Dir(ignoredPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoredPath, []byte("ignored cache"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ReleaseWorktree(repo, wt); err != nil {
		t.Fatalf("release clean worktree: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("released worktree path still exists: %v", err)
	}
	if got := strings.TrimSpace(gitTest(t, repo, "rev-parse", wt.Branch)); got != branchHead {
		t.Fatalf("release changed branch head: got %s, want %s", got, branchHead)
	}
	// 重复释放是安全的；后续恢复必须挂回原分支和提交。
	if err := ReleaseWorktree(repo, wt); err != nil {
		t.Fatalf("repeat release: %v", err)
	}
	if err := RestoreWorktree(repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("restore released worktree: %v", err)
	}
	if got := strings.TrimSpace(gitTest(t, wt.Path, "rev-parse", "HEAD")); got != branchHead {
		t.Fatalf("restored HEAD = %s, want %s", got, branchHead)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "result.txt")); err != nil {
		t.Fatalf("session branch result missing after restore: %v", err)
	}
	if _, err := os.Stat(ignoredPath); !os.IsNotExist(err) {
		t.Fatalf("ignored cache unexpectedly survived release: %v", err)
	}
}

func TestReleaseWorktreeRefusesUncommittedAndUntrackedChanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		data string
	}{
		{name: "tracked", path: "base.txt", data: "modified\n"},
		{name: "untracked", path: "new.txt", data: "keep me\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			gitTest(t, repo, "init")
			gitTest(t, repo, "config", "user.name", "Test")
			gitTest(t, repo, "config", "user.email", "test@example.invalid")
			if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			gitTest(t, repo, "add", "base.txt")
			gitTest(t, repo, "commit", "-m", "base")
			wt, err := CreateWorktree(repo, filepath.Join(root, "worktrees"), "project-1", "session-1")
			if err != nil {
				t.Fatal(err)
			}
			changed := filepath.Join(wt.Path, tc.path)
			if err := os.WriteFile(changed, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ReleaseWorktree(repo, wt); err == nil || !strings.Contains(err.Error(), "未提交或未跟踪") {
				t.Fatalf("release should refuse %s changes, got %v", tc.name, err)
			}
			got, err := os.ReadFile(changed)
			if err != nil || string(got) != tc.data {
				t.Fatalf("release damaged %s file: %q, %v", tc.name, got, err)
			}
		})
	}
}

// 零提交且完全干净的仓库：自动补一个空的初始提交做基线，会话照常建工作树。
// 钉住两点：基线提交是空的（用户目录里什么都没被带进历史）；基线之后流程与普通仓库一致。
func TestCreateWorktreeAutoBaselinesEmptyRepo(t *testing.T) {
	repo := t.TempDir()
	gitTest(t, repo, "init")
	gitTest(t, repo, "config", "user.name", "Test")
	gitTest(t, repo, "config", "user.email", "test@example.invalid")
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	wt, err := CreateWorktree(repo, worktreeRoot, "project", "session")
	if err != nil {
		t.Fatalf("empty repo should auto-baseline, got %v", err)
	}
	// 用户仓库历史里只有这一个空提交，且不含任何文件。
	log := gitTest(t, repo, "log", "--oneline")
	if n := len(strings.Fields(log)); n != 2 { // hash + 标题
		t.Fatalf("baseline history = %q, want exactly one commit", log)
	}
	if !strings.Contains(log, "初始提交") {
		t.Fatalf("baseline commit message unexpected: %q", log)
	}
	if files := gitTest(t, repo, "ls-tree", "-r", "HEAD"); strings.TrimSpace(files) != "" {
		t.Fatalf("baseline commit must be empty, got files: %q", files)
	}
	if wt.BaseCommit != strings.TrimSpace(gitTest(t, repo, "rev-parse", "HEAD")) {
		t.Fatalf("BaseCommit = %q, want current HEAD", wt.BaseCommit)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree missing after baseline: %v", err)
	}
	// 幂等重入：第二次 ensure 走恢复路径，不再新建。
	again, err := CreateWorktree(repo, worktreeRoot, "project", "session")
	if err != nil || again != wt {
		t.Fatalf("idempotent create = %+v, %v; want %+v", again, err, wt)
	}
}

// 零提交但有未跟踪内容：拒绝自动建基线——绝不代用户提交他们的文件，给可执行指引。
func TestCreateWorktreeRefusesUnbornWithUntrackedContent(t *testing.T) {
	repo := t.TempDir()
	gitTest(t, repo, "init")
	gitTest(t, repo, "config", "user.name", "Test")
	gitTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "draft.txt"), []byte("not mine to commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := CreateWorktree(repo, filepath.Join(t.TempDir(), "worktrees"), "project", "session")
	if err == nil || !strings.Contains(err.Error(), "HEAD") {
		t.Fatalf("unborn repo with content should fail with HEAD error, got %v", err)
	}
	if !strings.Contains(err.Error(), "自行创建一次提交") {
		t.Fatalf("error should carry actionable guidance, got %v", err)
	}
	if log, logErr := runGit(repo, "log", "--oneline"); logErr == nil {
		t.Fatalf("user repo must stay untouched, got commit: %s", log)
	}
	if got, readErr := os.ReadFile(filepath.Join(repo, "draft.txt")); readErr != nil || string(got) != "not mine to commit\n" {
		t.Fatalf("user file damaged: %q, %v", got, readErr)
	}
}

func TestCreateWorktreeRejectsUnsafeIDs(t *testing.T) {
	_, err := CreateWorktree(t.TempDir(), t.TempDir(), "../escape", "session")
	if err == nil || !strings.Contains(err.Error(), "不安全") {
		t.Fatalf("unsafe project id should be rejected, got %v", err)
	}
}

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
