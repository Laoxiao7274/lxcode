// 工作区三件套（workspace_status / workspace_sync / workspace_rollback）的验收用例
// 1~6：状态汇报、提交+起合并、推送 bare origin、回滚上一轮（含脏工作区拒绝）、
// 回滚会话起点、未分组会话报错。git 全部用临时目录，push 用本地 bare 仓库当
// origin——不碰网络、不碰真实 origin。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/tools"
)

// callWorkspaceTool 经工具注册表调用工作区工具（与模型走的同一条路径）。
// 返回回填模型的文本（错误以「错误: 」开头——Execute 的三层错误约定）。
func callWorkspaceTool(srv *Server, sessionID, name string, args map[string]any) string {
	raw, _ := json.Marshal(args)
	ctx := tools.WithSessionID(context.Background(), sessionID)
	return srv.treg.Execute(ctx, llm.ToolCall{ID: "call-ws", Function: struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: name, Arguments: string(raw)}})
}

// newProjectSessionWithCommits 建一个项目会话并伪造 n 个检查点提交
// （走与自动提交同一套 CommitAll——提交信息同款「第 N 轮：…」）。
func newProjectSessionWithCommits(t *testing.T, srv *Server, client *wsTestClient, repo string, n int) string {
	t.Helper()
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好") // 建 worktree 并写标题
	srv.waitAutoCommits()              // 等在途自动提交结束（避免与伪造提交抢 git index）
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not created: %+v err=%v", wt, err)
	}
	// 与自动提交同一把会话锁：提交必须串行（autocommit.go 的纪律）。
	lock := srv.autoCommitLock(id)
	lock.Lock()
	defer lock.Unlock()
	for i := 1; i <= n; i++ {
		if err := os.WriteFile(filepath.Join(wt.Path, fmt.Sprintf("file%d.txt", i)),
			[]byte(fmt.Sprintf("content %d\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := project.CommitAll(srv.Ctx(), wt.Path, fmt.Sprintf("第 %d 轮：测试改动 %d", i, i)); err != nil {
			t.Fatalf("伪造第 %d 轮提交失败: %v", i, err)
		}
	}
	return id
}

// TestWorkspaceStatusReportsProject（用例 1）：项目会话调 workspace_status →
// 返回主检出状态、本会话分支 ahead≥1、集成分支状态（已建后）。
func TestWorkspaceStatusReportsProject(t *testing.T) {
	repo := initGitProject(t)
	srv, client, mgr := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 2)

	out := callWorkspaceTool(srv, id, tools.WorkspaceStatusToolName, map[string]any{})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_status 不该报错: %q", out)
	}
	if !strings.Contains(out, "【主检出】") || !strings.Contains(out, repo) {
		t.Fatalf("状态应含主检出段与路径: %q", out)
	}
	if !strings.Contains(out, "【会话分支】") || !strings.Contains(out, "领先参考点 2 个提交") {
		t.Fatalf("状态应含本会话分支 ahead=2: %q", out)
	}
	if !strings.Contains(out, "【集成分支】") {
		t.Fatalf("状态应含集成分支段: %q", out)
	}

	// 起一次合并（建出集成分支）后再查：集成分支段应有真实数字。
	if out := callMergeRequest(srv, id, ""); strings.Contains(out, "错误:") {
		t.Fatalf("merge_request 应成功: %q", out)
	}
	var jobID string
	for _, snap := range mgr.List(id) {
		if snap.Kind == "merge" {
			jobID = snap.ID
		}
	}
	if jobID == "" {
		t.Fatal("没有合并任务")
	}
	waitMergeJob(t, mgr, id, jobID)
	out = callWorkspaceTool(srv, id, tools.WorkspaceStatusToolName, map[string]any{})
	if !strings.Contains(out, "lxcode/integration：领先主检出") {
		t.Fatalf("集成分支已建后状态应含领先/落后数字: %q", out)
	}
	waitSessionIdle(t, srv, id)
	srv.waitAutoCommits()
}

// TestWorkspaceSyncCommitsAndStartsMerge（用例 2）：workspace_sync{message:"测试提交"} →
// worktree 干净、返回提及合并进程已启动、jobs 列表出现 Kind=="merge"。
func TestWorkspaceSyncCommitsAndStartsMerge(t *testing.T) {
	repo := initGitProject(t)
	srv, client, mgr := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 1)
	wt, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}
	// 再留一处未提交改动，给 sync 东西可提交。
	if err := os.WriteFile(filepath.Join(wt.Path, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceSyncToolName, map[string]any{"message": "测试提交"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_sync 不该报错: %q", out)
	}
	if !strings.Contains(out, "合并进程已启动") {
		t.Fatalf("返回应提及合并进程已启动: %q", out)
	}
	// 提交成功 = worktree 干净（含刚写的 dirty.txt）。
	if lines, err := project.StatusLines(srv.Ctx(), wt.Path); err != nil || len(lines) != 0 {
		t.Fatalf("sync 后 worktree 应干净: lines=%v err=%v", lines, err)
	}
	// jobs 列表出现 Kind=="merge" 的任务。
	var found bool
	for _, snap := range mgr.List(id) {
		if snap.Kind == "merge" {
			found = true
			waitMergeJob(t, mgr, id, snap.ID)
		}
	}
	if !found {
		t.Fatal("jobs 列表里没有 Kind==merge 的任务")
	}
	waitSessionIdle(t, srv, id)
	srv.waitAutoCommits()
}

// TestWorkspaceSyncPushesToBareOrigin（用例 3）：workspace_sync{push:true} 且项目
// 配置本地 bare origin → 合并任务成功 Settle 后 bare 仓库里出现 lxcode/integration 分支。
func TestWorkspaceSyncPushesToBareOrigin(t *testing.T) {
	repo := initGitProject(t)
	// 本地 bare 仓库当 origin（不依赖网络）。目录先建好：gitServerTest 以它为 cwd。
	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, bare, "init", "--bare")
	gitServerTest(t, repo, "remote", "add", "origin", bare)

	srv, client, mgr := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 1)

	out := callWorkspaceTool(srv, id, tools.WorkspaceSyncToolName, map[string]any{"message": "测试提交", "push": true})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_sync 不该报错: %q", out)
	}
	if !strings.Contains(out, "推送将在合并成功后执行") {
		t.Fatalf("返回应说明 push 将在合并成功后执行: %q", out)
	}
	var jobID string
	for _, snap := range mgr.List(id) {
		if snap.Kind == "merge" {
			jobID = snap.ID
		}
	}
	if jobID == "" {
		t.Fatal("没有合并任务")
	}
	snap := waitMergeJob(t, mgr, id, jobID)
	if snap.Status != jobs.StatusCompleted {
		t.Fatalf("合并任务应成功: %+v", snap)
	}
	if snap.Detail != "合并完成，已推送 origin/lxcode/integration" {
		t.Fatalf("合并任务 detail 应写明已推送: %+v", snap)
	}
	// bare 仓库里出现 lxcode/integration 分支。
	branches := gitServerTest(t, bare, "branch")
	if !strings.Contains(branches, "lxcode/integration") {
		t.Fatalf("bare origin 应有 lxcode/integration 分支:\n%s", branches)
	}
	waitSessionIdle(t, srv, id)
	srv.waitAutoCommits()
}

// TestWorkspaceRollbackLastTurn（用例 4）：last-turn 回滚到上一轮、丢弃文件清单
// 正确；worktree 脏时拒绝。
func TestWorkspaceRollbackLastTurn(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 2)
	wt, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitServerTest(t, wt.Path, "log", "-1", "--pretty=%s")); got != "第 2 轮：测试改动 2" {
		t.Fatalf("前置：HEAD 应在第 2 轮: %q", got)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceRollbackToolName, map[string]any{"target": "last-turn"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_rollback 不该报错: %q", out)
	}
	// HEAD 退回上一轮。
	if got := strings.TrimSpace(gitServerTest(t, wt.Path, "log", "-1", "--pretty=%s")); got != "第 1 轮：测试改动 1" {
		t.Fatalf("回滚后 HEAD 应在第 1 轮: %q", got)
	}
	// 丢弃文件清单含第 2 轮新增的 file2.txt（方向 新..旧 → 状态 A=将删除）。
	if !strings.Contains(out, "file2.txt") {
		t.Fatalf("丢弃清单应含 file2.txt: %q", out)
	}

	// 脏工作区拒绝：写一个未提交文件再回滚。
	if err := os.WriteFile(filepath.Join(wt.Path, "handmade.txt"), []byte("hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = callWorkspaceTool(srv, id, tools.WorkspaceRollbackToolName, map[string]any{"target": "last-turn"})
	if !strings.Contains(out, "未提交改动") {
		t.Fatalf("脏工作区应被拒绝: %q", out)
	}
	// HEAD 没动。
	if got := strings.TrimSpace(gitServerTest(t, wt.Path, "log", "-1", "--pretty=%s")); got != "第 1 轮：测试改动 1" {
		t.Fatalf("拒绝后 HEAD 不该动: %q", got)
	}
	// 清掉未提交文件（未跟踪，直接删），恢复干净。
	if err := os.Remove(filepath.Join(wt.Path, "handmade.txt")); err != nil {
		t.Fatal(err)
	}
}

// TestWorkspaceRollbackSessionStart（用例 5）：session-start 回到基线。
func TestWorkspaceRollbackSessionStart(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 2)
	wt, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceRollbackToolName, map[string]any{"target": "session-start"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_rollback 不该报错: %q", out)
	}
	if got := strings.TrimSpace(gitServerTest(t, wt.Path, "rev-parse", "HEAD")); got != wt.BaseCommit {
		t.Fatalf("session-start 应回到基线 %s: got %s", wt.BaseCommit, got)
	}
	// 丢弃清单含会话期间新增的文件（基线只有 base.txt，会话里加了 file1/file2）。
	if !strings.Contains(out, "file1.txt") || !strings.Contains(out, "file2.txt") {
		t.Fatalf("丢弃清单应含会话期间新增的文件（file1/file2）: %q", out)
	}
}

// TestWorkspaceToolsRejectUngroupedSession（用例 6）：未分组会话三个工具都明确报错。
func TestWorkspaceToolsRejectUngroupedSession(t *testing.T) {
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := createTestSession(t, client, "") // 未分组会话

	for _, name := range []string{tools.WorkspaceStatusToolName, tools.WorkspaceSyncToolName, tools.WorkspaceRollbackToolName} {
		out := callWorkspaceTool(srv, id, name, map[string]any{})
		if !strings.Contains(out, "没有归属项目") {
			t.Fatalf("%s 对未分组会话应明确报错: %q", name, out)
		}
	}
}
