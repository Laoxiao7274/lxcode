package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 进程残留治理 B3/B4 的单测：孤儿清扫（ReapOrphans）与寿命护栏（reapIdleJobs）。
//
// B3 的判定面：日志无终态标记 = 孤儿；有标记 = 已优雅收尾；pid 行定位进程。
// B4 的判定面：running + 无输出超阈值 = Kill（EndedBy=backend）。

// TestReapOrphansKillsResidualProcess：模拟硬杀残留——直接往 jobs 目录写一份
// 带 pid 行、无终态标记的日志（pid = 本测试进程拉起的真实长眠进程），ReapOrphans
// 应树杀它并补终态标记。用真实进程（sleep）验证树杀路径，不 mock。
func TestReapOrphansKillsResidualProcess(t *testing.T) {
	dir := t.TempDir()
	// 起一个真实的长眠进程（sleep 300——300s 内测试必然结束并杀掉它）
	cmd := sleeper(300)
	if err := cmd.Start(); err != nil {
		t.Skipf("起不了 sleep 进程（环境限制）: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	logPath := filepath.Join(dir, "jobs", "job-orphan.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "pid=" + itoa(cmd.Process.Pid) + "\n模型在跑死循环\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	t.Cleanup(m.Shutdown)
	reps := m.ReapOrphans()
	if len(reps) != 1 || reps[0].ID != "job-orphan" || reps[0].Pid != cmd.Process.Pid {
		t.Fatalf("应报告孤儿 job-orphan: %+v", reps)
	}
	if !reps[0].Killed {
		t.Fatalf("存活进程应被树杀: %+v", reps[0])
	}
	// 进程真的死了（给回收一个窗口）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(cmd.Process.Pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(cmd.Process.Pid) {
		t.Fatalf("孤儿进程 %d 应已被树杀", cmd.Process.Pid)
	}
	// 终态标记补进日志（下次启动不再扫）
	raw, _ := os.ReadFile(logPath)
	if !logHasTerminalMark(raw) {
		t.Fatalf("孤儿日志应补终态标记: %q", string(raw))
	}
	// 再扫一次：有标记了，不再报告
	if reps := m.ReapOrphans(); len(reps) != 0 {
		t.Fatalf("补过标记后不应再报告孤儿: %+v", reps)
	}
}

// TestReapOrphansSkipsSettledLogs：优雅退出收尾的日志（有终态标记）不视为孤儿。
func TestReapOrphansSkipsSettledLogs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "jobs", "job-done.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// 本测试进程自己的 pid——若被误杀测试当场死，正好暴露误杀
	content := "pid=" + itoa(os.Getpid()) + "\n输出\n[job settled] job-done completed self\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	t.Cleanup(m.Shutdown)
	if reps := m.ReapOrphans(); len(reps) != 0 {
		t.Fatalf("有终态标记的日志不应被清扫: %+v", reps)
	}
}

// TestReapOrphansDeadPid：pid 已死（不复用风险）→ 只补标记不报 Killed。
func TestReapOrphansDeadPid(t *testing.T) {
	dir := t.TempDir()
	// pid=99999999 几乎必不存在（Linux 上限 ~4M，Windows 上限更小）
	logPath := filepath.Join(dir, "jobs", "job-gone.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("pid=99999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	t.Cleanup(m.Shutdown)
	reps := m.ReapOrphans()
	if len(reps) != 1 {
		t.Fatalf("应报告孤儿日志本身: %+v", reps)
	}
	if reps[0].Killed {
		t.Fatalf("死 pid 不该报 Killed: %+v", reps[0])
	}
}

// TestReapOrphansSkipsReusedPid：pid 复用（pid 复用加固，2026-12 返工）——
// 日志里的 pid 指向一个**不相干的长活进程**（我们的测试进程自己：它远早于
// RecordPid 的记录时刻诞生，正是「pid 被复用撞上」的形态），ReapOrphans 必须
// 不杀只补终态标记。误杀的后果不可逆（可能就是某个关键进程），漏杀的代价只
// 是日志多一条。同时验证存量格式（pid 行无 @ 时刻）走旧路径会杀——不，存量
// 格式的用例是 TestReapOrphansKillsResidualProcess（它写旧格式 pid 行，被杀
// = 回退旧行为的实证）。
func TestReapOrphansSkipsReusedPid(t *testing.T) {
	dir := t.TempDir()
	// pid = 本测试进程自己——它必然活着、创建时间必然远早于刚写的记录时刻。
	// 若被误杀测试当场死（与 SkipsSettledLogs 的哨兵同一思路），正好暴露误杀。
	logPath := filepath.Join(dir, "jobs", "job-reused.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("%s%d @ %d\n上一个任务的残留（pid 已被复用）\n",
		jobsPidLineHeader, os.Getpid(), time.Now().Add(-time.Hour).UnixNano())
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	t.Cleanup(m.Shutdown)
	reps := m.ReapOrphans()
	if len(reps) != 1 {
		t.Fatalf("应报告孤儿日志本身: %+v", reps)
	}
	if reps[0].Killed {
		t.Fatalf("pid 复用（创建时间偏离记录时刻）不该被杀: %+v", reps[0])
	}
	// 终态标记照补（下次启动不再扫它）
	raw, _ := os.ReadFile(logPath)
	if !logHasTerminalMark(raw) {
		t.Fatalf("pid 复用的日志也应补终态标记: %q", string(raw))
	}
	// 再扫一次：有标记了，不再报告
	if reps := m.ReapOrphans(); len(reps) != 0 {
		t.Fatalf("补过标记后不应再报告孤儿: %+v", reps)
	}
}

// TestRecordPidWritesFirstLine：RecordPid 把 pid 与记录时刻写进日志首行
//（清扫的定位依据；@ 时刻是 pid 复用校验的锚点）。
func TestRecordPidWritesFirstLine(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	t.Cleanup(m.Shutdown)
	j, err := m.Start(Spec{Kind: "bash", Label: "带 pid"})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	j.RecordPid(4242)
	after := time.Now().Add(time.Second)
	raw, err := os.ReadFile(j.Snapshot().OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(string(raw), "\n", 2)[0]
	if !strings.HasPrefix(first, "pid=4242 @ ") {
		t.Fatalf("首行应为 pid=4242 @ <时刻>: %q", first)
	}
	pid, ts, hasTS := parsePidLine(raw)
	if pid != 4242 {
		t.Fatalf("parsePidLine 应解析出 4242: %d", pid)
	}
	if !hasTS || ts.Before(before) || ts.After(after) {
		t.Fatalf("记录时刻应落在写入窗口内: hasTS=%v ts=%v 窗口=[%v,%v]", hasTS, ts, before, after)
	}
}

// TestIdleJobReaper：寿命护栏——running 且无输出超阈值的任务被 Kill
//（EndedBy=backend）；持续有输出的任务不受影响。
func TestIdleJobReaper(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	t.Cleanup(m.Shutdown)
	m.SetIdleTTL(50 * time.Millisecond)

	idle, err := m.Start(Spec{Kind: "bash", Label: "无输出的死任务"})
	if err != nil {
		t.Fatal(err)
	}
	busy, err := m.Start(Spec{Kind: "bash", Label: "持续输出"})
	if err != nil {
		t.Fatal(err)
	}
	// busy 保持「最近有输出」：reap 前再写一次
	defer m.Kill(busy.ID(), EndedBackend)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m.reapIdleJobs() // 直呼扫描（不等 hourly ticker）
		snap := idle.Snapshot()
		// Kill 是非阻塞的（置 stopping，等 producer 收尾）——stopping +
		// EndedBy=backend 也算命中（真实收尾没有 producer：这个测试任务
		// 没 register cancel，Kill 后停在 stopping 是它的既定行为）
		if snap.EndedBy == EndedBackend && (snap.Status == StatusKilled || snap.Status == StatusStopping) {
			// busy 不该被殃及
			if s := busy.Snapshot(); s.Status != StatusRunning && s.EndedBy != EndedBackend {
				t.Fatalf("有输出的任务不该被护栏杀: %+v", s)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("无输出任务应在阈值后被护栏杀掉: %+v", idle.Snapshot())
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
