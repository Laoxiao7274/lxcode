//go:build windows

package jobs

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// processAlive 探测 pid 是否存活（Windows：tasklist 按 PID 过滤）。
// 探测失败按「不存活」处理——孤儿清扫宁可漏杀不错杀无关进程。
func processAlive(pid int) bool {
	kill := exec.Command("tasklist", "/NH", "/FO", "CSV", "/FI", fmt.Sprintf("PID eq %d", pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := kill.Output()
	if err != nil {
		return false
	}
	// 无匹配时 tasklist 输出「INFO: ...」头（本地化）而非该 pid——按
	// 「输出里出现该数字」判存活（CSV 行首就是 pid）。
	return len(out) > 0 && containsField(string(out), strconv.Itoa(pid))
}

// processStartedWithin 校验 pid 的进程创建时间落在 [t−容差, t+容差] 窗口内
//（pid 复用加固）：我们的残留进程诞生于 RecordPid 记录 pid 的那一刻（容差
// 覆盖调度延迟与时间戳抖动）；被复用撞上的无关进程诞生于记录之后。
//
// 实现：OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION) + GetProcessTimes 取
// 创建时刻。打不开进程（权限/已死）按 false 处理——宁可漏杀不错杀，与
// processAlive 同一保守口径。
func processStartedWithin(pid int, t time.Time) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return false
	}
	// Filetime.Nanoseconds() 已归一为「自 1970-01-01 的纳秒」（x/sys 的口径，
	// epoch 差与 100ns→ns 换算它内部都做了）——直接与 t.UnixNano() 比。
	created := creation.Nanoseconds()
	return created >= t.Add(-pidTimeTolerance).UnixNano() &&
		created <= t.Add(pidTimeTolerance).UnixNano()
}

// containsField 判断 CSV 输出的**任一字段**是否等于 s（pid 在第二列；
// 只比行首会把所有进程都判成不存活）。
func containsField(out, s string) bool {
	for _, line := range splitLines(out) {
		for _, field := range splitCSVFields(trimSpace(line)) {
			if field == s {
				return true
			}
		}
	}
	return false
}

// splitCSVFields 切一行 CSV 的字段（我们的输出来自 tasklist，字段不含内嵌
// 逗号——按引号剥除后逐段切）。字段值内含逗号的（内存 "5,276 K"）只在末列
// ——pid 在第 2 列不受影响：多出的碎片是数字+字母混合，不会与纯数字 pid
// 相等，不会误判。
func splitCSVFields(line string) []string {
	// 简单切法：字段间是 ","（引号包裹形态）或 ,（裸形态）。
	line = trimQuotes(line)
	var out []string
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] == ',' {
			out = append(out, trimQuotes(line[start:i]))
			start = i + 1
		}
	}
	out = append(out, trimQuotes(line[start:]))
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// killPidTree 树杀一个 pid（Windows：taskkill /T——孤儿进程已是「父进程死了」
// 的，taskkill 按当前父子关系杀它和它的子树）。
func killPidTree(pid int) error {
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		// 退回单杀（进程可能已无子树）
		return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
	}
	return nil
}
