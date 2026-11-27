//go:build windows

package winutil

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job Object 兜底（进程残留治理 B1，治本）。
//
// taskkill /T 树杀（tools/proctree_windows.go）是**主动**路径——要求父进程
//（lxcode 后端）活着且记得去杀。逃逸口：后端被硬杀（任务管理器/SCM 的
// kill/断电）时没人执行树杀，sh 拉起的孙进程全部残留。Job Object 是 OS 级
// 兜底：把子进程关进一个 KILL_ON_JOB_CLOSE 的 Job，后端进程死亡 → 句柄
// 全部关闭 → 内核自动终止 Job 里的全部进程。没有用户态代码参与，也没有竞态。
//
// 实现方式（CreateProcess 竞态窗口的说明）：
//
// Go 标准库 exec.Command 走 CreateProcessW，我们无法在标准库的 CreateProcess
// 参数里插 ASSIGNMENT（syscall.SysProcAttr 没有 Job 相关字段）。业界惯用的
// CREATE_SUSPENDED → Assign → ResumeThread 需要接管整个进程创建流程
//（自己拼 CommandLine + CreateProcessW + 管道句柄继承——重写 os/exec 的
// 核心逻辑，放弃标准库的管道/Wait/Cancel 生态，得不偿失）。
//
// 所以这里取「启动后立即 Assign」的窗口期方案：cmd.Start() 返回即拿到
// ProcessHandle，马上 Assign。竞态窗口 = Start 返回到 Assign 成功之间的
// 几百微秒，窗口内父进程死亡则该子进程逃逸——但（a）窗口极窄且子进程此
// 期间几乎不可能已 spawn 孙进程，（b）逃逸子进程仍被 taskkill /T 主路径
// 覆盖（那是主动路径，不受此窗口影响）。两层互补：Job Object 管「父进程
// 意外死亡」，taskkill /T 管「主动取消」，配合后没有双保险空档。
//
// Job 句柄不 Close：KILL_ON_JOB_CLOSE 的语义是句柄归零即杀整组，而后端
// 进程退出时句柄自然归零——保留句柄正是让 OS 接管清理的方式（显式树杀
// taskkill /T 先行，Job 只是收尸）。

var (
	// jobHandle 是进程级单例的 Job Object 句柄：全部子进程共用一个 Job，
	// 子进程分叉出来的孙进程同样进组——语义正合需求（后端死 = 全树收尸），
	// 也避免每个子进程一个 Job 导致句柄无界增长。
	jobHandle windows.Handle
	jobErr    error // 创建失败的原因（兜底不可用时记下，Assign 返回它）
	jobOnce   sync.Once
)

// AssignToBackendJob 把进程关进后端的 KILL_ON_JOB_CLOSE Job Object。
//
// 返回 nil = 已入组（后端死亡时内核代杀）；返回非 nil = 创建/入组失败
//（调用方记日志继续跑——兜底失败不该让命令起不来，与落盘失败降级同一条纪律）。
func AssignToBackendJob(handle windows.Handle) error {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = fmt.Errorf("CreateJobObject: %w", err)
			return
		}
		// KILL_ON_JOB_CLOSE：句柄归零（后端进程退出）→ 内核终止整组进程
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if ret, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); ret == 0 || err != nil {
			jobErr = fmt.Errorf("SetInformationJobObject: %w", err)
			_ = windows.CloseHandle(h)
			return
		}
		jobHandle = h
	})
	if jobErr != nil {
		return jobErr
	}
	if err := windows.AssignProcessToJobObject(jobHandle, handle); err != nil {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	// 创建成功只报一次失败（失败原因在 jobErr；成功不打日志——每条命令都打是噪声）
	if os.Getenv("LXCODE_JOB_DEBUG") != "" {
		fmt.Fprintln(os.Stderr, "lxcode: 子进程已入 Job Object（KILL_ON_JOB_CLOSE 兜底生效）")
	}
	return nil
}
