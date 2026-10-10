// 合并进程 2026-10 升级的验收用例：
//   - 发起时扫描本项目「有改动的会话分支」（ahead / dirty / 已并入 / 无分支 / 跨项目）；
//   - 多分支任务说明书（范围硬限定段 + 逐分支清单行）；
//   - 收尾来源校验（可归因 → 通过；越界提交 → Failed + detail + 集成分支回滚）；
//   - 按项目互斥（同项目第二个会话拒绝、不同项目放行）。
//
// 全程不 spawn 真进程、不打网络（假 stream）；git 操作走真仓库（与 autocommit 同款）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/tools"
)

// mergeSourcesOf 把扫描结果整理成分支名集合（断言用）。
func mergeSourcesOf(sources []mergeSourceBranch) map[string]mergeSourceBranch {
	out := map[string]mergeSourceBranch{}
	for _, src := range sources {
		out[src.Branch] = src
	}
	return out
}

// TestMergeScanSelectsChangedBranches：扫描筛选——本项目 5 个会话（领先 / 已并入但
// 工作树脏 / 无提交轮 / 无分支）+ 其他项目 1 个，清单只含该合的。
func TestMergeScanSelectsChangedBranches(t *testing.T) {
	repoA := initGitProject(t)
	repoB := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	metaA := addAutoCommitProject(t, client, repoA)
	metaB := addAutoCommitProject(t, client, repoB)

	// A1：发起会话，领先 1 个提交
	a1 := createTestSession(t, client, metaA.ID)
	sendTurn(t, srv, client, a1, "A1")
	commitChange(t, srv, a1, "a1.txt")
	// A2：领先 1 个提交
	a2 := createTestSession(t, client, metaA.ID)
	sendTurn(t, srv, client, a2, "A2")
	commitChange(t, srv, a2, "a2.txt")
	// A3：提交后把集成分支指到它的头（已并入），再放一个未跟踪文件（工作树脏）
	//→ ahead 0、dirty 1，仍然入选
	a3 := createTestSession(t, client, metaA.ID)
	sendTurn(t, srv, client, a3, "A3")
	commitChange(t, srv, a3, "a3.txt")
	wt3, err := srv.st.WorktreeOf(a3)
	if err != nil || wt3.Branch == "" {
		t.Fatalf("A3 worktree 元数据缺失: %+v err=%v", wt3, err)
	}
	gitServerTest(t, repoA, "branch", "-f", "lxcode/integration", wt3.Branch)
	if err := os.WriteFile(filepath.Join(wt3.Path, "dirty.txt"), []byte("d\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A4：发过一轮但没有任何改动 → 分支存在但无提交且干净 → 跳过
	a4 := createTestSession(t, client, metaA.ID)
	sendTurn(t, srv, client, a4, "A4")
	// A5：从未发过消息 → 无 worktree 元数据 → 跳过
	_ = createTestSession(t, client, metaA.ID)
	// B1：其他项目的会话，有自己的提交 → 不属于本项目 → 跳过
	b1 := createTestSession(t, client, metaB.ID)
	sendTurn(t, srv, client, b1, "B1")
	commitChange(t, srv, b1, "b1.txt")

	sources, note, err := srv.scanProjectBranches(context.Background(), repoA, metaA.ID, "lxcode/integration", a1)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if note != "" {
		t.Fatalf("发起会话有改动，不应有跳过注明: %q", note)
	}
	got := mergeSourcesOf(sources)
	if len(got) != 3 {
		t.Fatalf("应入选 3 个分支，实际 %d: %+v", len(got), sources)
	}
	wt1, _ := srv.st.WorktreeOf(a1)
	wt2, _ := srv.st.WorktreeOf(a2)
	for _, want := range []string{wt1.Branch, wt2.Branch, wt3.Branch} {
		if _, ok := got[want]; !ok {
			t.Fatalf("清单缺 %s: %+v", want, sources)
		}
	}
	if got[wt3.Branch].Ahead != 0 || got[wt3.Branch].Dirty != 1 {
		t.Fatalf("A3 应为 ahead=0 dirty=1: %+v", got[wt3.Branch])
	}
	if got[wt1.Branch].Ahead != 1 {
		t.Fatalf("A1 应领先 1 个提交: %+v", got[wt1.Branch])
	}
	// 其他项目的会话分支绝不出现
	wtb, _ := srv.st.WorktreeOf(b1)
	if _, ok := got[wtb.Branch]; ok {
		t.Fatalf("其他项目的分支不该入选: %+v", sources)
	}
}

// TestMergeScanEdgeCases：扫描的三个边界——已并入且干净（报没有待合并改动）、
// 已并入但工作树脏（入选）、发起会话无改动但有其他分支（注明后合其他的）。
// 每个子测试独立项目：集成分支的指向是项目级状态，共用会互相污染。
func TestMergeScanEdgeCases(t *testing.T) {
	srv, client, _ := newJobTestServer(t, immediateStream)

	t.Run("已并入且干净：报没有待合并的改动", func(t *testing.T) {
		repo := initGitProject(t)
		meta := addAutoCommitProject(t, client, repo)
		id := createTestSession(t, client, meta.ID)
		sendTurn(t, srv, client, id, "已并入")
		commitChange(t, srv, id, "m.txt")
		wt, _ := srv.st.WorktreeOf(id)
		gitServerTest(t, repo, "branch", "-f", "lxcode/integration", wt.Branch)
		_, _, err := srv.scanProjectBranches(context.Background(), repo, meta.ID, "lxcode/integration", id)
		if err == nil || !strings.Contains(err.Error(), "本项目没有待合并的改动") {
			t.Fatalf("应报没有待合并的改动: %v", err)
		}
		if err == nil || !strings.Contains(err.Error(), "已并入集成分支") {
			t.Fatalf("错误应注明发起会话分支已并入: %v", err)
		}
	})
	t.Run("已并入但工作树脏：入选", func(t *testing.T) {
		repo := initGitProject(t)
		meta := addAutoCommitProject(t, client, repo)
		id := createTestSession(t, client, meta.ID)
		sendTurn(t, srv, client, id, "脏工作树")
		commitChange(t, srv, id, "m2.txt")
		wt, _ := srv.st.WorktreeOf(id)
		gitServerTest(t, repo, "branch", "-f", "lxcode/integration", wt.Branch)
		if err := os.WriteFile(filepath.Join(wt.Path, "u.txt"), []byte("u\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		sources, note, err := srv.scanProjectBranches(context.Background(), repo, meta.ID, "lxcode/integration", id)
		if err != nil || note != "" {
			t.Fatalf("不应报错/注明: sources=%+v note=%q err=%v", sources, note, err)
		}
		if len(sources) != 1 || sources[0].Ahead != 0 || sources[0].Dirty != 1 {
			t.Fatalf("已并入但脏的分支应入选: %+v", sources)
		}
	})
	t.Run("发起会话无改动但有其他分支：注明后合其他的", func(t *testing.T) {
		repo := initGitProject(t)
		meta := addAutoCommitProject(t, client, repo)
		other := createTestSession(t, client, meta.ID)
		sendTurn(t, srv, client, other, "别的会话")
		commitChange(t, srv, other, "o.txt")
		id := createTestSession(t, client, meta.ID)
		sendTurn(t, srv, client, id, "我这边没改") // 分支无提交且干净
		sources, note, err := srv.scanProjectBranches(context.Background(), repo, meta.ID, "lxcode/integration", id)
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if !strings.Contains(note, "无未合并改动") {
			t.Fatalf("应注明发起会话无改动: %q", note)
		}
		got := mergeSourcesOf(sources)
		wtOther, _ := srv.st.WorktreeOf(other)
		if len(got) != 1 {
			t.Fatalf("应只含其他会话的分支: %+v", sources)
		}
		if _, ok := got[wtOther.Branch]; !ok {
			t.Fatalf("清单缺 %s: %+v", wtOther.Branch, sources)
		}
	})
}

// TestMergeTaskTextMultiBranch：多分支任务说明书含范围硬限定段与逐分支清单行。
func TestMergeTaskTextMultiBranch(t *testing.T) {
	target := "lxcode/integration"
	sources := []mergeSourceBranch{
		{Branch: "lxcode/session-a", SessionID: "a", Title: "会话甲", Ahead: 2, Dirty: 0},
		{Branch: "lxcode/session-b", SessionID: "b", Title: "会话乙", Archived: true, Ahead: 1, Dirty: 3},
	}
	note := "发起会话分支 lxcode/session-c 无未合并改动（领先 0 个提交，工作树干净）"
	text := mergeTaskText(sources, note, target, "X:\\integration")
	for _, want := range []string{
		"范围硬限定",
		"不把清单之外的任何文件或提交带进目标分支",
		"不碰主检出",
		"它们都属于同一个项目",
		"目标分支 " + target,
		"逐个分支顺序合并",
		"- lxcode/session-a（会话「会话甲」）：领先 2 个提交，工作树 干净",
		"- lxcode/session-b（会话「会话乙」，已归档）：领先 1 个提交，工作树 3 条未提交改动",
		"已成功合并与尚未开始的分支",
		"（" + note + "，不在合并清单里）",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("任务说明书缺 %q:\n%s", want, text)
		}
	}
}

// TestMergeProvenanceAudit：来源校验的单元判定（真 git 仓库）——
// 合并带进来的提交与 merger 的合并提交都可归因；塞进来的无关提交被揪出；回滚还原。
func TestMergeProvenanceAudit(t *testing.T) {
	repo := initGitProject(t)
	ctx := context.Background()
	// src 分支：一个提交
	gitServerTest(t, repo, "checkout", "-b", "src")
	if err := os.WriteFile(filepath.Join(repo, "src.txt"), []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, repo, "add", "-A")
	gitServerTest(t, repo, "commit", "-m", "src change")
	gitServerTest(t, repo, "checkout", "-") // 回主分支
	// integration 从主分支建
	gitServerTest(t, repo, "branch", "lxcode/integration")
	oldHead := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "lxcode/integration"))
	// 真实合并（产生 merge commit，第二父 = src 头）
	gitServerTest(t, repo, "checkout", "lxcode/integration")
	gitServerTest(t, repo, "merge", "--no-edit", "src")
	sources := []mergeSourceBranch{{Branch: "src", SessionID: "s1", Title: "源会话"}}
	suspects, err := auditMergeProvenance(ctx, repo, oldHead, "lxcode/integration", sources)
	if err != nil {
		t.Fatalf("来源校验失败: %v", err)
	}
	if len(suspects) != 0 {
		t.Fatalf("正常合并不应有可疑提交: %+v", suspects)
	}
	// merger 塞一个无关提交 → 被揪出（hash、标题、改动文件）
	if err := os.WriteFile(filepath.Join(repo, "stray.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, repo, "add", "-A")
	gitServerTest(t, repo, "commit", "-m", "越界提交")
	suspects, err = auditMergeProvenance(ctx, repo, oldHead, "lxcode/integration", sources)
	if err != nil {
		t.Fatalf("来源校验失败: %v", err)
	}
	if len(suspects) != 1 {
		t.Fatalf("应揪出 1 个越界提交: %+v", suspects)
	}
	if suspects[0].Subject != "越界提交" {
		t.Fatalf("可疑提交标题不符: %+v", suspects[0])
	}
	found := false
	for _, f := range suspects[0].Files {
		if f == "stray.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("detail 应列改动文件 stray.txt: %+v", suspects[0].Files)
	}
	// 回滚：集成分支与工作树都回到 oldHead
	if err := project.ResetHard(ctx, repo, oldHead); err != nil {
		t.Fatalf("回滚失败: %v", err)
	}
	if head := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "lxcode/integration")); head != oldHead {
		t.Fatalf("回滚后集成分支头 = %s, want %s", head, oldHead)
	}
	if _, err := os.Stat(filepath.Join(repo, "stray.txt")); !os.IsNotExist(err) {
		t.Fatalf("回滚后 stray.txt 应消失: %v", err)
	}
}

// TestMergeJobRollsBackStrayCommit：端到端——merger 在集成分支塞无关提交 →
// 任务 Failed、detail 列出可疑提交、集成分支回滚到任务开始前的 HEAD。
func TestMergeJobRollsBackStrayCommit(t *testing.T) {
	repo := initGitProject(t)
	baseHead := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "HEAD"))
	var calls int32
	stream := func(ctx context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		text := ""
		for _, m := range msgs {
			if m.Role == "user" {
				text = m.Content
			}
		}
		if strings.Contains(text, "范围硬限定") && atomic.AddInt32(&calls, 1) == 1 {
			ch := make(chan llm.StreamEvent, 2)
			tc := llm.ToolCall{ID: "call-stray"}
			tc.Function.Name = "bash"
			tc.Function.Arguments = `{"command":"git commit --allow-empty -m 越界提交"}`
			ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			close(ch)
			return ch, nil
		}
		return immediateStream(ctx, config.ModelConfig{}, nil, nil)
	}
	srv, client, mgr := newJobTestServer(t, stream)
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好")
	commitChange(t, srv, id, "a.txt")

	out := callMergeRequest(srv, id, "")
	jobID := ""
	for _, snap := range mgr.List(id) {
		if snap.Kind == "merge" {
			jobID = snap.ID
		}
	}
	if jobID == "" || !strings.Contains(out, jobID) {
		t.Fatalf("应起一条合并任务: out=%q", out)
	}
	snap := waitMergeJob(t, mgr, id, jobID)
	if snap.Status != jobs.StatusFailed {
		t.Fatalf("越界提交应使任务失败: %+v", snap)
	}
	if !strings.Contains(snap.Detail, "无法归因于任何源分支") {
		t.Fatalf("detail 应说明越界: %q", snap.Detail)
	}
	if !strings.Contains(snap.Detail, "越界提交") {
		t.Fatalf("detail 应列出可疑提交标题: %q", snap.Detail)
	}
	if !strings.Contains(snap.Detail, "已回滚到") {
		t.Fatalf("detail 应说明已回滚: %q", snap.Detail)
	}
	// 集成分支确实回到了任务开始前的 HEAD（集成分支从主检出 HEAD 创建 = baseHead）
	if head := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "lxcode/integration")); head != baseHead {
		t.Fatalf("回滚后集成分支头 = %s, want %s", head, baseHead)
	}
	waitSessionIdle(t, srv, id)
	srv.waitAutoCommits()
}

// TestMergePerProjectMutex：按项目互斥——同项目第二个会话发起被拒，
// 不同项目的会话同时发起放行。
func TestMergePerProjectMutex(t *testing.T) {
	repoA := initGitProject(t)
	repoB := initGitProject(t)
	release := make(chan struct{}, 8)
	srv, client, mgr := newJobTestServer(t, mergeBlockStream(release))
	metaA := addAutoCommitProject(t, client, repoA)
	metaB := addAutoCommitProject(t, client, repoB)
	a1 := createTestSession(t, client, metaA.ID)
	sendTurn(t, srv, client, a1, "A1")
	commitChange(t, srv, a1, "a1.txt")
	a2 := createTestSession(t, client, metaA.ID)
	sendTurn(t, srv, client, a2, "A2")
	commitChange(t, srv, a2, "a2.txt")
	b1 := createTestSession(t, client, metaB.ID)
	sendTurn(t, srv, client, b1, "B1")
	commitChange(t, srv, b1, "b1.txt")

	// A1 发起（阻塞在合并子会话上，保持 running）
	if out := callMergeRequest(srv, a1, ""); strings.Contains(out, "错误:") {
		t.Fatalf("A1 发起应成功: %q", out)
	}
	// 同项目 A2 发起 → 拒绝（按项目互斥，即使不是同一会话）
	if out := callMergeRequest(srv, a2, ""); !strings.Contains(out, "该项目已有合并进程在跑") {
		t.Fatalf("同项目第二个会话发起应被拒: %q", out)
	}
	// 不同项目 B1 发起 → 放行
	if out := callMergeRequest(srv, b1, ""); strings.Contains(out, "错误:") {
		t.Fatalf("不同项目发起应放行: %q", out)
	}
	// 放行两个合并子会话，等任务收尾
	release <- struct{}{}
	release <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running := 0
		for _, snap := range mgr.List("") {
			if snap.Kind == "merge" && !snap.Status.Terminal() {
				running++
			}
		}
		if running == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitSessionIdle(t, srv, a1)
	waitSessionIdle(t, srv, b1)
	srv.waitAutoCommits()
}

// TestMergeSyncGetsMultiBranch：workspace_sync 走同一条 startMergeJob——
// 自动获得多分支行为（本项目另一个会话的分支被一并写进任务说明书）。
func TestMergeSyncGetsMultiBranch(t *testing.T) {
	repo := initGitProject(t)
	var mu sync.Mutex
	var taskText string
	stream := func(ctx context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		text := ""
		for _, m := range msgs {
			if m.Role == "user" {
				text = m.Content
			}
		}
		if strings.Contains(text, "范围硬限定") {
			mu.Lock()
			taskText = text
			mu.Unlock()
		}
		return immediateStream(ctx, config.ModelConfig{}, nil, nil)
	}
	srv, client, mgr := newJobTestServer(t, stream)
	meta := addAutoCommitProject(t, client, repo)
	a1 := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, a1, "A1")
	commitChange(t, srv, a1, "a1.txt")
	a2 := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, a2, "A2")
	commitChange(t, srv, a2, "a2.txt")

	// A1 走 workspace_sync（「帮我提交」链路）发起合并
	out := callWorkspaceTool(srv, a1, tools.WorkspaceSyncToolName, map[string]any{"message": "同步提交"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_sync 不该报错: %q", out)
	}
	deadline := time.Now().Add(5 * time.Second)
	var jobID string
	for time.Now().Before(deadline) && jobID == "" {
		for _, snap := range mgr.List(a1) {
			if snap.Kind == "merge" {
				jobID = snap.ID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitMergeJob(t, mgr, a1, jobID)
	mu.Lock()
	defer mu.Unlock()
	wt2, _ := srv.st.WorktreeOf(a2)
	if !strings.Contains(taskText, wt2.Branch) {
		t.Fatalf("任务说明书应包含本项目其他会话的分支 %s:\n%s", wt2.Branch, taskText)
	}
	if !strings.Contains(taskText, "范围硬限定") {
		t.Fatalf("任务说明书应含范围硬限定段:\n%s", taskText)
	}
	waitSessionIdle(t, srv, a1)
	srv.waitAutoCommits()
}
