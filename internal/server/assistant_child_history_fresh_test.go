// 助手域子会话历史重开必须新鲜（2026-12 用户报：助手 run_task 子会话生成中
// 打开视图 → 重进只见一堆定格时的尾部 bash 块）——主域同款 bug 的助手域镜像
// （child_history_fresh_test.go 钉过主域，assistantSession 漏网）。
//
// 根因：assistantSession(childID) 为子会话 id 现建运行时后把它**缓存进
// assistantSessions**——而派发运行时不在 assistantSessions（生命周期跟派发走），
// 缓存里那份是 AttachToWindowed 时刻的**静态快照**：收不到事件、不再追加。
// 第一次 assistant.history 定格快照，之后轮次落库推进但缓存不动，重进命中
// 陈旧快照。
//
// 钉子：子会话生成中拉一次 assistant.history（定格快照）→ 放行子会话跑完 →
// 再拉一次——第二次必须含跑完后落库的结论，不能命中第一次的快照。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// callAssistantHistory 经 assistant.history 拉子会话历史（视图打开的协议路径）。
func callAssistantHistory(t *testing.T, client *wsTestClient, sessionID string) protocol.ChatHistoryResult {
	t.Helper()
	resp := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: sessionID})
	if resp == nil || resp.Error != nil {
		t.Fatalf("assistant.history(%s) failed: %+v", sessionID, resp)
	}
	var hist protocol.ChatHistoryResult
	if err := unmarshalResult(resp, &hist); err != nil {
		t.Fatal(err)
	}
	return hist
}

func TestAssistantChildHistoryFreshAcrossReopens(t *testing.T) {
	var mu sync.Mutex
	var sendRounds int
	release := make(chan struct{})
	projIDCh := make(chan string, 1)
	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "个人智能体助手") {
			mu.Lock()
			sendRounds++
			round := sendRounds
			mu.Unlock()
			if round > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已转述。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-rt1"}
			tc.Function.Name = "run_task"
			projID := <-projIDCh
			tc.Function.Arguments = `{"project_id":"` + projID + `","task":"跑测试"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境：阻塞在生成中（模拟「子 Agent 还在跑时用户打开了视图」），
		// 放行后才产出结论并落库。
		go func() {
			defer close(ch)
			<-release
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "测试全绿。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "测试全绿。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	proj, err := srv.st.AddProject("demo-proj", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projIDCh <- proj.ID

	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("assistant.new: %+v", resp)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID,
		Text:      "帮我把 demo-proj 的测试跑了",
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("assistant.send: %+v", resp)
	}

	// 等派发开始，记下子会话 id（视图打开的钥匙）
	var childID string
	for i := 0; i < 500 && childID == ""; i++ {
		ev := client.waitEventAny()
		if ev == nil {
			break
		}
		if ev.Method != protocol.EventAssistantDispatchStart {
			continue
		}
		b, _ := json.Marshal(ev.Params)
		var dp protocol.DispatchStartParams
		json.Unmarshal(b, &dp)
		childID = dp.SessionID
	}
	if childID == "" {
		t.Fatal("应收到 assistant.dispatchStart（带子会话 id）")
	}

	// 子会话还在跑：第一次打开视图（assistant.history）——旧实现在此定格快照并缓存
	h1 := callAssistantHistory(t, client, childID)
	for _, m := range h1.Messages {
		if m.Role == "assistant" && m.Content == "测试全绿。" {
			t.Fatalf("子会话尚未跑完，第一次历史不应已含结论：%+v", h1.Messages)
		}
	}

	// 放行子会话跑完，等助手整轮收尾
	close(release)
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}

	// 修复点断言：子会话运行时不得进 assistantSessions 缓存（生命周期跟派发走，
	// 缓存里的都是静态快照——主域 session(id) 同一条不变量）
	if sess := srv.peekAssistantSession(childID); sess != nil {
		t.Fatal("子会话运行时不应注册进 assistantSessions（静态快照污染源）")
	}

	// 重开视图：这次的历史必须含跑完后落库的结论——不能是第一次打开时的快照
	h2 := callAssistantHistory(t, client, childID)
	found := false
	for _, m := range h2.Messages {
		if m.Role == "assistant" && m.Content == "测试全绿。" {
			found = true
		}
	}
	if !found {
		t.Fatalf("重进助手子会话视图后历史不新鲜（缺跑完后落库的结论）：got %d 条消息 %+v",
			len(h2.Messages), h2.Messages)
	}
}
