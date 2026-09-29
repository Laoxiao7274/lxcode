package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
)

// newJobRegistry 建一个挂了任务管理器的注册表（与 server 装配同款：SetJobs 注入，
// 工具自己不 new 一个）。
func newJobRegistry(t *testing.T) (*Registry, *jobs.Manager) {
	t.Helper()
	r := New()
	mgr := jobs.NewManager(t.TempDir())
	t.Cleanup(mgr.Shutdown)
	r.SetJobs(mgr)
	return r, mgr
}

// waitJobStatus 等任务到达期望状态（假 producer 用）。
func waitJobStatus(t *testing.T, mgr *jobs.Manager, id string, want jobs.Status) jobs.Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range mgr.List("") {
			if s.ID == id && s.Status == want {
				return s
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务 %s 未在 10s 内到达状态 %s", id, want)
	return jobs.Snapshot{}
}

// TestJobToolsRequireManager：未注入实现时报错必须可读（模型据此知道
// 「后端没装这个能力」，而不是拿到 panic 或空结果）。
func TestJobToolsRequireManager(t *testing.T) {
	r := New()
	ctx := context.Background()
	for _, c := range []struct{ name, args string }{
		{"job_output", `{"job_id":"job-1"}`},
		{"job_list", `{}`},
		{"job_kill", `{"job_id":"job-1"}`},
	} {
		got := r.Execute(ctx, call(c.name, c.args))
		if !strings.Contains(got, "未装配") {
			t.Fatalf("%s 未注入管理器时应回可读错误: %q", c.name, got)
		}
	}
}

// TestJobToolsParamValidation：参数校验在系统层硬拦（空 job_id / 负 timeout_ms），
// 不依赖模型自觉。
func TestJobToolsParamValidation(t *testing.T) {
	r, _ := newJobRegistry(t)
	ctx := context.Background()
	if got := r.Execute(ctx, call("job_output", `{}`)); !strings.Contains(got, "job_id 不能为空") {
		t.Fatalf("job_output 缺 job_id 应报错: %q", got)
	}
	if got := r.Execute(ctx, call("job_kill", `{"job_id":"  "}`)); !strings.Contains(got, "job_id 不能为空") {
		t.Fatalf("job_kill 缺 job_id 应报错: %q", got)
	}
	if got := r.Execute(ctx, call("job_output", `{"job_id":"job-1","timeout_ms":-1}`)); !strings.Contains(got, "不能为负") {
		t.Fatalf("job_output 负 timeout_ms 应报错: %q", got)
	}
	if got := r.Execute(ctx, call("job_output", `{"job_id":"job-1"}`)); !strings.Contains(got, "不存在") {
		t.Fatalf("job_output 未知任务应报错: %q", got)
	}
	// session_only=true 但没有会话 id：自解释报错（不是拿空列表骗人）
	if got := r.Execute(ctx, call("job_list", `{"session_only":true}`)); !strings.Contains(got, "无法只列本会话") {
		t.Fatalf("job_list 无会话 id 时应报错: %q", got)
	}
}

// TestJobToolsAreReadOnlyAndLowRisk：三件套低危 + Mutates=false——
// strict 只读模式必须能用（否则只读模式连停掉自己起的任务都做不到）。
func TestJobToolsAreReadOnlyAndLowRisk(t *testing.T) {
	r := New()
	for _, name := range []string{"job_output", "job_list", "job_kill"} {
		d, ok := r.Get(name)
		if !ok {
			t.Fatalf("%s 未注册", name)
		}
		if d.Risk != RiskLow || d.Mutates {
			t.Fatalf("%s 应为低危 + 不变更外部世界（strict 只读模式必须可用）: risk=%v mutates=%v", name, d.Risk, d.Mutates)
		}
		if r.IsMutating(name) {
			t.Fatalf("%s 不该被 strict 只读模式拒绝", name)
		}
		if got := r.Confirm(context.Background(), call(name, `{}`)); got != "" {
			t.Fatalf("%s 低危不该弹确认: %q", name, got)
		}
	}
}

// TestJobOutputIncrementalAndWait：job_output 的增量语义 + 会话隔离的游标 + wait 等收尾。
func TestJobOutputIncrementalAndWait(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "假任务", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Write([]byte("first line\n")); err != nil {
		t.Fatal(err)
	}
	got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`}`))
	if !strings.Contains(got, "first line") || !strings.Contains(got, "[status: running]") {
		t.Fatalf("首次读应回输出与状态: %q", got)
	}
	// 增量：没有新输出
	got = r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`}`))
	if !strings.Contains(got, "(no new output)") {
		t.Fatalf("无新输出应回 (no new output): %q", got)
	}
	// 另一个会话读同一任务：游标按会话隔离，仍应看到全部输出
	other := WithSessionID(context.Background(), "s2")
	got = r.Execute(other, call("job_output", `{"job_id":`+quote(j.ID())+`}`))
	if !strings.Contains(got, "first line") {
		t.Fatalf("游标应按会话隔离（别的会话应看到全部输出）: %q", got)
	}
	// wait=true：等它结束（假 producer 在 50ms 后 settle）
	go func() {
		time.Sleep(50 * time.Millisecond)
		if _, err := j.Write([]byte("done\n")); err != nil {
			t.Error(err)
		}
		j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	}()
	start := time.Now()
	got = r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":5000}`))
	if !strings.Contains(got, "done") || !strings.Contains(got, "[status: completed]") {
		t.Fatalf("wait=true 应在结束后回最终输出: %q", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("wait 不该超时（假任务 50ms 就结束了）: %v", elapsed)
	}
}

// TestJobOutputWaitTimesOut：wait 超时回当前输出 + [status: running]（不是错误）。
func TestJobOutputWaitTimesOut(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "常驻", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Write([]byte("still working\n")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":200}`))
	if !strings.Contains(got, "[status: running]") || !strings.Contains(got, "still working") {
		t.Fatalf("超时应回当前输出 + [status: running]: %q", got)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("wait 应按 timeout_ms 到点返回: %v", elapsed)
	}
}

// TestJobListAndKill：job_list 透传会话过滤，job_kill 归属记为 agent 且非阻塞。
func TestJobListAndKill(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	a, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "会话一的任务", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "别的会话的任务", SessionID: "s2"}); err != nil {
		t.Fatal(err)
	}
	got := r.Execute(ctx, call("job_list", `{}`))
	if !strings.Contains(got, a.ID()) || !strings.Contains(got, "别的会话的任务") {
		t.Fatalf("默认应列全部会话的任务: %q", got)
	}
	got = r.Execute(ctx, call("job_list", `{"session_only":true}`))
	if !strings.Contains(got, a.ID()) || strings.Contains(got, "别的会话的任务") {
		t.Fatalf("session_only 应只列本会话: %q", got)
	}
	// job_kill：非阻塞、归属 agent
	got = r.Execute(ctx, call("job_kill", `{"job_id":`+quote(a.ID())+`}`))
	if !strings.Contains(got, "已请求取消") {
		t.Fatalf("job_kill 应回「已请求取消」: %q", got)
	}
	snap := a.Snapshot()
	if snap.Status != jobs.StatusStopping || snap.EndedBy != jobs.EndedAgent {
		t.Fatalf("job_kill 应置 stopping + EndedBy=agent: %+v", snap)
	}
	// 真正的 settle 由 producer 收尾（这里模拟）
	a.Settle(jobs.StatusKilled, jobs.EndedAgent, "已取消")
	got = r.Execute(ctx, call("job_list", `{"session_only":true}`))
	if !strings.Contains(got, "结束方 agent") {
		t.Fatalf("列表应显示结束方: %q", got)
	}
}

// TestBashForegroundUnaffected：run_in_background 为 false（或省略）时前台路径
// 不变——输出、退出码照旧，且不登记任务。
func TestBashForegroundUnaffected(t *testing.T) {
	skipWithoutSh(t)
	r, mgr := newJobRegistry(t)
	got := r.Execute(context.Background(), call("bash", `{"command":"echo fg-hello; exit 3"}`))
	if !strings.Contains(got, "fg-hello") || !strings.Contains(got, "退出码 3") {
		t.Fatalf("前台路径不该被改动: %q", got)
	}
	if got := r.Execute(context.Background(), call("bash", `{"command":"echo fg2","run_in_background":false}`)); !strings.Contains(got, "fg2") {
		t.Fatalf("显式 false 也应走前台: %q", got)
	}
	if list := mgr.List(""); len(list) != 0 {
		t.Fatalf("前台执行不该登记后台任务: %+v", list)
	}
}

// TestBashRunInBackground：run_in_background=true 立刻回 job id，输出进任务，
// 收尾按退出码 settle（0 → completed / 非 0 → failed）。
func TestBashRunInBackground(t *testing.T) {
	skipWithoutSh(t)
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	got := r.Execute(ctx, call("bash", `{"command":"echo bg-hello","run_in_background":true}`))
	if !strings.Contains(got, "后台任务已启动") || !strings.Contains(got, "job-") {
		t.Fatalf("后台启动应回 job id 与用法: %q", got)
	}
	list := mgr.List("")
	if len(list) != 1 {
		t.Fatalf("应登记一个后台任务: %+v", list)
	}
	if list[0].SessionID != "s1" || list[0].Kind != "bash" {
		t.Fatalf("任务应记归属会话与 kind: %+v", list[0])
	}
	snap := waitJobStatus(t, mgr, list[0].ID, jobs.StatusCompleted)
	if snap.EndedBy != jobs.EndedSelf || snap.Detail != "退出码 0" {
		t.Fatalf("正常退出应 settle 成 self/completed: %+v", snap)
	}
	out := r.Execute(ctx, call("job_output", `{"job_id":`+quote(list[0].ID)+`}`))
	if !strings.Contains(out, "bg-hello") || !strings.Contains(out, "[status: completed]") {
		t.Fatalf("任务输出应可读: %q", out)
	}
	if !strings.Contains(out, "结束方: self") {
		t.Fatalf("结束方应可见: %q", out)
	}

	// 非零退出码 → failed（归属仍是 self）
	r.Execute(ctx, call("bash", `{"command":"exit 7","run_in_background":true}`))
	list = mgr.List("s1")
	var failed jobs.Snapshot
	for _, s := range list {
		if s.Label == "exit 7" {
			failed = s
		}
	}
	if failed.ID == "" {
		t.Fatalf("未登记 exit 7 任务: %+v", list)
	}
	failed = waitJobStatus(t, mgr, failed.ID, jobs.StatusFailed)
	if failed.Detail != "退出码 7" {
		t.Fatalf("非零退出码应记 failed/退出码 7: %+v", failed)
	}
}

// TestBashBackgroundKilledByTool：agent 用 job_kill 停掉的任务，收尾归属仍是 agent
// （producer 收尾时不能把它抹成 self——那正是本功能要修的缺陷）。
func TestBashBackgroundKilledByTool(t *testing.T) {
	skipWithoutSh(t)
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	got := r.Execute(ctx, call("bash", `{"command":"sleep 30","run_in_background":true}`))
	if !strings.Contains(got, "后台任务已启动") {
		t.Fatalf("后台启动失败: %q", got)
	}
	id := mgr.List("")[0].ID
	if kill := r.Execute(ctx, call("job_kill", `{"job_id":`+quote(id)+`}`)); !strings.Contains(kill, "已请求取消") {
		t.Fatalf("job_kill 失败: %q", kill)
	}
	snap := waitJobStatus(t, mgr, id, jobs.StatusKilled)
	if snap.EndedBy != jobs.EndedAgent {
		t.Fatalf("被 agent 停掉的任务归属必须是 agent（不能抹成 self）: %+v", snap)
	}
}

// TestBashBackgroundWithoutManager：未注入管理器时后台路径报错可读（不 panic）。
func TestBashBackgroundWithoutManager(t *testing.T) {
	skipWithoutSh(t)
	r := New()
	got := r.Execute(context.Background(), call("bash", `{"command":"echo x","run_in_background":true}`))
	if !strings.Contains(got, "未装配") {
		t.Fatalf("未注入管理器时应回可读错误: %q", got)
	}
}

// TestBashConfirmMentionsBackground：确认门在启动那一刻同步走完，且提示里说明
// 它是后台运行（批准后立刻返回 job id、不再有后续确认）。
func TestBashConfirmMentionsBackground(t *testing.T) {
	r := New()
	got := r.Confirm(context.Background(), call("bash", `{"command":"node scripts/dev.mjs","run_in_background":true}`))
	if !strings.Contains(got, "node scripts/dev.mjs") || !strings.Contains(got, "后台运行") {
		t.Fatalf("后台确认卡应显示命令并注明后台运行: %q", got)
	}
	if got := r.Confirm(context.Background(), call("bash", `{"command":"ls"}`)); strings.Contains(got, "后台运行") {
		t.Fatalf("前台确认卡不该说后台运行: %q", got)
	}
}
