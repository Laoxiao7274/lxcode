package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// integrationTimeout 是集成分支工作树相关 git 操作的硬超时：合并进程在后台
// goroutine 里跑，绝不能让卡住的 git 子进程一直挂着（对齐 CommitAll / tools/bash 的超时纪律）。
const integrationTimeout = 60 * time.Second

// EnsureIntegrationWorktree 确保**集成分支**在专用工作树里就绪并返回它：
// 分支已存在 → 复用（校验目录或按原路径重挂）；不存在 → 从当前 HEAD 建分支与工作树。
//
// 为什么不复用 CreateWorktree：它的分支名与目录名由 sessionID 固定
// （lxcode/session-<id>），而集成分支是**共享的目标分支**（默认 lxcode/integration）
// ——同一项目里所有会话都往它合并，命名不能挂在某个会话 id 上。
//
// 两条硬纪律：绝不 force 丢弃分支上已有的历史（合并是增量进行的）；绝不在主检出里
// checkout 这个分支（主检出的 HEAD 不能被动，见 AGENTS.md 的「只在集成分支工作树里合并」）。
// ctx 覆盖本函数自己的 git 调用（校验/重挂复用 RestoreWorktree 的同一套路径/分支一致性判定）。
func EnsureIntegrationWorktree(ctx context.Context, repoPath, worktreeRoot, projectID, dirName, branch string) (Worktree, error) {
	repo, err := ValidateDirectory(repoPath)
	if err != nil {
		return Worktree{}, err
	}
	if !safeWorktreeID.MatchString(projectID) || !safeWorktreeID.MatchString(dirName) {
		return Worktree{}, fmt.Errorf("项目或目录名含不安全字符")
	}
	ctx, cancel := context.WithTimeout(ctx, integrationTimeout)
	defer cancel()
	path := filepath.Join(worktreeRoot, projectID, dirName)
	// 分支已存在 = 集成分支已有历史：复用（校验目录；目录丢了按原路径重挂）。
	// 重挂沿用 RestoreWorktree 的同一套校验，不复制一份路径/分支一致性判定。
	if _, err := gitOutputCtx(ctx, repo, "show-ref", "--verify", "refs/heads/"+branch); err == nil {
		if err := RestoreWorktree(repo, path, branch); err != nil {
			return Worktree{}, err
		}
		base, _ := gitOutputCtx(ctx, repo, "merge-base", "HEAD", branch)
		return Worktree{Path: path, Branch: branch, BaseCommit: strings.TrimSpace(base)}, nil
	}
	// 分支不存在：从当前 HEAD 建（与 CreateWorktree 同款：未提交改动不复制）。
	base, err := gitOutputCtx(ctx, repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return Worktree{}, fmt.Errorf("集成分支需要项目有可用的 HEAD: %w", err)
	}
	base = strings.TrimSpace(base)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Worktree{}, fmt.Errorf("创建集成工作树父目录失败: %w", err)
	}
	if _, err := gitOutputCtx(ctx, repo, "worktree", "add", "-b", branch, path, base); err != nil {
		return Worktree{}, err
	}
	return Worktree{Path: path, Branch: branch, BaseCommit: base}, nil
}

// MergeConflicts 返回工作树里**尚未解决**的冲突文件清单，以及是否还有未完成的合并
// （MERGE_HEAD 存在 = 合并进行中）。
//
// 合并进程据此判定「合并没做完」并把清单写进任务 detail——而不是把合并失败谎报成
// 成功（任务说明里的「绝不谎报成功」在这里落地为机器可判的判据）。
func MergeConflicts(ctx context.Context, dir string) (files []string, mergeInProgress bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, integrationTimeout)
	defer cancel()
	// --quiet：没有 MERGE_HEAD 时以非零退出码收场（而不是把错误打到 stdout）
	if _, err := gitOutputCtx(ctx, dir, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err == nil {
		mergeInProgress = true
	}
	out, err := gitOutputCtx(ctx, dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, mergeInProgress, err
	}
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			files = append(files, s)
		}
	}
	return files, mergeInProgress, nil
}
