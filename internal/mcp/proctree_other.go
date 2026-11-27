//go:build !windows && !unix

package mcp

import (
	"os"
	"os/exec"
)

// killProcessTreeGroup 是非 Windows 且非 Unix 平台（plan9/js 等——没有
// syscall.Kill/进程组 API）的编译兜底：只单杀，杀不到孙进程。真 Unix 实现
// 见 proctree_unix.go（//go:build unix，负 pid 组杀）。
func killProcessTreeGroup(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	return p.Kill()
}

// configureProcTree 非 Windows 且非 Unix 平台无进程组 API，什么都不做。
func configureProcTree(cmd *exec.Cmd) {}
