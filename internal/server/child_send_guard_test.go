// 子会话 send 安全拦截（2026-12 安卓续跑输入的前提）：
//
//   - 危险点：chat.send 对子会话 id 无拦截，而 s.session() 对 parent_id≠空 的 id
//     **现建运行时**——派发运行中时真运行时挂在父 children 登记里，新现建的那份
//     busy 恒 false → 同一 session id 并行开第二轮，两份运行时并发写库（静默
//     历史污染）。
//   - 修复：send 前查「活跃会话 children 登记里是否有该子会话」（与
//     cancelChildAnywhere 同一套查找）；命中 → CodeBusy 拒绝；未命中（空闲 /
//     已结束的子会话）放行，现建 runtime 正常续跑。
//
// 钉子两条：① 运行中 send 被拒且**没有**写出第二条用户消息；② 子会话结束后
// send 成功，历史按序追加（拦截不能把正常续跑也挡掉）。
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

// TestChildSendRejectedWhileDispatchRunning：派发运行中，对子会话 id 发
// chat.send 必须回 CodeBusy，且子会话历史里不能多出用户消息。
func TestChildSendRejectedWhileDispatchRunning(t *testing.T) {
	release := make(chan struct{})
	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			tc := llm.ToolCall{ID: "call-d1"}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = `{"agent":"coder","task":"跑测试（验收：通过）"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境：阻塞（模拟还在跑），放行后才收尾。
		go func() {
			defer close(ch)
			<-release
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "子会话完成。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	_ = srv

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "把测试跑了"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}

	var childID, parentID string
	for i := 0; i < 500 && childID == ""; i++ {
		ev := client.waitEventAny()
		if ev == nil {
			break
		}
		if ev.Method != protocol.EventDispatchStart {
			continue
		}
		b, _ := json.Marshal(ev.Params)
		var dp protocol.DispatchStartParams
		json.Unmarshal(b, &dp)
		childID, parentID = dp.SessionID, dp.OwnerSessionID
	}
	if childID == "" {
		t.Fatal("应收到 chat.dispatchStart（带子会话 id）")
	}

	// 子会话还在跑：send 必须被拦截（CodeBusy）
	blocked := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: childID, Text: "补充指令"})
	if blocked == nil || blocked.Error == nil {
		t.Fatalf("运行中的子会话 send 应被拒绝: %+v", blocked)
	}
	if blocked.Error.Code != protocol.CodeBusy {
		t.Fatalf("错误码应为 CodeBusy: %+v", blocked.Error)
	}
	if !strings.Contains(blocked.Error.Message, "该子会话正在派发中") {
		t.Fatalf("文案不符: %q", blocked.Error.Message)
	}

	// 放行子会话跑完，等派发结束
	close(release)
	waitSessionEvent(t, client, protocol.EventDispatchEnd, parentID)

	// 拦截期间被拒的 send 不应写进子会话历史
	h := readTestHistory(t, client, childID)
	for _, m := range h.Messages {
		if m.Role == "user" && m.Content == "补充指令" {
			t.Fatal("被拦截的 send 不应写入子会话历史")
		}
	}
}

// TestChildSendAfterDispatchSucceeds：子会话**空闲**时 chat.send 必须放行
// （现建 runtime 续跑），历史按序追加。
func TestChildSendAfterDispatchSucceeds(t *testing.T) {
	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			tc := llm.ToolCall{ID: "call-d1"}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = `{"agent":"coder","task":"跑测试（验收：通过）"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境：立即收尾。
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "子会话完成。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	_ = srv

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "把测试跑了"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}

	var childID string
	for i := 0; i < 500 && childID == ""; i++ {
		ev := client.waitEventAny()
		if ev == nil {
			break
		}
		if ev.Method != protocol.EventDispatchStart {
			continue
		}
		b, _ := json.Marshal(ev.Params)
		var dp protocol.DispatchStartParams
		json.Unmarshal(b, &dp)
		childID = dp.SessionID
	}
	if childID == "" {
		t.Fatal("应收到 chat.dispatchStart（带子会话 id）")
	}
	// 等派发收尾（子会话空闲后才允许 send）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ev := client.waitEventAny()
		if ev == nil {
			t.Fatal("等待派发结束超时")
		}
		if ev.Method == protocol.EventDispatchEnd {
			var dp struct {
				SessionID string `json:"session_id"`
			}
			b, _ := json.Marshal(ev.Params)
			json.Unmarshal(b, &dp)
			if dp.SessionID == childID {
				break
			}
		}
	}

	// 空闲子会话：send 应放行（续跑成功）
	resume := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: childID, Text: "补充指令"})
	if resume == nil || resume.Error != nil {
		t.Fatalf("空闲子会话 send 应成功: %+v", resume)
	}
	if resume.Result == nil {
		t.Fatal("chat.send 应有结果")
	}
	var r map[string]any
	decodeServerResult(t, resume.Result, &r)
	if r["accepted"] != true {
		t.Fatalf("应返回 accepted=true: %v", r)
	}

	// 等子会话这一轮收尾（user 消息 → assistant 结论）
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ev := client.waitEventAny()
		if ev == nil {
			break
		}
		if ev.Method == protocol.EventDone {
			var dp struct {
				SessionID string `json:"session_id"`
			}
			b, _ := json.Marshal(ev.Params)
			json.Unmarshal(b, &dp)
			if dp.SessionID == childID {
				break
			}
		}
	}

	h := readTestHistory(t, client, childID)
	var sawUser, sawAssistant bool
	for _, m := range h.Messages {
		if m.Role == "user" && m.Content == "补充指令" {
			sawUser = true
		}
		if m.Role == "assistant" && m.Content == "子会话完成。" {
			sawAssistant = true
		}
	}
	if !sawUser || !sawAssistant {
		t.Fatalf("续跑后历史应含补充指令与新结论: %+v", h.Messages)
	}
}
