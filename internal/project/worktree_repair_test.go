package project

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// setupRepairFixture 建真实 git 仓库 + 会话工作树，并在工作树里埋好两类未提交
// 改动（未跟踪新文件 + 已跟踪文件修改），返回解析出的管理目录路径。
// 这份改动是修复过程的红线：任何一条路径都不允许动它。
func setupRepairFixture(t *testing.T) (repo string, wt Worktree, adminDir string) {
	t.Helper()
	root := t.TempDir()
	repo = filepath.Join(root, "repo")
	worktreeRoot := filepath.Join(root, "sessions", "worktrees")
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
	created, err := CreateWorktree(repo, worktreeRoot, "project-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.Path, "draft.txt"), []byte("用户未提交的修改\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.Path, "base.txt"), []byte("modified locally\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(created.Path, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	adminDir, _, err = parseWorktreeGitfile(created.Path, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return repo, created, adminDir
}

// assertUncommittedIntact 断言埋进去的未提交改动一个字节都没被动过，
// 且工作树在 git 眼里仍是带着这些改动的正常工作树。
func assertUncommittedIntact(t *testing.T, wtPath string) {
	t.Helper()
	for _, tc := range []struct{ name, want string }{
		{"draft.txt", "用户未提交的修改\n"},
		{"base.txt", "modified locally\n"},
	} {
		got, err := os.ReadFile(filepath.Join(wtPath, tc.name))
		if err != nil || string(got) != tc.want {
			t.Fatalf("repair damaged %s: %q, %v", tc.name, got, err)
		}
	}
	status := gitTest(t, wtPath, "status", "--porcelain")
	for _, name := range []string{"draft.txt", "base.txt"} {
		if !strings.Contains(status, name) {
			t.Fatalf("repaired worktree status lost %s: %q", name, status)
		}
	}
}

// snapshotDir 记录目录树全部文件的相对路径与内容，用于逐字节比对「零改动」。
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			snap[rel+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		snap[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// 场景 ①：管理目录被 prune 掉（gitfile 悬空）——走修复 B（重建三件套 + repair），
// 未提交改动原样保留，HEAD 仍在原分支原提交。
func TestRestoreWorktreeRepairsPrunedAdminDir(t *testing.T) {
	repo, wt, adminDir := setupRepairFixture(t)
	if err := os.RemoveAll(adminDir); err != nil {
		t.Fatal(err)
	}
	gitTest(t, repo, "worktree", "prune")
	if err := validateExistingWorktree(repo, wt.Path, wt.Branch); err == nil {
		t.Fatal("pruned worktree should fail validation before repair")
	}
	if err := RestoreWorktree(repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("restore should repair pruned worktree: %v", err)
	}
	assertUncommittedIntact(t, wt.Path)
	if got := strings.TrimSpace(gitTest(t, wt.Path, "rev-parse", "HEAD")); got != wt.BaseCommit {
		t.Fatalf("HEAD after repair = %s, want %s", got, wt.BaseCommit)
	}
	if got := strings.TrimSpace(gitTest(t, wt.Path, "branch", "--show-current")); got != wt.Branch {
		t.Fatalf("branch after repair = %q, want %q", got, wt.Branch)
	}
}

// 场景 ②：管理目录还在但 gitdir 回指针指向不存在路径（sessions 根迁移形态）。
// 实测（Windows git）：这个形态下 worktree 内 rev-parse 仍然通过——校验不会
// 失败，但注册表回指针是断的，`git worktree list` 会把分支登记到不存在的旧
// 路径上。所以这里直接驱动修复 A（repair），断言三件事：回指针被修回本工作树、
// 校验通过、未提交改动原样保留。
func TestRestoreWorktreeRepairsStaleBackPointer(t *testing.T) {
	repo, wt, adminDir := setupRepairFixture(t)
	stale := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(adminDir))),
		"old-sessions", "moved", "away", ".git")
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.ToSlash(stale)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repairBrokenWorktree(repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("repair stale back pointer: %v", err)
	}
	gitdir, err := os.ReadFile(filepath.Join(adminDir, "gitdir"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(filepath.Join(wt.Path, ".git"))
	if strings.TrimSpace(string(gitdir)) != want {
		t.Fatalf("back pointer after repair = %q, want %q", strings.TrimSpace(string(gitdir)), want)
	}
	if err := validateExistingWorktree(repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("validation after repair: %v", err)
	}
	assertUncommittedIntact(t, wt.Path)
}

// 场景 ② 的回归补充：回指针过期 + 目录被删时，重挂路径会因
// registeredBranchPath 读到旧路径而拒绝；repair 先行可解除这一死锁。
func TestRestoreWorktreeRemountAfterStaleBackPointerRepair(t *testing.T) {
	repo, wt, adminDir := setupRepairFixture(t)
	stale := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(adminDir))),
		"old-sessions", "moved", "away", ".git")
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.ToSlash(stale)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 目录还在时校验通过（回指针对本机 git 不可见），但 repair 已能修注册表。
	if err := repairBrokenWorktree(repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("repair stale back pointer: %v", err)
	}
	// 随后目录丢失：重挂不应被「已登记在另一工作树」挡住。
	if err := os.RemoveAll(wt.Path); err != nil {
		t.Fatal(err)
	}
	if err := RestoreWorktree(repo, wt.Path, wt.Branch); err != nil {
		t.Fatalf("remount after stale back pointer was repaired: %v", err)
	}
	if got := strings.TrimSpace(gitTest(t, wt.Path, "branch", "--show-current")); got != wt.Branch {
		t.Fatalf("branch after remount = %q, want %q", got, wt.Branch)
	}
}

// 场景 ③：分支被删 = 不可修——保留原报错语义，worktree 目录逐字节零改动。
func TestRestoreWorktreeKeepsUnrepairableWorktreeUntouched(t *testing.T) {
	repo, wt, adminDir := setupRepairFixture(t)
	if err := os.RemoveAll(adminDir); err != nil {
		t.Fatal(err)
	}
	gitTest(t, repo, "update-ref", "-d", "refs/heads/"+wt.Branch)
	before := snapshotDir(t, wt.Path)
	err := RestoreWorktree(repo, wt.Path, wt.Branch)
	if err == nil || !strings.Contains(err.Error(), "不是有效 Git 工作树") {
		t.Fatalf("unrepairable worktree must keep original error, got %v", err)
	}
	after := snapshotDir(t, wt.Path)
	if !reflect.DeepEqual(before, after) {
		var changed []string
		for k, v := range before {
			if after[k] != v {
				changed = append(changed, fmt.Sprintf("%s: %q -> %q", k, v, after[k]))
			}
		}
		for k, v := range after {
			if _, ok := before[k]; !ok {
				changed = append(changed, fmt.Sprintf("%s: added %q", k, v))
			}
		}
		t.Fatalf("failed repair modified worktree contents: %s", strings.Join(changed, "; "))
	}
}

// 修复 B 的只补缺失语义：在一个**新建的空管理目录**上重建三件套——已存在的
// 文件（哪怕是残留旧内容）绝不覆盖，缺失的补齐为 git 可用的绝对路径格式。
// 不借用 fixture 的真实管理目录：git 自己写的就是相对 commondir（../..），
// 拿它当「残留」会把断言搞混。
func TestRebuildWorktreeAdminDirOnlyFillsMissingFiles(t *testing.T) {
	repo, wt, _ := setupRepairFixture(t)
	fresh := filepath.Join(t.TempDir(), "fresh-admin")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	residual := "residual content\n"
	if err := os.WriteFile(filepath.Join(fresh, "HEAD"), []byte(residual), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rebuildWorktreeAdminDir(repo, fresh, wt.Path, wt.Branch); err != nil {
		t.Fatalf("rebuild admin dir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(fresh, "HEAD"))
	if err != nil || string(got) != residual {
		t.Fatalf("existing HEAD was overwritten: %q, %v", got, err)
	}
	common, err := os.ReadFile(filepath.Join(fresh, "commondir"))
	if err != nil || !filepath.IsAbs(strings.TrimSpace(string(common))) {
		t.Fatalf("missing commondir not filled with absolute path: %q, %v", common, err)
	}
	gitdir, err := os.ReadFile(filepath.Join(fresh, "gitdir"))
	if err != nil || filepath.ToSlash(filepath.Join(wt.Path, ".git")) != strings.TrimSpace(string(gitdir)) {
		t.Fatalf("missing gitdir not filled correctly: %q, %v", gitdir, err)
	}
}
