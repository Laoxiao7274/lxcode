// 合并进程（第二个 producer，Kind="merge"）的验收用例 1~5：项目会话起合并任务、
// 未分组会话报错、同一会话只允许一个、Settle 后父会话收到通告、merger Agent 定义。
// 全程不 spawn 真进程、不打网络（假 stream）。
package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
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

// mergeBlockStream 只把**合并任务**那一轮阻塞住（父轮的普通消息立刻返回）：
// 用来让合并任务停在 running，验证「同一会话只允许一个在跑的合并进程」。
func mergeBlockStream(release <-chan struct{}) testStream {
	return func(ctx context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		text := ""
		for _, m := range msgs {
			if m.Role == "user" {
				text = m.Content
			}
		}
		if strings.Contains(text, "源会话分支") {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
			}
		}
		return immediateStream(ctx, config.ModelConfig{}, nil, nil)
	}
}

// sendTurn 发一条消息并等这一轮结束（建立 worktree、写标题）。
// 之后还要等在途的自动提交收尾：合并发起时的扫描按「分支领先/工作树脏」判定
// 有无改动，检查点提交没落库会被判成无改动（生产路径当前轮改动以未提交形态
// 被 dirty 计入，不受影响）。
func sendTurn(t *testing.T, srv *Server, client *wsTestClient, id, text string) {
	t.Helper()
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: id, Text: text})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send(%q) 失败: %+v", text, resp)
	}
	waitSessionIdle(t, srv, id)
	srv.waitAutoCommits()
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

// callMergeRequest 直接经工具注册表调用 merge_request（与模型走的同一条路径）。
// 返回回填模型的文本（错误以「错误: 」开头——Execute 的三层错误约定）。
func callMergeRequest(srv *Server, sessionID, target string) string {
	args, _ := json.Marshal(map[string]string{"target_branch": target})
	ctx := tools.WithSessionID(context.Background(), sessionID)
	return srv.treg.Execute(ctx, llm.ToolCall{ID: "call-merge", Function: struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: tools.MergeRequestToolName, Arguments: string(args)}})
}

// waitMergeJob 等某条合并任务进入终态，返回它的快照。
func waitMergeJob(t *testing.T, mgr *jobs.Manager, sessionID, jobID string) jobs.Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, snap := range mgr.List(sessionID) {
			if snap.ID == jobID && snap.Status.Terminal() {
				return snap
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("合并任务 %s 未在期限内收尾", jobID)
	return jobs.Snapshot{}
}

// waitRecorderNotice 等假 LLM 收到含 want 的一轮（唤醒通告）。
func waitRecorderNotice(t *testing.T, rec *serverRecorder, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hasNotice(rec.lastMessages(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("父会话未收到含 %q 的通告", want)
}

// commitChange 在会话工作树里落一个文件改动并提交（模拟「这轮真的改了东西」）：
// 合并发起时的扫描按「分支领先/工作树脏」判定有无改动，纯空转的「你好」轮
// 没有检查点提交也没有未提交改动，会被如实判成无改动（新语义）。
func commitChange(t *testing.T, srv *Server, id, name string) {
	t.Helper()
	wt, err := srv.st.WorktreeOf(id)
	if err != nil || wt.Path == "" {
		t.Fatalf("worktree not ready: %+v err=%v", wt, err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, name), []byte(name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitServerTest(t, wt.Path, "add", "-A")
	gitServerTest(t, wt.Path, "commit", "-m", "test change "+name)
}

// TestMergeRequestStartsJob：用例 1 —— 项目会话起合并任务，立刻返回 job id，
// List 里出现一条 Kind=="merge" 的任务，标签含会话标题。
func TestMergeRequestStartsJob(t *testing.T) {
	repo := initGitProject(t)
	srv, client, mgr := newJobTestServer(t, immediateStream)
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好") // 建立 worktree 并写标题
	commitChange(t, srv, id, "a.txt")   // 留一个可合并的检查点提交

	out := callMergeRequest(srv, id, "")
	if !strings.Contains(out, "合并进程已启动") {
		t.Fatalf("merge_request 应回提示语: %q", out)
	}
	// 任务出现在列表里，Kind 与标签符合
	var found jobs.Snapshot
	for _, snap := range mgr.List(id) {
		if snap.Kind == "merge" {
			found = snap
			break
		}
	}
	if found.ID == "" {
		t.Fatal("jobs.List 里没有 Kind==merge 的任务")
	}
	if !strings.Contains(found.Label, "你好") {
		t.Fatalf("合并任务标签应含会话标题: %q", found.Label)
	}
	if !strings.Contains(out, found.ID) {
		t.Fatalf("提示语应含任务 id %s: %q", found.ID, out)
	}
	waitMergeJob(t, mgr, id, found.ID)
	waitSessionIdle(t, srv, id) // 等唤醒开的这一轮与在途自动提交收尾
	srv.waitAutoCommits()
}

// TestMergeRequestRejectsUngroupedSession：用例 2 —— 未分组会话没有可合并的分支。
func TestMergeRequestRejectsUngroupedSession(t *testing.T) {
	srv, client, _ := newJobTestServer(t, immediateStream)
	id := createTestSession(t, client, "") // 未分组会话
	out := callMergeRequest(srv, id, "")
	if !strings.Contains(out, "没有可合并的分支") {
		t.Fatalf("未分组会话应明确报错: %q", out)
	}
}

// TestMergeRequestRejectsConcurrent：用例 3 —— 同一项目同时只允许一个在跑的合并进程。
func TestMergeRequestRejectsConcurrent(t *testing.T) {
	repo := initGitProject(t)
	release := make(chan struct{}, 4)
	srv, client, mgr := newJobTestServer(t, mergeBlockStream(release))
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好") // 建立 worktree
	commitChange(t, srv, id, "a.txt")   // 留一个可合并的检查点提交

	if out := callMergeRequest(srv, id, ""); strings.Contains(out, "错误:") {
		t.Fatalf("第一次 merge_request 应成功: %q", out)
	}
	if out := callMergeRequest(srv, id, ""); !strings.Contains(out, "已有合并进程在跑") {
		t.Fatalf("第二次 merge_request 应报已有在跑: %q", out)
	}
	// 放行合并子会话，等它收尾（避免测试结束时 goroutine 还在写库）
	release <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running := false
		for _, snap := range mgr.List(id) {
			if snap.Kind == "merge" && !snap.Status.Terminal() {
				running = true
			}
		}
		if !running {
			waitSessionIdle(t, srv, id)
			srv.waitAutoCommits()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("合并任务未收尾")
}

// TestMergeJobSettleNotifiesParent：用例 4 —— 合并任务 Settle 后，父会话经唤醒投递收到通告。
func TestMergeJobSettleNotifiesParent(t *testing.T) {
	repo := initGitProject(t)
	rec := &serverRecorder{}
	srv, client, mgr := newJobTestServer(t, rec.stream)
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好")
	commitChange(t, srv, id, "a.txt") // 留一个可合并的检查点提交（空转轮无改动会被拒绝）

	out := callMergeRequest(srv, id, "")
	jobID := ""
	for _, snap := range mgr.List(id) {
		if snap.Kind == "merge" {
			jobID = snap.ID
		}
	}
	if jobID == "" || !strings.Contains(out, jobID) {
		t.Fatalf("应起一条合并任务: out=%q", out)
	}
	snap := waitMergeJob(t, mgr, id, jobID)
	if snap.Status != jobs.StatusCompleted {
		t.Fatalf("空仓库合并应成功: %+v", snap)
	}
	// 通告经现有唤醒投递自动回到父会话（含任务标签）
	waitRecorderNotice(t, rec, "后台任务")
	waitRecorderNotice(t, rec, "合并")
	waitSessionIdle(t, srv, id) // 等唤醒开的这一轮收尾（避免测试结束时还在跑）
	srv.waitAutoCommits()
}

// TestMergerAgentDefinition：用例 5 —— merger Agent 定义存在且字段符合，且不在 main 的委派名单里。
func TestMergerAgentDefinition(t *testing.T) {
	srv, _, _ := newJobTestServer(t, immediateStream)
	agents, err := srv.st.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	var merger, main *sessiondata.AgentDef
	for i := range agents {
		switch agents[i].ID {
		case "merger":
			merger = &agents[i]
		case "main":
			main = &agents[i]
		}
	}
	if merger == nil {
		t.Fatal("内置 Agent 名单缺 merger")
	}
	if merger.Name != "合并 Agent" || !merger.Enabled || merger.IsMain {
		t.Fatalf("merger 字段不符: %+v", merger)
	}
	if merger.Approval != "auto" {
		t.Fatalf("merger 权限默认应为 auto（后台进程没人看着确认门）: %q", merger.Approval)
	}
	if merger.Workflow != "merge-verify" {
		t.Fatalf("merger 流程应为 merge-verify: %q", merger.Workflow)
	}
	for _, want := range []string{"read_file", "search", "edit", "write_file", "bash"} {
		if !containsStr(merger.Tools, want) {
			t.Fatalf("merger 白名单缺 %s: %v", want, merger.Tools)
		}
	}
	if main == nil {
		t.Fatal("名单缺主 Agent")
	}
	if containsStr(main.Delegates, "merger") {
		t.Fatalf("merger 不该进主 Agent 的委派名单（它不是被派活的）: %v", main.Delegates)
	}
}

// TestMergeJobBroadcastsDispatchEvents：合并任务拉起 merger 子会话后，客户端应收到
// chat.dispatchStart（owner=父会话、session_id=子会话、dispatch_id=子会话 id）与
// chat.dispatchEnd（结论定格）——前端据此在父时间线挂卡、开子会话标签页（2026-10
// 用户拍板「合并进程要像子 Agent 一样有标签页」）。
func TestMergeJobBroadcastsDispatchEvents(t *testing.T) {
	repo := initGitProject(t)
	srv, client, mgr := newJobTestServer(t, immediateStream)
	meta := addAutoCommitProject(t, client, repo)
	id := createTestSession(t, client, meta.ID)
	sendTurn(t, srv, client, id, "你好")
	commitChange(t, srv, id, "a.txt") // 留一个可合并的检查点提交

	// 用户从后台任务面板发起（chat.mergeRequest——与 merge_request 工具同一条路径）
	resp := client.call(protocol.MethodChatMergeRequest, protocol.ChatMergeRequestParams{SessionID: id})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.mergeRequest 失败: %+v", resp)
	}
	var mr protocol.MergeRequestResult
	if b, err := json.Marshal(resp.Result); err != nil || json.Unmarshal(b, &mr) != nil || mr.JobID == "" {
		t.Fatalf("chat.mergeRequest 未回 job_id: %s", string(b))
	}

	// dispatchStart：归属父会话，带子会话 id 作 dispatch_id 与 session_id
	start := client.waitEvent(protocol.EventDispatchStart)
	if start == nil {
		t.Fatal("未收到 chat.dispatchStart")
	}
	var sp protocol.DispatchStartParams
	if b, err := json.Marshal(start.Params); err != nil || json.Unmarshal(b, &sp) != nil {
		t.Fatalf("dispatchStart 载荷解析失败: %v", err)
	}
	if sp.OwnerSessionID != id {
		t.Fatalf("dispatchStart 应归属父会话: %+v", sp)
	}
	if sp.SessionID == "" || sp.AgentName != "合并 Agent" {
		t.Fatalf("dispatchStart 应带子会话 id 与合并 Agent 身份: %+v", sp)
	}
	if sp.DispatchID != sp.SessionID {
		t.Fatalf("合并卡的 dispatch_id 应取子会话 id（确认/提问代理同键）: %+v", sp)
	}

	// dispatchEnd：结论定格（immediateStream 回 "ok"）
	end := client.waitEvent(protocol.EventDispatchEnd)
	if end == nil {
		t.Fatal("未收到 chat.dispatchEnd")
	}
	var ep protocol.DispatchEndParams
	if b, err := json.Marshal(end.Params); err != nil || json.Unmarshal(b, &ep) != nil {
		t.Fatalf("dispatchEnd 载荷解析失败: %v", err)
	}
	if ep.OwnerSessionID != id || ep.SessionID == "" || ep.IsError {
		t.Fatalf("dispatchEnd 归属/状态不符: %+v", ep)
	}
	if !strings.Contains(ep.Result, "ok") {
		t.Fatalf("dispatchEnd 应带子会话结论: %+v", ep)
	}
	// 任务本身照常收尾，且结束后父会话收到唤醒通告（既有链路不受影响）
	snap := waitMergeJob(t, mgr, id, mr.JobID)
	if snap.Status != jobs.StatusCompleted {
		t.Fatalf("合并任务应收尾为完成: %+v", snap)
	}
	waitSessionIdle(t, srv, id)
	srv.waitAutoCommits()
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
