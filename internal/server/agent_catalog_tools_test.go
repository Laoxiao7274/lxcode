// agent_read / agent_write 的 server 装配集成测试：AttachSessionStore 注入的
// ops 走真库（store 种子 → 校验 → 落库 → agent.changed 广播）——验证 lxagent
// 进可委派名单、工具读写全链路可用。
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func catalogCall(name, args string) llm.ToolCall {
	var c llm.ToolCall
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

func TestAgentCatalogToolsWired(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)

	t.Run("lxagent 在主 Agent 的有效委派名单里", func(t *testing.T) {
		ac, ok := (&storeAgentResolver{st: srv.st}).Resolve("main")
		if !ok {
			t.Fatal("应能解析主 Agent")
		}
		found := false
		for _, d := range ac.Delegates {
			if d.ID == "lxagent" {
				found = true
				if !strings.Contains(d.Desc, "Agent") || d.Desc == "" {
					t.Fatalf("lxagent 缺职责描述（主 Agent 无从选人）: %q", d.Desc)
				}
			}
		}
		if !found {
			t.Fatalf("主 Agent 的有效委派名单缺 lxagent: %+v", ac.Delegates)
		}
	})

	t.Run("agent_read 列出种子（含 lxagent 与它的配置工具）", func(t *testing.T) {
		out := srv.treg.Execute(context.Background(), catalogCall("agent_read", `{}`))
		if strings.HasPrefix(out, "错误") {
			t.Fatalf("agent_read 失败: %s", out)
		}
		for _, want := range []string{"lxagent", "agent_read", "agent_write"} {
			if !strings.Contains(out, want) {
				t.Fatalf("列表缺 %s:\n%s", want, out)
			}
		}
	})

	t.Run("agent_write 全链路：校验拒绝 + 落库 + 广播", func(t *testing.T) {
		// 非法工具名 → 校验拒绝（错误回填模型）
		out := srv.treg.Execute(context.Background(), catalogCall("agent_write", `{"id":"tester","tools":["ghost_tool"]}`))
		if !strings.Contains(out, "ghost_tool") {
			t.Fatalf("未知工具名应被拒绝:\n%s", out)
		}
		// 合法更新：tester 白名单加 web_search
		out = srv.treg.Execute(context.Background(), catalogCall("agent_write", `{"id":"tester","tools":["read_file","search","web_search"],"desc":"验证与测试（含联网检索）"}`))
		if strings.HasPrefix(out, "错误") {
			t.Fatalf("合法更新失败: %s", out)
		}
		// 变更广播（编辑器界面同步）
		if ev := client.waitEvent(protocol.EventAgentChanged); ev == nil {
			t.Fatal("agent_write 落库后应广播 agent.changed")
		}
		// agent_read 读到新值
		out = srv.treg.Execute(context.Background(), catalogCall("agent_read", `{"id":"tester"}`))
		if !strings.Contains(out, "web_search") || !strings.Contains(out, "含联网检索") {
			t.Fatalf("agent_read 应读到新配置:\n%s", out)
		}
	})
}
