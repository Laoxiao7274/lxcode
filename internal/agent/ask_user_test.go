// ask_user（确认门的「提问」形态）的会话侧契约：
//   - 提问 = Kind=="ask" 的确认请求（同一张确认卡通道，与高危确认共用挂起槽位）；
//   - 用户文本回答 → 答案进工具结果；跳过 → 「用户没有回答」语义且配对不破；
//   - auto 审批档**不豁免提问**（提问必须等用户，不能替用户表态）；
//   - 子会话（RunAgentTask 路径）的提问经确认代理上抛父会话，答案转回子会话。
package agent

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// waitAskPending 等到一次 ask 形态的确认请求挂起并返回它。
func waitAskPending(t *testing.T, cap *toolEventCapture) *ConfirmRequest {
	t.Helper()
	waitFor(t, func() bool {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		for _, p := range cap.pending {
			if p.Request.Kind == ConfirmKindAsk {
				return true
			}
		}
		return false
	})
	cap.mu.Lock()
	defer cap.mu.Unlock()
	for _, p := range cap.pending {
		if p.Request.Kind == ConfirmKindAsk {
			return p.Request
		}
	}
	t.Fatal("未等到 ask 请求")
	return nil
}

// toolResultOf 取包含指定子串的工具结果内容（找不到返回空串）。
func toolResultOf(cap *toolEventCapture, substr string) string {
	cap.mu.Lock()
	defer cap.mu.Unlock()
	for _, r := range cap.results {
		if strings.Contains(r.Content, substr) {
			return r.Content
		}
	}
	return ""
}

// 用例 1：模型调 ask_user（带 options）→ 收到 Kind=="ask" 的确认请求 →
// Answer(id, "选 A") → 工具结果包含「选 A」。
func TestAskUserAnswerFlowsToToolResult(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("ask_user", `{"question":"保留哪一边的改动?","options":["选 A","选 B"]}`),
		textResult("按你的回答执行"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("遇到冲突抉择", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	req := waitAskPending(t, cap)
	if req.Name != "ask_user" {
		t.Fatalf("ask 请求应来自 ask_user 工具: %+v", req)
	}
	if len(req.Options) != 2 || req.Options[0] != "选 A" {
		t.Fatalf("预设选项应随请求透传: %+v", req.Options)
	}
	if err := s.Answer(req.ID, "选 A"); err != nil {
		t.Fatalf("Answer 应接受与挂起请求匹配的 id: %v", err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if got := toolResultOf(cap, "选 A"); !strings.Contains(got, "用户回答：选 A") {
		t.Fatalf("用户的回答应作为工具结果回填: %q", got)
	}
}

// 用例 2：用户「跳过」（二元拒绝路径）→ 工具结果为「用户没有回答」语义、
// 轮次正常收尾、工具调用与结果配对不破（严格端点不 400 的前提）。
func TestAskUserSkipKeepsPairing(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("ask_user", `{"question":"怎么办?"}`),
		textResult("好的，我自己决策"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("问我", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	req := waitAskPending(t, cap)
	// 跳过 = 前端的 tool.confirm{allow:false}（二元拒绝路径）
	if err := s.Confirm(req.ID, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if got := toolResultOf(cap, "用户没有回答"); got == "" {
		t.Fatalf("跳过应回填「用户没有回答」语义: %+v", cap.results)
	}
	// 配对不破：历史里 assistant 的每个 tool_call 都有配对的 tool 结果
	msgs := s.History().Messages
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			paired := false
			for _, tm := range msgs {
				if tm.Role == "tool" && tm.ToolCallID == tc.ID {
					paired = true
					break
				}
			}
			if !paired {
				t.Fatalf("tool_call %s（%s）没有配对的 tool 结果——严格端点会 400", tc.ID, tc.Function.Name)
			}
		}
	}
}

// 用例 4：auto 审批档下 ask 请求**不被自动放行**（保持挂起等用户）。
// SetApproval(auto) 放行的只是 Kind=="" 的高危确认；替用户回答提问等于冒充表态。
func TestAskNotReleasedByAutoApproval(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("ask_user", `{"question":"保哪边?"}`),
		textResult("收尾"),
	}}
	s.stream = fake.stream
	cap := &toolEventCapture{}
	s.emit = cap.handle

	if err := s.Send("问我", WithApproval("auto")); err != nil {
		t.Fatal(err)
	}
	req := waitAskPending(t, cap)
	// 挂起后再切一次 auto（真实路径：用户中途改档会放行挂起确认）——ask 不该被放行
	if got := s.SetApproval("auto"); got != "auto" {
		t.Fatalf("SetApproval 应返回规范化档位: %q", got)
	}
	// 给放行逻辑留出窗口：若被错误放行，工具结果早已回填、轮次早已结束
	time.Sleep(150 * time.Millisecond)
	if !s.Busy() {
		t.Fatal("auto 档不应自动回答提问（提问必须等用户）")
	}
	if got := toolResultOf(cap, "用户没有回答"); got != "" {
		t.Fatalf("auto 档不应把提问当跳过处理: %q", got)
	}
	// 用户回答后照常收尾
	if err := s.Answer(req.ID, "保留我的改动"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if got := toolResultOf(cap, "保留我的改动"); got == "" {
		t.Fatalf("回答后应正常回填: %+v", cap.results)
	}
}

// 用例 3：子会话（RunAgentTask 路径，即合并进程拉起 merger 的那条路）调 ask_user
// → 父会话收到带 DispatchID 的 ask 请求 → 父侧 Answer → 子会话拿到答案文本。
func TestRunAgentTaskAskProxiedToParent(t *testing.T) {
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	cap := &toolEventCapture{}
	s := New(reg, tools.New(), cap.handle)
	t.Cleanup(s.Close)
	// merger 语境（合并进程的内置 Agent，白名单含 ask_user）
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main": {Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true}},
		"merger": {Def: sessiondata.AgentDef{ID: "merger", Name: "合并 Agent", Enabled: true,
			Tools: []string{"ask_user"}, Approval: "auto"}},
	}})

	// 假流：子会话第一轮调 ask_user；第二轮（答案回来后）从**收到的消息**里
	// 断言答案确实进了子会话历史，然后给出结论。
	var seenAnswer string
	s.SetStream(func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		round := 0
		for _, msg := range msgs {
			if msg.Role == "tool" && strings.Contains(msg.Content, "用户回答：采用我的方案") {
				seenAnswer = msg.Content
			}
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				round++
			}
		}
		go func() {
			defer close(ch)
			if round == 0 {
				tc := llm.ToolCall{ID: "sub-ask-1"}
				tc.Function.Name = "ask_user"
				tc.Function.Arguments = `{"question":"两边都改了 main.go 的同一处，保留哪边?","options":["采用我的方案","采用对方方案"]}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
				return
			}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "按用户回答完成合并。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})

	done := make(chan error, 1)
	var conclusion string
	go func() {
		res, err := s.RunAgentTask(context.Background(), "merger", "合并任务", "", &bytes.Buffer{})
		conclusion = res
		done <- err
	}()
	req := waitAskPending(t, cap)
	if req.DispatchID == "" {
		t.Fatal("子会话的 ask 请求应带上 dispatch_id（前端归属）")
	}
	if err := s.Answer(req.ID, "采用我的方案"); err != nil {
		t.Fatalf("父侧 Answer 应能把答案转给子会话: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunAgentTask 失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunAgentTask 超时——答案没有转回子会话（它还挂在提问上）")
	}
	if seenAnswer == "" {
		t.Fatal("答案应进入子会话历史（tool 结果含「用户回答：采用我的方案」）")
	}
	if !strings.Contains(conclusion, "按用户回答完成合并") {
		t.Fatalf("子会话应拿到答案后给出结论: %q", conclusion)
	}
}
