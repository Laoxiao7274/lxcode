// 每轮结束的检查点提交的集成用例（用例 1~4）：项目会话每轮一个提交、无改动不提交、
// 未分组会话不提交。
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
	"github.com/moyunteng/lxcode/internal/protocol"
)

// autoCommitStream 是一个可控的假流：进入时把本轮用户消息发给测试（测试趁机往
// worktree 写文件），等测试放行后返回一句最终答复（无工具调用 = 这一轮结束）。
func autoCommitStream(started chan<- string, release <-chan struct{}) testStream {
	return func(ctx context.Context, _ config.ModelConfig, messages []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		text := ""
		for _, m := range messages {
			if m.Role == "user" {
				text = m.Content
			}
		}
		select {
		case started <- text:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "ok"}
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: "ok"}, FinishReason: llm.FinishStop,
		}}
		close(ch)
		return ch, nil
	}
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

// addAutoCommitProject 注册一个项目并返回元数据。
func addAutoCommitProject(t *testing.T, client *wsTestClient, path string) protocol.ProjectMeta {
	t.Helper()
	resp := client.call(protocol.MethodProjectAdd, protocol.ProjectAddParams{Name: "autocommit", Path: path})
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

// sendProjectTurn 跑一轮：等流进入（趁机会写 change），放行收尾，等 chat.done 与
// 在途自动提交都结束 Frobenius。change 为 nil = 这一轮不改动任何文件。
func sendProjectTurn(t *testing.T, srv *Server, client *wsTestClient, id, text string, started chan string, release chan struct{}, change func(worktreePath string)) {
	t.Helper()
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: id, Text: text})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send(%q) failed: %+v", text, resp)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("stream never started for %q", text)
	}
	if change != nil {
		meta, err := srv.st.WorktreeOf(id)
		if err != nil || meta.Path == "" {
			t.Fatalf("worktree not ready: %+v err=%v", meta, err)
		}
		change(meta.Path)
	}
	release <- struct{}{}
	waitSessionEvent(t, client, protocol.EventDone, id)
	srv.waitAutoCommits() // 提交是异步的：等它结束再断言
}

// TestAutoCommitCommitsEachTurn 覆盖用例 1~3：每轮有改动就一个提交、信息含轮次与摘要、
// 无改动不提交。
func TestAutoCommitCommitsEachTurn(t *testing.T) {
	repo := initGitProject(t)
	started := make(chan string, 8)
	release := make(chan struct{}, 8)
	srv, client, _ := newTestServer(t, autoCommitStream(started, release))
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)

	// 用例 1：第一轮有文件改动 → worktree 里出现一个新提交，信息含轮次与摘要。
	sendProjectTurn(t, srv, client, id, "第一轮请求：新建文件", started, release, func(path string) {
		if err := os.WriteFile(filepath.Join(path, "a.txt"), []byte("a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not created: %+v err=%v", wt, err)
	}
	logOut := gitServerTest(t, wt.Path, "log", "--oneline")
	if !strings.Contains(logOut, "第 1 轮") {
		t.Fatalf("git log 缺少第 1 轮提交:\n%s", logOut)
	}
	if got := strings.TrimSpace(gitServerTest(t, wt.Path, "log", "-1", "--pretty=%s")); got != "第 1 轮：第一轮请求：新建文件" {
		t.Fatalf("提交信息 = %q", got)
	}
	if count := commitCount(t, wt.Path); count != 2 {
		t.Fatalf("第 1 轮后提交数 = %d, want 2（base + 第 1 轮）", count)
	}

	// 用例 2：再跑一轮（有改动）→ 提交数变 3（base + 两轮）。
	sendProjectTurn(t, srv, client, id, "第二轮请求：再改一处", started, release, func(path string) {
		if err := os.WriteFile(filepath.Join(path, "b.txt"), []byte("b\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if got := strings.TrimSpace(gitServerTest(t, wt.Path, "log", "-1", "--pretty=%s")); got != "第 2 轮：第二轮请求：再改一处" {
		t.Fatalf("第 2 轮提交信息 = %q", got)
	}
	if count := commitCount(t, wt.Path); count != 3 {
		t.Fatalf("第 2 轮后提交数 = %d, want 3（base + 两轮）", count)
	}

	// 用例 3：一轮没有任何文件改动 → 不产生提交（提交数不变，且不报错）。
	before := commitCount(t, wt.Path)
	sendProjectTurn(t, srv, client, id, "第三轮请求：不改动", started, release, nil)
	if after := commitCount(t, wt.Path); after != before {
		t.Fatalf("无改动的一轮不该产生提交: before=%d after=%d", before, after)
	}
}

// TestAutoCommitSkipsUngroupedSession 覆盖用例 4：未分组会话（无 worktree）跑一轮 →
// 不尝试提交、不报错。
func TestAutoCommitSkipsUngroupedSession(t *testing.T) {
	started := make(chan string, 8)
	release := make(chan struct{}, 8)
	srv, client, _ := newTestServer(t, autoCommitStream(started, release))
	id := createTestSession(t, client, "") // 未分组会话：没有 worktree
	sendProjectTurn(t, srv, client, id, "未分组会话的一轮", started, release, nil)
	meta, err := srv.st.WorktreeOf(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Path != "" || meta.Branch != "" {
		t.Fatalf("未分组会话不该有 worktree: %+v", meta)
	}
}
