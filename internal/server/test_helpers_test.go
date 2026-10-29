// 合并进程验收用例与 workspace 遗留链路测试共用的**无标签**测试 helper：
// immediateStream / legacyWorktree / sendTurn / waitSessionIdle 被默认构建里的
// gitview / workspace_publish / workspace 测试引用，而 mergejob_test.go 带
// lxmerge 标签隔离——通用 helper 必须住在无标签文件里，默认构建才编译得过。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// immediateStream 是立刻返回一句最终答复的假流（父轮与合并子会话共用）。
func immediateStream(_ context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 2)
	ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "ok"}
	ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
		Message: llm.Message{Role: "assistant", Content: "ok"}, FinishReason: llm.FinishStop,
	}}
	close(ch)
	return ch, nil
}

// legacyWorktree 给会话伪造存量 worktree 状态（2026-10 直连架构不再自动建 worktree；
// 合并链路与 workspace 遗留工具的测试都用老库的数据形态驱动）。
func legacyWorktree(t *testing.T, srv *Server, projectID, sessionID string) project.Worktree {
	t.Helper()
	meta, found, err := srv.st.ProjectByID(projectID)
	if err != nil || !found {
		t.Fatalf("project %s not found: found=%v err=%v", projectID, found, err)
	}
	wt, err := project.CreateWorktree(meta.Path, srv.st.WorktreeRoot(), projectID, sessionID)
	if err != nil {
		t.Fatalf("构造老 worktree 失败: %v", err)
	}
	if err := srv.st.SetWorktree(sessionID, store.WorktreeMeta{Path: wt.Path, Branch: wt.Branch, BaseCommit: wt.BaseCommit}); err != nil {
		t.Fatal(err)
	}
	return wt
}

// sendTurn 发一条消息并等这一轮结束（对准主检出并写标题）。
// 轮末自动检查点提交已移除（2026-10 用户拍板）：这一轮结束后工作区仍保留未提交
// 改动，需要提交的用例自己调 CommitAll / workspace_sync。
func sendTurn(t *testing.T, srv *Server, client *wsTestClient, id, text string) {
	t.Helper()
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: id, Text: text})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send(%q) 失败: %+v", text, resp)
	}
	waitSessionIdle(t, srv, id)
}

// initGitProject 建一个带基线提交的 Git 仓库（项目根）。
func initGitProject(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, repo, "init")
	gitServerTest(t, repo, "config", "user.name", "Test")
	gitServerTest(t, repo, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, repo, "add", "base.txt")
	gitServerTest(t, repo, "commit", "-m", "base")
	return repo
}

// addGitProject 注册一个 git 项目并返回元数据。
func addGitProject(t *testing.T, client *wsTestClient, path string) protocol.ProjectMeta {
	t.Helper()
	resp := client.call(protocol.MethodProjectAdd, protocol.ProjectAddParams{Name: "project", Path: path})
	if resp == nil || resp.Error != nil {
		t.Fatalf("project.add failed: %+v", resp)
	}
	var meta protocol.ProjectMeta
	decodeServerResult(t, resp.Result, &meta)
	return meta
}

// commitCount 数本工作树 HEAD 上的提交数。
func commitCount(t *testing.T, dir string) int {
	t.Helper()
	out := strings.TrimSpace(gitServerTest(t, dir, "rev-list", "--count", "HEAD"))
	n, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("解析提交数失败: %q", out)
	}
	return n
}

// waitSessionIdle 轮询会话忙闲直到空闲。
func waitSessionIdle(t *testing.T, srv *Server, id string) {
	t.Helper()
	sess, err := srv.session(id)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && sess.Busy() {
		time.Sleep(10 * time.Millisecond)
	}
	if sess.Busy() {
		t.Fatalf("会话 %s 一直忙", id)
	}
}
