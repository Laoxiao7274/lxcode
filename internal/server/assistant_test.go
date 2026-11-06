// 助手域（assistant space）的 server 侧验收测试：
// 能力隔离（助手 Agent 的工具清单不含 edit/write_file/bash——验收②）、
// assistant.* 六方法的域寻址、事件域映射。
package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestAssistantAgentCapabilityIsolation（验收②）：助手 Agent 定义的工具清单
// 是只读面 + run_task——不含 edit / write_file / bash（能力隔离的硬边界）；
// run_task 在清单里（能力桥在场）。
func TestAssistantAgentCapabilityIsolation(t *testing.T) {
	denied := map[string]bool{"edit": true, "write_file": true, "bash": true, "agent_dispatch": true}
	seen := map[string]bool{}
	for _, name := range assistantAgentDef.Tools {
		seen[name] = true
		if denied[name] {
			t.Fatalf("助手白名单不应含 %s（改动必须经 run_task 派进项目）", name)
		}
	}
	for _, need := range []string{"run_task", "read_file", "search", "web_search", "web_fetch",
		"read_image", "bash_readonly", "todo", "ask_user", "read_skill", "project_list", "session_search"} {
		if !seen[need] {
			t.Fatalf("助手白名单缺 %s", need)
		}
	}
	// Space 域标记：协议推导（compose.go 的 switch）按它走助手内置协议
	if assistantAgentDef.Space != sessiondata.SpaceAssistant {
		t.Fatalf("助手 Agent 的 Space 应是 assistant: %q", assistantAgentDef.Space)
	}
	if assistantAgentDef.Approval != "auto" {
		t.Fatalf("助手默认审批档应是 auto: %q", assistantAgentDef.Approval)
	}
}

// projectListOutput 经工具注册表调 project_list（与模型走的同一条路径）。
func projectListOutput(srv *Server) string {
	return srv.treg.Execute(context.Background(), llm.ToolCall{ID: "call-pl", Function: struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: tools.ProjectListToolName, Arguments: `{}`}})
}

// TestProjectListMergesRunningSessions（感知用户当前活动之二）：project_list
// 的 server 装配回调把 busySessions() 合流进 ListProjectStats——运行中的会话
// 按归属聚合进项目的 Running 位，工具输出行尾出现「运行中」；未分组 busy
// 会话不挂到任何项目上。busy 用卡在 gate 里的假流制造。
func TestProjectListMergesRunningSessions(t *testing.T) {
	gate := make(chan struct{})
	stream := func(_ context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		go func() {
			<-gate
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "ok"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "ok"}, FinishReason: llm.FinishStop,
			}}
			close(ch)
		}()
		return ch, nil
	}
	srv, client, _ := newTestServer(t, stream)
	dir := initGitProject(t)
	meta := addGitProject(t, client, dir)

	// 项目会话跑起来（busy）
	created := createTestSession(t, client, meta.ID)
	sendResp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: created, Text: "跑起来"})
	if sendResp == nil || sendResp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", sendResp)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !srv.peekSession(created).Busy() {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.peekSession(created).Busy() {
		t.Fatal("项目会话应处于运行中（假流被 gate 卡住）")
	}

	// project_list 输出：该项目行尾有「运行中」
	out := projectListOutput(srv)
	if !strings.Contains(out, meta.Name) {
		t.Fatalf("输出应含项目名:\n%s", out)
	}
	if !strings.Contains(out, "，运行中\n") {
		t.Fatalf("运行中的项目行尾应有「运行中」:\n%s", out)
	}

	// 未分组 busy 会话：不挂到任何项目上（唯一一个「运行中」仍在项目行上）
	ungrouped := createTestSession(t, client, "")
	ugResp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: ungrouped, Text: "也跑起来"})
	if ugResp == nil || ugResp.Error != nil {
		t.Fatalf("未分组 chat.send 失败: %+v", ugResp)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !srv.peekSession(ungrouped).Busy() {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.peekSession(ungrouped).Busy() {
		t.Fatal("未分组会话应处于运行中")
	}
	out = projectListOutput(srv)
	if strings.Count(out, "运行中") != 1 {
		t.Fatalf("未分组 busy 会话不应计入任何项目（输出只应有一处「运行中」）:\n%s", out)
	}
	close(gate)
	waitSessionIdle(t, srv, created)
	waitSessionIdle(t, srv, ungrouped)
}

// TestAssistantCurrentActivityInjection（感知用户当前活动之三）：助手运行时
// 的 SetCurrentActivity 装配——助手真实跑一轮（经 assistant.send 假流），LLM
// 收到的 system prompt 里有「当前状态」段：最近会话（标题+项目名+时间）与此
// 刻运行中的会话；未分组会话项目名记「主域 · 未分组」（域前缀——呈现层归属
// 标注，防助手把主域会话说成「咱俩的对话」）。
func TestAssistantCurrentActivityInjection(t *testing.T) {
	var promptMu sync.Mutex
	var lastPrompt string
	stream := func(_ context.Context, cfg config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		if len(msgs) > 0 && msgs[0].Role == "system" {
			promptMu.Lock()
			lastPrompt = msgs[0].Content
			promptMu.Unlock()
		}
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "ok"}
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: "ok"}, FinishReason: llm.FinishStop,
		}}
		close(ch)
		return ch, nil
	}
	srv, client, _ := newTestServer(t, stream)
	dir := initGitProject(t)
	meta := addGitProject(t, client, dir)

	// 一条有标题、归属项目的历史会话
	sendTurn(t, srv, client, createTestSession(t, client, meta.ID), "做点事")

	// 未分组会话跑起来（busy）
	ungrouped := createTestSession(t, client, "")
	ugResp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: ungrouped, Text: "也跑起来"})
	if ugResp == nil || ugResp.Error != nil {
		t.Fatalf("未分组 chat.send 失败: %+v", ugResp)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !srv.peekSession(ungrouped).Busy() {
		time.Sleep(10 * time.Millisecond)
	}

	// 助手真实跑一轮 → prompt 里应注入「当前状态」段
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
	if r := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID, Text: "我在做什么？"}); r.Error != nil {
		t.Fatalf("assistant.send: %v", r.Error)
	}
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}
	var prompt string
	promptMu.Lock()
	prompt = lastPrompt
	promptMu.Unlock()
	if prompt == "" {
		t.Fatal("假流应捕获到 system prompt")
	}
	if !strings.Contains(prompt, agent.CurrentActivityHeader) {
		t.Fatalf("助手提示词应注入当前状态段:\n%s", prompt)
	}
	if !strings.Contains(prompt, "做点事") || !strings.Contains(prompt, meta.Name) {
		t.Fatalf("最近会话应含标题与项目名:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- 正在运行：") && !strings.Contains(prompt, "此刻没有正在运行的会话") {
		t.Fatalf("应有运行中条目或明确的空态:\n%s", prompt)
	}
	if strings.Contains(prompt, "- 正在运行：") && !strings.Contains(prompt, "主域 · 未分组") {
		t.Fatalf("运行中的未分组会话项目名应记「主域 · 未分组」:\n%s", prompt)
	}
	waitSessionIdle(t, srv, ungrouped)
}

// TestAssistantEventNameMapping：事件域映射表——chat.* 与 todo.updated /
// files.changed 换 assistant 前缀；全局事件（session.changed）原样。
func TestAssistantEventNameMapping(t *testing.T) {
	cases := map[string]string{
		"chat.userMessage":     "assistant.userMessage",
		"chat.delta":           "assistant.delta",
		"chat.toolCall":        "assistant.toolCall",
		"chat.toolResult":      "assistant.toolResult",
		"chat.confirmRequest":  "assistant.confirmRequest",
		"chat.done":            "assistant.done",
		"chat.error":           "assistant.error",
		"chat.busy":            "assistant.busy",
		"chat.dispatchStart":   "assistant.dispatchStart",
		"chat.dispatchEnd":     "assistant.dispatchEnd",
		"chat.compacted":       "assistant.compacted",
		"todo.updated":         "assistant.todoUpdated",
		"files.changed":        "assistant.filesChanged",
		"chat.approvalChanged": "assistant.approvalChanged",
		"chat.modelChanged":    "assistant.modelChanged",
		"session.changed":      "session.changed", // 全局事件两域共用
		"model.changed":        "model.changed",
		"agent.changed":        "agent.changed",
	}
	for in, want := range cases {
		if got := assistantEventName(in); got != want {
			t.Fatalf("assistantEventName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAssistantResolverDelegates（验收② 的名单面）：助手解析器的 Delegates
// 从库里现读（启用且非主）——run_task 的 agent 参数候选面。
func TestAssistantResolverDelegates(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	r := &assistantAgentResolver{st: srv.st}
	ac, ok := r.Resolve("assistant")
	if !ok {
		t.Fatal("assistant 应可解析")
	}
	if ac.Def.ID != "assistant" {
		t.Fatalf("解析出的定义应是助手: %+v", ac.Def)
	}
	for _, d := range ac.Delegates {
		if d.IsMain {
			t.Fatalf("委派名单不应含主 Agent: %+v", d)
		}
		if !d.Enabled {
			t.Fatalf("委派名单不应含停用 Agent: %+v", d)
		}
	}
	if len(ac.Delegates) == 0 {
		t.Fatal("种子里有启用的执行 Agent，委派名单不应为空")
	}
	// 非助手 id：走库现读（四层组合按被指名的 Agent 走）
	if _, ok := r.Resolve("main"); !ok {
		t.Fatal("main 应可按 id 解析")
	}
	if _, ok := r.Resolve("nobody"); ok {
		t.Fatal("不存在的 id 不应解析成功")
	}
}
