// 并发恢复的回归钉子（性能审计 2026-12）：
//   - TestConcurrentResumeOfBigSessionsDoesNotSerialize：N 个大会话并发恢复 +
//     M 个会话正在流式生成时，恢复墙钟必须接近「单个恢复的耗时」而不是
//     「N 个恢复耗时之和」——曾经 s.session() 在全局写锁 sessionsMu 内跑
//     newRuntime（全量历史加载），一个会话的慢加载把所有其他会话的恢复串成一列，
//     前端 10s 超时（「恢复会话失败: 请求超时」）的根因。
//   - TestSlowRequestDoesNotHeadOfLineBlockPing：同一连接上一个慢请求（阻塞的
//     chat.compact 摘要调用）不得队头阻塞心跳 connection.ping——读循环曾同步
//     dispatch，心跳 3s 超时连续两次即误判断连，是恢复超时的放大器。
package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// compactionMarker 是摘要指令里的特征串（compaction_prompt.go 的
// compactionInstruction 开头）——测试的假 stream 用它识别「这次调用是压缩摘要」。
const compactionMarker = "上下文压缩引擎"

// newResumeTestServer 与 newTestServer 同构，但额外返回 ts.URL 与 store，
// 供本文件的并发场景自拨多条连接、直接灌大历史。
func newResumeTestServer(t *testing.T, stream testStream) (*Server, *store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "models.json")
	reg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		ContextWindow: 8192, MaxOutputTokens: 1024, Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(reg)
	t.Cleanup(srv.CloseSessions)
	if stream != nil {
		srv.SetStream(stream)
	}
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := srv.AttachSessionStore(st); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + protocol.Path
	return srv, st, url
}

// seedBigSession 直接往库里灌一条大会话（不经过运行时——运行时是恢复时才建的）。
// 每条消息 content 约 size 字节，msgCount 条。返回会话 id。
func seedBigSession(t *testing.T, st *store.Store, msgCount, size int) string {
	t.Helper()
	id, err := st.Create()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", size)
	for i := 0; i < msgCount; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := st.AppendMsg(id, llm.Message{Role: role, Content: body}); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// resumeOnce 走一遍完整的恢复往返（session.resume + chat.history），返回耗时。
func resumeOnce(t *testing.T, client *wsTestClient, id string) time.Duration {
	t.Helper()
	start := time.Now()
	if resp := client.call(protocol.MethodSessionResume, protocol.SessionResumeParams{ID: id}); resp == nil || resp.Error != nil {
		t.Fatalf("session.resume(%s) 失败: %+v", id, resp)
	}
	if resp := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: id}); resp == nil || resp.Error != nil {
		t.Fatalf("chat.history(%s) 失败: %+v", id, resp)
	}
	return time.Since(start)
}

// TestConcurrentResumeOfBigSessionsDoesNotSerialize 钉住：并发恢复的墙钟必须显著
// 小于串行恢复之和（≈ 单个恢复的耗时 + 并发开销），且每个恢复都在阈值内完成。
func TestConcurrentResumeOfBigSessionsDoesNotSerialize(t *testing.T) {
	release := make(chan struct{})
	stream := func(_ context.Context, _ config.ModelConfig, messages []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		// 只有携带真实用户文本的调用会阻塞（流式生成中）；压缩摘要/空调用直接完成。
		text := ""
		for _, m := range messages {
			if m.Role == "user" && !strings.Contains(m.Content, compactionMarker) {
				text = m.Content
			}
		}
		ch := make(chan llm.StreamEvent, 2)
		if text == "" {
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
			close(ch)
			return ch, nil
		}
		go func() {
			<-release
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "reply"}, FinishReason: llm.FinishStop}}
			close(ch)
		}()
		return ch, nil
	}
	srv, st, url := newResumeTestServer(t, stream)
	client := dialTest(t, url)

	// 构建并发度观测：进入 newRuntime 时 +1、返回时 -1，记最大值。
	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	runtimeBuildSpan = func() func() {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		return func() {
			mu.Lock()
			inFlight--
			mu.Unlock()
		}
	}
	t.Cleanup(func() { runtimeBuildSpan = nil })

	const n = 6
	const msgCount, msgSize = 120, 2048 // 每条约 240KB 历史——单条恢复毫秒~几十毫秒级

	// —— 基线：串行恢复 n 条全新会话，得到单份耗时与串行总和 ——
	seqIDs := make([]string, n)
	for i := range seqIDs {
		seqIDs[i] = seedBigSession(t, st, msgCount, msgSize)
	}
	var serialSum time.Duration
	var single time.Duration
	for i, id := range seqIDs {
		d := resumeOnce(t, client, id)
		serialSum += d
		if i == 0 {
			single = d
		}
	}
	t.Logf("串行基线：单条 %v，%d 条合计 %v", single, n, serialSum)

	// —— 并发相位：n 条全新会话并发恢复，同时 2 条会话在流式生成（阻塞在 stream 上）——
	blockIDs := make([]string, 2)
	for i := range blockIDs {
		blockIDs[i] = seedBigSession(t, st, 1, 16)
	}
	for _, id := range blockIDs {
		if resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: id, Text: "block-" + id}); resp == nil || resp.Error != nil {
			t.Fatalf("chat.send(%s) 失败: %+v", id, resp)
		}
	}
	for _, id := range blockIDs {
		waitSessionBusy(t, client, id, true)
	}

	concIDs := make([]string, n)
	for i := range concIDs {
		concIDs[i] = seedBigSession(t, st, msgCount, msgSize)
	}
	type result struct {
		i   int
		d   time.Duration
		err string
	}
	results := make(chan result, n)
	var wg sync.WaitGroup
	start := time.Now()
	for i, id := range concIDs {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			// 每条连接独立拨号：隔离「单连接队头阻塞」，本测试只钉全局锁串行。
			c := dialTest(t, url)
			defer c.conn.Close()
			d := resumeOnce(t, c, id)
			results <- result{i: i, d: d}
		}(i, id)
	}
	wg.Wait()
	wall := time.Since(start)
	close(results)
	var maxD time.Duration
	for r := range results {
		if r.err != "" {
			t.Fatalf("并发恢复 #%d 失败: %s", r.i, r.err)
		}
		if r.d > maxD {
			maxD = r.d
		}
	}
	t.Logf("并发相位：墙钟 %v，最慢单条 %v（串行和 %v）", wall, maxD, serialSum)

	// 停掉流式生成（释放 stream），等忙闲落定再收尾。两条轮并发结束，busy=false
	// 事件的广播序不确定——waitSessionBusy 会把另一条的事件消费掉，这里直接轮询
	// 运行时忙闲（事实源）。
	close(release)
	deadline := time.After(10 * time.Second)
	for _, id := range blockIDs {
		for {
			if sess := srv.peekSession(id); sess != nil && !sess.Busy() {
				break
			}
			select {
			case <-deadline:
				t.Fatalf("释放后 %s 仍未结束", id)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}

	// 断言一：每个恢复都在阈值内完成（前端超时 10s 的一半以下，留足抖动余量）。
	if maxD > 5*time.Second {
		t.Fatalf("最慢的并发恢复耗时 %v，超过 5s 阈值（前端 10s 超时会偶发命中）", maxD)
	}
	// 断言二：构建并发度 ≥2——恢复的慢段（newRuntime 全量历史加载）必须并行执行。
	// 墙钟对比在这台开发机上噪声过大（同负载单条恢复 8.8~24.5ms 波动），不适合当
	// 回归钉子；并发度是直接测量：全局锁串行化时它恒为 1（修复前的实测）。
	if maxInFlight < 2 {
		t.Fatalf("并发恢复期间运行时构建的最大并发度 = %d：慢段仍被全局串行点排队", maxInFlight)
	}
}

// callWithTimeout 发请求并在 timeout 内等应答（不阻塞测试主流程——超时返回 nil）。
func (c *wsTestClient) callWithTimeout(method string, params any, timeout time.Duration) *protocol.Response {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan *protocol.Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	idRaw, _ := json.Marshal(id)
	b, _ := json.Marshal(params)
	c.conn.WriteJSON(protocol.Request{JSONRPC: "2.0", ID: idRaw, Method: method, Params: b})
	select {
	case resp := <-ch:
		return resp
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil
	}
}

// TestSlowRequestDoesNotHeadOfLineBlockPing 钉住：同一连接上慢请求（阻塞的
// chat.compact 摘要调用）不得队头阻塞心跳 connection.ping。读循环曾同步 dispatch
// ——心跳跟着慢请求一起等，3s 超时连续两次即误判「后端断连」。
func TestSlowRequestDoesNotHeadOfLineBlockPing(t *testing.T) {
	inCompact := make(chan struct{})
	release := make(chan struct{})
	stream := func(_ context.Context, _ config.ModelConfig, messages []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		for _, m := range messages {
			if m.Role == "user" && strings.Contains(m.Content, compactionMarker) {
				// 压缩摘要调用：阻塞住，把 chat.compact 变成确定性的慢请求。
				close(inCompact)
				go func() {
					<-release
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "<compacted-summary>摘要</compacted-summary>"}}}
					close(ch)
				}()
				return ch, nil
			}
		}
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "ok"}}}
		close(ch)
		return ch, nil
	}
	_, st, url := newResumeTestServer(t, stream)
	id := seedBigSession(t, st, 60, 512)
	client := dialTest(t, url)

	compactDone := make(chan *protocol.Response, 1)
	go func() {
		compactDone <- client.call(protocol.MethodChatCompact, protocol.CompactParams{SessionID: id})
	}()
	select {
	case <-inCompact:
	case <-time.After(10 * time.Second):
		t.Fatal("压缩摘要调用没有进入假 stream")
	}
	// 慢请求还在跑：心跳必须在 2s 内得到应答（前端心跳超时 3s）。
	start := time.Now()
	pong := client.callWithTimeout(protocol.MethodPing, nil, 2*time.Second)
	if pong == nil {
		close(release)
		t.Fatalf("慢请求（chat.compact 摘要阻塞中）队头阻塞了心跳：connection.ping 在 2s 内无应答")
	}
	t.Logf("慢请求进行中心跳往返 %v", time.Since(start))
	close(release)
	select {
	case resp := <-compactDone:
		if resp == nil || resp.Error != nil {
			t.Fatalf("chat.compact 失败: %+v", resp)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("释放后 chat.compact 未返回")
	}
}
