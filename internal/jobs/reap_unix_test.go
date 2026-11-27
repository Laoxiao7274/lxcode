//go:build !windows

package jobs

import "os/exec"

// sleeper 非 Windows 直接 sleep（标准核心组件，PATH 恒在）。
func sleeper(sec int) *exec.Cmd {
	return exec.Command("sleep", itoa(sec))
}
