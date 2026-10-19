// 工作区三件套（workspace_status / workspace_sync / workspace_rollback）的验收用例
// （2026-10 直连架构）：状态汇报、提交到主检出当前分支、直接 push bare origin、
// 无 origin 报错、回滚上一轮（含脏工作区拒绝）、回滚会话基线、已推送提交 reset 被
// 拒、未分组会话报错。git 全部用临时目录，push 用本地 bare 仓库当 origin——不碰
// 网络、不碰真实 origin。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// newProjectSessionWithCommits 建一个项目会话并造 n 个提交（直连模式：提交直接
// 落在主检出当前分支，走与 workspace_sync 同一套 CommitAll——提交信息同款
// 「第 N 轮：…」）。返回会话 id。
func newProjectSessionWithCommits(t *testing.T, srv *Server, client *wsTestClient, repo string, n int) string {
	t.Helper()
	meta := addGitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好") // 对准主检出并写标题
	for i := 1; i <= n; i++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("file%d.txt", i)),
			[]byte(fmt.Sprintf("content %d\n", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := project.CommitAll(srv.Ctx(), repo, fmt.Sprintf("第 %d 轮：测试改动 %d", i, i)); err != nil {
			t.Fatalf("伪造第 %d 轮提交失败: %v", i, err)
		}
	}
	return id
}

// TestWorkspaceStatusReportsProject：项目会话调 workspace_status → 返回主检出状态
// 与直连模式标注；遗留会话分支段照常存在。
func TestWorkspaceStatusReportsProject(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 2)

	out := callWorkspaceTool(srv, id, tools.WorkspaceStatusToolName, map[string]any{})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_status 不该报错: %q", out)
	}
	if !strings.Contains(out, "【主检出】") || !strings.Contains(out, repo) {
		t.Fatalf("状态应含主检出段与路径: %q", out)
	}
	if !strings.Contains(out, "【遗留会话分支】") || !strings.Contains(out, "直连主检出模式") {
		t.Fatalf("状态应含遗留会话分支段与直连模式标注: %q", out)
	}
	if !strings.Contains(out, "【集成分支（遗留）】") {
		t.Fatalf("状态应含集成分支（遗留）段: %q", out)
	}
	waitSessionIdle(t, srv, id)
}

// TestWorkspaceSyncCommitsToCurrentBranch：workspace_sync{message:"测试提交"} →
// 主检出的未提交改动被提交到当前分支（git log 可见），返回不提合并、说明本次不推送。
func TestWorkspaceSyncCommitsToCurrentBranch(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 1)
	// 再留一处未提交改动，给 sync 东西可提交。
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceSyncToolName, map[string]any{"message": "测试提交"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_sync 不该报错: %q", out)
	}
	if strings.Contains(out, "合并") {
		t.Fatalf("合并流程已退役，返回不该再提合并: %q", out)
	}
	if !strings.Contains(out, "本次不推送") {
		t.Fatalf("push=false 应说明本次不推送: %q", out)
	}
	// 提交成功 = 主检出干净（含刚写的 dirty.txt），且 git log 顶部是这条提交。
	if lines, err := project.StatusLines(srv.Ctx(), repo); err != nil || len(lines) != 0 {
		t.Fatalf("sync 后主检出应干净: lines=%v err=%v", lines, err)
	}
	if got := strings.TrimSpace(gitServerTest(t, repo, "log", "-1", "--pretty=%s")); got != "测试提交" {
		t.Fatalf("当前分支顶部的提交信息 = %q", got)
	}
	waitSessionIdle(t, srv, id)
}

// TestWorkspaceSyncPushesToBareOrigin：workspace_sync{push:true} 且项目配置本地
// bare origin → 当前分支直接出现在 bare 仓库里（不经过任何合并）。
func TestWorkspaceSyncPushesToBareOrigin(t *testing.T) {
	repo := initGitProject(t)
	// 本地 bare 仓库当 origin（不依赖网络）。目录先建好：gitServerTest 以它为 cwd。
	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, bare, "init", "--bare")
	gitServerTest(t, repo, "remote", "add", "origin", bare)

	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 1)

	out := callWorkspaceTool(srv, id, tools.WorkspaceSyncToolName, map[string]any{"message": "测试提交", "push": true})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_sync 不该报错: %q", out)
	}
	if !strings.Contains(out, "推送到远程 origin") {
		t.Fatalf("返回应说明已直接推送: %q", out)
	}
	// bare 仓库里出现主检出当前分支（默认 master/main，看 init 的默认名）。
	branch := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "--abbrev-ref", "HEAD"))
	branches := gitServerTest(t, bare, "branch")
	if !strings.Contains(branches, branch) {
		t.Fatalf("bare origin 应有当前分支 %s:\n%s", branch, branches)
	}
	waitSessionIdle(t, srv, id)
}

// TestWorkspaceSyncPushWithoutOrigin：项目没有远程 origin → 明确报错且提交已完成。
func TestWorkspaceSyncPushWithoutOrigin(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 0)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceSyncToolName, map[string]any{"push": true})
	if !strings.Contains(out, "没有配置远程 origin") {
		t.Fatalf("无 origin 应明确报错: %q", out)
	}
	// 提交已完成（fail-open 于推送）。
	if lines, err := project.StatusLines(srv.Ctx(), repo); err != nil || len(lines) != 0 {
		t.Fatalf("推送失败但提交应已完成: lines=%v err=%v", lines, err)
	}
	waitSessionIdle(t, srv, id)
}

// TestWorkspaceRollbackLastTurn：last-turn 回滚到上一轮、丢弃文件清单正确；
// 主检出脏时拒绝。
func TestWorkspaceRollbackLastTurn(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 2)
	if got := strings.TrimSpace(gitServerTest(t, repo, "log", "-1", "--pretty=%s")); got != "第 2 轮：测试改动 2" {
		t.Fatalf("前置：HEAD 应在第 2 轮: %q", got)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceRollbackToolName, map[string]any{"target": "last-turn"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_rollback 不该报错: %q", out)
	}
	// HEAD 退回上一轮。
	if got := strings.TrimSpace(gitServerTest(t, repo, "log", "-1", "--pretty=%s")); got != "第 1 轮：测试改动 1" {
		t.Fatalf("回滚后 HEAD 应在第 1 轮: %q", got)
	}
	// 丢弃文件清单含第 2 轮新增的 file2.txt（方向 新..旧 → 状态 A=将删除）。
	if !strings.Contains(out, "file2.txt") {
		t.Fatalf("丢弃清单应含 file2.txt: %q", out)
	}

	// 脏工作区拒绝：写一个未提交文件再回滚。
	if err := os.WriteFile(filepath.Join(repo, "handmade.txt"), []byte("hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = callWorkspaceTool(srv, id, tools.WorkspaceRollbackToolName, map[string]any{"target": "last-turn"})
	if !strings.Contains(out, "未提交改动") {
		t.Fatalf("脏工作区应被拒绝: %q", out)
	}
	// HEAD 没动。
	if got := strings.TrimSpace(gitServerTest(t, repo, "log", "-1", "--pretty=%s")); got != "第 1 轮：测试改动 1" {
		t.Fatalf("拒绝后 HEAD 不该动: %q", got)
	}
	// 清掉未提交文件（未跟踪，直接删），恢复干净。
	if err := os.Remove(filepath.Join(repo, "handmade.txt")); err != nil {
		t.Fatal(err)
	}
}

// TestWorkspaceRollbackSessionStart：session-start 回到会话基线（worktree_base
// 列的新语义 = 会话开始时的 HEAD）。
func TestWorkspaceRollbackSessionStart(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 2)
	base, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}

	out := callWorkspaceTool(srv, id, tools.WorkspaceRollbackToolName, map[string]any{"target": "session-start"})
	if strings.Contains(out, "错误:") {
		t.Fatalf("workspace_rollback 不该报错: %q", out)
	}
	if got := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "HEAD")); got != base.BaseCommit {
		t.Fatalf("session-start 应回到基线 %s: got %s", base.BaseCommit, got)
	}
	// 丢弃清单含会话期间新增的文件（基线只有 base.txt，会话里加了 file1/file2）。
	if !strings.Contains(out, "file1.txt") || !strings.Contains(out, "file2.txt") {
		t.Fatalf("丢弃清单应含会话期间新增的文件（file1/file2）: %q", out)
	}
}

// TestWorkspaceRollbackRejectsPushedCommit：reset 目标已被推送到远程 → 拒绝
// （硬纪律：已推送的提交只能 revert 不能 reset）。
func TestWorkspaceRollbackRejectsPushedCommit(t *testing.T) {
	repo := initGitProject(t)
	bare := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, bare, "init", "--bare")
	gitServerTest(t, repo, "remote", "add", "origin", bare)

	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 2)
	// 把第 1 轮的提交推上远程（推整个当前分支）。
	gitServerTest(t, repo, "push", "-u", "origin", "HEAD")
	pushed := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "HEAD~1"))

	out := callWorkspaceTool(srv, id, tools.WorkspaceRollbackToolName, map[string]any{"target": pushed})
	if !strings.Contains(out, "已推送到远程") || !strings.Contains(out, "revert") {
		t.Fatalf("已推送的提交应被拒绝回滚: %q", out)
	}
	// HEAD 没动。
	if got := strings.TrimSpace(gitServerTest(t, repo, "log", "-1", "--pretty=%s")); got != "第 2 轮：测试改动 2" {
		t.Fatalf("拒绝后 HEAD 不该动: %q", got)
	}
	waitSessionIdle(t, srv, id)
}

// TestWorkspaceToolsRejectUngroupedSession：未分组会话三个工具都明确报错。
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
