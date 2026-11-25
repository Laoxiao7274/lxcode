// 助手域子会话三操作串域的**后端语义核对**（安卓 P0 修的另一半——后端方法的
// 子会话寻址路径已各就各位，这里用端到端钉住三条关键语义，防止将来回归）：
//
//   - assistant.send 对**空闲**的 run_task 子会话 id 必须放行并按子会话落库的
//     agent_id 续跑（不是助手默认——coder 子会话续跑轮被换成助手语境是串域实害）；
//   - assistant.send 对**派发中**的子会话 id 必须回 CodeBusy（busyChildOfAssistant
//     ——两份运行时并发写库的拦截面，child_send_guard_test.go 的助手域镜像）；
//   - assistant.cancel 对派发中的子会话 id 经 cancelChildOfAssistant 真停（子会话
//     运行时不在 assistantSessions，直接建运行时的 Cancel 是空操作）。
package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// runTaskHoldStream 造「助手派 run_task → 子会话阻塞在生成中 → 放行收尾」的假流。
func runTaskHoldStream(t *testing.T, projIDCh <-chan string, release chan struct{}, mu *sync.Mutex, sendRounds *int) testStream {
	t.Helper()
	return func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "个人智能体助手") {
			mu.Lock()
			*sendRounds++
			r := *sendRounds
			mu.Unlock()
			if r > 1 {
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
		// 子会话语境：阻塞；放行后收尾。
		go func() {
			defer close(ch)
			<-release
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "子会话完成。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// waitAssistantDispatchStart 等第一条 assistant.dispatchStart，回子会话 id。
func waitAssistantDispatchStart(t *testing.T, client *wsTestClient) string {
	t.Helper()
	for i := 0; i < 500; i++ {
		ev := client.waitEventAny()
		if ev == nil {
			t.Fatal("等待 assistant.dispatchStart 超时")
		}
		if ev.Method != protocol.EventAssistantDispatchStart {
			continue
		}
		b, _ := json.Marshal(ev.Params)
		var dp protocol.DispatchStartParams
		json.Unmarshal(b, &dp)
		return dp.SessionID
	}
	t.Fatal("未收到 assistant.dispatchStart")
	return ""
}

// TestAssistantChildSendBusyWhileDispatchRunning：派发运行中，对子会话 id 发
// assistant.send 必须回 CodeBusy 且不写历史（child_send_guard 的助手域镜像）。
func TestAssistantChildSendBusyWhileDispatchRunning(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var sendRounds int
	projIDCh := make(chan string, 1)
	srv, client, _ := newTestServer(t, runTaskHoldStream(t, projIDCh, release, &mu, &sendRounds))
	proj, err := srv.st.AddProject("demo-proj", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projIDCh <- proj.ID

	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	if resp := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{SessionID: created.SessionID, Text: "跑测试"}); resp.Error != nil {
		t.Fatalf("assistant.send: %v", resp.Error)
	}
	childID := waitAssistantDispatchStart(t, client)

	// 派发中：busyChildOfAssistant 必须拦（CodeBusy）
	blocked := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{SessionID: childID, Text: "补充指令"})
	if blocked == nil || blocked.Error == nil {
		t.Fatalf("派发中的子会话 assistant.send 应被拒绝: %+v", blocked)
	}
	if blocked.Error.Code != protocol.CodeBusy {
		t.Fatalf("错误码应为 CodeBusy: %+v", blocked.Error)
	}
	if !strings.Contains(blocked.Error.Message, "正在派发中") {
		t.Fatalf("文案不符: %q", blocked.Error.Message)
	}

	// 放行 → 整轮收尾
	close(release)
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}
	// 被拦截的 send 不得写入子会话历史
	hresp := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: childID})
	if hresp.Error != nil {
		t.Fatalf("assistant.history: %v", hresp.Error)
	}
	var hist protocol.ChatHistoryResult
	if err := unmarshalResult(hresp, &hist); err != nil {
		t.Fatal(err)
	}
	for _, m := range hist.Messages {
		if m.Role == "user" && m.Content == "补充指令" {
			t.Fatal("被拦截的 assistant.send 不应写入子会话历史")
		}
	}
}

// TestAssistantChildSendResumesWithOwnAgent：空闲子会话的 assistant.send 放行续跑，
// 且续跑轮按子会话落库的 agent_id 解析（不是助手默认——coder 子会话不能被换成
// 助手语境）。验收面：续跑轮的消息真的进了子会话历史（Send 打的是子会话运行时）。
func TestAssistantChildSendResumesWithOwnAgent(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var sendRounds int
	projIDCh := make(chan string, 1)
	// 续跑语境判定：助手协议文本不在 → 走「子会话」分支。第一轮是派发里的
	// 子会话（阻塞），续跑轮只给结论——历史里「续跑结论」出现即证明 send 打对了
	// 会话与 Agent（助手语境的轮写进助手会话，不会出现在子会话历史里）。
	var subRound int
	srv, client, _ := newTestServer(t, func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "个人智能体助手") {
			mu.Lock()
			sendRounds++
			r := sendRounds
			mu.Unlock()
			if r > 1 {
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
		subRound++
		if subRound == 1 {
			// 第一轮（派发中的子会话）：阻塞
			go func() {
				defer close(ch)
				<-release
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "子会话完成。"}, FinishReason: llm.FinishStop}}
			}()
			return ch, nil
		}
		// 续跑轮：直接给结论（这正是 coder 语境的白名单行为；若被换成助手语境，
		// run_task 默认 Agent 语义会让这轮照样跑——所以结论文案区分开）
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "续跑完成。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	proj, err := srv.st.AddProject("demo-proj", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projIDCh <- proj.ID

	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	if resp := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{SessionID: created.SessionID, Text: "跑测试"}); resp.Error != nil {
		t.Fatalf("assistant.send: %v", resp.Error)
	}
	childID := waitAssistantDispatchStart(t, client)
	close(release)
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}

	// 空闲子会话：assistant.send 放行续跑
	resume := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{SessionID: childID, Text: "补充指令"})
	if resume == nil || resume.Error != nil {
		t.Fatalf("空闲子会话 assistant.send 应放行: %+v", resume)
	}
	// 等子会话续跑轮收尾。注意事件域名：子会话运行时不在 assistantSessions
	//（生命周期跟派发走），isAssistantSession 判否 → 内核事件走 chat.* 前缀
	//（主域同款行为——安卓 childRouteEvent 按 session_id 归属，域名无关）。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ev := client.waitEventAny()
		if ev == nil {
			t.Fatal("等待子会话续跑收尾超时")
		}
		if ev.Method != protocol.EventDone {
			continue
		}
		b, _ := json.Marshal(ev.Params)
		var dp struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(b, &dp)
		if dp.SessionID == childID {
			break
		}
	}
	// 子会话历史应含「补充指令」user 消息与续跑结论
	hresp := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: childID})
	if hresp.Error != nil {
		t.Fatalf("assistant.history: %v", hresp.Error)
	}
	var hist protocol.ChatHistoryResult
	if err := unmarshalResult(hresp, &hist); err != nil {
		t.Fatal(err)
	}
	sawUser, sawAssistant := false, false
	for _, m := range hist.Messages {
		if m.Role == "user" && m.Content == "补充指令" {
			sawUser = true
		}
		if m.Role == "assistant" && m.Content == "续跑完成。" {
			sawAssistant = true
		}
	}
	if !sawUser || !sawAssistant {
		t.Fatalf("续跑轮应按子会话自身的 agent_id 打进子会话历史: user=%v assistant=%v", sawUser, sawAssistant)
	}
}

// TestAssistantCancelStopsRunningChild：派发运行中 assistant.cancel 带子会话 id
// 必须真停（cancelChildOfAssistant 按 children 登记寻址——直接建运行时的 Cancel
// 是空操作）。验收面：取消后子会话轮以「已取消」收尾，助手整轮空闲。
func TestAssistantCancelStopsRunningChild(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var sendRounds int
	projIDCh := make(chan string, 1)
	srv, client, _ := newTestServer(t, runTaskHoldStream(t, projIDCh, release, &mu, &sendRounds))
	proj, err := srv.st.AddProject("demo-proj", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projIDCh <- proj.ID

	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	if resp := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{SessionID: created.SessionID, Text: "跑测试"}); resp.Error != nil {
		t.Fatalf("assistant.send: %v", resp.Error)
	}
	childID := waitAssistantDispatchStart(t, client)

	// assistant.cancel 带子会话 id：真停（不报错即可——流被卡住时取消是唯一出路）
	if resp := client.call(protocol.MethodAssistantCancel, protocol.ChatSessionParams{SessionID: childID}); resp == nil || resp.Error != nil {
		t.Fatalf("assistant.cancel(child) 应成功: %+v", resp)
	}
	// 助手整轮（含子会话收尾）应空闲；流 goroutine 卡在 release 上——取消路径
	//（SendWait 的 ctx.Done → child.Cancel → 轮收尾）不等流的下一个事件，但
	// goroutine 泄漏要收掉（测试进程退出前无害，仍显式 close）。
	close(release)
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatalf("取消后助手会话应收尾空闲: %v", err)
	}
}
