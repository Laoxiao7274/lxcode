// 后台任务的协议端到端测试：job.* 三个方法（假 producer）+ 唤醒投递
// （契约 docs/jobs.md §4/§5）。全程不 spawn 真进程、不打网络。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/jobs"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// serverRecorder 是可观测的假 LLM：记录每次调用收到的消息并回固定文本。
type serverRecorder struct {
	mu    sync.Mutex
	calls [][]llm.Message
}

func (r *serverRecorder) stream(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]llm.Message(nil), msgs...))
	r.mu.Unlock()
	ch := make(chan llm.StreamEvent, 2)
	ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "收到"}
	ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
		Message: llm.Message{Role: "assistant", Content: "收到"}, FinishReason: llm.FinishStop,
	}}
	close(ch)
	return ch, nil
}

func (r *serverRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// lastMessages 返回最近一次模型调用收到的消息（断言通告是否进历史）。
func (r *serverRecorder) lastMessages() []llm.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}

func hasNotice(msgs []llm.Message, want string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, want) {
			return true
		}
	}
	return false
}

// newJobTestServer 起一个挂了后台任务管理器的测试服务端。
func newJobTestServer(t *testing.T, stream testStream) (*Server, *wsTestClient, *jobs.Manager) {
	t.Helper()
	srv, client, _ := newTestServer(t, stream)
	mgr := jobs.NewManager(t.TempDir())
	t.Cleanup(mgr.Shutdown)
	srv.AttachJobs(mgr)
	return srv, client, mgr
}

// waitCalls 等模型调用次数到达 n，并等这一轮收尾。
func waitCalls(t *testing.T, srv *Server, rec *serverRecorder, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && rec.count() < n {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.count() < n {
		t.Fatalf("模型调用次数未达 %d（实际 %d）", n, rec.count())
	}
	waitBusyClear(t, srv)
}

// newSessionViaChatSend 用 chat.send 建一个会话并等它收尾，返回会话 id。
func newSessionViaChatSend(t *testing.T, srv *Server, client *wsTestClient) string {
	t.Helper()
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "你好"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	b, _ := json.Marshal(resp.Result)
	var res struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(b, &res); err != nil || res.SessionID == "" {
		t.Fatalf("chat.send 未回 session_id: %s", b)
	}
	waitBusyClear(t, srv)
	return res.SessionID
}

// TestJobMethodsEndToEnd：job.list / job.kill / job.log 三个方法的端到端
// （假 producer：直接调 Manager，不起真进程）。
func TestJobMethodsEndToEnd(t *testing.T) {
	srv, client, mgr := newJobTestServer(t, nil)
	_ = srv
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "假任务", SessionID: "s-none"})
	if err != nil {
		t.Fatal(err)
	}
	// 启动即广播 job.started
	ev := client.waitEvent(protocol.EventJobStarted)
	if ev == nil {
		t.Fatal("应收到 job.started")
	}
	var started protocol.JobInfo
	b, _ := json.Marshal(ev.Params)
	mustJSON(t, b, &started)
	if started.ID != j.ID() || started.Status != string(jobs.StatusRunning) || started.Label != "假任务" {
		t.Fatalf("job.started 载荷不符: %+v", started)
	}

	// job.list
	resp := client.call(protocol.MethodJobList, protocol.JobListParams{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("job.list 失败: %+v", resp)
	}
	var list protocol.JobListResult
	b, _ = json.Marshal(resp.Result)
	mustJSON(t, b, &list)
	if len(list.Jobs) != 1 || list.Jobs[0].ID != j.ID() || list.Jobs[0].OutputPath == "" {
		t.Fatalf("job.list 载荷不符: %+v", list)
	}
	if list.Jobs[0].StartedAt == "" {
		t.Fatal("job.list 应带 RFC3339 开始时间")
	}

	// job.log：全量落盘日志
	if _, err := j.Write([]byte("第一行\n第二行\n")); err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodJobLog, protocol.JobLogParams{ID: j.ID()})
	if resp == nil || resp.Error != nil {
		t.Fatalf("job.log 失败: %+v", resp)
	}
	var logRes protocol.JobLogResult
	b, _ = json.Marshal(resp.Result)
	mustJSON(t, b, &logRes)
	if !strings.Contains(logRes.Data, "第一行") || logRes.Truncated {
		t.Fatalf("job.log 应回全量日志: %+v", logRes)
	}

	// job.kill = 用户点「结束」→ EndedBy=user（与 agent 的 job_kill 同一条路径）
	resp = client.call(protocol.MethodJobKill, protocol.JobKillParams{ID: j.ID()})
	if resp == nil || resp.Error != nil {
		t.Fatalf("job.kill 失败: %+v", resp)
	}
	var killRes protocol.JobKillResult
	b, _ = json.Marshal(resp.Result)
	mustJSON(t, b, &killRes)
	if killRes.Job.Status != string(jobs.StatusStopping) || killRes.Job.EndedBy != string(jobs.EndedUser) {
		t.Fatalf("job.kill 应回 stopping + EndedBy=user: %+v", killRes.Job)
	}

	// producer 收尾 → job.settled 带 EndedBy
	j.Settle(jobs.StatusKilled, jobs.EndedUser, "已取消")
	ev = client.waitEvent(protocol.EventJobSettled)
	if ev == nil {
		t.Fatal("应收到 job.settled")
	}
	var settled protocol.JobInfo
	b, _ = json.Marshal(ev.Params)
	mustJSON(t, b, &settled)
	if settled.ID != j.ID() || settled.Status != string(jobs.StatusKilled) || settled.EndedBy != string(jobs.EndedUser) {
		t.Fatalf("job.settled 载荷不符: %+v", settled)
	}

	// 未知任务：自解释错误
	resp = client.call(protocol.MethodJobKill, protocol.JobKillParams{ID: "job-nope"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("未知任务应报错: %+v", resp)
	}
	resp = client.call(protocol.MethodJobLog, protocol.JobLogParams{ID: "job-nope"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("未知任务读日志应报错: %+v", resp)
	}
	// 缺 id
	resp = client.call(protocol.MethodJobKill, protocol.JobKillParams{})
	if resp == nil || resp.Error == nil {
		t.Fatalf("缺 id 应报参数错误: %+v", resp)
	}
	// job.* 域不吞别的域（§5 坑 14）：model.list 仍可用
	if resp = client.call(protocol.MethodModelList, nil); resp == nil || resp.Error != nil {
		t.Fatalf("job.* 域不该吞掉 model.list: %+v", resp)
	}
}

// TestJobMethodsNotAttached：未装配任务管理器时 job.* 回可读错误
// （而不是 panic 或「未知方法」）。
func TestJobMethodsNotAttached(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	resp := client.call(protocol.MethodJobList, protocol.JobListParams{})
	if resp == nil || resp.Error == nil || !strings.Contains(resp.Error.Message, "未装配") {
		t.Fatalf("未装配应回可读错误: %+v", resp)
	}
}

// TestJobSettledNoticeWakesIdleSession：settle（self+completed）→ 空闲会话被唤醒，
// 通告带 protocol.JobNoticePrefix 进历史（前端据此渲染成区别于用户气泡的提示条）。
func TestJobSettledNoticeWakesIdleSession(t *testing.T) {
	rec := &serverRecorder{}
	srv, client, mgr := newJobTestServer(t, rec.stream)
	sessionID := newSessionViaChatSend(t, srv, client)
	before := rec.count()

	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "go test ./...", SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Write([]byte("ok\n")); err != nil {
		t.Fatal(err)
	}
	j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")

	waitCalls(t, srv, rec, before+1)
	msgs := rec.lastMessages()
	if !hasNotice(msgs, "后台任务 go test ./... 结束") {
		t.Fatalf("空闲会话应被唤醒且通告进历史: %+v", msgs)
	}
	if !hasNotice(msgs, protocol.JobNoticePrefix) {
		t.Fatalf("通告应带 %q 前缀（前端据此区别于用户气泡）: %+v", protocol.JobNoticePrefix, msgs)
	}
	if !hasNotice(msgs, "job_output") {
		t.Fatal("通告应告诉模型怎么读输出")
	}
}

// TestJobSettledNoticeSilentCases：agent 自己杀的 / 后端重启都不投递
// （契约 §5 的表：前者同一轮内已知，后者 agent 也不在了）。
func TestJobSettledNoticeSilentCases(t *testing.T) {
	rec := &serverRecorder{}
	srv, client, mgr := newJobTestServer(t, rec.stream)
	sessionID := newSessionViaChatSend(t, srv, client)

	for _, tc := range []struct {
		name   string
		status jobs.Status
		by     jobs.EndedBy
	}{
		{"agent 自己杀的", jobs.StatusKilled, jobs.EndedAgent},
		{"后端重启", jobs.StatusKilled, jobs.EndedBackend},
	} {
		before := rec.count()
		j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: tc.name, SessionID: sessionID})
		if err != nil {
			t.Fatal(err)
		}
		j.Settle(tc.status, tc.by, "x")
		time.Sleep(150 * time.Millisecond)
		if rec.count() != before {
			t.Fatalf("%s 不该唤醒（避免噪声/唤醒不存在的会话）: %d → %d", tc.name, before, rec.count())
		}
		if srv.Session().Busy() {
			t.Fatalf("%s 不该开新一轮", tc.name)
		}
	}
}

// TestJobNoticeBudgetExhaustedKeepsQueue：连续唤醒预算耗尽 → 不开新一轮，但通告
// **留在队列里**，用户下一次说话后由轮边界投递（契约 §5：绝不静默丢弃）。
func TestJobNoticeBudgetExhaustedKeepsQueue(t *testing.T) {
	rec := &serverRecorder{}
	srv, client, mgr := newJobTestServer(t, rec.stream)
	sessionID := newSessionViaChatSend(t, srv, client)

	// 先用满预算（每次都要等会话空闲，否则走的是「忙时注入」不耗预算）
	for i := 0; i < maxConsecutiveWakes; i++ {
		before := rec.count()
		j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "唤醒", SessionID: sessionID})
		if err != nil {
			t.Fatal(err)
		}
		j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
		waitCalls(t, srv, rec, before+1)
	}

	// 第 4 次：预算耗尽 → 不开新一轮
	before := rec.count()
	last, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "超预算", SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	last.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	time.Sleep(200 * time.Millisecond)
	if rec.count() != before {
		t.Fatalf("预算耗尽不该开新一轮: %d → %d", before, rec.count())
	}

	// 用户说话：重置预算，并把留在队列里的通告投递出去
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "我回来了"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	waitCalls(t, srv, rec, before+1)
	msgs := rec.lastMessages()
	if !hasNotice(msgs, "超预算") {
		t.Fatalf("预算耗尽时通告必须留在队列里（用户下次说话后投递，绝不丢弃）: %+v", msgs)
	}
}

// TestJobKillResetsWakeBudget：用户点 job.kill 也重置唤醒预算（契约 §5：
// 用户交互本身就是交互，不该被自己的操作耗掉预算）。
func TestJobKillResetsWakeBudget(t *testing.T) {
	rec := &serverRecorder{}
	srv, client, mgr := newJobTestServer(t, rec.stream)
	sessionID := newSessionViaChatSend(t, srv, client)

	// 用满预算
	for i := 0; i < maxConsecutiveWakes; i++ {
		before := rec.count()
		j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "唤醒", SessionID: sessionID})
		if err != nil {
			t.Fatal(err)
		}
		j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
		waitCalls(t, srv, rec, before+1)
	}

	// 用户点「结束」→ 重置预算（这里用一个别的任务来验证下一次仍能唤醒）
	killed, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "用户停的", SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	if resp := client.call(protocol.MethodJobKill, protocol.JobKillParams{ID: killed.ID()}); resp == nil || resp.Error != nil {
		t.Fatalf("job.kill 失败: %+v", resp)
	}
	waitBusyClear(t, srv) // 用户停的会投递（EndedBy=user 走投递）

	before := rec.count()
	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "重置后", SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	j.Settle(jobs.StatusCompleted, jobs.EndedSelf, "退出码 0")
	waitCalls(t, srv, rec, before+1)
	if !hasNotice(rec.lastMessages(), "重置后") {
		t.Fatal("用户点 job.kill 之后预算应已重置，下一次仍能唤醒")
	}
}

// TestJobSettledUserNoticeWording：用户主动结束的措辞必须明说「不是失败、不要重启」
// （契约 §5：否则 agent 醒来看到「结束了」会自己脑补把它拉起来）。
func TestJobSettledUserNoticeWording(t *testing.T) {
	rec := &serverRecorder{}
	srv, client, mgr := newJobTestServer(t, rec.stream)
	sessionID := newSessionViaChatSend(t, srv, client)
	before := rec.count()

	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "dev server", SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	// 用户点「结束」→ Manager 记 EndedBy=user → producer 收尾
	if _, err := mgr.Kill(j.ID(), jobs.EndedUser); err != nil {
		t.Fatal(err)
	}
	j.Settle(jobs.StatusKilled, jobs.EndedUser, "已取消")

	waitCalls(t, srv, rec, before+1)
	msgs := rec.lastMessages()
	if !hasNotice(msgs, "用户主动结束了后台任务 dev server") {
		t.Fatalf("用户结束的措辞不符: %+v", msgs)
	}
	if !hasNotice(msgs, "不要重启") {
		t.Fatalf("必须明说不要重启（否则 agent 会自己把它拉起来）: %+v", msgs)
	}
}

// TestJobTimeoutNoticeWording：超时被杀（Detail=jobs.TimeoutDetail）的措辞。
func TestJobTimeoutNoticeWording(t *testing.T) {
	rec := &serverRecorder{}
	srv, client, mgr := newJobTestServer(t, rec.stream)
	sessionID := newSessionViaChatSend(t, srv, client)
	before := rec.count()

	j, err := mgr.Start(jobs.Spec{Kind: "bash", Label: "长任务", SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	j.Settle(jobs.StatusKilled, jobs.EndedSelf, jobs.TimeoutDetail)
	waitCalls(t, srv, rec, before+1)
	if !hasNotice(rec.lastMessages(), "超时被杀") {
		t.Fatalf("超时措辞不符: %+v", rec.lastMessages())
	}
}

// TestJobNoticeTextTable：措辞表的判定直接钉住（含"表里没有这一行 → 不投递"）。
func TestJobNoticeTextTable(t *testing.T) {
	cases := []struct {
		name string
		snap jobs.Snapshot
		want bool
		text string
	}{
		{"self+completed", jobs.Snapshot{Label: "构建", Status: jobs.StatusCompleted, EndedBy: jobs.EndedSelf, Detail: "退出码 0"}, true, "结束（退出码 0）"},
		{"self+failed", jobs.Snapshot{Label: "测试", Status: jobs.StatusFailed, EndedBy: jobs.EndedSelf, Detail: "退出码 1"}, true, "失败（退出码 1）"},
		{"self+timeout", jobs.Snapshot{Label: "长任务", Status: jobs.StatusKilled, EndedBy: jobs.EndedSelf, Detail: jobs.TimeoutDetail}, true, "超时被杀"},
		{"user", jobs.Snapshot{Label: "dev", Status: jobs.StatusKilled, EndedBy: jobs.EndedUser}, true, "不要重启"},
		{"agent", jobs.Snapshot{Label: "dev", Status: jobs.StatusKilled, EndedBy: jobs.EndedAgent}, false, ""},
		{"backend", jobs.Snapshot{Label: "dev", Status: jobs.StatusKilled, EndedBy: jobs.EndedBackend}, false, ""},
		{"表里没有的行（self+killed 非超时）", jobs.Snapshot{Label: "dev", Status: jobs.StatusKilled, EndedBy: jobs.EndedSelf, Detail: "已取消"}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, ok := jobNoticeText(tc.snap)
			if ok != tc.want {
				t.Fatalf("投递判定不符: got %v want %v", ok, tc.want)
			}
			if ok {
				if !strings.HasPrefix(text, protocol.JobNoticePrefix) {
					t.Fatalf("通告应带前缀: %q", text)
				}
				if !strings.Contains(text, tc.snap.Label) || !strings.Contains(text, tc.text) {
					t.Fatalf("措辞不符: %q（应含 %q）", text, tc.text)
				}
			}
		})
	}
}

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, b)
	}
}
