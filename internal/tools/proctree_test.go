package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
)

// treeKillCommand 造一条「孙进程必然比 shell 活得久」的命令：子 shell 睡 2 秒后写
// marker，外层 sh 用 wait 挂着。只有**孙进程还活着**才写得出 marker——这就是
// 「树有没有被杀干净」的判据。
func treeKillCommand(marker string) string {
	return "(sleep 2; echo SURVIVED > " + marker + ") & wait"
}

// startTreeJob 起一个后台任务并返回它的 id（从管理器里找，不去解析回填文本）。
func startTreeJob(t *testing.T, r *Registry, mgr *jobs.Manager, marker string) string {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"command":           treeKillCommand(marker),
		"run_in_background": true,
	})
	if err != nil {
		t.Fatalf("marshal 参数: %v", err)
	}
	if got := r.Execute(context.Background(), call("bash", string(args))); !strings.Contains(got, "后台") {
		t.Fatalf("起后台任务应回可读文本: %q", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range mgr.List("") {
			if s.Status == jobs.StatusRunning {
				return s.ID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("后台任务未在 5s 内出现")
	return ""
}

// shOnly 在 shell 不是 sh 家族时跳过（用例依赖子 shell 语法）。
func shOnly(t *testing.T) {
	t.Helper()
	name, _ := selectShell()
	if strings.Contains(strings.ToLower(filepath.Base(name)), "cmd") {
		t.Skip("本机 shell 是 cmd.exe：用例需要 sh 的子 shell 语法")
	}
}

// TestTreeKillNegativeControl 是下面那条断言的**正对照**：不杀它，marker 必须写得出来。
// 没有它，「marker 不存在」可能只是因为命令压根没跑起来（比如语法不被 shell 接受），
// 那条回归测试就会变成永远通过的空断言。
func TestTreeKillNegativeControl(t *testing.T) {
	shOnly(t)
	r, mgr := newJobRegistry(t)
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "survived.txt"))
	startTreeJob(t, r, mgr, marker)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return // 孙进程活着并写出了 marker：判据有效
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("正对照失败：没杀它，孙进程也没写出 marker —— 判据本身不成立")
}

// TestBackgroundKillKillsWholeProcessTree 钉住「点结束真的把活干完的那棵树杀掉」。
//
// 背景（2026-09-29 真链路实测的缺陷）：后台任务的形态是 sh -c "<命令>"，干活的是
// 孙进程。默认的 Cancel 只 TerminateProcess 直接子进程，于是 kill 之后 sh 死了、
// 孙进程还在跑，而 cmd.Wait 要等 stdout 管道写端**全部**关闭才返回——任务永远停在
// stopping，结束通告永远发不出去。实测：kill 一个 sleep 600 之后 30 秒仍未 settle，
// sleep.exe 仍在跑。对永不退出的 dev server，就是「用户点了结束，什么都没发生」。
func TestBackgroundKillKillsWholeProcessTree(t *testing.T) {
	shOnly(t)
	r, mgr := newJobRegistry(t)
	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "survived.txt"))
	id := startTreeJob(t, r, mgr, marker)

	// 趁孙进程还没写出 marker 就杀（它睡 2 秒）
	time.Sleep(400 * time.Millisecond)
	snap, err := mgr.Kill(id, jobs.EndedUser)
	if err != nil {
		t.Fatalf("Kill 失败: %v", err)
	}
	if snap.EndedBy != jobs.EndedUser {
		t.Fatalf("Kill 应记归属 user，得到 %q", snap.EndedBy)
	}

	// 任务必须收尾（否则结束通告发不出去——那正是这个缺陷最严重的后果）
	var final jobs.Snapshot
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range mgr.List("") {
			if s.ID == id && s.Status != jobs.StatusRunning && s.Status != jobs.StatusStopping {
				final = s
			}
		}
		if final.ID != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if final.ID == "" {
		t.Fatal("kill 之后任务没在 8s 内收尾（卡在 stopping）——结束通告将永远发不出去")
	}
	if final.Status != jobs.StatusKilled {
		t.Fatalf("收尾状态应为 killed，得到 %q（detail=%q）", final.Status, final.Detail)
	}
	if final.EndedBy != jobs.EndedUser {
		t.Fatalf("归属应保持 user（不能被 producer 抹成 self），得到 %q", final.EndedBy)
	}

	// 决定性断言：孙进程必须已经死掉，写不出 marker
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("孙进程活了下来并写出了 marker —— 只杀了直接子进程，dev server 会继续占着端口")
	}
}
