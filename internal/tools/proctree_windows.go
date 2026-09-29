//go:build windows

package tools

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// configureProcTree 在 Windows 上无需额外设置：树杀靠 taskkill 按父子关系走。
func configureProcTree(cmd *exec.Cmd) {}

// killProcessTree 杀掉整棵进程树（取消前台命令或后台任务时调用）。
//
// 为什么不能只 Process.Kill：Windows 的 TerminateProcess 只作用于**直接子进程**，
// 而后台任务的形态恒为 sh -c "<命令>"——真正干活的是孙进程（npm run dev → node）。
// 2026-09-29 真链路实测：kill 一个 sleep 600 之后 sh.exe 死了、sleep.exe 还在跑，
// 任务卡在 stopping 三十秒仍未 settle，结束通告因此永远发不出去。对一个永不退出的
// dev server，这就是「用户点了结束，什么都没发生」。
//
// taskkill /T 按父子关系整棵树杀；必须在父进程还活着时调用（父死了就找不到子进程，
// 所以不能先 Kill 再 taskkill）。失败时退回 Process.Kill，至少把直接子进程杀掉。
func killProcessTree(p *os.Process) error {
	if p == nil {
		return os.ErrProcessDone
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		return p.Kill()
	}
	return nil
}
