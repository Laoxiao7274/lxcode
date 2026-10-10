// 轮末自动合并钩子（2026-10 硬钩子）的验收用例：
//   - 轮末自动提交产生了新提交 → 自动出现 Kind=merge 的合并任务；
//   - 无改动轮（CommitAll 返回 false）→ 不起任务；
//   - 模型已先发起合并（按项目互斥占住）→ 钩子静默跳过，始终只有一个任务；
//   - 钩子起任务失败（互斥之外的原因）→ 只记日志，不影响会话、不留任务。
//
// 全程不 spawn 真进程、不打网络（假 stream）；git 操作走真仓库（与 autocommit 同款）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/llm"
)

// waitMergeJobForHook 等某条合并任务进入终态（钩子用例专用：全量套件下机器负载高，
// 集成工作树准备 + merger 子会话一轮可能超过 5s，给足 20s）。
func waitMergeJobForHook(t *testing.T, mgr *jobs.Manager, sessionID, jobID string) jobs.Snapshot {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, snap := range mgr.List(sessionID) {
			if snap.ID == jobID && snap.Status.Terminal() {
				return snap
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("合并任务 %s 未在期限内收尾", jobID)
	return jobs.Snapshot{}
}

// waitAutoMergeSettled 等某会话名下全部合并任务进入终态（钩子是异步的，
// 测试收尾前不能留在途 goroutine）。
func waitAutoMergeSettled(t *testing.T, srv *Server, mgr *jobs.Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		running := false
		for _, snap := range mgr.List(id) {
			if snap.Kind == "merge" && !snap.Status.Terminal() {
				running = true
			}
		}
		if !running {
			waitSessionIdle(t, srv, id)
			srv.waitAutoCommits()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("合并任务未在期限内收尾")
}

// hookTestStream 是钩子用例的可控流：只有**带 gateText 的那一轮**（测试要趁机
// 往工作树写文件的目标轮）走 started/release 门控，其余全部直通——merger 子会话
// 的任务说明书、合并收尾后的唤醒通告轮若也被门控，测试会被自己的假流卡死
//（通告轮卡忙 = 会话永不空闲）。
func hookTestStream(gateText string, started chan<- string, release <-chan struct{}) testStream {
	return func(ctx context.Context, m config.ModelConfig, messages []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		text := ""
		for _, msg := range messages {
			if msg.Role == "user" {
				text = msg.Content
			}
		}
		if text == gateText {
			return autoCommitStream(started, release)(ctx, m, messages, opts)
		}
		return immediateStream(ctx, m, messages, opts)
	}
}

// TestAutoMergeHookFiresAfterCommit：轮末有新提交 → 钩子自动起合并任务，任务正常收尾。
func TestAutoMergeHookFiresAfterCommit(t *testing.T) {
	repo := initGitProject(t)
	started := make(chan string, 8)
	release := make(chan struct{}, 8)
	srv, client, mgr := newJobTestServer(t, hookTestStream("自动合并钩子：新建文件", started, release))
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)

	sendProjectTurn(t, srv, client, id, "自动合并钩子：新建文件", started, release, func(path string) {
		if err := os.WriteFile(filepath.Join(path, "a.txt"), []byte("a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	srv.waitAutoCommits()
	srv.waitAutoMergeHooks() // 等钩子完成发起尝试

	// 自动出现 Kind=merge 的任务
	var job jobs.Snapshot
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, snap := range mgr.List(id) {
			if snap.Kind == "merge" {
				job = snap
			}
		}
		if job.ID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.ID == "" {
		t.Fatal("轮末有新提交，但自动合并任务未出现")
	}
	// 任务正常收尾（merger 在集成分支工作树里合并成功）
	final := waitMergeJobForHook(t, mgr, id, job.ID)
	if final.Status != jobs.StatusCompleted {
		t.Fatalf("自动合并任务应成功收尾: %s detail=%q", final.Status, final.Detail)
	}
	waitAutoMergeSettled(t, srv, mgr, id)
}

// TestAutoMergeHookSkipsCleanTurn：无改动轮（CommitAll 返回 false）→ 不起任务。
func TestAutoMergeHookSkipsCleanTurn(t *testing.T) {
	repo := initGitProject(t)
	started := make(chan string, 8)
	release := make(chan struct{}, 8)
	srv, client, mgr := newJobTestServer(t, autoCommitStream(started, release))
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)

	sendProjectTurn(t, srv, client, id, "自动合并钩子：这轮不改东西", started, release, nil)
	srv.waitAutoCommits()
	srv.waitAutoMergeHooks()

	// 给钩子留出理论上的触发窗口：1 秒内不得出现合并任务
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		for _, snap := range mgr.List(id) {
			if snap.Kind == "merge" {
				t.Fatalf("无改动轮不该触发自动合并: %+v", snap)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAutoMergeHookSilentWhenMergeRunning：模型已先发起合并（互斥占住）→
// 钩子静默跳过，自始至终只有一个合并任务。
func TestAutoMergeHookSilentWhenMergeRunning(t *testing.T) {
	repo := initGitProject(t)
	release := make(chan struct{}, 4)
	srv, client, mgr := newJobTestServer(t, mergeBlockStream(release))
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好")
	commitChange(t, srv, id, "a.txt") // 已提交的改动：分支 ahead，工作树干净

	// 模型纪律先触发 merge_request：任务起、停在 running（阻塞流）
	if out := callMergeRequest(srv, id, ""); strings.Contains(out, "错误:") {
		t.Fatalf("merge_request 应成功: %q", out)
	}
	first := mergeJobOf(mgr, id)
	if first.ID == "" {
		t.Fatal("merge_request 任务未启动")
	}

	// 钩子触发（自动提交产生新提交后走的就是这条路径）：互斥拒绝，静默跳过
	srv.maybeAutoMerge(id)

	// 自始至终只有一个合并任务（没有第二条、没有失败任务）
	count := 0
	for _, snap := range mgr.List(id) {
		if snap.Kind == "merge" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("互斥下应只有 1 个合并任务，实际 %d", count)
	}

	// 放行合并子会话，等它收尾（测试结束时不留在途 goroutine）
	release <- struct{}{}
	waitAutoMergeSettled(t, srv, mgr, id)
}

// TestAutoMergeHookFailsSilently：钩子起任务失败（互斥之外的原因，这里是
// 未分组会话没有可合并的分支）→ 只记日志，不 panic、不留任务、不影响会话。
func TestAutoMergeHookFailsSilently(t *testing.T) {
	srv, client, mgr := newJobTestServer(t, immediateStream)
	id := createTestSession(t, client, "") // 未分组会话：startMergeJob 必报错

	srv.maybeAutoMerge(id) // 钩子本体：错误只进日志

	for _, snap := range mgr.List(id) {
		if snap.Kind == "merge" {
			t.Fatalf("钩子失败不该留任务: %+v", snap)
		}
	}
}
