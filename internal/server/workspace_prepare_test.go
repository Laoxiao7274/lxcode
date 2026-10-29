// 项目会话的工作区准备（2026-10 直连架构）：会话直接工作在用户的主检出目录与当前
// 分支，**不创建**任何分支/worktree、不做预热。四个用例分别钉住：首条发送即直连
// （无 git 子进程链、无 worktree 产物）、快路径幂等、老元数据会话 resume 切直连且
// 旧目录与分支原样保留、未分组会话行为不变。
package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/project"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// registerProject 走协议注册一个指向 dir 的项目，返回项目 id。
func registerProject(t *testing.T, client *wsTestClient, dir string) string {
	t.Helper()
	resp := client.call(protocol.MethodProjectAdd, protocol.ProjectAddParams{Name: "workspace-test", Path: dir})
	if resp == nil || resp.Error != nil {
		t.Fatalf("project.add failed: %+v", resp)
	}
	var meta protocol.ProjectMeta
	decodeServerResult(t, resp.Result, &meta)
	return meta.ID
}

// okStream 是立刻返回一句最终答复的假流。
func okStream() testStream {
	return func(_ context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
		close(ch)
		return ch, nil
	}
}

// assertNoWorktreeArtifacts 断言直连模式没有任何 worktree 产物：元数据 path/branch
// 为空、WorktreeRoot 下没有本会话的目录、仓库里没有 lxcode/session-<id> 分支。
func assertNoWorktreeArtifacts(t *testing.T, srv *Server, repo, sessionID string) store.WorktreeMeta {
	t.Helper()
	meta, err := srv.st.WorktreeOf(sessionID)
	if err != nil {
		t.Fatalf("WorktreeOf(%s): %v", sessionID, err)
	}
	if meta.Path != "" || meta.Branch != "" {
		t.Fatalf("直连模式不该有 worktree 元数据: %+v", meta)
	}
	if _, err := os.Stat(filepath.Join(srv.st.WorktreeRoot(), sessionID)); !os.IsNotExist(err) {
		t.Fatalf("直连模式不该创建 worktree 目录: err=%v", err)
	}
	if exists, err := project.BranchExists(srv.Ctx(), repo, "lxcode/session-"+sessionID); err != nil || exists {
		t.Fatalf("直连模式不该创建会话分支: exists=%v err=%v", exists, err)
	}
	return meta
}

// 用例 1：项目会话首条消息 → workDir=主检出、worktree_base=当时 HEAD、不建任何
// 分支/worktree；session.new 后没有预热动作（给 300ms 窗口，元数据保持为空直到
// 真正发送——旧架构的预热会在这之前把工作树建出来）。
func TestFirstSendWorksInMainCheckout(t *testing.T) {
	repo := initGitProject(t)
	head := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", "HEAD"))
	srv, client, _ := newTestServer(t, okStream())
	sessionID := createTestSession(t, client, registerProject(t, client, repo))

	// 预热已退役：session.new 之后不该有任何异步准备动作。
	time.Sleep(300 * time.Millisecond)
	if wmeta, err := srv.st.WorktreeOf(sessionID); err != nil || (wmeta.Path != "" || wmeta.Branch != "" || wmeta.BaseCommit != "") {
		t.Fatalf("session.new 后不该有 worktree 元数据（预热应已退役）: %+v err=%v", wmeta, err)
	}

	// 首条 chat.send：直连主检出，正常完成。
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: "首条"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("首条 chat.send 失败: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventDone, sessionID)

	meta := assertNoWorktreeArtifacts(t, srv, repo, sessionID)
	if meta.BaseCommit != head {
		t.Fatalf("worktree_base = %q, want 会话基线（当时 HEAD）%q", meta.BaseCommit, head)
	}
	sess, err := srv.session(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkDir() != repo {
		t.Fatalf("运行时 workDir = %q, want 主检出 %q", sess.WorkDir(), repo)
	}
	if !srv.worktreeReadyNow(sessionID) {
		t.Fatal("首条发送后快路径标记未置位")
	}
}

// 用例 2：快路径幂等——第二次 prepare 只付 os.Stat，不产生任何新的 git 产物
// （元数据与分支均不变，且 base 不被重写）。
func TestWorkspacePrepareIsIdempotent(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newTestServer(t, okStream())
	sessionID := createTestSession(t, client, registerProject(t, client, repo))
	if _, err := srv.session(sessionID); err != nil {
		t.Fatal(err)
	}
	sess, _ := srv.session(sessionID)
	if err := srv.prepareWorkspace(sessionID, sess); err != nil {
		t.Fatalf("首次 prepare 失败: %v", err)
	}
	before, err := srv.st.WorktreeOf(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	// 再 prepare 两次（快路径 + 完整路径都要幂等）。
	if err := srv.prepareWorkspace(sessionID, sess); err != nil {
		t.Fatalf("快路径 prepare 失败: %v", err)
	}
	srv.clearWorktreeReady(sessionID)
	if err := srv.prepareWorkspace(sessionID, sess); err != nil {
		t.Fatalf("完整路径 prepare 失败: %v", err)
	}
	after, err := srv.st.WorktreeOf(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("prepare 必须幂等: before=%+v after=%+v", before, after)
	}
	assertNoWorktreeArtifacts(t, srv, repo, sessionID)
}

// 用例 5：零提交仓库（git init 后没有任何提交，HEAD 指向未出生分支）建会话发
// 消息——分支解析回退 symbolic-ref 不再炸出「ambiguous argument 'HEAD'」（用户
// 实测的原始 bug）；轮末不再自动提交（2026-10 移除），改动留在工作区；不编基线。
func TestFirstSendWorksInUnbornRepo(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, repo, "init")
	gitServerTest(t, repo, "config", "user.name", "Test")
	gitServerTest(t, repo, "config", "user.email", "test@example.invalid")
	// 预放一个未跟踪文件：工作区有东西可提交（零提交仓库上第一次 CommitAll 就是
	// 根提交——CommitAll 只做 status/add/commit，不碰 HEAD 基础的 diff/stash，
	// unborn HEAD 也能走通）。
	if err := os.WriteFile(filepath.Join(repo, "draft.txt"), []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, client, _ := newTestServer(t, okStream())
	sessionID := createTestSession(t, client, registerProject(t, client, repo))
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: "首条"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("零提交仓库的首条 chat.send 失败: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventDone, sessionID)

	// 轮末自动提交已移除：这一轮不产生任何提交（unborn HEAD 仍是 unborn）。
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "HEAD").CombinedOutput(); err == nil {
		t.Fatalf("轮末不该再自动提交，但 HEAD 已存在: %s", out)
	}
	meta := assertNoWorktreeArtifacts(t, srv, repo, sessionID)
	if meta.BaseCommit != "" {
		t.Fatalf("零提交仓库不该编基线（unborn HEAD 没有可记的 HEAD）: %+v", meta)
	}
	sess, err := srv.session(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkDir() != repo {
		t.Fatalf("运行时 workDir = %q, want 主检出 %q", sess.WorkDir(), repo)
	}
}

// 用例 6：非 git 目录降级运行——注册时 EnsureRepo 会 git init，删掉 .git 模拟
// 「不是 git 仓库」的存量项目（.git 被删 / git init 从未成功）。会话照常创建、
// 发送成功，且不偷偷替用户 git init、不产生任何检查点提交。
func TestFirstSendWorksInNonGitDir(t *testing.T) {
	dir := t.TempDir()
	srv, client, _ := newTestServer(t, okStream())
	sessionID := createTestSession(t, client, registerProject(t, client, dir))
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: "首条"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("非 git 目录的首条 chat.send 失败（应降级而非报错）: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventDone, sessionID)

	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatal("非 git 降级不该偷偷 git init")
	}
	sess, err := srv.session(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkDir() != dir {
		t.Fatalf("运行时 workDir = %q, want 项目目录 %q", sess.WorkDir(), dir)
	}
}

// 用例 3：老元数据会话（有 worktree 元数据 + 磁盘目录 + lxcode/session-* 分支）
// resume → 切到直连模式（workDir=主检出），**旧目录与分支原样保留不删**，旧基线
// 沿用为会话基线。
func TestLegacySessionResumeSwitchesToMainCheckout(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newTestServer(t, okStream())
	meta := addGitProject(t, client, repo)
	sessionID := createTestSession(t, client, meta.ID)

	// 伪造存量状态：按旧架构建 worktree + 分支 + 元数据（数据形态与老库一致）。
	wt, err := project.CreateWorktree(repo, srv.st.WorktreeRoot(), meta.ID, sessionID)
	if err != nil {
		t.Fatalf("构造老 worktree 失败: %v", err)
	}
	if err := srv.st.SetWorktree(sessionID, store.WorktreeMeta{Path: wt.Path, Branch: wt.Branch, BaseCommit: wt.BaseCommit}); err != nil {
		t.Fatal(err)
	}
	// 在旧 worktree 里留一个未合并的改动（老会话的典型遗留）。
	if err := os.WriteFile(filepath.Join(wt.Path, "legacy.txt"), []byte("legacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 首条 chat.send：直连主检出，正常完成（老会话场景里这条路径同样触发 prepare）。
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: "resume 后第一条"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventDone, sessionID)
	sess, err := srv.session(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.WorkDir() != repo {
		t.Fatalf("resume 后 workDir = %q, want 主检出 %q", sess.WorkDir(), repo)
	}
	// 旧目录与分支原样保留（数据零丢失）；元数据也不被改写（SetWorktree 守卫 + 直连
	// 准备根本不碰老元数据）。
	if _, err := os.Stat(filepath.Join(wt.Path, "legacy.txt")); err != nil {
		t.Fatalf("旧 worktree 目录应原样保留: %v", err)
	}
	if exists, err := project.BranchExists(srv.Ctx(), repo, wt.Branch); err != nil || !exists {
		t.Fatalf("旧分支 %s 应原样保留: exists=%v err=%v", wt.Branch, exists, err)
	}
	if m, err := srv.st.WorktreeOf(sessionID); err != nil || m.Path != wt.Path || m.Branch != wt.Branch || m.BaseCommit != wt.BaseCommit {
		t.Fatalf("老元数据不该被改写: got %+v err=%v", m, err)
	}
}

// 用例 4：子会话 prepare 同样直连主检出，不建 worktree。
func TestChildSessionPrepareUsesMainCheckout(t *testing.T) {
	repo := initGitProject(t)
	srv, client, _ := newTestServer(t, okStream())
	meta := addGitProject(t, client, repo)
	parentID := createTestSession(t, client, meta.ID)
	if _, err := srv.session(parentID); err != nil {
		t.Fatal(err)
	}
	childID, err := srv.st.CreateChild(parentID, "main", "disp-1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	child, err := srv.session(childID)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.prepareWorkspace(childID, child); err != nil {
		t.Fatalf("子会话 prepare 失败: %v", err)
	}
	if child.WorkDir() != repo {
		t.Fatalf("子会话 workDir = %q, want 主检出 %q", child.WorkDir(), repo)
	}
	assertNoWorktreeArtifacts(t, srv, repo, childID)
}

// 用例 5：未分组会话行为不变（无 workspace、无元数据、快路径标记不置位），
// 首条 chat.send 照常工作。
func TestUngroupedSessionUnchanged(t *testing.T) {
	srv, client, _ := newTestServer(t, okStream())
	sessionID := createTestSession(t, client, "")
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
