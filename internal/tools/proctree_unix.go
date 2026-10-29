//go:build !windows

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcTree 让命令自成进程组，killProcessTree 才能一次杀掉整组。
func configureProcTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessTree 杀掉整个进程组（负 pid）。
//
// 与 Windows 侧同一个理由：只杀直接子进程会留下它拉起的孙进程
// （sh -c "npm run dev" 的 node），而 Wait 要等 stdout 管道写端全部关闭才返回，
// 于是任务卡在 stopping、结束通告发不出去。
func killProcessTree(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	// 负号 = 整个进程组（configureProcTree 已让子进程自成组）
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		return p.Kill()
	}
	return nil
}
