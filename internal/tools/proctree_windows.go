//go:build windows

package tools

import (
	"log"
	"os"
	"os/exec"
	"strconv"

	"github.com/moyunteng/lxcode/internal/winutil"
)

// configureProcTree 在 Windows 上无需额外设置：树杀靠 taskkill 按父子关系走。
// 进程残留治理 B1：进程组的 OS 级兜底（Job Object）在 assignJobObject 里做——
// ConfigureProcTree 保持原签名，Job Object 的分配挂在 Start 之后（需要进程句柄）。
func configureProcTree(cmd *exec.Cmd) {}

// assignJobObject 把已启动的子进程关进后端的 KILL_ON_JOB_CLOSE Job Object
//（进程残留治理 B1）：后端进程死亡（硬杀/断电/SCM 强停）→ 句柄归零 → 内核
// 自动终止整组进程。这是 taskkill /T 主路径之外的 OS 级兜底，两层互补——
// 主路径管「主动取消」，Job Object 管「父进程意外死亡」。
//
// 竞态窗口的说明见 winutil/jobobject_windows.go 头注（Start 返回到 Assign
// 之间的几百微秒；窗口内逃逸的子进程仍被 taskkill /T 覆盖）。
//
// 失败只记日志：兜底失败不该让命令失败（与任务日志落盘失败降级同一条纪律）。
func assignJobObject(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := cmd.Process.WithHandle(func(h uintptr) {
		if aerr := winutil.AssignToBackendJob(winutil.Handle(h)); aerr != nil {
			log.Printf("Job Object 兜底未生效（进程照常跑，依赖 taskkill 主路径）: %v", aerr)
		}
	}); err != nil {
		log.Printf("Job Object 兜底未生效（拿不到进程句柄）: %v", err)
	}
}

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
	kill.SysProcAttr = noWindowAttr()
	if err := kill.Run(); err != nil {
		return p.Kill()
	}
	return nil
}

