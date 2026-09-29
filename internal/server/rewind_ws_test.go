// chat.rewind 的 WS 集成测试：撤回那条用户消息 → 服务端删掉它及其之后的历史、回
// {removed}、广播 chat.rewound（带重算后的 context）；实时 user 消息带 seq（前端拿它
// 当锚点）；非法/未知序号的处理。这是「用户点撤回」的服务端验收。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// rewindStream 假 LLM：每轮回一句助手消息并回报 prompt_tokens（占用测量因此有真实锚点）。
func rewindStream(promptTokens int) testStream {
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "收到"}, FinishReason: llm.FinishStop,
				PromptTokens: promptTokens,
			}}
		}()
		return ch, nil
	}
}

func TestChatRewindOverWS(t *testing.T) {
	_, client, _ := newTestServer(t, rewindStream(5000))

	var sessionID string
	var userSeqs []int64
	for i := 0; i < 2; i++ {
		resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: fmt.Sprintf("第%d轮", i+1)})
		if resp == nil || resp.Error != nil {
			t.Fatalf("chat.send 失败: %+v", resp)
		}
		var sent struct {
			SessionID string `json:"session_id"`
		}
		b, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(b, &sent)
		sessionID = sent.SessionID

		// 实时那条 user 消息必须带 seq——只让刷新后的历史有序号等于当场点撤回时没有锚点
		ev := client.waitEvent(protocol.EventUserMsg)
		if ev == nil {
			t.Fatal("应收到 chat.userMessage")
		}
		var up protocol.UserMessageParams
		pb, _ := json.Marshal(ev.Params)
		_ = json.Unmarshal(pb, &up)
		if up.Message.Seq == 0 {
			t.Fatalf("实时用户消息必须带 seq: %+v", up.Message)
		}
		userSeqs = append(userSeqs, up.Message.Seq)
		if done := client.waitEvent(protocol.EventDone); done == nil {
			t.Fatal("应收到 chat.done")
		}
	}

	// 非法序号：0 不是消息序号
	bad := client.call(protocol.MethodChatRewind, protocol.ChatRewindParams{SessionID: sessionID})
	if bad == nil || bad.Error == nil || bad.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("seq=0 应回 CodeInvalidParams: %+v", bad)
	}

	// 撤回第二条用户消息：它和它之后的助手回复一起消失
	resp := client.call(protocol.MethodChatRewind, protocol.ChatRewindParams{SessionID: sessionID, Seq: userSeqs[1]})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.rewind 失败: %+v", resp)
	}
	var res protocol.ChatRewindResult
	b, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(b, &res)
	if res.Removed != 2 {
		t.Fatalf("应删掉 user+assistant 两条，实际 %d", res.Removed)
	}

	// 广播 chat.rewound：所有客户端在同一时刻收到同一份事实（前端据此截断时间线）
	ev := client.waitEvent(protocol.EventRewound)
	if ev == nil {
		t.Fatal("应广播 chat.rewound")
	}
	var rw protocol.ChatRewoundParams
	pb, _ := json.Marshal(ev.Params)
	_ = json.Unmarshal(pb, &rw)
	if rw.SessionID != sessionID || rw.Seq != userSeqs[1] || rw.Removed != 2 {
		t.Fatalf("chat.rewound 载荷不符: %+v", rw)
	}
	// 重算后的占用：未知时整键缺席，已知时必须是重算过的真值（不是撤回前的 5000）
	if rw.Context == nil || rw.Context.Used <= 0 || rw.Context.Used >= 5000 {
		t.Fatalf("chat.rewound 应带重算后的 context: %+v", rw.Context)
	}

	// 会话元数据变化：撤回让消息数变小了，侧栏要跟着刷新（客户端收到即重拉列表）
	changedEv := client.waitEvent(protocol.EventSessionChanged)
	if changedEv == nil {
		t.Fatal("应广播 session.changed（侧栏消息数变了）")
	}
	var sc protocol.SessionChangedParams
	cb, _ := json.Marshal(changedEv.Params)
	_ = json.Unmarshal(cb, &sc)
	if sc.ID != sessionID || sc.Reason != "rewound" {
		t.Fatalf("session.changed 载荷不符: %+v", sc)
	}

	// 历史回放：只剩第一轮的两条，且每条都带 seq
	hist := readTestHistory(t, client, sessionID)
	if len(hist.Messages) != 2 {
		t.Fatalf("撤回后应只剩第一轮两条: %+v", hist.Messages)
	}
	for i, m := range hist.Messages {
		if m.Seq == 0 {
			t.Fatalf("第 %d 条历史消息缺 seq: %+v", i, m)
		}
	}

	// 幂等：同一条再撤一次回 removed=0，不报错
	again := client.call(protocol.MethodChatRewind, protocol.ChatRewindParams{SessionID: sessionID, Seq: userSeqs[1]})
	if again == nil || again.Error != nil {
		t.Fatalf("重复撤回不该报错: %+v", again)
	}
	var againRes protocol.ChatRewindResult
	b, _ = json.Marshal(again.Result)
	_ = json.Unmarshal(b, &againRes)
	if againRes.Removed != 0 {
		t.Fatalf("重复撤回应回 removed=0: %+v", againRes)
	}
}
