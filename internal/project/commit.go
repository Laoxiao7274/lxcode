package project

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// commitTimeout 是自动提交的硬超时：提交发生在轮结束的异步钩子上，绝不能让一个
// 卡住的 git 子进程一直挂着（对齐 tools/bash 的超时纪律）。
const commitTimeout = 30 * time.Second

// CommitAll 把工作树 dir 里的全部改动提交为一个检查点提交（每轮对话一个提交）。
//
// 语义（用户拍板）：**git add -A 会把工作树里所有改动（含用户手工改动）一起提交**
// ——这正是设计目标「每次修改都有痕迹」，所以不做路径筛选。
//
// 只做 add + commit：**绝不** reset / checkout / stash / push，不改写历史。
// 无改动时返回 (false, nil)——不产生空提交。dir 不是 Git 工作树或提交失败时
// 返回错误，由调用方 fail-open 处理（本函数不 panic）。
func CommitAll(ctx context.Context, dir, message string) (committed bool, err error) {
	if dir == "" {
		return false, fmt.Errorf("工作目录为空")
	}
	ctx, cancel := context.WithTimeout(ctx, commitTimeout)
	defer cancel()
	// 先看有没有改动：空输出 = 干净，不提交（不产生空提交）。
	status, err := gitOutputCtx(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	if _, err := gitOutputCtx(ctx, dir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := gitOutputCtx(ctx, dir, "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// gitOutputCtx 是既有 gitOutput 的带 ctx/超时版本（既有私有 runGit/gitOutput 与其
// 调用点保持不变）。自动提交走异步路径，必须能被取消/超时，不能像既有调用点那样无限等。
func gitOutputCtx(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), truncate(string(out), 200))
	}
	return string(out), nil
}
