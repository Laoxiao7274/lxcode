// session.new 的 worktree 异步预热：把建工作树的 git 链挪出首条 chat.send 的
// 等待路径（体验修复批次 2）。三个用例分别钉住：预热成功 → 首条发送走快路径；
// 预热失败 → fail-open（不影响 session.new 响应，chat.send 报既有错误）；
// 未分组会话不预热、行为不变。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// registerProject 走协议注册一个指向 dir 的项目，返回项目 id。
func registerProject(t *testing.T, client *wsTestClient, dir string) string {
	t.Helper()
	resp := client.call(protocol.MethodProjectAdd, protocol.ProjectAddParams{Name: "preheat-test", Path: dir})
	if resp == nil || resp.Error != nil {
		t.Fatalf("project.add failed: %+v", resp)
	}
	var meta protocol.ProjectMeta
	decodeServerResult(t, resp.Result, &meta)
	return meta.ID
}

// waitPreheatedWorktree 轮询等待预热完成（WorktreeOf 的 Path 非空即成）。
// 预热是纯异步的 goroutine——这里不用测试钩子，轮询存储元数据就是验收口径本身。
func waitPreheatedWorktree(t *testing.T, s *Server, sessionID string) (path, branch, base string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		meta, err := s.st.WorktreeOf(sessionID)
		if err != nil {
			t.Fatalf("WorktreeOf(%s): %v", sessionID, err)
		}
		if meta.Path != "" {
			return meta.Path, meta.Branch, meta.BaseCommit
		}
		select {
		case <-deadline:
			t.Fatalf("预热 5s 内未完成：session=%s 的 worktree 元数据仍为空", sessionID)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// 用例 1：项目会话 session.new 后预热完成——worktree 目录存在、元数据齐全
// （分支/基线/运行时 workDir），且快路径标记已就位；随后首条 chat.send 正常工作。
// 快路径的证明方式：发送前 worktreeReadyNow 已为 true（只有 prepareWorktreeLocked
// 成功收尾才会置位）——若预热没生效，send 要现跑整条 git 链才能置位。
func TestSessionNewPreheatsProjectWorktree(t *testing.T) {
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
	head := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "HEAD"))

	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
		close(ch)
		return ch, nil
	})
	sessionID := createTestSession(t, client, registerProject(t, client, repo))

	// 预热是异步的：等它的产物落进存储元数据。
	path, branch, base := waitPreheatedWorktree(t, srv, sessionID)
	if branch != "lxcode/session-"+sessionID {
		t.Fatalf("预热建的分支 = %q, want %q", branch, "lxcode/session-"+sessionID)
	}
	if base != head {
		t.Fatalf("预热基线 = %q, want 当时的 HEAD %q", base, head)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("预热后的 worktree 目录不可用: path=%s err=%v", path, err)
	}
	sess, err := srv.session(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkDir() != path {
		t.Fatalf("运行时 workDir = %q, want 预热路径 %q", sess.WorkDir(), path)
	}
	if !srv.worktreeReadyNow(sessionID) {
		t.Fatal("预热完成后快路径标记未置位——首条 send 会白跑一遍 git 链")
	}

	// 首条 chat.send：预热已把工作树备好，应走快路径正常完成（不超时、不报错）。
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: "首条"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("预热后的首条 chat.send 失败: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventDone, sessionID)
	history := readTestHistory(t, client, sessionID)
	if len(history.Messages) < 1 || history.Messages[0].Content != "首条" {
		t.Fatalf("首条消息没有进历史: %+v", history.Messages)
	}
	srv.waitAutoCommits()
}

// 用例 2：预热失败（workspace 指向的目录不是 git 仓库）→ fail-open：
// session.new 照常响应；后续 chat.send 返回明确的既有错误（不 panic、不悬挂）。
// 直接用 store.AddProject 绕过 project.Add（后者会顺手 git init——那就构不成失败了）。
func TestPreheatFailureDoesNotBlockSessionNewOrSend(t *testing.T) {
	notRepo := filepath.Join(t.TempDir(), "not-a-repo")
	if err := os.MkdirAll(notRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
		close(ch)
		return ch, nil
	})
	meta, err := srv.st.AddProject("preheat-fail", notRepo)
	if err != nil {
		t.Fatal(err)
	}
	// session.new 必须照常成功（预热失败绝不上抛、绝不阻塞响应）。
	sessionID := createTestSession(t, client, meta.ID)
	// 给预热 goroutine 一个落空的机会窗口：它应当只记日志，不产生任何元数据。
	time.Sleep(200 * time.Millisecond)
	if wmeta, err := srv.st.WorktreeOf(sessionID); err != nil || wmeta.Path != "" {
		t.Fatalf("预热失败却写下了 worktree 元数据: %+v err=%v", wmeta, err)
	}
	// 后续 chat.send：返回明确的既有错误（不是 panic / 不是超时悬挂）。
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: "首条"})
	if resp == nil {
		t.Fatal("chat.send 无响应（悬挂）")
	}
	if resp.Error == nil {
		t.Fatalf("chat.send 应当失败（目录不是 git 仓库）: %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "HEAD") {
		t.Fatalf("错误信息不含既有判定的人话原因: %+v", resp.Error)
	}
}

// 用例 3：未分组会话 session.new → 不预热、行为不变（无 workspace、无 worktree
// 元数据、快路径标记不置位），首条 chat.send 照常工作。
func TestUngroupedSessionNewDoesNotPreheat(t *testing.T) {
	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
		close(ch)
		return ch, nil
	})
	sessionID := createTestSession(t, client, "")
	// 给可能被错误启动的预热一个机会窗口：未分组会话不该有任何动作。
	time.Sleep(200 * time.Millisecond)
	ws, err := srv.st.WorkspaceOf(sessionID)
	if err != nil || ws != "" {
		t.Fatalf("未分组会话的 workspace = %q err=%v, want 空", ws, err)
	}
	if wmeta, err := srv.st.WorktreeOf(sessionID); err != nil || wmeta.Path != "" {
		t.Fatalf("未分组会话不该有 worktree 元数据: %+v err=%v", wmeta, err)
	}
	if srv.worktreeReadyNow(sessionID) {
		t.Fatal("未分组会话不该置快路径标记")
	}
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: "未分组首条"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("未分组会话的 chat.send 失败: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventDone, sessionID)
}
