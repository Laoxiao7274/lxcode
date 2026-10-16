// 归档测试（session.archive）。原先的 release_worktree 参数组用例已随直连模式
// 移除（2026-12 用户拍板：per-session worktree 废除，「释放工作区」失去主场景；
// 归档不再顺带释放工作区目录）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// archiveTestServer 起一个服务端并注册一个 git 项目仓库，返回项目元数据与仓库路径。
func archiveTestServer(t *testing.T, name string) (*Server, *wsTestClient, protocol.ProjectMeta, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, repo, "init")
	gitServerTest(t, repo, "config", "user.name", "Test")
	gitServerTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, repo, "add", "base.txt")
	gitServerTest(t, repo, "commit", "-m", "base")

	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
		close(ch)
		return ch, nil
	})
	resp := client.call(protocol.MethodProjectAdd, protocol.ProjectAddParams{Name: name, Path: repo})
	if resp == nil || resp.Error != nil {
		t.Fatalf("project.add failed: %+v", resp)
	}
	var projectMeta protocol.ProjectMeta
	decodeServerResult(t, resp.Result, &projectMeta)
	return srv, client, projectMeta, repo
}

// newProjectSessionWithWorktree 建一个项目会话并**伪造存量 worktree 状态**（直连
// 架构下不再自动建 worktree；本组用例测的是老库数据形态下归档的兼容行为）。
func newProjectSessionWithWorktree(t *testing.T, srv *Server, client *wsTestClient, projectID string) (string, string) {
	t.Helper()
	repoMeta, found, err := srv.st.ProjectByID(projectID)
	if err != nil || !found {
		t.Fatalf("project %s not found: found=%v err=%v", projectID, found, err)
	}
	id := createTestSession(t, client, projectID)
	if resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: id, Text: "work"}); resp == nil || resp.Error != nil {
		t.Fatalf("chat.send failed: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventDone, id)
	srv.waitAutoCommits() // 轮结束的检查点提交是异步的：等它结束再做工作树操作
	wt, err := project.CreateWorktree(repoMeta.Path, srv.st.WorktreeRoot(), projectID, id)
	if err != nil {
		t.Fatalf("构造老 worktree 失败: %v", err)
	}
	if err := srv.st.SetWorktree(id, store.WorktreeMeta{Path: wt.Path, Branch: wt.Branch, BaseCommit: wt.BaseCommit}); err != nil {
		t.Fatal(err)
	}
	return id, wt.Path
}

// sessionArchived 读库判断会话是否已归档。
func sessionArchived(t *testing.T, srv *Server, id string) bool {
	t.Helper()
	metas, err := srv.st.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range metas {
		if meta.ID == id {
			return meta.Archived
		}
	}
	t.Fatalf("归档后会话 %s 不在列表里（记录被删了？）", id)
	return false
}

// 用例1：项目会话（伪造存量 worktree 元数据）+ 普通归档 → 归档成功，目录与
// 分支**一律保留**（兼容纪律：用户数据零丢失，归档不做清理——清理走 temp/
// 一次性脚本）。
func TestArchiveKeepsLegacyWorktree(t *testing.T) {
	srv, client, projectMeta, repo := archiveTestServer(t, "archive-keep-worktree")
	id, path := newProjectSessionWithWorktree(t, srv, client, projectMeta.ID)
	meta, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}

	resp := client.call(protocol.MethodSessionArchive, protocol.SessionArchiveParams{ID: id, Archived: true})
	if resp == nil || resp.Error != nil {
		t.Fatalf("plain archive failed: %+v", resp)
	}
	var result protocol.SessionArchiveResult
	decodeServerResult(t, resp.Result, &result)
	if !result.Archived {
		t.Fatalf("archive result mismatch: %+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("archive must not remove the worktree directory: %v", err)
	}
	if got := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", meta.Branch)); got == "" {
		t.Fatal("archive deleted the session branch")
	}
	if !sessionArchived(t, srv, id) {
		t.Fatal("archived session record went missing")
	}
}

// 用例2：带子会话（父有 worktree 元数据、子为空）→ 归档父会话，双方元数据与
// 目录都不动。
func TestArchiveWithChildKeepsWorktrees(t *testing.T) {
	srv, client, projectMeta, _ := archiveTestServer(t, "archive-child-worktree")
	parent, parentPath := newProjectSessionWithWorktree(t, srv, client, projectMeta.ID)
	// 子会话只继承父的 workspace，worktree 三列为空（标签页没被打开过）。
	child, err := srv.st.CreateChild(parent, "agent-x", "dispatch-x")
	if err != nil {
		t.Fatalf("CreateChild failed: %v", err)
	}

	resp := client.call(protocol.MethodSessionArchive, protocol.SessionArchiveParams{ID: parent, Archived: true})
	if resp == nil || resp.Error != nil {
		t.Fatalf("archive with child failed: %+v", resp)
	}
	var result protocol.SessionArchiveResult
	decodeServerResult(t, resp.Result, &result)
	if !result.Archived {
		t.Fatalf("archive result mismatch: %+v", result)
	}
	if _, err := os.Stat(parentPath); err != nil {
		t.Fatalf("archive removed the parent worktree: %v", err)
	}
	childMeta, err := srv.st.WorktreeOf(child)
	if err != nil || childMeta.Path != "" {
		t.Fatalf("child worktree meta should stay empty: %+v err=%v", childMeta, err)
	}
	if !sessionArchived(t, srv, parent) {
		t.Fatal("parent archive did not commit the DB row")
	}
}
