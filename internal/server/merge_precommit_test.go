// merge_request 前置提交（startMergeJob 内部 commitBeforeMerge）的验收用例：
//   - 用例 1：轮内有未提交改动（自动提交尚未跑）→ 先提交、工作树变干净、
//     提交进分支（ahead），扫描清单含发起会话分支（merger 必然合到本轮改动）；
//   - 用例 2：工作树干净 → 不产生空提交、正常起任务；
//   - 用例 3：CommitAll 失败（git 坏了）→ 报错、不起任务；
//   - 用例 4：前置提交与 TurnDone 自动提交并发 → 无 index 竞争、恰好一个提交。
//
// 全程不 spawn 真进程、不打网络（假 stream）；git 操作走真仓库（与 autocommit 同款）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/project"
)

// writeMidTurnChange 往会话工作树写一个未提交文件（模拟「模型在轮内改了东西，
// TurnDone 的自动提交还没跑」——merge_request 正是在这个时点被调用的）。
func writeMidTurnChange(t *testing.T, srv *Server, id, name string) {
	t.Helper()
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not ready: %+v err=%v", wt, err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, name), []byte(name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// assertWorktreeClean 断言工作树没有未提交改动（提交前置的「消除时序缺口」落点）。
func assertWorktreeClean(t *testing.T, srv *Server, id string) {
	t.Helper()
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not ready: %+v err=%v", wt, err)
	}
	lines, err := project.StatusLines(context.Background(), wt.Path)
	if err != nil {
		t.Fatalf("读工作树状态失败: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("工作树应已干净，仍有 %d 条未提交改动: %v", len(lines), lines)
	}
}

// mergeJobOf 从任务列表里找某会话的合并任务（找不到返回空快照）。
func mergeJobOf(mgr *jobs.Manager, sessionID string) jobs.Snapshot {
	for _, snap := range mgr.List(sessionID) {
		if snap.Kind == "merge" {
			return snap
		}
	}
	return jobs.Snapshot{}
}

// finishMergeTest 等合并任务与其触发的通告轮全部收尾（测试结束时不留在途 goroutine）。
func finishMergeTest(t *testing.T, srv *Server, mgr *jobs.Manager, id, jobID string) {
	t.Helper()
	if jobID != "" {
		waitMergeJob(t, mgr, id, jobID)
	}
	waitSessionIdle(t, srv, id)
	srv.waitAutoCommits()
}

// TestMergeRequestPrecommitsMidTurnChanges：用例 1 —— 轮内有未提交改动时调
// merge_request：先提交（工作树变干净、分支 ahead+1、提交信息与自动提交同口径），
// 扫描清单含发起会话分支且按提交判定（ahead≥1、dirty=0）——本轮改动必然被 merger 合并。
func TestMergeRequestPrecommitsMidTurnChanges(t *testing.T) {
	repo := initGitProject(t)
	srv, client, mgr := newJobTestServer(t, immediateStream)
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "实现合并前置提交") // 第 1 轮，无改动、无提交
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not ready: %+v err=%v", wt, err)
	}
	base := commitCount(t, wt.Path)

	// 模拟轮内改动：文件已写、还没被 TurnDone 的自动提交落盘
	writeMidTurnChange(t, srv, id, "midturn.txt")

	out := callMergeRequest(srv, id, "")
	if strings.Contains(out, "错误:") {
		t.Fatalf("merge_request 应成功（前置提交兜住轮内改动）: %q", out)
	}
	// 工作树变干净：改动已提交
	assertWorktreeClean(t, srv, id)
	// 恰好多一个提交，信息与自动提交同一口径（LastUserTurn 轮次 + 用户消息摘要）
	if got := commitCount(t, wt.Path); got != base+1 {
		t.Fatalf("应恰好新增 1 个提交（base %d → %d）", base, got)
	}
	if msg := strings.TrimSpace(gitServerTest(t, wt.Path, "log", "-1", "--pretty=%s")); msg != "第 1 轮：实现合并前置提交" {
		t.Fatalf("提交信息应与自动提交同口径: %q", msg)
	}
	// 扫描清单含发起会话分支，且本轮改动以提交形态入选（ahead≥1、dirty=0）
	//——merger 只合提交，dirty 部分合不进，这是本修复要消除的缺口
	deadline := 0
	for {
		sources, note, serr := srv.scanProjectBranches(context.Background(), repo, meta.ID, "lxcode/integration", id)
		if serr == nil {
			got := mergeSourcesOf(sources)
			if src, ok := got[wt.Branch]; ok && src.Ahead >= 1 && src.Dirty == 0 && note == "" {
				break
			}
		}
		deadline++
		if deadline > 200 { // 与 waitMergeJob 同量级的轮询上限（2s）
			t.Fatalf("扫描清单应含发起会话分支（ahead≥1、dirty=0）: sources=%+v note=%q err=%v", sources, note, serr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	finishMergeTest(t, srv, mgr, id, mergeJobOf(mgr, id).ID)
}

// TestMergeRequestPrecommitSkipsCleanWorktree：用例 2 —— 工作树干净时调
// merge_request：不产生空提交（提交数不变），任务正常起。
func TestMergeRequestPrecommitSkipsCleanWorktree(t *testing.T) {
	repo := initGitProject(t)
	srv, client, mgr := newJobTestServer(t, immediateStream)
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好")
	commitChange(t, srv, id, "a.txt") // 已提交的改动，工作树干净
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not ready: %+v err=%v", wt, err)
	}
	base := commitCount(t, wt.Path)

	out := callMergeRequest(srv, id, "")
	if strings.Contains(out, "错误:") {
		t.Fatalf("干净工作树应正常起任务: %q", out)
	}
	if got := commitCount(t, wt.Path); got != base {
		t.Fatalf("不应产生空提交（base %d → %d）", base, got)
	}
	job := mergeJobOf(mgr, id)
	if job.ID == "" {
		t.Fatal("合并任务未启动")
	}
	finishMergeTest(t, srv, mgr, id, job.ID)
}

// TestMergeRequestPrecommitFailsClosed：用例 3 —— 前置提交失败（git 出错）→
// 返回人话错误、合并任务不起。
func TestMergeRequestPrecommitFailsClosed(t *testing.T) {
	repo := initGitProject(t)
	srv, client, mgr := newJobTestServer(t, immediateStream)
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好")
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not ready: %+v err=%v", wt, err)
	}
	// 弄坏 worktree 的 git（worktree 的 .git 是一个指向 gitdir 的文件，删掉后
	// 所有 git 调用都失败）——CommitAll 的 status 第一步就报错
	if err := os.Remove(filepath.Join(wt.Path, ".git")); err != nil {
		t.Fatalf("破坏 worktree git 失败: %v", err)
	}
	writeMidTurnChange(t, srv, id, "will-not-merge.txt")

	out := callMergeRequest(srv, id, "")
	if !strings.Contains(out, "提交本次改动失败") || !strings.Contains(out, "合并未发起") {
		t.Fatalf("应报人话错误且说明合并未发起: %q", out)
	}
	if job := mergeJobOf(mgr, id); job.ID != "" {
		t.Fatalf("提交失败时不应起合并任务: %s", job.ID)
	}
}

// TestMergeRequestPrecommitConcurrentWithAutoCommit：用例 4 —— merge_request 的
// 前置提交（请求 goroutine）与轮结束的自动提交（异步 goroutine）并发：共用按会话
// 提交锁串行化，无 index 竞争错误，且恰好产生一个提交（后到的发现干净即跳过）。
func TestMergeRequestPrecommitConcurrentWithAutoCommit(t *testing.T) {
	repo := initGitProject(t)
	srv, client, mgr := newJobTestServer(t, immediateStream)
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "并发用例")
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not ready: %+v err=%v", wt, err)
	}
	base := commitCount(t, wt.Path)

	writeMidTurnChange(t, srv, id, "race.txt")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		srv.maybeAutoCommit(id) // TurnDone 钩子调的就是它（异步 goroutine）
	}()
	go func() {
		defer wg.Done()
		if out := callMergeRequest(srv, id, ""); strings.Contains(out, "错误:") {
			t.Errorf("merge_request 不应报错: %q", out)
		}
	}()
	wg.Wait()
	srv.waitAutoCommits()

	assertWorktreeClean(t, srv, id)
	if got := commitCount(t, wt.Path); got != base+1 {
		t.Fatalf("并发下应恰好产生 1 个提交（base %d → %d，无空提交/无重复）", base, got)
	}
	if msg := strings.TrimSpace(gitServerTest(t, wt.Path, "log", "-1", "--pretty=%s")); msg != "第 1 轮：并发用例" {
		t.Fatalf("提交信息应与自动提交同口径: %q", msg)
	}
	finishMergeTest(t, srv, mgr, id, mergeJobOf(mgr, id).ID)
}
