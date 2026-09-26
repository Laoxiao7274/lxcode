package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func TestConcurrentSessionsHaveIndependentRuntimeAndHistory(t *testing.T) {
	started := make(chan string, 2)
	release := map[string]chan struct{}{"first": make(chan struct{}), "second": make(chan struct{})}
	stream := func(ctx context.Context, _ config.ModelConfig, messages []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		text := ""
		for _, message := range messages {
			if message.Role == "user" {
				text = message.Content
			}
		}
		started <- text
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release[text]:
		}
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "reply-" + text}
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: "reply-" + text}, FinishReason: llm.FinishStop,
		}}
		close(ch)
		return ch, nil
	}
	srv, client, _ := newTestServer(t, stream)
	firstID := createTestSession(t, client, "")
	secondID := createTestSession(t, client, "")

	for _, item := range []struct{ id, text string }{{firstID, "first"}, {secondID, "second"}} {
		resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: item.id, Text: item.text})
		if resp == nil || resp.Error != nil {
			t.Fatalf("chat.send(%s) failed: %+v", item.text, resp)
		}
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case text := <-started:
			seen[text] = true
		case <-time.After(3 * time.Second):
			t.Fatalf("both sessions did not enter their own stream; started=%v", seen)
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("stream invocations crossed or went missing: %v", seen)
	}

	if resp := client.call(protocol.MethodChatCancel, protocol.ChatSessionParams{SessionID: firstID}); resp == nil || resp.Error != nil {
		t.Fatalf("cancel first session failed: %+v", resp)
	}
	waitSessionEvent(t, client, protocol.EventError, firstID)
	waitSessionBusy(t, client, firstID, false)
	second, err := srv.session(secondID)
	if err != nil || !second.Busy() {
		t.Fatalf("canceling first session affected second: busy=%v err=%v", err == nil && second.Busy(), err)
	}

	close(release["second"])
	waitSessionEvent(t, client, protocol.EventDone, secondID)
	waitSessionBusy(t, client, secondID, false)

	firstHistory := readTestHistory(t, client, firstID)
	secondHistory := readTestHistory(t, client, secondID)
	if len(firstHistory.Messages) != 1 || firstHistory.Messages[0].Content != "first" {
		t.Fatalf("canceled session history leaked or lost data: %+v", firstHistory.Messages)
	}
	if len(secondHistory.Messages) != 2 || secondHistory.Messages[0].Content != "second" || secondHistory.Messages[1].Content != "reply-second" {
		t.Fatalf("second session history mismatch: %+v", secondHistory.Messages)
	}
}

func TestProjectSessionsGetIsolatedWorktreesFromHEAD(t *testing.T) {
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
	projectResp := client.call(protocol.MethodProjectAdd, protocol.ProjectAddParams{Name: "worktree-test", Path: repo})
	if projectResp == nil || projectResp.Error != nil {
		t.Fatalf("project.add failed: %+v", projectResp)
	}
	var projectMeta protocol.ProjectMeta
	decodeServerResult(t, projectResp.Result, &projectMeta)
	firstID := createTestSession(t, client, projectMeta.ID)
	secondID := createTestSession(t, client, projectMeta.ID)
	for _, id := range []string{firstID, secondID} {
		resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: id, Text: "work"})
		if resp == nil || resp.Error != nil {
			t.Fatalf("project chat.send(%s) failed: %+v", id, resp)
		}
		waitSessionEvent(t, client, protocol.EventDone, id)
	}
	first, err := srv.st.WorktreeOf(firstID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := srv.st.WorktreeOf(secondID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == "" || second.Path == "" || first.Path == second.Path {
		t.Fatalf("sessions did not receive independent worktree paths: first=%+v second=%+v", first, second)
	}
	if first.BaseCommit != head || second.BaseCommit != head {
		t.Fatalf("worktrees did not start at initial HEAD %s: first=%+v second=%+v", head, first, second)
	}
	firstRuntime, err := srv.session(firstID)
	if err != nil {
		t.Fatal(err)
	}
	secondRuntime, err := srv.session(secondID)
	if err != nil {
		t.Fatal(err)
	}
	if firstRuntime.WorkDir() != first.Path || secondRuntime.WorkDir() != second.Path {
		t.Fatalf("runtime work dirs do not match their worktrees: %q %q", firstRuntime.WorkDir(), secondRuntime.WorkDir())
	}
	if err := os.WriteFile(filepath.Join(first.Path, "first-only.txt"), []byte("isolated"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(second.Path, "first-only.txt"), filepath.Join(repo, "first-only.txt")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("write leaked outside first session worktree at %s: %v", path, err)
		}
	}

	rejected := client.call(protocol.MethodSessionWorktreeRelease, protocol.SessionWorktreeReleaseParams{ID: firstID})
	if rejected == nil || rejected.Error == nil || !strings.Contains(rejected.Error.Message, "未提交或未跟踪") {
		t.Fatalf("release must reject the untracked file: %+v", rejected)
	}
	if _, err := os.Stat(filepath.Join(first.Path, "first-only.txt")); err != nil {
		t.Fatalf("rejected release damaged untracked file: %v", err)
	}
	if err := os.Remove(filepath.Join(first.Path, "first-only.txt")); err != nil {
		t.Fatal(err)
	}
	released := client.call(protocol.MethodSessionWorktreeRelease, protocol.SessionWorktreeReleaseParams{ID: firstID})
	if released == nil || released.Error != nil {
		t.Fatalf("release clean worktree failed: %+v", released)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("released worktree path still exists: %v", err)
	}
	if got := strings.TrimSpace(gitServerTest(t, repo, "rev-parse", first.Branch)); got == "" {
		t.Fatal("release deleted the session branch")
	}
	resume := client.call(protocol.MethodSessionResume, protocol.SessionResumeParams{ID: firstID})
	if resume == nil || resume.Error != nil {
		t.Fatalf("resume should restore released worktree: %+v", resume)
	}
	if got := strings.TrimSpace(gitServerTest(t, first.Path, "branch", "--show-current")); got != first.Branch {
		t.Fatalf("restored worktree branch = %q, want %q", got, first.Branch)
	}
	if _, err := os.Stat(filepath.Join(first.Path, "base.txt")); err != nil {
		t.Fatalf("worktree was not restored from its recorded branch: %v", err)
	}
}

func TestBusyProjectSessionCannotReleaseWorktree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "project")
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

	started := make(chan struct{}, 1)
	finish := make(chan struct{})
	defer func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
	}()
	srv, client, _ := newTestServer(t, func(ctx context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-finish:
		}
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
		close(ch)
		return ch, nil
	})
	projectResp := client.call(protocol.MethodProjectAdd, protocol.ProjectAddParams{Name: "busy-release-test", Path: repo})
	if projectResp == nil || projectResp.Error != nil {
		t.Fatalf("project.add failed: %+v", projectResp)
	}
	var projectMeta protocol.ProjectMeta
	decodeServerResult(t, projectResp.Result, &projectMeta)
	sessionID := createTestSession(t, client, projectMeta.ID)
	send := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: "hold"})
	if send == nil || send.Error != nil {
		t.Fatalf("chat.send failed: %+v", send)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model stream did not start")
	}
	meta, err := srv.st.WorktreeOf(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	rejected := client.call(protocol.MethodSessionWorktreeRelease, protocol.SessionWorktreeReleaseParams{ID: sessionID})
	if rejected == nil || rejected.Error == nil || rejected.Error.Code != protocol.CodeBusy {
		t.Fatalf("busy worktree release should return CodeBusy: %+v", rejected)
	}
	if _, err := os.Stat(meta.Path); err != nil {
		t.Fatalf("busy release removed the worktree: %v", err)
	}

	close(finish)
	waitSessionEvent(t, client, protocol.EventDone, sessionID)
	released := client.call(protocol.MethodSessionWorktreeRelease, protocol.SessionWorktreeReleaseParams{ID: sessionID})
	if released == nil || released.Error != nil {
		t.Fatalf("idle clean worktree release failed: %+v", released)
	}
	if _, err := os.Stat(meta.Path); !os.IsNotExist(err) {
		t.Fatalf("idle release did not remove worktree: %v", err)
	}
}

func createTestSession(t *testing.T, client *wsTestClient, workspace string) string {
	t.Helper()
	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{Workspace: workspace})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.new failed: %+v", resp)
	}
	var result protocol.SessionResult
	decodeServerResult(t, resp.Result, &result)
	if result.SessionID == "" {
		t.Fatal("session.new returned an empty session id")
	}
	return result.SessionID
}

func readTestHistory(t *testing.T, client *wsTestClient, sessionID string) protocol.ChatHistoryResult {
	t.Helper()
	resp := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sessionID})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.history(%s) failed: %+v", sessionID, resp)
	}
	var result protocol.ChatHistoryResult
	decodeServerResult(t, resp.Result, &result)
	return result
}

func waitSessionEvent(t *testing.T, client *wsTestClient, method, sessionID string) protocol.Response {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-client.events:
			if !ok {
				t.Fatalf("event stream closed waiting for %s/%s", method, sessionID)
			}
			if event.Method != method {
				continue
			}
			var params struct {
				SessionID      string `json:"session_id"`
				OwnerSessionID string `json:"owner_session_id"`
				Aborted        bool   `json:"aborted"`
			}
			decodeServerResult(t, event.Params, &params)
			owner := params.OwnerSessionID
			if owner == "" {
				owner = params.SessionID
			}
			if owner == sessionID {
				if method == protocol.EventError && !params.Aborted {
					t.Fatalf("expected cancellation error for %s, got %+v", sessionID, params)
				}
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s event owned by %s", method, sessionID)
		}
	}
}

func waitSessionBusy(t *testing.T, client *wsTestClient, sessionID string, busy bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-client.events:
			if !ok {
				t.Fatalf("event stream closed waiting for busy=%v", busy)
			}
			if event.Method != protocol.EventBusy {
				continue
			}
			var params protocol.BusyParams
			decodeServerResult(t, event.Params, &params)
			if params.SessionID == sessionID && params.Busy == busy {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s busy=%v", sessionID, busy)
		}
	}
}

func decodeServerResult(t *testing.T, value any, target any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func gitServerTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
