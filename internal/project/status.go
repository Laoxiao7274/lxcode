// 工作区状态查询与回滚/推送的 git 封装：**全部只读或显式单一动作**，且一律
// 带 ctx + 超时（照 commit.go 的 CommitAll / gitOutputCtx 模式——私有 runGit/gitOutput
// 无 ctx 无超时，新函数一律不走它们）。
//
// 这些封装只服务 workspace_status / workspace_sync / workspace_rollback 三个
// Agent 工具：status 只读；push/reset 是显式的单一动作，调用方（server 层）
// 负责前置校验（脏工作区拒绝回滚、确认门等），本层不做策略。
package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// statusTimeout 是只读状态查询的硬超时（查询可能发生在协议请求路径上，不能无限等）。
const statusTimeout = 30 * time.Second

// pushTimeout 是 push 的硬超时：push 可能等网络/凭据，绝不能让子进程一直挂着。
const pushTimeout = 120 * time.Second

// resetTimeout 是 reset --hard 的硬超时（本仓库场景都是本地操作，30s 足够宽裕）。
const resetTimeout = 30 * time.Second

// StatusInfo 是主检出的只读快照。
type StatusInfo struct {
	Branch string   // 当前分支（游离 HEAD 时为 "HEAD"）
	Files  []string // status --porcelain 的原始行（含未跟踪；调用方决定截断）
}

// ProjectStatus 查询工作树 dir 的当前分支与未提交/未跟踪文件清单（只读）。
func ProjectStatus(ctx context.Context, dir string) (StatusInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	branch, err := gitOutputCtx(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return StatusInfo{}, err
	}
	lines, err := gitPorcelain(ctx, dir)
	if err != nil {
		return StatusInfo{}, err
	}
	return StatusInfo{Branch: strings.TrimSpace(branch), Files: lines}, nil
}

// StatusLines 返回工作树 dir 的 status --porcelain --untracked-files=all 行清单
// （只读；空切片 = 干净）。
func StatusLines(ctx context.Context, dir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	return gitPorcelain(ctx, dir)
}

// gitPorcelain 是 status --porcelain 的公共实现（调用方负责超时）。
func gitPorcelain(ctx context.Context, dir string) ([]string, error) {
	out, err := gitOutputCtx(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// CurrentBranch 返回工作树 dir 当前检出的分支名（只读）。
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// BranchExists 报告仓库 repoDir 里本地分支 branch 是否存在（只读）。
// repoDir 可以是主检出或任一工作树——分支引用是仓库级的。
// `show-ref --verify --quiet` 找不到引用时以退出码 1 收场（正常结论，不是错误）。
func BranchExists(ctx context.Context, repoDir, branch string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err := cmd.Run(); err != nil {
		if isExitCode(err, 1) {
			return false, nil
		}
		return false, fmt.Errorf("git show-ref --verify %s: %w", branch, err)
	}
	return true, nil
}

// CommitCountBetween 返回 from..to 区间内的提交数（to 有而 from 没有的提交；
// 只读）。ahead = CommitCountBetween(ctx, repo, base, branch)。
func CommitCountBetween(ctx context.Context, repoDir, from, to string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, repoDir, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &n); err != nil {
		return 0, fmt.Errorf("解析 rev-list --count 输出失败: %q", out)
	}
	return n, nil
}

// IsAncestor 报告提交 a 是否是 b 的祖先（git merge-base --is-ancestor）。
// 退出码 1 = 不是祖先（正常结论，不是错误）；其他错误如实上报。
func IsAncestor(ctx context.Context, repoDir, a, b string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "merge-base", "--is-ancestor", a, b)
	if err := cmd.Run(); err != nil {
		if isExitCode(err, 1) {
			return false, nil
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", a, b, err)
	}
	return true, nil
}

// ResolveCommit 把任意 rev（hash / 分支名 / HEAD~1）解析成完整 commit hash；
// 解析不到（不存在/拼写错）返回错误。
func ResolveCommit(ctx context.Context, dir, rev string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, dir, "rev-parse", "--verify", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("找不到提交 %q", rev)
	}
	return strings.TrimSpace(out), nil
}

// RootCommits 返回 HEAD 可达的根提交（rev-list --max-parents=0 HEAD）：
// 零基线元数据的会话用它兜底「会话起点」。
func RootCommits(ctx context.Context, dir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, dir, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// DiffNameStatus 返回 from..to 之间变更的文件清单（git diff --name-status，
// 状态字母 + 路径；只读）。回滚场景传 (新HEAD, 旧HEAD)，状态字母即「丢弃了什么」。
func DiffNameStatus(ctx context.Context, dir, from, to string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, dir, "diff", "--name-status", from+".."+to)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// ResetHard 把工作树 dir 硬重置到 commit（git reset --hard）。
// **调用方必须先做完前置校验**（脏工作区拒绝等）——本层不做任何策略判断。
func ResetHard(ctx context.Context, dir, commit string) error {
	ctx, cancel := context.WithTimeout(ctx, resetTimeout)
	defer cancel()
	_, err := gitOutputCtx(ctx, dir, "reset", "--hard", commit)
	return err
}

// RemoteExists 报告仓库 repoDir 是否配置了名为 name 的远程（只读）。
func RemoteExists(ctx context.Context, repoDir, name string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	out, err := gitOutputCtx(ctx, repoDir, "remote")
	if err != nil {
		return false, err
	}
	for _, line := range splitLines(out) {
		if line == name {
			return true, nil
		}
	}
	return false, nil
}

// PushBranch 把本地分支 branch 推到远程 origin（git push origin <branch>）。
// remote 名固定 origin（本工具面的约定）；推送是显式的单一动作，不做 force。
// GIT_TERMINAL_PROMPT=0：凭据缺失时立刻失败而不是挂起等终端输入。
func PushBranch(ctx context.Context, repoDir, branch string) error {
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "push", "origin", branch)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push origin %s: %s", branch, truncate(string(out), 300))
	}
	return nil
}

// splitLines 把命令输出按行切干净（去掉空行与 \r）。
func splitLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// isExitCode 判断 err 是否是退出码为 code 的进程退出（merge-base --is-ancestor
// 的「不是祖先」= 退出码 1，必须与真正的失败区分开）。
func isExitCode(err error, code int) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode() == code
	}
	return false
}
