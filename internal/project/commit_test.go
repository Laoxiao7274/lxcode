package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitAllCommitsChangesAndSkipsClean：有改动才提交（不产生空提交），
// 且 git add -A 会把未跟踪文件一起收进去（本次设计的既定语义）。
func TestCommitAllCommitsChangesAndSkipsClean(t *testing.T) {
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
	before := strings.TrimSpace(gitTest(t, repo, "rev-parse", "HEAD"))

	// 干净工作树：不提交，不报错。
	committed, err := CommitAll(context.Background(), repo, "第 1 轮：空改动")
	if err != nil || committed {
		t.Fatalf("clean worktree: committed=%v err=%v; want false, nil", committed, err)
	}
	if got := strings.TrimSpace(gitTest(t, repo, "rev-parse", "HEAD")); got != before {
		t.Fatalf("clean worktree created a commit: %s", got)
	}

	// 有改动（含未跟踪文件）：提交，提交信息一致，历史可见。
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const message = "第 1 轮：改了一个文件"
	committed, err = CommitAll(context.Background(), repo, message)
	if err != nil || !committed {
		t.Fatalf("dirty worktree: committed=%v err=%v; want true, nil", committed, err)
	}
	if got := strings.TrimSpace(gitTest(t, repo, "log", "-1", "--pretty=%s")); got != message {
		t.Fatalf("commit message = %q, want %q", got, message)
	}
	if got := strings.TrimSpace(gitTest(t, repo, "status", "--porcelain", "--untracked-files=all")); got != "" {
		t.Fatalf("worktree still dirty after commit: %q", got)
	}
	if got := strings.TrimSpace(gitTest(t, repo, "log", "--oneline")); strings.Count(got, "\n") != 1 {
		t.Fatalf("expected exactly one new commit:\n%s", got)
	}
}
