// Git 管理页只读查询（git.overview / git.diff）的验收用例：干净/脏主检出、
// 会话分支 ahead/merged/hasWorktree、log 条数、diff（修改/未跟踪/32KB 截断）、
// 无归属项目报错。git 全部用临时目录——不碰任何真实仓库。
package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// callGitOverview 调 git.overview 并解码结果（wantErr 非空 = 期望报错且文案含它）。
func callGitOverview(t *testing.T, client *wsTestClient, projectID string) protocol.GitOverviewResult {
	t.Helper()
	var params protocol.GitOverviewParams
	if projectID != "" {
		params.ProjectID = projectID
	}
	resp := client.call(protocol.MethodGitOverview, params)
	if resp == nil || resp.Error != nil {
		t.Fatalf("git.overview 失败: %+v", resp)
	}
	var result protocol.GitOverviewResult
	decodeServerResult(t, resp.Result, &result)
	return result
}

// TestGitOverviewDirtyAndSessionBranch（干净/脏主检出 + 会话分支 ahead/hasWorktree）。
func TestGitOverviewDirtyAndSessionBranch(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 2)

	// 主检出弄脏：改一个已跟踪文件 + 一个未跟踪文件。
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("brand new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	overview := callGitOverview(t, client, "")
	if overview.Path != repo {
		t.Fatalf("Path = %q, want %q", overview.Path, repo)
	}
	if overview.Branch == "" {
		t.Fatal("Branch 不应为空")
	}

	// 脏清单：modified（base.txt）与 untracked（new.txt）都要在，类别正确。
	byPath := map[string]string{}
	for _, ch := range overview.Dirty {
		byPath[ch.Path] = ch.Kind
	}
	if byPath["base.txt"] != "modified" {
		t.Fatalf("base.txt 应为 modified: %v", byPath)
	}
	if byPath["new.txt"] != "untracked" {
		t.Fatalf("new.txt 应为 untracked: %v", byPath)
	}

	// 分支：第一条 = 主检出当前分支；会话分支带元数据与工作树。
	if len(overview.Branches) < 2 {
		t.Fatalf("应有当前分支 + 会话分支: %+v", overview.Branches)
	}
	current := overview.Branches[0]
	if !current.Current || current.Name != overview.Branch {
		t.Fatalf("第一条应是当前分支: %+v", current)
	}
	var sessionBranch *protocol.GitBranchInfo
	for i := range overview.Branches {
		if overview.Branches[i].SessionID == id {
			sessionBranch = &overview.Branches[i]
		}
	}
	if sessionBranch == nil {
		t.Fatalf("应列出会话分支 lxcode/session-%s: %+v", id, overview.Branches)
	}
	if sessionBranch.Name != "lxcode/session-"+id {
		t.Fatalf("会话分支名不符: %+v", sessionBranch)
	}
	if sessionBranch.Ahead != 2 {
		t.Fatalf("会话分支应领先主检出 2 个提交: %+v", sessionBranch)
	}
	if !sessionBranch.HasWorktree || sessionBranch.WorktreePath == "" {
		t.Fatalf("会话分支应有工作树: %+v", sessionBranch)
	}
	if sessionBranch.DirtyCount != 0 {
		t.Fatalf("会话工作树刚提交完应干净: %+v", sessionBranch)
	}

	// 提交历史：主检出只有基线 1 条（会话提交在会话分支上）。
	if len(overview.Commits) != 1 {
		t.Fatalf("主检出应有 1 条提交: %+v", overview.Commits)
	}
	if overview.Commits[0].Message != "base" || len(overview.Commits[0].Hash) != 40 {
		t.Fatalf("提交字段不符: %+v", overview.Commits[0])
	}

	// 释放工作区后：分支仍在、工作树消失。
	resp := client.call(protocol.MethodSessionWorktreeRelease, protocol.SessionWorktreeReleaseParams{ID: id})
	if resp == nil || resp.Error != nil {
		t.Fatalf("释放工作区失败: %+v", resp)
	}
	overview = callGitOverview(t, client, "")
	sessionBranch = nil
	for i := range overview.Branches {
		if overview.Branches[i].SessionID == id {
			sessionBranch = &overview.Branches[i]
		}
	}
	if sessionBranch == nil {
		t.Fatal("释放后分支应仍然列出")
	}
	if sessionBranch.HasWorktree || sessionBranch.WorktreePath != "" {
		t.Fatalf("释放后不应再有工作树: %+v", sessionBranch)
	}
}

// TestGitOverviewMergedBranch：会话分支被并入主检出后 Merged=true。
func TestGitOverviewMergedBranch(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := newProjectSessionWithCommits(t, srv, client, repo, 1)
	srv.waitAutoCommits()

	// 把会话分支快进并进主检出（不碰工作区文件内容——会话分支只新增了文件）。
	gitServerTest(t, repo, "merge", "--ff-only", "lxcode/session-"+id)

	overview := callGitOverview(t, client, "")
	var sessionBranch *protocol.GitBranchInfo
	for i := range overview.Branches {
		if overview.Branches[i].SessionID == id {
			sessionBranch = &overview.Branches[i]
		}
	}
	if sessionBranch == nil {
		t.Fatalf("应列出会话分支: %+v", overview.Branches)
	}
	if !sessionBranch.Merged {
		t.Fatalf("并入后 Merged 应为真: %+v", sessionBranch)
	}
	if sessionBranch.Ahead != 0 || sessionBranch.Behind != 0 {
		t.Fatalf("并入后 ahead/behind 都应为 0: %+v", sessionBranch)
	}
	// 主检出历史随之多了一条会话提交。
	if len(overview.Commits) != 2 {
		t.Fatalf("主检出应有 2 条提交: %+v", overview.Commits)
	}
}

// TestGitOverviewNoProject：未分组会话不带 project_id 查询 → 人话报错。
func TestGitOverviewNoProject(t *testing.T) {
	_, client, _ := newJobTestServer(t, immediateStream)
	id := createTestSession(t, client, "")
	client.call(protocol.MethodSessionResume, protocol.SessionResumeParams{ID: id})
	resp := client.call(protocol.MethodGitOverview, protocol.GitOverviewParams{})
	if resp == nil || resp.Error == nil {
		t.Fatalf("未分组会话应报错: %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "归属项目") {
		t.Fatalf("报错应说明没有归属项目: %q", resp.Error.Message)
	}
}

// TestGitDiffModifiedUntrackedAndTruncation：diff 三态 + 路径越界拒绝。
func TestGitDiffModifiedUntrackedAndTruncation(t *testing.T) {
	repo := initGitProject(t)
	_, client, _ := newJobTestServer(t, immediateStream)
	meta := addAutoCommitProject(t, client, repo)

	// ① 已跟踪文件的修改 diff。
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := client.call(protocol.MethodGitDiff, protocol.GitDiffParams{ProjectID: meta.ID, Path: "base.txt"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("git.diff 失败: %+v", resp)
	}
	var result protocol.GitDiffResult
	decodeServerResult(t, resp.Result, &result)
	if !strings.Contains(result.Diff, "-base") || !strings.Contains(result.Diff, "+changed") {
		t.Fatalf("修改文件应给出 -/+ 行: %q", result.Diff)
	}

	// ② 未跟踪文件：全量内容按新增标记。
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("brand new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodGitDiff, protocol.GitDiffParams{ProjectID: meta.ID, Path: "new.txt"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("git.diff(未跟踪) 失败: %+v", resp)
	}
	decodeServerResult(t, resp.Result, &result)
	if !strings.Contains(result.Diff, "brand new") || !strings.HasPrefix(result.Diff, "+++") {
		t.Fatalf("未跟踪文件应给全量内容标记: %q", result.Diff)
	}

	// ③ 超过 32KB 的未跟踪文件：截断并注明。
	big := strings.Repeat("x", 40*1024) + "\n"
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodGitDiff, protocol.GitDiffParams{ProjectID: meta.ID, Path: "big.txt"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("git.diff(大文件) 失败: %+v", resp)
	}
	decodeServerResult(t, resp.Result, &result)
	if len(result.Diff) > 42*1024 || !strings.Contains(result.Diff, "截断") {
		t.Fatalf("大文件 diff 应截断并注明: len=%d", len(result.Diff))
	}

	// ④ 路径越界直接拒绝。
	resp = client.call(protocol.MethodGitDiff, protocol.GitDiffParams{ProjectID: meta.ID, Path: "../outside.txt"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("路径越界应报错: %+v", resp)
	}
}

// TestGitDiffParamsRequirePath：缺 path 报参数错误。
func TestGitDiffParamsRequirePath(t *testing.T) {
	repo := initGitProject(t)
	_, client, _ := newJobTestServer(t, immediateStream)
	meta := addAutoCommitProject(t, client, repo)
	resp := client.call(protocol.MethodGitDiff, protocol.GitDiffParams{ProjectID: meta.ID})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("缺 path 应报参数错误: %+v", resp)
	}
	// wire 形态抽查：GitBranchInfo 的会话字段是 omitempty（主检出分支不带会话键）。
	overview := callGitOverview(t, client, meta.ID)
	raw, err := json.Marshal(overview.Branches[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "session_id") {
		t.Fatalf("主检出分支不该带会话键: %s", raw)
	}
}
