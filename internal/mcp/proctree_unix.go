//go:build unix

package mcp

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcTree 让命令自成进程组，killProcessTreeGroup 才能一次杀掉
// 整组（与 tools.configureProcTree 同一条理由，照抄其写法）。
func configureProcTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessTreeGroup Unix 侧整组杀（configureProcTree 的 Setpgid 已让
// 子进程自成组——负 pid 杀整组；失败退回单杀）。Windows 构建里的同名占位见
// proctree_windows.go（运行期不可达的编译兜底）。
func killProcessTreeGroup(p *os.Process) error {
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		return p.Kill()
	}
	return nil
}
