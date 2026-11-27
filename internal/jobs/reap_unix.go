//go:build !windows

package jobs

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// processAlive 探测 pid 是否存活（Unix：signal 0 探测）。
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// processStartedWithin 校验 pid 的进程创建时间落在 [t−容差, t+容差] 窗口内
//（pid 复用加固，语义见 reap_windows.go 同名函数）。Linux 读 /proc/<pid>/stat
// 的 starttime（字段 22，系统启动后的 clock tick）+ /proc/stat 的 btime（系统
// 启动的 Unix 时刻）换算成绝对时刻比对；/proc 不可用（非 Linux 的 Unix/容器
// 裁剪）按 false 处理——宁可漏杀不错杀，与 processAlive 同一保守口径。
func processStartedWithin(pid int, t time.Time) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	startTicks, ok := procStatStartTime(string(raw))
	if !ok {
		return false
	}
	btime, ok := procStatBootTime()
	if !ok {
		return false
	}
	// ticks → 秒：内核按 USER_HZ=100 导出 stat（acct 系列同款，Linux 惯例）
	const userHZ = 100
	startUnix := btime + startTicks/userHZ
	return startUnix >= t.Add(-pidTimeTolerance).Unix() &&
		startUnix <= t.Add(pidTimeTolerance).Unix()
}

// procStatStartTime 从 /proc/<pid>/stat 抽 starttime（第 22 字段）。第 2 字段
// （comm）可含空格与括号——从**最后一个 ')'** 之后按空格切，第 22 字段对应
// 切片下标 19（前两个字段吃掉了 2 个）。
func procStatStartTime(stat string) (int64, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(stat[i+1:])
	// fields[0] = state（第 3 字段）；starttime 是第 22 字段 → 下标 22-3=19
	if len(fields) < 20 {
		return 0, false
	}
	n, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// procStatBootTime 读 /proc/stat 的 btime 行（系统启动的 Unix 时刻，秒）。
func procStatBootTime() (int64, bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "btime ") {
			n, err := strconv.ParseInt(strings.TrimSpace(line[len("btime "):]), 10, 64)
			if err != nil {
				return 0, false
			}
			return n, true
		}
	}
	return 0, false
}

// killPidTree 树杀一个 pid（Unix：负 pid 杀进程组——bash 后台任务经
// tools.configureProcTree 的 Setpgid 自成组；组已不存在时退回单杀）。
func killPidTree(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		p, ferr := os.FindProcess(pid)
		if ferr != nil {
			return ferr
		}
		return p.Kill()
	}
	return nil
}
