// 合并收尾「来源校验」用的只读 git 原语：合并进程在任务结束后要逐条核对
// 集成分支上新增的提交是否都来自预期的源分支——发现不可归因的提交就回滚
// （对 merger 越界的机器判据，见 internal/server/mergejob.go）。
//
// 与 status.go 同一套纪律：全部只读、一律带 ctx + 超时（statusTimeout）。
package project

import (
	"context"
	"fmt"
	"strings"
)

// CommitRange 返回 from..to 区间内的提交 hash 清单（to 可达而 from 不可达的提交，
// 按时间正序；只读）。合并收尾用它列出「本次任务往目标分支新增了哪些提交」。
func CommitRange(ctx context.Context, repoDir, from, to string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, repoDir, "rev-list", from+".."+to)
	if err != nil {
		return nil, fmt.Errorf("git rev-list %s..%s: %w", from, to, err)
	}
	return splitLines(out), nil
}

// CommitParents 返回提交的父提交 hash 清单（git rev-list --parents -1；
// 根提交返回空切片；只读）。来源校验用它识别 merge commit 的第二父。
func CommitParents(ctx context.Context, repoDir, commit string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, repoDir, "rev-list", "--parents", "-1", commit)
	if err != nil {
		return nil, fmt.Errorf("git rev-list --parents %s: %w", commit, err)
	}
	parts := strings.Fields(out)
	if len(parts) == 0 {
		return nil, fmt.Errorf("git rev-list --parents %s 无输出", commit)
	}
	// 第一个字段是提交自身，其余才是父提交
	return parts[1:], nil
}

// CommitSubject 返回提交的标题行（git log -1 --format=%s；只读）。
// 来源校验把它写进失败 detail——hash 对人不友好，标题一眼能认出是哪笔改动。
func CommitSubject(ctx context.Context, repoDir, commit string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, repoDir, "log", "-1", "--format=%s", commit)
	if err != nil {
		return "", fmt.Errorf("git log -1 %s: %w", commit, err)
	}
	return strings.TrimSpace(out), nil
}

// CommitChangedFiles 返回提交改动的文件清单（git diff-tree --root
// --no-commit-id --name-only -r；根提交用 --root 列出全部文件；只读）。
// 来源校验把可疑提交的改动文件写进 detail，让人一眼看出越界范围。
func CommitChangedFiles(ctx context.Context, repoDir, commit string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, repoDir, "diff-tree", "--root", "--no-commit-id",
		"--name-only", "-r", commit)
	if err != nil {
		return nil, fmt.Errorf("git diff-tree %s: %w", commit, err)
	}
	return splitLines(out), nil
}
