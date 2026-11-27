//go:build !windows

package tools

import "os/exec"

// assignJobObject 非 Windows 无 Job Object（进程组的 OS 级兜底由
// configureProcTree 的 Setpgid 覆盖：父进程死后进程组仍在，但 Unix 下
// 后端退出路径会显式杀整组——与 Windows 的 taskkill /T 同一条主动路径）。
func assignJobObject(cmd *exec.Cmd) {}
