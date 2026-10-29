// chat.history 的 running_dispatches（WS 集成）：**观察端回填**的 wire 契约——
//
// 场景（2026-12 安卓端报）：派发由电脑（桌面端）发起，安卓端只是观察者。安卓
// 重进会话走 chat.history 回放，回放重建的派发卡**没有子会话 id**（dispatchStart
// 早于本次连接、结果提示行要派发结束才落库）——点不开子会话、todo/子工具事件
// 按卡反查也无处可投（「任务进度消失」）。修法 = 应答带**此刻正在跑**的派发
// 快照，观察端按 dispatch_id 回填卡。
//
// 钉住四条：
//   ① 派发进行中：应答带 running_dispatches（dispatch_id/child_session_id/agent_name/task）；
//   ② 派发结束后：整键缺席（生命周期——跑完必须从观察面消失）；
//   ③ 子会话寻址：不导出（登记在父会话上，子会话自己没有）；
//   ④ 空闲会话：整键缺席（不是空数组——omitempty）。
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

// 派发流：主 Agent 派发一次；子会话慢 300ms 再给结论（留出「跑着的时候拉历史」
// 的观察窗口）。
func runningDispatchStream(t *testing.T, subDelay time.Duration) testStream {
	var mainCalls int
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			mainCalls++
			if mainCalls > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-rd"}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = `{"agent":"coder","task":"跑观察测试"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		go func() {
			defer close(ch)
			time.Sleep(subDelay)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "子结论。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "子结论。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// 派发进行中拉 chat.history：应答带运行中派发快照；跑完后再拉：整键缺席。
func TestHistoryRunningDispatchesOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, runningDispatchStream(t, 300*time.Millisecond))

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "把测试跑了"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	var accepted struct {
		SessionID string `json:"session_id"`
	}
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &accepted); err != nil || accepted.SessionID == "" {
		t.Fatalf("chat.send 应答应带 session_id: %s", b)
	}
	sid := accepted.SessionID
	// 等派发真的开起来（dispatchStart 广播到达 = 登记/开卡都已完成）
	start := client.waitEvent(protocol.EventDispatchStart)
	if start == nil {
		t.Fatal("应收到 chat.dispatchStart")
	}

	var mid protocol.ChatHistoryResult
	for i := 0; i < 100; i++ {
		r := tailWSHistory(t, client, protocol.ChatHistoryParams{SessionID: sid, Tail: 40})
		mid = r
		if len(r.RunningDispatches) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(mid.RunningDispatches) != 1 {
		t.Fatalf("派发进行中应带 running_dispatches: %+v", mid.RunningDispatches)
	}
	d := mid.RunningDispatches[0]
	if d.DispatchID != "call-rd" || d.AgentName != "代码 Agent" || !strings.Contains(d.Task, "观察测试") {
		t.Fatalf("观察快照字段不符: %+v", d)
	}
	if d.ChildSessionID == "" {
		t.Fatal("子会话 id 是回填卡的钥匙——不能为空")
	}

	// ③ 子会话寻址：不导出（登记在父会话上）
	child := tailWSHistory(t, client, protocol.ChatHistoryParams{SessionID: d.ChildSessionID, Tail: 40})
	if len(child.RunningDispatches) != 0 {
		t.Fatalf("子会话寻址不应导出 running_dispatches: %+v", child.RunningDispatches)
	}

	// ② 跑完：整键缺席（wire 上无此键——不是空数组）
	_ = client.waitEvent(protocol.EventDone)
	for i := 0; i < 100; i++ {
		r := tailWSHistory(t, client, protocol.ChatHistoryParams{SessionID: sid, Tail: 40})
		if len(r.RunningDispatches) == 0 {
			raw, _ := json.Marshal(r)
			if strings.Contains(string(raw), "running_dispatches") {
				t.Fatalf("跑完后 running_dispatches 必须整键缺席（不是空数组）: %s", raw)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("派发结束后 running_dispatches 仍存在（注销漏了——把已结束的报成运行中）")
	_ = srv
}
