package project

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// commitTimeout 是提交的硬超时：提交由 workspace_sync（请求路径）或遗留合并入口
// 触发，绝不能让一个卡住的 git 子进程一直挂着（对齐 tools/bash 的超时纪律）。
const commitTimeout = 30 * time.Second

// CommitAll 把工作树 dir 里的全部改动提交为一个提交（workspace_sync 的唯一提交原语；
// 轮末自动检查点提交已于 2026-10 移除）。
//
// 语义（用户拍板）：**git add -A 会把工作树里所有改动（含用户手工改动）一起提交**
// ——这正是设计目标「提交即快照」，所以不做路径筛选。
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

// gitOutputCtx 已收敛到 git.go 的 runGitCtx（唯一 exec 原语）——commit.go 不再
// 自带执行实现。
