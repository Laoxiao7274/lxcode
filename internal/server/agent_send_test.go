// M2 chat.send 带 agent 参数的 WS 测试：Agent 直选（四层组合 + 白名单 +
// 模型绑定 + 权限取严）真实链路。
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

func TestChatSendWithAgent(t *testing.T) {
	var gotPrompt string
	var gotModel config.ModelConfig
	srv, client, reg := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		gotModel = m
		gotPrompt = msgs[0].Content
		ch := make(chan llm.StreamEvent, 1)
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: "ok"},
		}}
		close(ch)
		return ch, nil
	})
	// 加第二个模型（验证绑定优先）
	if err := reg.Add(config.ModelConfig{
		ID: "m2", BaseURL: "http://127.0.0.1:1/v1", Model: "m2", Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}

	// 直选 coder（种子子 Agent，workflow=minimal-change，skills=frontend-design/gsap）
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "干活", Agent: "coder"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 带 agent 失败: %+v", resp)
	}
	waitBusyClear(t, srv)
	if !strings.Contains(gotPrompt, "你是执行 Agent") {
		t.Fatal("应走子 Agent 执行协议（四层组合）")
	}
	if !strings.Contains(gotPrompt, "最小改动") {
		t.Fatal("workflow 模块（minimal-change）应注入提示词")
	}
	if !strings.Contains(gotPrompt, "前端视觉设计") || !strings.Contains(gotPrompt, "GSAP") {
		t.Fatal("skills 模块应注入提示词")
	}
	// 白名单过滤：coder 白名单含 read_file（出现）不含 session_search
	//（守则第 10 条文本会提到 session_search——断言只看工具清单的行格式）
	if !strings.Contains(gotPrompt, "- read_file：") {
		t.Fatal("白名单内的 read_file 应出现在工具清单")
	}
	if strings.Contains(gotPrompt, "- session_search：") {
		t.Fatal("白名单外的 session_search 不应出现在工具清单（守则文本的提及不算）")
	}

	// 不存在的 Agent 拒绝（错误码 1005）
	resp = client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "x", Agent: "nobody"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeAgentNotFound {
		t.Fatalf("不存在的 Agent 应 1005: %+v", resp.Error)
	}

	// 停用的 Agent 拒绝（新建一个停用的——种子只有 main/coder）
	client.call(protocol.MethodAgentAdd, protocol.AgentAddParams{
		Agent: protocol.AgentEntry{ID: "off", Name: "停用者", Enabled: false, Tools: []string{},
			Skills: []string{}, Delegates: []string{}, Approval: "confirm", Custom: true},
	})
	resp = client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "x", Agent: "off"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeAgentDisabled {
		t.Fatalf("停用的 Agent 应 1006: %+v", resp.Error)
	}

	// 模型绑定：把 coder 绑到 m2 后发送 → 用 m2
	resp = client.call(protocol.MethodAgentList, nil)
	var list []protocol.AgentEntry
	b, _ := json.Marshal(resp.Result)
	json.Unmarshal(b, &list)
	for i := range list {
		if list[i].ID == "coder" {
			list[i].Model = "m2"
			client.call(protocol.MethodAgentUpdate, protocol.AgentAddParams{Agent: list[i]})
		}
	}
	resp = client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "再干", Agent: "coder"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("第二次发送失败: %+v", resp)
	}
	waitBusyClear(t, srv)
	if gotModel.ID != "m2" {
		t.Fatalf("Agent 绑定模型应优先: %+v", gotModel)
	}
}

func waitBusyClear(t *testing.T, srv *Server) {
	t.Helper()
	for i := 0; i < 200 && srv.Session().Busy(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
}
