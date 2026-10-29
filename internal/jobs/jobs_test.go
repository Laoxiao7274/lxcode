package jobs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestManager 建一个落盘到临时目录的注册表（测试结束统一 Shutdown，
// 关闭日志句柄——Windows 上未关闭的文件会挡住 TempDir 清理）。
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(t.TempDir())
	t.Cleanup(m.Shutdown)
	return m
}

// TestConcurrentWritesAndRead：多任务（这里是多 goroutine 写同一任务）并发写
// 必须不丢字节：全量落盘长度 == 写入总量，内存尾缓冲只保留最后 OutputLimit 字节，
// 游标停在总字节数上。这是「全程并发安全」的钉子。
func TestConcurrentWritesAndRead(t *testing.T) {
	m := newTestManager(t)
	const writers, chunks, chunkLen, limit = 8, 50, 17, 4096
	j, err := m.Start(Spec{Kind: "bash", Label: "并发写", SessionID: "s1", OutputLimit: limit})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			chunk := strings.Repeat(string(rune('a'+w)), chunkLen)
			for i := 0; i < chunks; i++ {
				if _, err := j.Write([]byte(chunk)); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	total := int64(writers * chunks * chunkLen)
	// 全量落盘：文件里一个字节都不少
	raw, err := os.ReadFile(j.Snapshot().OutputPath)
	if err != nil {
		t.Fatalf("读日志: %v", err)
	}
	if int64(len(raw)) != total {
		t.Fatalf("落盘字节数应 %d，实际 %d", total, len(raw))
	}
	// 内存只保留尾部
	data, next, snap, err := m.Read(j.ID(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != limit {
		t.Fatalf("内存尾缓冲应保留 %d 字节，实际 %d", limit, len(data))
	}
	if next != total {
		t.Fatalf("游标应停在写入总量 %d，实际 %d", total, next)
	}
	if !strings.HasSuffix(string(raw), data) {
		t.Fatal("内存尾缓冲应与落盘内容尾部一致")
	}
	if snap.Status != StatusRunning {
		t.Fatalf("未 settle 的任务状态应为 running: %s", snap.Status)
	}
}

// TestReadIncrementalCursor：Read 是**增量**语义——游标之后的新输出，
// 且 maxBytes 分片时游标按返回字节数前进。
func TestReadIncrementalCursor(t *testing.T) {
	m := newTestManager(t)
	j, err := m.Start(Spec{Kind: "bash", Label: "游标"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, next, _, err := m.Read(j.ID(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" || next != 5 {
		t.Fatalf("首次读: %q next=%d", got, next)
	}
	// 没有新输出：增量返回空、游标不动（工具层据此回 "(no new output)"）
	got, next2, _, _ := m.Read(j.ID(), next, 0)
	if got != "" || next2 != next {
		t.Fatalf("无新输出时应为空且游标不动: %q next=%d", got, next2)
	}
	if _, err := j.Write([]byte(" world")); err != nil {
		t.Fatal(err)
	}
	got, next3, _, _ := m.Read(j.ID(), next, 0)
	if got != " world" || next3 != 11 {
		t.Fatalf("增量读: %q next=%d", got, next3)
	}
	// maxBytes 分片：游标按实际返回的字节数前进，不丢不重
	got, next4, _, _ := m.Read(j.ID(), 0, 4)
	if got != "hell" || next4 != 4 {
		t.Fatalf("分片读: %q next=%d", got, next4)
	}
	got, next5, _, _ := m.Read(j.ID(), next4, 0)
	if got != "o world" || next5 != 11 {
		t.Fatalf("分片续读: %q next=%d", got, next5)
	}
	// 缓冲滚动后旧游标被钳到缓冲起点（不返回越界数据）
	small, err := m.Start(Spec{Kind: "bash", Label: "小缓冲", OutputLimit: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := small.Write([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	got, next6, _, _ := m.Read(small.ID(), 0, 0)
	if got != "efgh" || next6 != 8 {
		t.Fatalf("缓冲滚动后应返回尾部: %q next=%d", got, next6)
	}
	if _, _, _, err := m.Read("job-nope", 0, 0); err == nil {
		t.Fatal("未知任务应报错")
	}
}

// TestSettleIdempotent：Settle 幂等——重复 settle 不改状态、不再发事件。
func TestSettleIdempotent(t *testing.T) {
	m := newTestManager(t)
	j, err := m.Start(Spec{Kind: "bash", Label: "幂等"})
	if err != nil {
		t.Fatal(err)
	}
	var settled int32
	m.Subscribe(func(ev Event) {
		if ev.Kind == EventSettled {
			atomic.AddInt32(&settled, 1)
		}
	})
	j.Settle(StatusCompleted, EndedSelf, "退出码 0")
	j.Settle(StatusFailed, EndedSelf, "退出码 1") // 重复：no-op
	if got := atomic.LoadInt32(&settled); got != 1 {
		t.Fatalf("settled 事件应恰好一次，实际 %d", got)
	}
	snap := j.Snapshot()
	if snap.Status != StatusCompleted || snap.EndedBy != EndedSelf || snap.Detail != "退出码 0" {
		t.Fatalf("重复 settle 不该覆盖首次结果: %+v", snap)
	}
	if snap.FinishedAt.IsZero() {
		t.Fatal("settle 应记录结束时间")
	}
	// 非终态传参当作完成（不让任务永远停在 running）
	j2, err := m.Start(Spec{Kind: "bash", Label: "非终态"})
	if err != nil {
		t.Fatal(err)
	}
	j2.Settle(StatusRunning, EndedSelf, "")
	if got := j2.Snapshot().Status; got != StatusCompleted {
		t.Fatalf("非终态 settle 应回落 completed: %s", got)
	}
}

// TestKillOwnershipAndCancel：Kill 置 stopping、调 producer 登记的 cancel、
// 重复 Kill 是 no-op（不重复调 cancel）；by 是归属的唯一入口。
func TestKillOwnershipAndCancel(t *testing.T) {
	m := newTestManager(t)
	j, err := m.Start(Spec{Kind: "bash", Label: "归属", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	var cancels int32
	cr, ok := j.(CancelRegistrar)
	if !ok {
		t.Fatal("Start 返回的句柄应实现 CancelRegistrar")
	}
	cr.SetCancel(func() { atomic.AddInt32(&cancels, 1) })

	snap, err := m.Kill(j.ID(), EndedUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Status != StatusStopping || snap.EndedBy != EndedUser {
		t.Fatalf("Kill 应置 stopping + 记录 by: %+v", snap)
	}
	if got := atomic.LoadInt32(&cancels); got != 1 {
		t.Fatalf("cancel 应被调用一次，实际 %d", got)
	}
	// 重复 Kill：no-op（不再调 cancel，也不改归属）
	again, err := m.Kill(j.ID(), EndedAgent)
	if err != nil {
		t.Fatal(err)
	}
	if again.EndedBy != EndedUser || atomic.LoadInt32(&cancels) != 1 {
		t.Fatalf("重复 Kill 应是 no-op: %+v cancels=%d", again, cancels)
	}
	// 真正的 settle 由 producer 收尾时给（它是唯一知道退出码的人）
	j.Settle(StatusKilled, EndedUser, "已取消")
	if got := j.Snapshot(); got.Status != StatusKilled || got.EndedBy != EndedUser {
		t.Fatalf("producer 收尾应定稿: %+v", got)
	}
	if _, err := m.Kill("job-nope", EndedUser); err == nil {
		t.Fatal("未知任务 Kill 应报错")
	}
}

// TestKillBeforeCancelRegistered：Start 返回与 producer 登记 cancel 之间有窗口，
// 此时 Kill 不能静默失效（登记时补调一次）。
func TestKillBeforeCancelRegistered(t *testing.T) {
	m := newTestManager(t)
	j, err := m.Start(Spec{Kind: "bash", Label: "窗口"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Kill(j.ID(), EndedAgent); err != nil {
		t.Fatal(err)
	}
	var called int32
	j.(CancelRegistrar).SetCancel(func() { atomic.AddInt32(&called, 1) })
	if atomic.LoadInt32(&called) != 1 {
		t.Fatal("Kill 先到、cancel 后登记时必须补调一次（否则进程永远收不了尾）")
	}
}

// TestShutdownKillsAll：后端退出即全杀（EndedBy=backend），已结束的不动。
func TestShutdownKillsAll(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	a, err := m.Start(Spec{Kind: "bash", Label: "a", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Start(Spec{Kind: "bash", Label: "b", SessionID: "s2"})
	if err != nil {
		t.Fatal(err)
	}
	done, err := m.Start(Spec{Kind: "bash", Label: "done"})
	if err != nil {
		t.Fatal(err)
	}
	done.Settle(StatusCompleted, EndedSelf, "退出码 0")

	var canceled int32
	a.(CancelRegistrar).SetCancel(func() { atomic.AddInt32(&canceled, 1) })
	b.(CancelRegistrar).SetCancel(func() { atomic.AddInt32(&canceled, 1) })

	m.Shutdown()

	if got := atomic.LoadInt32(&canceled); got != 2 {
		t.Fatalf("Shutdown 应取消全部在跑任务（不留孤儿进程），实际 %d", got)
	}
	for _, j := range []Job{a, b} {
		snap := j.Snapshot()
		if snap.Status != StatusKilled || snap.EndedBy != EndedBackend || snap.Detail != ShutdownDetail {
			t.Fatalf("Shutdown 应把在跑任务 settle 成 killed/backend: %+v", snap)
		}
	}
	if got := done.Snapshot(); got.Status != StatusCompleted {
		t.Fatalf("已结束的任务不该被 Shutdown 改写: %+v", got)
	}
	// 幂等 + 关停后不再收新任务
	m.Shutdown()
	if _, err := m.Start(Spec{Kind: "bash", Label: "after"}); err == nil {
		t.Fatal("Shutdown 后不应再能起任务")
	}
	// 句柄已关：日志文件可以删掉（Windows 上未关闭的文件删不掉）
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("Shutdown 后日志句柄应已关闭: %v", err)
	}
}

// TestLogFileContent：全量落盘——内容与写入逐字节一致，且 job 结束后仍可读
// （DSH 的纯内存缓冲做不到这点）。
func TestLogFileContent(t *testing.T) {
	m := newTestManager(t)
	j, err := m.Start(Spec{Kind: "bash", Label: "落盘"})
	if err != nil {
		t.Fatal(err)
	}
	want := "第一行\n第二行\n退出码 0\n"
	if _, err := j.Write([]byte(want)); err != nil {
		t.Fatal(err)
	}
	path := j.Snapshot().OutputPath
	if path == "" {
		t.Fatal("落盘路径不能为空")
	}
	if filepath.Dir(path) != filepath.Join(m.Dir(), "jobs") {
		t.Fatalf("日志应落 <sessions>/jobs/<id>.log: %s", path)
	}
	if filepath.Base(path) != j.ID()+".log" {
		t.Fatalf("日志文件名应为 <job-id>.log: %s", path)
	}
	j.Settle(StatusCompleted, EndedSelf, "退出码 0")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("任务结束后日志仍应可读: %v", err)
	}
	if string(raw) != want {
		t.Fatalf("落盘内容不符: %q", raw)
	}
}

// TestDiskFailureDegradesToMemory：落盘失败不能让任务起不来——降级为纯内存，
// 输出照常可读，只是没有全量日志（OutputPath 为空）。
func TestDiskFailureDegradesToMemory(t *testing.T) {
	// 把「会话目录」指向一个普通文件：MkdirAll(<file>/jobs) 必然失败
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(blocker)
	t.Cleanup(m.Shutdown)
	j, err := m.Start(Spec{Kind: "bash", Label: "降级"})
	if err != nil {
		t.Fatalf("落盘失败不该让任务起不来: %v", err)
	}
	if _, err := j.Write([]byte("内存里的输出")); err != nil {
		t.Fatalf("降级后写入应成功: %v", err)
	}
	data, _, snap, err := m.Read(j.ID(), 0, 0)
	if err != nil || data != "内存里的输出" {
		t.Fatalf("降级后仍应能读内存输出: %q %v", data, err)
	}
	if snap.OutputPath != "" {
		t.Fatalf("降级后不该谎报落盘路径: %q", snap.OutputPath)
	}
}

// TestListFilterAndOrder：List 按会话过滤、按开始时间倒序。
func TestListFilterAndOrder(t *testing.T) {
	m := newTestManager(t)
	first, err := m.Start(Spec{Kind: "bash", Label: "first", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Start(Spec{Kind: "bash", Label: "second", SessionID: "s2"})
	if err != nil {
		t.Fatal(err)
	}
	all := m.List("")
	if len(all) != 2 {
		t.Fatalf("应列出全部任务: %d", len(all))
	}
	if all[0].ID != second.ID() || all[1].ID != first.ID() {
		t.Fatalf("应按开始时间倒序: %v", []string{all[0].ID, all[1].ID})
	}
	only := m.List("s1")
	if len(only) != 1 || only[0].ID != first.ID() {
		t.Fatalf("按会话过滤失败: %+v", only)
	}
	if got := m.List("nope"); len(got) != 0 {
		t.Fatalf("未知会话应回空: %+v", got)
	}
}

// TestSubscribeEventsAndUnsubscribe：started/output/settled 三种事件都能收到，
// 取消订阅后不再收；订阅者回调 Read 不能死锁（事件不持锁派发）。
func TestSubscribeEventsAndUnsubscribe(t *testing.T) {
	m := newTestManager(t)
	var kinds []EventKind
	unsub := m.Subscribe(func(ev Event) {
		kinds = append(kinds, ev.Kind)
		// 订阅者回调注册表：持锁派发会在这里自锁死
		_, _, _, _ = m.Read(ev.Snapshot.ID, 0, 0)
		_ = m.List("")
	})
	j, err := m.Start(Spec{Kind: "bash", Label: "订阅"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	j.Settle(StatusCompleted, EndedSelf, "退出码 0")
	want := []EventKind{EventStarted, EventOutput, EventSettled}
	if len(kinds) != len(want) {
		t.Fatalf("事件序列不符: %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("事件序列不符: %v", kinds)
		}
	}
	unsub()
	unsub() // 取消是幂等的
	before := len(kinds)
	if _, err := j.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if len(kinds) != before {
		t.Fatalf("取消订阅后不该再收到事件: %v", kinds)
	}
}

// TestStartRequiresKind：Kind 是路由与展示的依据，空 Kind 拒绝启动。
func TestStartRequiresKind(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Start(Spec{Label: "没有 kind"}); err == nil {
		t.Fatal("空 Kind 应拒绝")
	}
}

// TestSnapshotTimes：startedAt 在启动时就有，finishedAt 只在 settle 后出现。
func TestSnapshotTimes(t *testing.T) {
	m := newTestManager(t)
	before := time.Now()
	j, err := m.Start(Spec{Kind: "bash", Label: "时间"})
	if err != nil {
		t.Fatal(err)
	}
	snap := j.Snapshot()
	if snap.StartedAt.Before(before) || !snap.FinishedAt.IsZero() {
		t.Fatalf("启动快照时间不符: %+v", snap)
	}
	j.Settle(StatusFailed, EndedSelf, "退出码 2")
	snap = j.Snapshot()
	if snap.FinishedAt.IsZero() || snap.FinishedAt.Before(snap.StartedAt) {
		t.Fatalf("结束时间不符: %+v", snap)
	}
	if snap.Detail != "退出码 2" {
		t.Fatalf("Detail 应保留退出码: %q", snap.Detail)
	}
}

// TestOwnerOfFallsBackToSession：时间线归属的唯一判定点——没设 owner 时回落执行
// 会话本身（顶层会话起的任务，owner 就是它自己）。回落逻辑只此一处，免得事件路由、
// 唤醒投递、job.list 过滤三处各写一遍而漂移。
func TestOwnerOfFallsBackToSession(t *testing.T) {
	if got := OwnerOf(Spec{SessionID: "s-1"}); got != "s-1" {
		t.Fatalf("未设 owner 时应回落 SessionID，得到 %q", got)
	}
	if got := OwnerOf(Spec{SessionID: "s-1", OwnerSessionID: "s-root"}); got != "s-root" {
		t.Fatalf("设了 owner 时应以它为准，得到 %q", got)
	}
}

// TestListFiltersByOwner：List 的按会话过滤走**时间线归属**——父会话要看得见子
// Agent 起的任务（那正是它自己时间线上的卡）；别的会话看不到。
func TestListFiltersByOwner(t *testing.T) {
	m := NewManager("")
	defer m.Shutdown()
	childJob, err := m.Start(Spec{Kind: "bash", Label: "子 Agent 起的", SessionID: "s-child", OwnerSessionID: "s-owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(Spec{Kind: "bash", Label: "顶层自己起的", SessionID: "s-owner"}); err != nil {
		t.Fatal(err)
	}

	// 父会话看到两张（子 Agent 起的 + 自己起的）
	if got := m.List("s-owner"); len(got) != 2 {
		t.Fatalf("父会话应看到 2 张卡，得到 %d: %+v", len(got), got)
	}
	// 别的会话看不到
	if got := m.List("s-other"); len(got) != 0 {
		t.Fatalf("别的会话不该看到，得到 %d: %+v", len(got), got)
	}
	// 父会话看得到子 Agent 起的那个（归属生效，不是"父会话只看见自己起的"）。
	// 断言**在不在**而不是排第几：两张卡启动时刻相同，顺序由 seq 兜底、不是契约。
	seen := false
	for _, s := range m.List("s-owner") {
		if s.ID == childJob.ID() {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("父会话应看到子 Agent 起的那个任务: %+v", m.List("s-owner"))
	}
	// 子会话**不是**一条时间线：按子会话 id 过滤看不到东西。这是对的——
	// 子会话自己的 job_list 走的是 OwnerSessionID(ctx)（= 父会话），不传自己的 id。
	if got := m.List("s-child"); len(got) != 0 {
		t.Fatalf("子会话不是时间线，按它过滤应为空: %+v", got)
	}
	// 快照里 owner 恒非空（Start 时定稿）
	for _, s := range m.List("s-owner") {
		if s.OwnerSessionID == "" {
			t.Fatalf("快照的 owner 应已定稿: %+v", s)
		}
	}
}
