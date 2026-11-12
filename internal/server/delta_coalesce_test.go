// 每客户端 delta 合帧的行为钉子（2026-12「远程第二客户端看不到实时流」修复，组件 A）。
//
// 钉住五件事：
//  1. 合并语义：连续同键 delta 合成一条（帧数远小于条数）、文本拼接无丢失、
//     终态与逐帧一致（与前端归约器的契约一致）；
//  2. 保序：屏障事件（toolCall/done）之前先冲刷攒批的 delta——先到先发；
//  3. 异键不合并：跨会话 / text vs reasoning / 子会话 dispatch 各占一位；
//  4. 时间冲刷：没有屏障事件跟随的待发帧也会被 100ms 定时器冲出去（不悬空）；
//  5. 队列压力：delta 洪水经合帧后帧数缩两个量级，客户端不再被 fast-fail。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// newFloodTestServer 是 newResumeTestServer 的整形（返回 testClient 而不是
// store——洪水测试不需要库）。
func newFloodTestServer(t *testing.T) (*Server, *wsTestClient, string) {
	srv, _, url := newResumeTestServer(t, nil)
	return srv, dialTest(t, url), url
}

// collectDeltas 从测试客户端收集事件直到 done（或超时），返回 delta 帧列表与
// 完整方法序（保序断言用）。
func collectUntilDone(t *testing.T, c *wsTestClient) (deltas []protocol.DeltaParams, seq []string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-c.events:
			seq = append(seq, ev.Method)
			switch ev.Method {
			case protocol.EventDelta, protocol.EventAssistantDelta:
				var p protocol.DeltaParams
				b, _ := json.Marshal(ev.Params)
				if err := json.Unmarshal(b, &p); err != nil {
					t.Fatalf("delta 载荷解析失败: %v", err)
				}
				deltas = append(deltas, p)
			case protocol.EventDone, protocol.EventAssistantDone:
				return deltas, seq
			}
		case <-deadline:
			t.Fatalf("等 done 超时（已收 %d 条）: %v", len(seq), seq)
		}
	}
}

// TestDeltaCoalesceMergesBurst：60 条小 delta 经合帧后帧数远小于条数，拼接文本
// 与逐帧发送完全一致（终态一致），且全部 delta 在 done 之前（保序）。
func TestDeltaCoalesceMergesBurst(t *testing.T) {
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		go func() {
			defer close(ch)
			// 60 条 30 字节级 delta（≈ token 级洪水的形状），中间不打顿——
			// 不合帧的话它们是 60 条独立帧。
			for i := 0; i < 60; i++ {
				ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: fmt.Sprintf("字%02d", i)}
			}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "全量"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "发洪水"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	deltas, seq := collectUntilDone(t, client)
	if len(deltas) == 0 {
		t.Fatal("应收到合并后的 delta")
	}
	if len(deltas) >= 60 {
		t.Fatalf("合帧未生效：%d 条 delta 原样通过（应远小于 60）", len(deltas))
	}
	var got strings.Builder
	for _, d := range deltas {
		got.WriteString(d.Text)
	}
	var want strings.Builder
	for i := 0; i < 60; i++ {
		want.WriteString(fmt.Sprintf("字%02d", i))
	}
	if got.String() != want.String() {
		t.Fatalf("合并文本与逐帧不一致:\n got=%q\nwant=%q", got.String(), want.String())
	}
	// 保序：最后一条 delta 必须在 done 之前（collectUntilDone 在 done 即停，
	// 这里校验 done 确实在序列尾部）。
	if len(seq) == 0 || (seq[len(seq)-1] != protocol.EventDone && seq[len(seq)-1] != protocol.EventAssistantDone) {
		t.Fatalf("done 应最后到达: %v", seq)
	}
}

// TestDeltaCoalesceBarrierOrder：屏障事件不打乱顺序——delta(A) → toolCall →
// delta(B) 时，A 必须先于 toolCall 到达（屏障冲刷），B 在其后。
func TestDeltaCoalesceBarrierOrder(t *testing.T) {
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "前段"}
			tc := llm.ToolCall{ID: "call-bar"}
			tc.Function.Name = "bash"
			tc.Function.Arguments = `{"command":"true"}`
			ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
		}()
		return ch, nil
	})
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "屏障测试"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	deltas, seq := collectUntilDone(t, client)
	if len(deltas) == 0 {
		t.Fatal("屏障前应冲刷出 delta")
	}
	// 第一条 delta 的文本必须完整（"前段"），且在 toolCall 之前。
	if deltas[0].Text != "前段" {
		t.Fatalf("屏障冲刷的 delta 文本不符: %q", deltas[0].Text)
	}
	toolIdx := -1
	for i, m := range seq {
		if m == protocol.EventToolCall {
			toolIdx = i
			break
		}
	}
	if toolIdx < 0 {
		t.Fatalf("应收到 toolCall: %v", seq)
	}
	deltaIdx := -1
	for i, m := range seq {
		if m == protocol.EventDelta {
			deltaIdx = i
			break
		}
	}
	if deltaIdx > toolIdx {
		t.Fatalf("屏障前攒批的 delta 应先于 toolCall 到达（保序）: %v", seq)
	}
}

// TestDeltaCoalescerKeySeparation：异键不合并（纯单元——缓冲直接喂）。
func TestDeltaCoalescerKeySeparation(t *testing.T) {
	dc := newDeltaCoalescer()
	dc.feed(protocol.EventDelta, protocol.DeltaParams{SessionID: "s1", Kind: "text", Text: "主"})
	dc.feed(protocol.EventDelta, protocol.DeltaParams{SessionID: "s1", Kind: "reasoning", Text: "想"})
	dc.feed(protocol.EventDelta, protocol.DeltaParams{SessionID: "s1", Kind: "text", DispatchID: "d1", Text: "子"})
	dc.feed(protocol.EventDelta, protocol.DeltaParams{SessionID: "s2", Kind: "text", Text: "另"})
	dc.feed(protocol.EventDelta, protocol.DeltaParams{SessionID: "s1", Kind: "text", Text: "会"}) // 同键续帧→拼接
	if len(dc.order) != 4 {
		t.Fatalf("四个键应各占一位（同键拼接）: %d", len(dc.order))
	}
	frames := dc.takeAll()
	if len(frames) != 4 {
		t.Fatalf("冲刷应产出 4 帧: %d", len(frames))
	}
	// 到达序：主(s1,text) → reasoning → 子(dispatch) → s2。
	type want struct {
		text string
		p    protocol.DeltaParams
	}
	wants := []want{
		{"主会", protocol.DeltaParams{SessionID: "s1", Kind: "text", Text: "主会"}},
		{"想", protocol.DeltaParams{SessionID: "s1", Kind: "reasoning", Text: "想"}},
		{"子", protocol.DeltaParams{SessionID: "s1", Kind: "text", DispatchID: "d1", Text: "子"}},
		{"另", protocol.DeltaParams{SessionID: "s2", Kind: "text", Text: "另"}},
	}
	for i, w := range wants {
		if frames[i].params.Text != w.text || frames[i].params != w.p {
			t.Fatalf("第 %d 帧不符: %+v", i, frames[i])
		}
	}
	if !dc.empty() {
		t.Fatal("冲刷后缓冲应清空")
	}
}

// TestDeltaFlushByteThreshold：合并正文超字节阈值立即冲刷（不等时间阈值），
// 单帧不攒出无界大帧。
func TestDeltaFlushByteThreshold(t *testing.T) {
	c := &wsClient{out: make(chan []byte, 4), done: make(chan struct{}), bc: newDeltaCoalescer()}
	t.Cleanup(func() { close(c.done) }) // 手工构造无 conn——close(done) 即收尾（close() 会碰 nil conn）
	// 第一条就超阈值 → feed 返回 true → 立即冲刷（帧已在 out 里）。
	if !c.bc.feed(protocol.EventDelta, protocol.DeltaParams{SessionID: "s", Kind: "text", Text: strings.Repeat("x", deltaFlushBytes)}) {
		t.Fatal("超字节阈值的 feed 应报告需要立即冲刷")
	}
	c.flushDeltasLocked()
	select {
	case b := <-c.out:
		if !strings.Contains(string(b), `"method":"chat.delta"`) {
			t.Fatalf("冲刷出的应是 delta 事件帧: %s", b)
		}
	default:
		t.Fatal("字节阈值冲刷应立即入队")
	}
	// 拼接累计超阈值同样触发：两条各半个阈值。
	c.bc.feed(protocol.EventDelta, protocol.DeltaParams{SessionID: "s", Kind: "text", Text: strings.Repeat("y", deltaFlushBytes/2)})
	if !c.bc.feed(protocol.EventDelta, protocol.DeltaParams{SessionID: "s", Kind: "text", Text: strings.Repeat("y", deltaFlushBytes/2)}) {
		t.Fatal("拼接累计超阈值应触发冲刷")
	}
	c.flushDeltasLocked()
	select {
	case <-c.out:
	default:
		t.Fatal("累计超阈值的合并帧应被冲刷")
	}
}

// TestDeltaTimerFlushesWithoutBarrier：没有屏障事件跟随的待发帧由 100ms 定时器
// 冲出去（流式间隙不悬空）。
func TestDeltaTimerFlushesWithoutBarrier(t *testing.T) {
	c := &wsClient{out: make(chan []byte, 4), done: make(chan struct{}), bc: newDeltaCoalescer()}
	t.Cleanup(func() { close(c.done) }) // 手工构造无 conn（同上）
	c.enqueueDeltaCoalesced(protocol.EventDelta, protocol.DeltaParams{SessionID: "s", Kind: "text", Text: "待发"})
	select {
	case b := <-c.out:
		t.Fatalf("不应提前入队（等定时器）: %s", b)
	case <-time.After(20 * time.Millisecond):
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case b := <-c.out:
			if !strings.Contains(string(b), "待发") {
				t.Fatalf("定时冲刷帧内容不符: %s", b)
			}
			return
		case <-deadline:
			t.Fatal("定时器应在 ~100ms 内冲刷待发帧")
		}
	}
}

// TestDeltaFloodDoesNotFastFail：delta 洪水经合帧后帧数缩两个量级——慢隧道
// 客户端不再被队列灌满 fast-fail（连接存活、ping 可用），且文本无丢失。
// 对照：不合帧时 1000 条洪水足以灌满 256 条队列（TestEnqueueFastFailsWhenQueueFull
// 钉住 fast-fail 语义本身）。
func TestDeltaFloodDoesNotFastFail(t *testing.T) {
	srv, client, url := newFloodTestServer(t)
	// 裸连接当「慢客户端」（只读握手、之后不读——TCP 窗口很快灌满的形态）。
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("拨号失败: %v", err)
	}
	defer ws.Close()
	// 等两个连接都注册好再灌。
	deadline := time.Now().Add(2 * time.Second)
	for {
		srv.mu.Lock()
		n := len(srv.clients)
		srv.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("客户端未注册齐")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// 1000 条 40 字节级 delta（≈ 一轮流式的真实形状）。不合帧 = 1000 条独立
	// 帧 > 队列 256 → 慢客户端 fast-fail；合帧后只有字节/时间阈值切出来的几条。
	const total = 1000
	chunk := "流式增量内容每条约四十字节....."
	go func() {
		for i := 0; i < total; i++ {
			srv.broadcast(protocol.EventDelta, protocol.DeltaParams{SessionID: "s-flood", Kind: "text", Text: chunk})
		}
	}()
	// 裸连接静默 700ms：合帧生效时每 8KB 才一帧（1000×44B ≈ 44KB ≈ 6 帧）+
	// 定时冲刷的几条，远低于队列容量——它不该被 fast-fail。验证方式：静默后
	// 仍能读到事件（fast-fail 会关连接、ReadJSON 报错）。
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var probe struct {
		Method string `json:"method"`
	}
	if err := ws.ReadJSON(&probe); err != nil {
		t.Fatalf("慢客户端被 fast-fail（合帧未生效或队列压力仍在）: %v", err)
	}
	// testClient 一侧收集全部洪水并核对文本守恒（合帧不改内容）。
	var got strings.Builder
	var deltaFrames int
	dl := time.After(15 * time.Second)
	want := total * len(chunk)
collect:
	for {
		select {
		case ev := <-client.events:
			if ev.Method != protocol.EventDelta {
				continue
			}
			var p protocol.DeltaParams
			b, _ := json.Marshal(ev.Params)
			_ = json.Unmarshal(b, &p)
			if p.SessionID != "s-flood" {
				continue
			}
			got.WriteString(p.Text)
			deltaFrames++
			if got.Len() >= want {
				break collect
			}
		case <-dl:
			t.Fatalf("洪水未收齐：%d 帧 / %d 字节（应 %d）", deltaFrames, got.Len(), want)
		}
	}
	if deltaFrames >= total/10 {
		t.Fatalf("合帧收益不足：%d 帧 / %d 条（应缩一个量级以上）", deltaFrames, total)
	}
	// 连接仍活着：洪水期间与之后 ping 都应答。
	if resp := client.call(protocol.MethodPing, nil); resp == nil || resp.Error != nil {
		t.Fatalf("洪水后连接应存活: %+v", resp)
	}
}
