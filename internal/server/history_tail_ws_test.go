// chat.history 的 tail 参数（WS 集成）：tail 缺省全量、tail>0 只回尾部且
// 配平。协议侧 wire 兼容由 golden 钉住，这里钉服务端行为。
package server

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func tailWSHistory(t *testing.T, client *wsTestClient, params protocol.ChatHistoryParams) protocol.ChatHistoryResult {
	t.Helper()
	resp := client.call(protocol.MethodChatHistory, params)
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.history 失败: %+v", resp)
	}
	var r protocol.ChatHistoryResult
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("解析应答: %v", err)
	}
	return r
}

func TestChatHistoryTailOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	// 建会话并灌 6 条消息（含一组工具配对，落在会话中段）
	res := client.call(protocol.MethodSessionNew, nil)
	var created protocol.SessionResult
	b, _ := json.Marshal(res.Result)
	json.Unmarshal(b, &created)

	pair := []llm.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "read_file", Arguments: "{}"}}}},
		{Role: "tool", Content: "out", ToolCallID: "c1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	}
	sess, err := srv.session(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range pair {
		sess.AppendForTest(m)
	}

	// 缺省 = 全量（老客户端语义）
	full := tailWSHistory(t, client, protocol.ChatHistoryParams{SessionID: created.SessionID})
	if len(full.Messages) != 6 {
		t.Fatalf("缺省应全量 6 条，得到 %d", len(full.Messages))
	}
	// tail=2：从尾部裁剪；start=4 在平衡切点上，窗口 = [user q2, assistant a2]
	tail := tailWSHistory(t, client, protocol.ChatHistoryParams{SessionID: created.SessionID, Tail: 2})
	if len(tail.Messages) != 2 || tail.Messages[0].Content != "q2" {
		t.Fatalf("tail=2 应为尾部 2 条（q2 起），得到 %d 条", len(tail.Messages))
	}
	// tail=3：start=3 恰是平衡切点（tool 结果已回填），窗口 = 尾部 3 条，
	// 首条是 assistant（无悬空调用）
	t3 := tailWSHistory(t, client, protocol.ChatHistoryParams{SessionID: created.SessionID, Tail: 3})
	if len(t3.Messages) != 3 || t3.Messages[0].Role != "assistant" || len(t3.Messages[0].ToolCalls) != 0 {
		t.Fatalf("tail=3 应为 3 条且首条是无调用的 assistant，得到 %d 条", len(t3.Messages))
	}
	// 占用统计不随窗口缩水（传输裁剪不改计账；nil = 未知，两侧一致即可）
	if (tail.Context == nil) != (full.Context == nil) ||
		(tail.Context != nil && tail.Context.Used != full.Context.Used) {
		t.Fatalf("窗口占用不应随窗口变化: tail=%v full=%v", tail.Context, full.Context)
	}
}
