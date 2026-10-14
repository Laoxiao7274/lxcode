// git 子进程的**唯一执行原语**：runGitCtx。
//
// 收敛纪律（2026-10 重构审查 L2）：原先包里有三套执行方式——commit.go 的
// gitOutputCtx（带 ctx）、worktree.go 的 gitOutput/runGit（无 ctx 无超时）、
// status.go 的四处内联 exec.CommandContext——超时语义各自为政，worktree 链路
// 的 git 卡住会无限等。现在 exec.CommandContext 只出现在这一处：
//   - 所有调用最终都走 runGitCtx；
//   - ctx 没有 deadline 时自动套 defaultGitTimeout（30s，与 statusTimeout/
//     commitTimeout 同一档——本地 git 操作 30s 足够宽裕，卡住就快速失败）；
//   - 错误统一包装成 *gitError：文案 `git <args>: <输出截断>`（原 gitOutputCtx
//     同款），并 Unwrap 到原始 *exec.ExitError——isExitCode 的退出码判定照常工作；
//   - GIT_TERMINAL_PROMPT=0：凭据缺失时立刻失败而不是挂起等终端输入
//    （原 push 路径的防挂起措施，统一到所有 git 调用）。
package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/winutil"
)

// defaultGitTimeout 是 ctx 未带 deadline 时的默认硬超时。
const defaultGitTimeout = 30 * time.Second

// runGitCtx 执行一条 git 命令，返回合并输出（stdout+stderr）。
// ctx 无 deadline 时套 defaultGitTimeout；错误统一 *gitError。
func runGitCtx(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultGitTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// 无窗口：git 是控制台程序，GUI/服务形态的后端下会往桌面弹黑窗
	if attr := winutil.NoWindowAttr(); attr != nil {
		cmd.SysProcAttr = attr
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, &gitError{args: strings.Join(args, " "), out: truncate(string(out), 200), err: err}
	}
	return out, nil
}

// gitError 是 runGitCtx 的统一错误：人话文案 + 可 Unwrap 到原始 exec 错误
//（退出码判定经 errors.As(*exec.ExitError) 照常工作）。
type gitError struct {
	args string
	out  string
	err  error
}

func (e *gitError) Error() string { return fmt.Sprintf("git %s: %s", e.args, e.out) }
func (e *gitError) Unwrap() error { return e.err }

// gitOutputCtx 是 string 形态的便捷包装（既有调用点用它，落点仍是 runGitCtx）。
func gitOutputCtx(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := runGitCtx(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
