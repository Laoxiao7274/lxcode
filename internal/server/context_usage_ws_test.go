// 上下文占用的 wire 契约（chat.history / chat.done）——**live 与 replay 必须是同一份数字**，
// 而且后端重启之后仍然给得出来。
//
// 为什么要在 WS 这一层钉：会话页是**按 id 重建运行时**的（刷新/重启后 newRuntime →
// AttachTo），占用原先只在内存里，重启后旧会话的 context 整键缺席（用户实测
// 「查看会话，他的上下文信息怎么是空的」）。落库 + 回落估算两条路都要在这里可见：
// 前者给权威值，后者给老会话一个**标注了 estimated** 的估算。
package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// usageStream 按调用序号回报不同的真实用量（prompt_tokens）：主会话 1111、子会话 2222。
// 数字必须不同——相同的话"子会话写了主指示器"这个 bug 就测不出来。
func usageStream(prompts ...int) testStream {
	var mu sync.Mutex
	calls := 0
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		mu.Lock()
		i := calls
		calls++
		mu.Unlock()
		p := 0
		if i < len(prompts) {
			p = prompts[i]
		}
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "好"}
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message:     llm.Message{Role: "assistant", Content: "好"},
			UsageTokens: 5, PromptTokens: p, FinishReason: llm.FinishStop,
		}}
		close(ch)
		return ch, nil
	}
}

// newServerOnStore 在**同一个会话库**上再起一个服务端 = 模拟后端重启（新进程、新内存
// 运行时、同一份磁盘状态）。这是"重启后还看得见占用"唯一诚实的验证方式。
func newServerOnStore(t *testing.T, st *store.Store, reg *config.Registry, stream testStream) (*Server, *wsTestClient) {
	t.Helper()
	srv := NewServer(reg)
	t.Cleanup(srv.CloseSessions)
	if stream != nil {
		srv.SetStream(stream)
	}
	if err := srv.AttachSessionStore(st); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + protocol.Path
	client := dialTest(t, url)
	t.Cleanup(func() { client.conn.Close() })
	return srv, client
}

// sendAndWaitDone 发一条消息并等这一轮收尾，返回 chat.done 携带的上下文占用。
func sendAndWaitDone(t *testing.T, client *wsTestClient, sessionID, text string) protocol.ContextUsage {
	t.Helper()
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: sessionID, Text: text})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send(%s) 失败: %+v", sessionID, resp)
	}
	ev := waitSessionEvent(t, client, protocol.EventDone, sessionID)
	var p protocol.DoneParams
	decodeServerResult(t, ev.Params, &p)
	if p.Context == nil {
		t.Fatalf("主轮的 chat.done 应带 context: %+v", p)
	}
	return *p.Context
}

// TestContextUsageSurvivesRestartOverWS：跑一轮 → 同进程的 chat.history 与 chat.done 一致
// （live vs replay）→ 换一个服务端（同一份库 = 重启）→ chat.history 还是**同一份数字**。
func TestContextUsageSurvivesRestartOverWS(t *testing.T) {
	srv, client, reg := newTestServer(t, usageStream(5180))
	id := createTestSession(t, client, "")

	live := sendAndWaitDone(t, client, id, "你好")
	if live.Used != 5180 || live.Window != 8192 || live.Estimated {
		t.Fatalf("chat.done 应带 provider 回报的真实用量: %+v", live)
	}
	// 同进程回放：chat.history 读的是同一份内存测量
	if got := readTestHistory(t, client, id).Context; got == nil || *got != live {
		t.Fatalf("live 与 replay 必须是同一份数字: live=%+v replay=%+v", live, got)
	}

	// 重启：同一个库上再起一个服务端
	_, client2 := newServerOnStore(t, srv.st, reg, nil)
	got := readTestHistory(t, client2, id).Context
	if got == nil {
		t.Fatal("重启后 chat.history 的 context 不该缺席（用户报的就是这个空）")
	}
	if *got != live {
		t.Fatalf("重启后应是**同一份数字**（落库的权威值）: live=%+v replay=%+v", live, *got)
	}
}

// TestContextUsageFallbackOverWS：老会话（库里有历史、没有落库测量）→ chat.history 给出
// **估算值**，并且 estimated=true（前端据此显示「估」）。
func TestContextUsageFallbackOverWS(t *testing.T) {
	srv, client, reg := newTestServer(t, nil)
	id := createTestSession(t, client, "")
	// 模拟旧版本后端留下的会话：直接往库里塞历史，没有任何 context_usage
	for _, m := range []llm.Message{
		{Role: "user", Content: strings.Repeat("早先的问题", 40)},
		{Role: "assistant", Content: strings.Repeat("早先的回答", 40)},
	} {
		if _, err := srv.st.AppendMsg(id, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := srv.st.ContextUsageOf(id); err != nil || ok {
		t.Fatalf("前置条件：库里不该有占用测量（ok=%v err=%v）", ok, err)
	}

	// **重启后**打开这条老会话：进程里没有它的运行时，历史与占用都从库里来。
	//（session.new 建的那个运行时手里是空历史——拿它测等于什么都没测）
	_, client2 := newServerOnStore(t, srv.st, reg, nil)
	got := readTestHistory(t, client2, id).Context
	if got == nil {
		t.Fatal("老会话不该是空的——库里没有真实测量时按历史回落估算")
	}
	if got.Used <= 0 {
		t.Fatalf("回落估算的 used 必须大于 0: %+v", got)
	}
	if !got.Estimated {
		t.Fatalf("回落估算必须标注 estimated（前端据此显示「估」）: %+v", got)
	}
	if got.Window != 8192 {
		t.Fatalf("窗口应按当前模型解析: %+v", got)
	}
}

// TestContextUsageAbsentForEmptySession：两个来源都没有（空会话）→ 整键缺席（不编数）。
func TestContextUsageAbsentForEmptySession(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	id := createTestSession(t, client, "")
	resp := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: id})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.history 失败: %+v", resp)
	}
	b, _ := json.Marshal(resp.Result)
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if v, hit := raw["context"]; hit {
		t.Fatalf("空会话应整键缺席（不编一个 0）: %v (%s)", v, b)
	}
}

// TestChildSessionContextOverWS：子会话也是会话——chat.history{子会话 id} 给的是**它自己的**
// 占用，而且**不写主指示器**（AGENTS.md §2.2：只有主轮写主会话的值）。
func TestChildSessionContextOverWS(t *testing.T) {
	srv, client, reg := newTestServer(t, usageStream(1111, 2222))
	mainID := createTestSession(t, client, "")
	mainLive := sendAndWaitDone(t, client, mainID, "主会话的一轮")
	if mainLive.Used != 1111 {
		t.Fatalf("主会话应记录它自己的用量: %+v", mainLive)
	}

	// 子会话（派发开的独立会话：自己的行 + 自己的历史）
	childID, err := srv.st.CreateChild(mainID, "coder", "d1")
	if err != nil {
		t.Fatal(err)
	}
	childLive := sendAndWaitDone(t, client, childID, "子任务")
	if childLive.Used != 2222 {
		t.Fatalf("子会话应记录它自己的用量: %+v", childLive)
	}

	// 子会话的 chat.history 给的是它自己的占用
	childHist := readTestHistory(t, client, childID).Context
	if childHist == nil || childHist.Used != 2222 {
		t.Fatalf("chat.history{子会话} 应给它自己的占用: %+v", childHist)
	}
	// 主指示器没有被那一轮改动
	if got := readTestHistory(t, client, mainID).Context; got == nil || got.Used != 1111 {
		t.Fatalf("子会话的轮次不该写主指示器: %+v", got)
	}

	// 重启后两边各自恢复自己的值
	_, client2 := newServerOnStore(t, srv.st, reg, nil)
	if got := readTestHistory(t, client2, mainID).Context; got == nil || got.Used != 1111 {
		t.Fatalf("重启后主会话应是它自己的值: %+v", got)
	}
	if got := readTestHistory(t, client2, childID).Context; got == nil || got.Used != 2222 {
		t.Fatalf("重启后子会话应是它自己的值: %+v", got)
	}
}
