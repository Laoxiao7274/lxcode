package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
)

// wait 工具的单测（契约 docs/jobs.md §3 的 wait 段）。
//
// 五个行为面：终态立即返回 / 阻塞到 settle / 超时返回当前快照 / ctx 取消 /
// job 不存在与参数非法照 jobOutputDef 同款校验。返回只带状态不带输出是
// 本工具的存在理由（省 token），formatWaitResult 的措辞也钉住。

// TestWaitImmediateOnTerminal：已终态的任务不进等待循环，立即返回状态行。
func TestWaitImmediateOnTerminal(t *testing.T) {
	r, mgr := newJobRegistry(t)
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "已完成"})
	if err != nil {
		t.Fatal(err)
	}
	j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	start := time.Now()
	got := r.Execute(context.Background(), call("wait", `{"job_id":"`+j.ID()+`"}`))
	if time.Since(start) > 2*time.Second {
		t.Fatalf("终态任务应立即返回（耗时 %v）", time.Since(start))
	}
	if !strings.Contains(got, "状态: completed") || !strings.Contains(got, "退出码 0") {
		t.Fatalf("应带终态与退出码: %q", got)
	}
	if strings.Contains(got, "仍在运行") {
		t.Fatalf("终态不该出现运行中措辞: %q", got)
	}
}

// TestWaitBlocksUntilSettle：running 任务阻塞等待，producer settle 后返回终态。
func TestWaitBlocksUntilSettle(t *testing.T) {
	r, mgr := newJobRegistry(t)
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "稍后结束"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		j.Write([]byte("干了一点活\n"))
		j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	}()
	start := time.Now()
	got := r.Execute(context.Background(), call("wait", `{"job_id":"`+j.ID()+`","timeout_sec":5}`))
	if time.Since(start) < 50*time.Millisecond {
		t.Fatalf("应真的阻塞等到 settle（耗时 %v）", time.Since(start))
	}
	if !strings.Contains(got, "状态: completed") {
		t.Fatalf("settle 后应返回 completed: %q", got)
	}
	// 核心契约：不带输出——写了的「干了一点活」绝不能出现在返回里
	if strings.Contains(got, "干了一点活") {
		t.Fatalf("wait 只回状态不带输出: %q", got)
	}
}

// TestWaitTimeoutReturnsRunningSnapshot：超时返回当前快照并如实报 running。
func TestWaitTimeoutReturnsRunningSnapshot(t *testing.T) {
	r, mgr := newJobRegistry(t)
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "永不结束"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Kill(j.ID(), jobs.EndedBackend) })
	start := time.Now()
	got := r.Execute(context.Background(), call("wait", `{"job_id":"`+j.ID()+`","timeout_sec":1}`))
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("应等满 timeout_sec（耗时 %v）", time.Since(start))
	}
	if !strings.Contains(got, "状态: running") || !strings.Contains(got, "仍在运行") {
		t.Fatalf("超时应如实报 running: %q", got)
	}
}

// TestWaitCtxCancel：ctx 取消 → 返回当前快照（waitJob 现行为），不报错。
func TestWaitCtxCancel(t *testing.T) {
	r, mgr := newJobRegistry(t)
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "被取消的等待"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.Kill(j.ID(), jobs.EndedBackend) })
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	got := r.Execute(ctx, call("wait", `{"job_id":"`+j.ID()+`","timeout_sec":30}`))
	if time.Since(start) > 5*time.Second {
		t.Fatalf("ctx 取消应尽快返回（耗时 %v）", time.Since(start))
	}
	if !strings.Contains(got, "状态: running") {
		t.Fatalf("取消后应返回当前快照: %q", got)
	}
}

// TestWaitParamValidation：job_id 缺失/未知任务/负 timeout 照 jobOutputDef 同款校验。
func TestWaitParamValidation(t *testing.T) {
	r, _ := newJobRegistry(t)
	ctx := context.Background()
	if got := r.Execute(ctx, call("wait", `{}`)); !strings.Contains(got, "job_id 不能为空") {
		t.Fatalf("wait 缺 job_id 应报错: %q", got)
	}
	if got := r.Execute(ctx, call("wait", `{"job_id":"job-nope"}`)); !strings.Contains(got, "不存在") {
		t.Fatalf("wait 未知任务应报错: %q", got)
	}
	if got := r.Execute(ctx, call("wait", `{"job_id":"x","timeout_sec":-1}`)); !strings.Contains(got, "不能为负") {
		t.Fatalf("wait 负 timeout_sec 应报错: %q", got)
	}
	if got := r.Execute(ctx, call("wait", `{"job_id":"x","condition":"bogus"}`)); !strings.Contains(got, "未知谓词") {
		t.Fatalf("wait 非法 condition 应报错: %q", got)
	}
	// 未注入管理器：可读错误
	if got := New().Execute(ctx, call("wait", `{"job_id":"x"}`)); !strings.Contains(got, "未装配") {
		t.Fatalf("未装配管理器应回可读错误: %q", got)
	}
}

// TestWaitLowRiskReadOnly：wait 低危 + Mutates=false——strict 只读模式可用。
func TestWaitLowRiskReadOnly(t *testing.T) {
	r := New()
	d, ok := r.Get("wait")
	if !ok {
		t.Fatal("wait 未注册")
	}
	if d.Risk != RiskLow || d.Mutates {
		t.Fatalf("wait 应为低危 + 不变更外部世界: risk=%v mutates=%v", d.Risk, d.Mutates)
	}
	if r.IsMutating("wait") {
		t.Fatal("wait 不该被 strict 只读模式拒绝")
	}
	if got := r.Confirm(context.Background(), call("wait", `{}`)); got != "" {
		t.Fatalf("wait 低危不该弹确认: %q", got)
	}
}

// TestWaitExitOKCondition：exit_ok 谓词——任务结束时若不是 completed 则继续等到超时。
func TestWaitExitOKCondition(t *testing.T) {
	r, mgr := newJobRegistry(t)
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "会失败的任务"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "退出码 1")
	}()
	got := r.Execute(context.Background(), call("wait", `{"job_id":"`+j.ID()+`","timeout_sec":5,"condition":"exit_ok"}`))
	if !strings.Contains(got, "状态: failed") {
		t.Fatalf("exit_ok 在失败收尾时应返回 failed 终态（settle 也是终态、等待应被收割）: %q", got)
	}
}
