// chat.history 的 before_seq 分页 WS 集成测试：多轮会话按 anchor 分页拉更早
// 历史、has_more/first_seq 元数据、非分页应答不带这两个键、非法 anchor 报参数错。
// 这是「安卓向上翻页」的服务端验收（内核层的配平/检查点单测在 agent 包）。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// pageStream 假 LLM：每轮回一句助手消息。
func pageStream() testStream {
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "收到"}, FinishReason: llm.FinishStop,
			}}
		}()
		return ch, nil
	}
}

func TestChatHistoryPagingOverWS(t *testing.T) {
	_, client, _ := newTestServer(t, pageStream())

	// 跑 3 轮：user1 assistant1 user2 assistant2 user3 assistant3
	var sessionID string
	var userSeqs []int64
	for i := 0; i < 3; i++ {
		resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "问题"})
		if resp == nil || resp.Error != nil {
			t.Fatalf("chat.send 失败: %+v", resp)
		}
		var sent struct {
			SessionID string `json:"session_id"`
		}
		b, _ := json.Marshal(resp.Result)
		_ = json.Unmarshal(b, &sent)
		sessionID = sent.SessionID
		ev := client.waitEvent(protocol.EventUserMsg)
		if ev == nil {
			t.Fatal("应收到 chat.userMessage")
		}
		var up protocol.UserMessageParams
		pb, _ := json.Marshal(ev.Params)
		_ = json.Unmarshal(pb, &up)
		userSeqs = append(userSeqs, up.Message.Seq)
		if done := client.waitEvent(protocol.EventDone); done == nil {
			t.Fatal("应收到 chat.done")
		}
	}

	// 非分页应答：has_more/first_seq 整键缺席（omitempty——老客户端零影响的钉子）
	resp := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sessionID})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.history 失败: %+v", resp)
	}
	raw, _ := json.Marshal(resp.Result)
	if strings.Contains(string(raw), "has_more") {
		t.Fatalf("非分页应答不应带 has_more 键: %s", raw)
	}
	if strings.Contains(string(raw), "first_seq") {
		t.Fatalf("非分页应答不应带 first_seq 键: %s", raw)
	}

	// 非法 anchor：不存在的 seq → CodeInvalidParams
	bad := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{
		SessionID: sessionID, Tail: 2, BeforeSeq: 99999,
	})
	if bad == nil || bad.Error == nil || bad.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("未知 before_seq 应回 CodeInvalidParams: %+v", bad)
	}
	// before_seq 缺 tail（窗口大小）→ CodeInvalidParams
	bad = client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{
		SessionID: sessionID, BeforeSeq: userSeqs[2],
	})
	if bad == nil || bad.Error == nil || bad.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("before_seq 缺 tail 应回 CodeInvalidParams: %+v", bad)
	}

	// 以第 3 问为 anchor、窗口 2：窗口 = 之前 2 条（assistant2 user3 前面那两条
	// —— user3 是 anchor 不含；assistant2 / user3-1 即 user2 之前还有 assistant1）。
	// 严格讲：anchor=user3 的位置 4，start = 4-2 = 2（assistant2 前的切点平衡），
	// 窗口 = [user2, assistant2]。has_more = true（前面还有 user1/assistant1）。
	resp = client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{
		SessionID: sessionID, Tail: 2, BeforeSeq: userSeqs[2],
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("分页 chat.history 失败: %+v", resp)
	}
	var page protocol.ChatHistoryResult
	pb, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(pb, &page)
	if !page.HasMore {
		t.Fatal("窗口前面还有 2 条，has_more 应为 true")
	}
	if len(page.Messages) != 2 {
		t.Fatalf("窗口应 2 条，得到 %d: %+v", len(page.Messages), page.Messages)
	}
	if page.Messages[0].Role != "user" || page.Messages[0].Seq != userSeqs[1] {
		t.Fatalf("窗口首条应是 user2（seq=%d），得到 %+v", userSeqs[1], page.Messages[0])
	}
	if page.FirstSeq != page.Messages[0].Seq {
		t.Fatalf("first_seq 应等于窗口首条 seq: first=%d msg=%d", page.FirstSeq, page.Messages[0].Seq)
	}

	// 以 first_seq 为 anchor 再翻一页、窗口足够大：到头，has_more=false
	resp = client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{
		SessionID: sessionID, Tail: 50, BeforeSeq: page.FirstSeq,
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("第二页 chat.history 失败: %+v", resp)
	}
	var page2 protocol.ChatHistoryResult
	pb, _ = json.Marshal(resp.Result)
	_ = json.Unmarshal(pb, &page2)
	if page2.HasMore {
		t.Fatal("已到历史开头，has_more 应为 false")
	}
	if len(page2.Messages) != 2 {
		t.Fatalf("剩余窗口应 2 条（user1 assistant1），得到 %d", len(page2.Messages))
	}
	if page2.Messages[0].Seq != userSeqs[0] {
		t.Fatalf("窗口首条应是 user1（seq=%d），得到 %+v", userSeqs[0], page2.Messages[0])
	}
}
