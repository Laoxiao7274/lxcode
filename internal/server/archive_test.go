// 归档 + 可选释放工作区（session.archive 的 release_worktree 参数）。
// 核心不变量：**归档绝不因释放失败而回滚**——释放只把原因带回给前端提示。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
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

// newProjectSessionWithWorktree 建一个项目会话并跑一轮，让它绑定出独立 worktree。
func newProjectSessionWithWorktree(t *testing.T, srv *Server, client *wsTestClient, projectID string) (string, string) {
	t.Helper()
	id := createTestSession(t, client, projectID)
	if resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: id, Text: "work"}); resp == nil || resp.Error != nil {
		t.Fatalf("chat.send failed: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventDone, id)
	srv.waitAutoCommits() // 轮结束的检查点提交是异步的：等它结束再做工作树操作
	meta, err := srv.st.WorktreeOf(id)
	if err != nil || meta.Path == "" {
		t.Fatalf("worktree not created: %+v err=%v", meta, err)
	}
	return id, meta.Path
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

// 用例1：项目会话 + release_worktree=true → 归档成功、目录消失、分支与会话记录保留。
func TestArchiveWithReleaseWorktreeRemovesCleanWorktree(t *testing.T) {
	srv, client, projectMeta, repo := archiveTestServer(t, "archive-release-clean")
	id, path := newProjectSessionWithWorktree(t, srv, client, projectMeta.ID)
	meta, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}

	resp := client.call(protocol.MethodSessionArchive, protocol.SessionArchiveParams{ID: id, Archived: true, ReleaseWorktree: true})
	if resp == nil || resp.Error != nil {
		t.Fatalf("archive with release failed: %+v", resp)
	}
	var result protocol.SessionArchiveResult
	decodeServerResult(t, resp.Result, &result)
	if !result.Archived || !result.ReleasedWorktree || result.ReleaseError != "" {
		t.Fatalf("archive result mismatch: %+v", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("released worktree directory still exists: %v", err)
	}
	if got := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", meta.Branch)); got == "" {
		t.Fatal("release deleted the session branch")
	}
	if !sessionArchived(t, srv, id) {
		t.Fatal("archived session record went missing")
	}
}

// 用例2：worktree 里有未提交改动 + release_worktree=true → 归档仍成功，释放失败只回报原因。
func TestArchiveWithReleaseWorktreeKeepsDirtyWorktree(t *testing.T) {
	srv, client, projectMeta, _ := archiveTestServer(t, "archive-release-dirty")
	id, path := newProjectSessionWithWorktree(t, srv, client, projectMeta.ID)
	dirty := filepath.Join(path, "dirty.txt")
	if err := os.WriteFile(dirty, []byte("uncommitted"), 0o600); err != nil {
		t.Fatal(err)
	}

	resp := client.call(protocol.MethodSessionArchive, protocol.SessionArchiveParams{ID: id, Archived: true, ReleaseWorktree: true})
	if resp == nil || resp.Error != nil {
		t.Fatalf("archive must succeed even when release fails: %+v", resp)
	}
	var result protocol.SessionArchiveResult
	decodeServerResult(t, resp.Result, &result)
	if !result.Archived {
		t.Fatalf("archive should have succeeded: %+v", result)
	}
	if result.ReleasedWorktree || result.ReleaseError == "" {
		t.Fatalf("dirty release must report a failure: %+v", result)
	}
	if !strings.Contains(result.ReleaseError, "未提交或未跟踪") {
		t.Fatalf("release error should carry the dirty reason: %q", result.ReleaseError)
	}
	if !sessionArchived(t, srv, id) {
		t.Fatal("archive did not commit the DB row")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dirty release removed the worktree: %v", err)
	}
	got, err := os.ReadFile(dirty)
	if err != nil || string(got) != "uncommitted" {
		t.Fatalf("dirty file damaged by rejected release: %q err=%v", got, err)
	}
}

// 用例3：release_worktree=false（或不传）→ 与现在完全一致，目录保留。
func TestArchiveWithoutReleaseWorktreeKeepsWorktree(t *testing.T) {
	srv, client, projectMeta, _ := archiveTestServer(t, "archive-keep-worktree")
	id, path := newProjectSessionWithWorktree(t, srv, client, projectMeta.ID)

	resp := client.call(protocol.MethodSessionArchive, protocol.SessionArchiveParams{ID: id, Archived: true})
	if resp == nil || resp.Error != nil {
		t.Fatalf("plain archive failed: %+v", resp)
	}
	var result protocol.SessionArchiveResult
	decodeServerResult(t, resp.Result, &result)
	if result.ReleasedWorktree || result.ReleaseError != "" {
		t.Fatalf("plain archive must not release anything: %+v", result)
	}
	if !sessionArchived(t, srv, id) {
		t.Fatal("plain archive did not commit the DB row")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plain archive removed the worktree: %v", err)
	}
}

// 用例4：带子会话（父有 worktree 元数据、子为空）→ 只释放父，跳过子不报错。
func TestArchiveWithReleaseSkipsChildWithoutWorktree(t *testing.T) {
	srv, client, projectMeta, _ := archiveTestServer(t, "archive-release-child")
	parent, parentPath := newProjectSessionWithWorktree(t, srv, client, projectMeta.ID)
	// 子会话只继承父的 workspace，worktree 三列为空（标签页没被打开过）。
	child, err := srv.st.CreateChild(parent, "agent-x", "dispatch-x")
	if err != nil {
		t.Fatalf("CreateChild failed: %v", err)
	}

	resp := client.call(protocol.MethodSessionArchive, protocol.SessionArchiveParams{ID: parent, Archived: true, ReleaseWorktree: true})
	if resp == nil || resp.Error != nil {
		t.Fatalf("archive with child failed: %+v", resp)
	}
	var result protocol.SessionArchiveResult
	decodeServerResult(t, resp.Result, &result)
	if result.ReleaseError != "" {
		t.Fatalf("child without worktree must be skipped silently: %q", result.ReleaseError)
	}
	if !result.ReleasedWorktree {
		t.Fatalf("parent worktree should have been released: %+v", result)
	}
	if _, err := os.Stat(parentPath); !os.IsNotExist(err) {
		t.Fatalf("parent worktree still exists: %v", err)
	}
	childMeta, err := srv.st.WorktreeOf(child)
	if err != nil || childMeta.Path != "" {
		t.Fatalf("child worktree meta should stay empty: %+v err=%v", childMeta, err)
	}
	if !sessionArchived(t, srv, parent) {
		t.Fatal("parent archive did not commit the DB row")
	}
}
