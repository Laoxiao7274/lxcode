// panic 防护的钉子（健壮性加固，2026-10）。
//
// 钉住的事：任务执行 goroutine 里的 panic 不得打死进程、也不得让任务永远停在
// running——settleOnPanic 把它转成失败收尾（进程存活由「测试本身没崩」证明）。
package tools

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/jobs"
)

func TestJobPanicSettlesFailed(t *testing.T) {
	mgr := jobs.NewManager(t.TempDir())
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "会 panic 的任务", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled := false
	cancel := func() { cancelled = true }

	func() {
		// 与 bash.go 后台任务 goroutine 同一条 defer 注册方式
		defer settleOnPanic(j, cancel)
		panic("boom: 注入的假工具 panic")
	}()

	snap := j.Snapshot()
	if snap.Status != jobs.StatusFailed {
		t.Fatalf("panic 后任务应按失败收尾，实际 %s", snap.Status)
	}
	if snap.EndedBy != jobs.EndedSelf {
		t.Fatalf("结束归属应为 self，实际 %s", snap.EndedBy)
	}
	if snap.Detail == "" {
		t.Fatal("失败详情不应为空（用户/日志要能看出为什么）")
	}
	if !cancelled {
		t.Fatal("panic 收尾应触发 cancel（清理由调用方链路完成）")
	}
	// Settle 幂等： panic 收尾后再正常 Settle 不会覆盖终态
	j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	if snap2 := j.Snapshot(); snap2.Status != jobs.StatusFailed {
		t.Fatalf("Settle 应幂等（终态不被覆盖），实际 %s", snap2.Status)
	}
}
