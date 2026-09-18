// agent.*/catalog.* 的 WS 分发测试（M1）：真实 WS 往返 + 种子可见 +
// 校验拒绝 + 变更广播（多客户端同步语义）。
package server

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func TestAgentCatalogOverWS(t *testing.T) {
	_, client, _ := newTestServer(t, nil)

	t.Run("agent.list 返回种子（主 Agent 首位 + is_main）", func(t *testing.T) {
		resp := client.call(protocol.MethodAgentList, nil)
		if resp.Error != nil {
			t.Fatalf("agent.list 失败: %v", resp.Error)
		}
		var list []protocol.AgentEntry
		b, _ := json.Marshal(resp.Result)
		if err := json.Unmarshal(b, &list); err != nil {
			t.Fatal(err)
		}
		if len(list) < 2 {
			t.Fatalf("种子 Agent 不足: %d", len(list))
		}
		if !list[0].IsMain || list[0].ID != "main" {
			t.Fatalf("主 Agent 应首位: %+v", list[0])
		}
		// wire 键名钉住（前端按这些键消费——映射层漏字段会静默丢）
		var m []map[string]any
		json.Unmarshal(b, &m)
		for _, key := range []string{"id", "name", "desc", "color", "model", "tools", "workflow", "skills", "delegates", "approval", "enabled", "prompt", "protocol", "custom", "is_main"} {
			if _, ok := m[0][key]; !ok {
				t.Fatalf("wire 载荷缺键 %s: %v", key, m[0])
			}
		}
	})

	t.Run("agent.add → 广播 → remove 被引用拒绝", func(t *testing.T) {
		resp := client.call(protocol.MethodAgentAdd, protocol.AgentAddParams{
			Agent: protocol.AgentEntry{ID: "t1", Name: "测试", Tools: []string{}, Skills: []string{}, Delegates: []string{}, Approval: "confirm", Enabled: true, Custom: true},
		})
		if resp.Error != nil {
			t.Fatalf("agent.add 失败: %v", resp.Error)
		}
		ev := client.waitEvent(protocol.EventAgentChanged)
		if ev == nil {
			t.Fatal("应广播 agent.changed")
		}
		// 被主 Agent 引用（把 t1 加进 delegates）→ 删除拒绝
		resp = client.call(protocol.MethodAgentList, nil)
		var list []protocol.AgentEntry
		b, _ := json.Marshal(resp.Result)
		json.Unmarshal(b, &list)
		for i := range list {
			if list[i].IsMain {
				list[i].Delegates = append(list[i].Delegates, "t1")
			}
		}
		if resp := client.call(protocol.MethodAgentUpdate, protocol.AgentAddParams{Agent: list[0]}); resp.Error != nil {
			t.Fatalf("update 失败: %v", resp.Error)
		}
		// 未解除引用删除 → 错误带引用方
		resp = client.call(protocol.MethodAgentRemove, protocol.AgentRemoveParams{ID: "t1"})
		if resp.Error == nil {
			t.Fatal("被引用的 Agent 删除应拒绝")
		}
		var eText string
		b2, _ := json.Marshal(resp.Error)
		json.Unmarshal(b2, &struct {
			Message *string `json:"message"`
		}{})
		_ = eText
		// 简单断言：错误消息里含 main（引用方）
		var errObj map[string]any
		json.Unmarshal(b2, &errObj)
		if msg, _ := errObj["message"].(string); msg == "" || !contains(msg, "main") {
			t.Fatalf("错误应列出引用方 main: %v", errObj)
		}
		// 解除引用后可删
		for i := range list {
			if list[i].IsMain {
				list[i].Delegates = []string{"coder"}
			}
		}
		client.call(protocol.MethodAgentUpdate, protocol.AgentAddParams{Agent: list[0]})
		if resp := client.call(protocol.MethodAgentRemove, protocol.AgentRemoveParams{ID: "t1"}); resp.Error != nil {
			t.Fatalf("解除引用后删除应成功: %v", resp.Error)
		}
	})

	t.Run("catalog.modules 全链路（建/改/广播/内置拒绝）", func(t *testing.T) {
		resp := client.call(protocol.MethodCatalogModuleAdd, protocol.ModuleAddParams{
			Module: protocol.ModuleEntry{ID: "smoke-mod", Desc: "冒烟", Kind: "skill", Body: "# 内容", Custom: true},
		})
		if resp.Error != nil {
			t.Fatalf("modules.add 失败: %v", resp.Error)
		}
		if ev := client.waitEvent(protocol.EventCatalogChanged); ev == nil {
			t.Fatal("应广播 catalog.changed")
		} else {
			var p protocol.CatalogChangedParams
			b, _ := json.Marshal(ev.Params)
			json.Unmarshal(b, &p)
			if p.Kind != "modules" {
				t.Fatalf("kind 应为 modules: %v", p)
			}
		}
		// 内置删除拒绝
		resp = client.call(protocol.MethodCatalogModuleRemove, protocol.ModuleRemoveParams{ID: "plan-execute-verify"})
		if resp.Error == nil {
			t.Fatal("内置模块删除应拒绝")
		}
		// 自建可删
		if resp = client.call(protocol.MethodCatalogModuleRemove, protocol.ModuleRemoveParams{ID: "smoke-mod"}); resp.Error != nil {
			t.Fatalf("自建模块删除失败: %v", resp.Error)
		}
	})

	t.Run("catalog.tools 全链路（建 → list 往返 params → 删）", func(t *testing.T) {
		resp := client.call(protocol.MethodCatalogToolAdd, protocol.ToolAddParams{
			Tool: protocol.ToolEntry{
				ID: "smoke-tool", Desc: "冒烟工具", Risk: "low", Source: "binary",
				Command: "smoke {input}", Custom: true,
				Params: []protocol.ToolParamEntry{{Name: "input", Type: "string", Required: true, Desc: "输入"}},
			},
		})
		if resp.Error != nil {
			t.Fatalf("tools.add 失败: %v", resp.Error)
		}
		resp = client.call(protocol.MethodCatalogToolList, nil)
		var list []protocol.ToolEntry
		b, _ := json.Marshal(resp.Result)
		json.Unmarshal(b, &list)
		var found bool
		for _, x := range list {
			if x.ID == "smoke-tool" {
				found = true
				if len(x.Params) != 1 || !x.Params[0].Required {
					t.Fatalf("params wire 往返失真: %+v", x.Params)
				}
			}
		}
		if !found {
			t.Fatal("新建工具应出现在 catalog.tools.list")
		}
		if resp = client.call(protocol.MethodCatalogToolRemove, protocol.ToolRemoveParams{ID: "smoke-tool"}); resp.Error != nil {
			t.Fatalf("删除失败: %v", resp.Error)
		}
	})

	t.Run("catalog.mcp 全链路（stdio 建 → sse 校验 → 删）", func(t *testing.T) {
		resp := client.call(protocol.MethodCatalogMcpAdd, protocol.McServerAddParams{
			Server: protocol.McServerEntry{ID: "smoke-mcp", Transport: "stdio", Command: "npx", Args: []string{"-y"}, Enabled: true, Custom: true},
		})
		if resp.Error != nil {
			t.Fatalf("mcp.add 失败: %v", resp.Error)
		}
		// sse 缺 url 拒绝
		resp = client.call(protocol.MethodCatalogMcpAdd, protocol.McServerAddParams{
			Server: protocol.McServerEntry{ID: "bad", Transport: "sse", Custom: true},
		})
		if resp.Error == nil {
			t.Fatal("sse 缺 url 应拒绝")
		}
		if resp = client.call(protocol.MethodCatalogMcpRemove, protocol.McServerRemoveParams{ID: "smoke-mcp"}); resp.Error != nil {
			t.Fatalf("删除失败: %v", resp.Error)
		}
	})
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
