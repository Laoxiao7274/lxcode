// chat.approval 的 WS 集成测试：中途改权限档 → 挂起的确认被放行、后续调用按新档、
// 多客户端同步广播。这是「执行过程中把权限改成完全放开，正在跑的那一轮还是弹确认」
// 那条用户反馈的服务端验收。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func TestChatApprovalOverWS(t *testing.T) {
	var calls int
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		calls++
		go func() {
			defer close(ch)
			if calls > 1 {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "改档后收尾。"}, FinishReason: llm.FinishStop}}
				return
			}
			tc := llm.ToolCall{ID: "call-approval-1"}
			tc.Function.Name = "bash"
			tc.Function.Arguments = `{"command":"echo live-approval-marker"}`
			ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
		}()
		return ch, nil
	})

	// 直接给 coder（种子默认 confirm、白名单含 bash）发消息
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "跑条命令", Agent: "coder"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	var sent struct {
		SessionID string `json:"session_id"`
	}
	b, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(b, &sent)
	if sent.SessionID == "" {
		t.Fatalf("chat.send 应回 session_id: %s", b)
	}

	// 确认门挂起（coder 默认 confirm + bash 高危）
	if ev := client.waitEvent(protocol.EventConfirm); ev == nil {
		t.Fatal("应收到 chat.confirmRequest")
	}

	// 非法档位：复用 ValidateApproval，回 CodeInvalidParams
	bad := client.call(protocol.MethodChatApproval, protocol.ChatApprovalParams{
		SessionID: sent.SessionID, Approval: "yolo"})
	if bad == nil || bad.Error == nil || bad.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("非法 approval 应回 CodeInvalidParams: %+v", bad)
	}

	// 中途改档：挂起的确认必须被直接放行
	resp = client.call(protocol.MethodChatApproval, protocol.ChatApprovalParams{
		SessionID: sent.SessionID, Approval: protocol.ApprovalAuto})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.approval 失败: %+v", resp)
	}
	var got protocol.ChatApprovalResult
	b, _ = json.Marshal(resp.Result)
	_ = json.Unmarshal(b, &got)
	if got.Approval != protocol.ApprovalAuto {
		t.Fatalf("chat.approval 应回规范化后的档位: %+v", got)
	}

	// 广播（含发请求的这个客户端）+ 命令真的执行了 + 这一轮正常收尾。
	// 用一个循环收事件而不是逐个 waitEvent：广播与工具执行是并发的，先到先得，
	// waitEvent 会把不匹配的事件丢掉。
	var sawChanged, sawMarker, sawDone bool
	var changed protocol.ApprovalChangedParams
	deadline := time.After(10 * time.Second)
	for !sawDone {
		select {
		case ev, ok := <-client.events:
			if !ok {
				t.Fatal("事件通道被关闭")
			}
			pb, _ := json.Marshal(ev.Params)
			switch ev.Method {
			case protocol.EventApproval:
				_ = json.Unmarshal(pb, &changed)
				sawChanged = true
			case protocol.EventToolRslt:
				var tr protocol.ToolResultParams
				_ = json.Unmarshal(pb, &tr)
				if strings.Contains(tr.Content, "live-approval-marker") {
					sawMarker = true
				}
			case protocol.EventDone:
				sawDone = true
			}
		case <-deadline:
			t.Fatalf("超时：changed=%v marker=%v", sawChanged, sawMarker)
		}
	}
	if !sawChanged {
		t.Fatal("应广播 chat.approvalChanged（多客户端同步）")
	}
	if changed.SessionID != sent.SessionID || changed.Approval != protocol.ApprovalAuto {
		t.Fatalf("chat.approvalChanged 载荷不符: %+v", changed)
	}
	if !sawMarker {
		t.Fatal("改档后挂起的确认应被放行，命令应真的执行")
	}
}
