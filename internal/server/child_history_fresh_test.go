// 子会话历史重开必须新鲜（2026-12 用户报）：关闭子会话临时视图 → 操作其他会话 →
// 再点派发卡回来，子会话的历史丢了（空白或只剩实时新事件）。
//
// 根因：s.session(childID) 为子会话 id 现建运行时后把它**缓存进 s.sessions**——
// 而子会话运行时的生命周期跟着派发走（openChildSession 每次都 New + AttachTo，
// 从不查 s.sessions），缓存里那份是 AttachTo 时刻的**静态快照**：收不到事件、
// 不再追加。第一次打开子会话视图（往往在子 Agent 还在跑时）把快照定格在那一刻，
// 之后每次 chat.history 都命中陈旧缓存——重开视图看到的就是第一次打开时的历史，
// 之后的轮次全部「丢」。
//
// 钉子：子会话还在跑时拉一次 chat.history（定格快照），放行子会话跑完后再拉
// 一次——第二次必须读到**完整**历史（含跑完后落库的结论），不能是第一次的快照。
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

func TestChildHistoryFreshAcrossReopens(t *testing.T) {
	var mu sync.Mutex
	var mainCalls int
	release := make(chan struct{})
	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			mu.Lock()
			mainCalls++
			first := mainCalls == 1
			mu.Unlock()
			if !first {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已汇总。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
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
		// 子会话语境：阻塞在确认门位置（模拟「子 Agent 还在跑时用户打开了视图」），
		// 放行后才产出结论并落库。
		go func() {
			defer close(ch)
			<-release
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "测试全部通过。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "测试全部通过。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	_ = srv

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "把测试跑了"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}

	// 等派发开始，记下子会话 id（视图打开的钥匙）与父会话 id
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

	// 子会话还在跑：第一次打开视图（chat.history）——旧实现在此定格快照并缓存
	_ = readTestHistory(t, client, childID)

	// 放行子会话跑完，等派发结束与主轮收尾
	close(release)
	waitSessionEvent(t, client, protocol.EventDispatchEnd, parentID)
	waitSessionEvent(t, client, protocol.EventDone, parentID)

	// 重开视图：这次的历史必须含跑完后落库的结论——不能是第一次打开时的快照
	h2 := readTestHistory(t, client, childID)
	found := false
	for _, m := range h2.Messages {
		if m.Role == "assistant" && m.Content == "测试全部通过。" {
			found = true
		}
	}
	if !found {
		t.Fatalf("重开子会话视图后历史不完整（缺结论）：got %d 条消息 %+v",
			len(h2.Messages), h2.Messages)
	}
}
