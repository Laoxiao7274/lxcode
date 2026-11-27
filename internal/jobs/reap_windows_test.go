//go:build windows

package jobs

import (
	"os/exec"
	"syscall"
)

// sleeper 在 Windows 上起一个常驻 N 秒的进程：Git Bash 的 sleep.exe 在 PATH
// 里（后端自身依赖 Git Bash 跑 bash 工具），直接用它——与生产树杀的对象同款。
func sleeper(sec int) *exec.Cmd {
	cmd := exec.Command("sleep", itoa(sec))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
