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
	if list[0].Workspace != "" {
		t.Fatalf("ctx 未注入 Workspace 时应为空串（未分组）: %+v", list[0])
	}

	// 项目归属（面板分组用）：Start 时从执行会话的 ctx 焊进 Spec（docs/jobs.md §4）。
	r.Execute(WithWorkspace(ctx, "lxcode"), call("bash", `{"command":"echo bg-ws","run_in_background":true}`))
	var wsJob jobs.Snapshot
	for _, s := range mgr.List("") {
		if s.Label == "echo bg-ws" {
			wsJob = s
		}
	}
	if wsJob.ID == "" || wsJob.Workspace != "lxcode" {
		t.Fatalf("bash 起任务应把 ctx 的 Workspace 焊进 Spec: %+v", wsJob)
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

// ---- wait_condition（等待原语，2026-12 用户拍板）----

// TestWaitConditionOutput：output 谓词——有新输出即返回（不等任务结束）。
func TestWaitConditionOutput(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "跑批", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	// 200ms 后写一行（不 settle——任务继续跑）
	go func() {
		time.Sleep(200 * time.Millisecond)
		_, _ = j.Write([]byte("phase 1 done\n"))
	}()
	start := time.Now()
	got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":5000,"wait_condition":"output"}`))
	if !strings.Contains(got, "[hit: output") || !strings.Contains(got, "phase 1 done") {
		t.Fatalf("output 谓词应在新输出时命中: %q", got)
	}
	if !strings.Contains(got, "[status: running]") {
		t.Fatalf("任务未结束应仍是 running（不等 settle）: %q", got)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("output 命中不该等到超时: %v", elapsed)
	}
}

// TestWaitConditionExitOK：exit_ok——结束且成功才命中；失败结束不命中、等到超时。
func TestWaitConditionExitOK(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "会挂的", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		j.Settle(jobs.StatusFailed, jobs.EndedSelf, "退出码 2")
	}()
	got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":400,"wait_condition":"exit_ok"}`))
	if !strings.Contains(got, "[status: failed]") || strings.Contains(got, "[hit:") {
		t.Fatalf("exit_ok 在失败结束时不该命中（应超时返回 failed、无 hit）: %q", got)
	}

	// 成功结束：命中
	j2, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "会成的", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		j2.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	}()
	got = r.Execute(ctx, call("job_output", `{"job_id":`+quote(j2.ID())+`,"wait":true,"timeout_ms":4000,"wait_condition":"exit_ok"}`))
	if !strings.Contains(got, "[hit: exit_ok") || !strings.Contains(got, "[status: completed]") {
		t.Fatalf("exit_ok 应在成功结束时命中: %q", got)
	}
}

// TestWaitConditionIdle：idle——输出静默 idle_ms 即返回；静默被打断则重计。
func TestWaitConditionIdle(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "断续输出", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	// 100ms 写一行，之后静默（idle_ms=150 → 250ms 左右命中）
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = j.Write([]byte("last line\n"))
	}()
	start := time.Now()
	got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":5000,"wait_condition":"idle","idle_ms":150}`))
	if !strings.Contains(got, "[hit: idle") || !strings.Contains(got, "[status: running]") {
		t.Fatalf("idle 谓词应在静默超窗时命中: %q", got)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("idle 应在最后一次输出之后 idle_ms 命中: %v", elapsed)
	}
}

// TestWaitConditionPattern：pattern——对全量落盘日志匹配（含等待前已存在的行），
// 结果带回命中行原文。
func TestWaitConditionPattern(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "构建", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	// 等待**之前**就写好命中行——pattern 是全量匹配，历史行也命中（快照预判定）
	if _, err := j.Write([]byte("compiling...\nBUILD DONE in 3s\n")); err != nil {
		t.Fatal(err)
	}
	got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":3000,"wait_condition":{"pattern":"BUILD DONE"}}`))
	if !strings.Contains(got, `[hit: pattern "BUILD DONE" 命中] BUILD DONE in 3s`) {
		t.Fatalf("pattern 谓词应带回命中行原文: %q", got)
	}
	// 未编译成功的正则：超时返回、无 hit
	got = r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":200,"wait_condition":{"pattern":"NEVER APPEARS"}}`))
	if !strings.Contains(got, "[status: running]") || strings.Contains(got, "[hit:") {
		t.Fatalf("pattern 未命中应超时返回无 hit: %q", got)
	}
}

// TestWaitConditionOrFirstWins：OR 组合——谁先到返回谁。
func TestWaitConditionOrFirstWins(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "两路赛跑", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	// 100ms 写输出（output 先到），500ms 才 settle（exit 后到）
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = j.Write([]byte("early line\n"))
		time.Sleep(400 * time.Millisecond)
		j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	}()
	start := time.Now()
	got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":5000,"wait_condition":["output","exit"]}`))
	if !strings.Contains(got, "[hit: output") || !strings.Contains(got, "early line") {
		t.Fatalf("OR 组合应先到的 output 命中: %q", got)
	}
	if !strings.Contains(got, "[status: running]") {
		t.Fatalf("output 先到时任务应仍在跑: %q", got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("先到的条件应立刻返回（不等后到的 exit）: %v", elapsed)
	}
}

// TestWaitConditionSnapshotPreSatisfied：快照预判定——条件在 wait 调用前已满足
// 也要命中（不能白等到超时）。output 用已读基线判定，pattern 用全量日志判定。
func TestWaitConditionSnapshotPreSatisfied(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "早就写完了", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Write([]byte("already here\n")); err != nil {
		t.Fatal(err)
	}
	// 先读一次建立基线（游标推进——「已有输出读过」）
	r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`}`))
	// output：等待期间没有新输出 → 不该命中（基线已读到最新）——超时返回
	got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":250,"wait_condition":"output"}`))
	if strings.Contains(got, "[hit: output") {
		t.Fatalf("基线之后无新输出，output 不该命中: %q", got)
	}
	// 前一次超时返回**补读了等待期间无新输出**（wait 路径的增量补读把游标又推进
	// 了一次，语义不变）；再写一行：**不读**直接 wait——基线之上的新输出必须命中
	if _, err := j.Write([]byte("fresh line\n")); err != nil {
		t.Fatal(err)
	}
	got = r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":3000,"wait_condition":"output"}`))
	if !strings.Contains(got, "[hit: output") || !strings.Contains(got, "fresh line") {
		t.Fatalf("基线之上的新输出应命中（含等待前已写入的）: %q", got)
	}
	// pattern 同理：命中行早已在日志里，wait 之前就该判中
	got = r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"timeout_ms":3000,"wait_condition":{"pattern":"fresh line"}}`))
	if !strings.Contains(got, `[hit: pattern "fresh line" 命中]`) {
		t.Fatalf("pattern 的快照预判定应命中历史行: %q", got)
	}
}

// TestWaitConditionParamErrors：非法谓词 / 非法正则报参数错（模型当场重试，
// 不是静默等到超时）。
func TestWaitConditionParamErrors(t *testing.T) {
	r, mgr := newJobRegistry(t)
	ctx := WithSessionID(context.Background(), "s1")
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "x", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"wait_condition":"nope"}`)); !strings.Contains(got, "未知谓词") {
		t.Fatalf("未知谓词应报参数错: %q", got)
	}
	if got := r.Execute(ctx, call("job_output", `{"job_id":`+quote(j.ID())+`,"wait":true,"wait_condition":{"pattern":"([bad"}}`)); !strings.Contains(got, "不是合法正则") {
		t.Fatalf("非法正则应报参数错: %q", got)
	}
}

// TestWaitConditionLastOutputAt：idle 的静默锚是 lastOutputAt（jobs 快照暴露）——
// 从未写过输出的任务从 startedAt 起算（不编零值时刻）。
func TestWaitConditionLastOutputAt(t *testing.T) {
	_, mgr := newJobRegistry(t)
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "沉默的", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	snap := j.Snapshot()
	if !snap.LastOutputAt.IsZero() {
		t.Fatalf("未写过输出时 LastOutputAt 应为零值: %v", snap.LastOutputAt)
	}
	if _, err := j.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	snap = j.Snapshot()
	if snap.LastOutputAt.IsZero() {
		t.Fatalf("写过输出后 LastOutputAt 应有值: %+v", snap)
	}
}
