// M3 agent.dispatch 的 WS 集成测试：主 Agent 直发消息 → 派发 →
// chat.dispatchStart/End + 子事件带 dispatch_id → 前端 store 的数据面。
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

func TestDispatchOverWS(t *testing.T) {
	var mainCalls, subCalls int
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		// 主 Agent 语境（种子 main 的协议文本）
		if strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			mainCalls++
			if mainCalls > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已验收并汇总。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-d1"}
			tc.Function.Name = "agent.dispatch"
			tc.Function.Arguments = `{"agent":"coder","task":"跑测试（验收：通过）"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子 Agent 语境（种子 coder 的执行协议）
		subCalls++
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "测试全部通过。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "测试全部通过。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})

	// 直发消息（不带 agent——M3 语义：默认主 Agent 调度）
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "把测试跑了"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}

	// 等事件：dispatchStart → 子 delta（dispatch_id）→ dispatchEnd → done
	var sawStart, sawSubDelta, sawEnd bool
	var startParams protocol.DispatchStartParams
	var endParams protocol.DispatchEndParams
	var subDeltaID string
	for i := 0; i < 500 && !(sawStart && sawSubDelta && sawEnd); i++ {
		ev := client.waitEventAny()
		if ev == nil {
			break
		}
		switch ev.Method {
		case protocol.EventDispatchStart:
			b, _ := json.Marshal(ev.Params)
			json.Unmarshal(b, &startParams)
			sawStart = startParams.DispatchID == "call-d1" && startParams.AgentID == "coder" &&
				startParams.AgentName == "代码 Agent" && strings.Contains(startParams.Task, "跑测试")
		case protocol.EventDelta:
			b, _ := json.Marshal(ev.Params)
			var dp protocol.DeltaParams
			json.Unmarshal(b, &dp)
			if dp.DispatchID == "call-d1" {
				sawSubDelta = true
				subDeltaID = dp.DispatchID
			}
		case protocol.EventDispatchEnd:
			b, _ := json.Marshal(ev.Params)
			json.Unmarshal(b, &endParams)
			sawEnd = endParams.DispatchID == "call-d1" && !endParams.IsError && strings.Contains(endParams.Result, "测试全部通过")
		}
	}
	if !sawStart {
		t.Fatalf("应收到 chat.dispatchStart（coder）: %+v", startParams)
	}
	if !sawSubDelta || subDeltaID != "call-d1" {
		t.Fatal("子 delta 应带 dispatch_id=call-d1")
	}
	if !sawEnd {
		t.Fatalf("应收到 chat.dispatchEnd（带结果）: %+v", endParams)
	}
	_ = client.waitEvent(protocol.EventDone) // 主轮收尾（已验收汇总）
	if mainCalls != 2 || subCalls != 1 {
		t.Fatalf("调用数不符 main=%d sub=%d", mainCalls, subCalls)
	}
}
