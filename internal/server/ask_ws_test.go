// ask_user 的 WS 集成测试（确认门「提问」形态的协议面）：
//   - chat.confirmRequest 上 wire 带 kind="ask" 与 options（协议序列化）；
//   - tool.confirm 带 answer 能路由到会话的 Answer 路径（答案回填工具结果）。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func TestAskUserOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		round := 0
		for _, msg := range msgs {
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				round++
			}
		}
		go func() {
			defer close(ch)
			if round == 0 {
				tc := llm.ToolCall{ID: "call-ask-1"}
				tc.Function.Name = "ask_user"
				tc.Function.Arguments = `{"question":"保留哪一边?","options":["选 A","选 B"]}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
				return
			}
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "按回答执行。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "按回答执行。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})

	// 造一个白名单只含 ask_user 的 Agent（种子 Agent 的白名单不含它）——
	// 白名单防御层会拒绝清单外的调用，聚焦协议面就不能借道别的工具。
	if err := srv.st.AddAgent(sessiondata.AgentDef{
		ID: "asker", Name: "提问 Agent", Enabled: true, Tools: []string{"ask_user"},
	}); err != nil {
		t.Fatalf("造测试 Agent 失败: %v", err)
	}
	// ask_user 低危自动执行：带 agent 参数指定上面的名单 id。
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "问我一个冲突抉择", Agent: "asker"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}

	// 等确认请求，**校验 wire 原始 JSON** 带 kind/options（序列化面）
	var confirm protocol.ConfirmRequest
	var sawAsk bool
	for i := 0; i < 500 && !sawAsk; i++ {
		ev := client.waitEventAny()
		if ev == nil {
			break
		}
		if ev.Method != protocol.EventConfirm {
			continue
		}
		b, _ := json.Marshal(ev.Params)
		var raw map[string]any
		_ = json.Unmarshal(b, &raw)
		if raw["kind"] != "ask" {
			continue
		}
		sawAsk = true
		if err := json.Unmarshal(b, &confirm); err != nil {
			t.Fatalf("confirmRequest 解析失败: %v", err)
		}
		opts, _ := raw["options"].([]any)
		if len(opts) != 2 || opts[0] != "选 A" {
			t.Fatalf("ask 请求上 wire 应带 options: %v", raw)
		}
	}
	if !sawAsk {
		t.Fatal("应收到 kind=ask 的确认请求")
	}
	if confirm.Name != "ask_user" {
		t.Fatalf("ask 请求应来自 ask_user: %+v", confirm)
	}

	// 用户在提问卡里作答：tool.confirm 带 answer → 路由到 Answer
	resp = client.call(protocol.MethodToolConfirm, protocol.ToolConfirmParams{ID: confirm.ID, Answer: "选 A"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("tool.confirm{answer} 失败: %+v", resp)
	}
	// 工具结果应包含用户回答，随后轮次收尾
	var sawResult, sawDone bool
	for i := 0; i < 500 && !(sawResult && sawDone); i++ {
		ev := client.waitEventAny()
		if ev == nil {
			break
		}
		switch ev.Method {
		case protocol.EventToolRslt:
			b, _ := json.Marshal(ev.Params)
			var tp protocol.ToolResultParams
			_ = json.Unmarshal(b, &tp)
			if strings.Contains(tp.Content, "用户回答：选 A") {
				sawResult = true
			}
		case protocol.EventDone:
			sawDone = true
		}
	}
	if !sawResult || !sawDone {
		t.Fatalf("回答应回填工具结果且轮次收尾: result=%v done=%v", sawResult, sawDone)
	}
}
