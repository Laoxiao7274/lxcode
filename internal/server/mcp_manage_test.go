// mcp_manage 的 server 侧集成验收：工具执行体直调 store CRUD + syncMCPServers
// 对账 + 广播（catalog.changed / assistant.configChanged）+ assistant.json
// 绑定联动——三处落点（连接、目录、绑定）缺一处就是半截功能。
package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/tools"
)

// execMcpManage 是「模型调 mcp_manage」的测试入口：走注册表 Execute（与真实
// 工具循环同一条路径——校验/确认门之前的 Exec 语义）。
func execMcpManage(t *testing.T, srv *Server, args string) string {
	t.Helper()
	d, ok := srv.treg.Get(tools.McpManageToolName)
	if !ok {
		t.Fatal("mcp_manage 应已注册（AttachSessionStore 装配）")
	}
	var call json.RawMessage = []byte(args)
	out, err := d.Exec(srv.Ctx(), call)
	if err != nil {
		return "错误: " + err.Error()
	}
	return out
}

// TestMcpManageEndToEnd：add（含绑定联动）→ 目录/绑定/广播三处落点 →
// remove → 目录删 + 提示文本。
func TestMcpManageEndToEnd(t *testing.T) {
	srv, client, _ := newAssistantConfigStack(t) // 装配 assistant.json 路径

	// 1) add（默认 bind）：目录落行 + assistant.json 绑定生效 + 双广播
	out := execMcpManage(t, srv, `{"action":"add","server":{"id":"exa","desc":"Exa 搜索","transport":"sse","url":"http://127.0.0.1:1/sse"}}`)
	if strings.HasPrefix(out, "错误") {
		t.Fatalf("add 不应报错: %s", out)
	}
	if !strings.Contains(out, "已自动绑定") {
		t.Fatalf("bind 默认 true，结果文本应注明绑定:\n%s", out)
	}
	// 目录落行
	servers, err := srv.st.ListMcServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].ID != "exa" || !servers[0].Enabled {
		t.Fatalf("add 应落 mcp_servers 行: %+v", servers)
	}
	// assistant.json 绑定生效
	a := config.LoadAssistant(assistantCfgPathOf(t, srv))
	if len(a.MCPServers) != 1 || a.MCPServers[0] != "exa" {
		t.Fatalf("add 应把服务器 id 绑进 assistant.json: %+v", a)
	}
	// 双广播（assistant.configChanged 先、catalog.changed 后——绑定联动先落盘广播，
	// 工具层再对账广播；waitEvent 会丢弃不匹配的事件，按实际顺序等）
	if ev := client.waitEvent(protocol.EventAssistantConfigChanged); ev == nil {
		t.Fatal("add 绑定后应广播 assistant.configChanged")
	}
	if ev := client.waitEvent(protocol.EventCatalogChanged); ev == nil {
		t.Fatal("add 后应广播 catalog.changed")
	} else {
		var p protocol.CatalogChangedParams
		b, _ := json.Marshal(ev.Params)
		if err := json.Unmarshal(b, &p); err != nil || p.Kind != "mcp" {
			t.Fatalf("catalog.changed 的 Kind 应为 mcp: %s", string(b))
		}
	}

	// 2) add 重复 id：store 拒绝，绑定不重复追加
	out = execMcpManage(t, srv, `{"action":"add","server":{"id":"exa","transport":"sse","url":"http://x"}}`)
	if !strings.Contains(out, "已存在") {
		t.Fatalf("重复 id 应报已存在: %s", out)
	}
	a = config.LoadAssistant(assistantCfgPathOf(t, srv))
	if len(a.MCPServers) != 1 {
		t.Fatalf("失败的 add 不应动绑定: %+v", a)
	}

	// 3) remove：目录删 + 结果文本提示绑定失效
	out = execMcpManage(t, srv, `{"action":"remove","server":{"id":"exa"}}`)
	if strings.HasPrefix(out, "错误") {
		t.Fatalf("remove 不应报错: %s", out)
	}
	if !strings.Contains(out, "已从目录删除") || !strings.Contains(out, "失效") {
		t.Fatalf("remove 的结果文本应提示绑定失效:\n%s", out)
	}
	servers, _ = srv.st.ListMcServers()
	if len(servers) != 0 {
		t.Fatalf("remove 后目录应为空: %+v", servers)
	}
	// 绑定里的死 id 无害（resolver merge 现查过滤），配置原样保留——用户手工清。
	a = config.LoadAssistant(assistantCfgPathOf(t, srv))
	if len(a.MCPServers) != 1 {
		t.Fatalf("remove 不应改用户的绑定声明（死 id 由 merge 过滤）: %+v", a)
	}
}

// TestMcpManageAddBindFalse：bind=false 显式跳过绑定联动。
func TestMcpManageAddBindFalse(t *testing.T) {
	srv, _, _ := newAssistantConfigStack(t)
	out := execMcpManage(t, srv, `{"action":"add","server":{"id":"solo","transport":"stdio","command":"npx","args":["-y","s"]},"bind":false}`)
	if strings.HasPrefix(out, "错误") {
		t.Fatalf("add 不应报错: %s", out)
	}
	if strings.Contains(out, "已自动绑定") {
		t.Fatalf("bind=false 的结果文本不应说绑定:\n%s", out)
	}
	if a := config.LoadAssistant(assistantCfgPathOf(t, srv)); len(a.MCPServers) != 0 {
		t.Fatalf("bind=false 不应写绑定: %+v", a)
	}
}

// TestMcpManageAddIdempotentBind：已在绑定里的 id 再 add（先 remove 后重装
// 的场景）不重复追加。
func TestMcpManageAddIdempotentBind(t *testing.T) {
	srv, _, _ := newAssistantConfigStack(t)
	path := assistantCfgPathOf(t, srv)
	if err := config.SaveAssistant(path, config.Assistant{MCPServers: []string{"exa"}}); err != nil {
		t.Fatal(err)
	}
	srv.AttachAssistantConfig(path) // 重装配基线
	out := execMcpManage(t, srv, `{"action":"add","server":{"id":"exa","transport":"sse","url":"http://x"}}`)
	if strings.HasPrefix(out, "错误") {
		t.Fatalf("add 不应报错: %s", out)
	}
	if a := config.LoadAssistant(path); len(a.MCPServers) != 1 {
		t.Fatalf("已在绑定里的 id 不应重复追加: %+v", a)
	}
}

// TestMcpManageNotAttached：assistant.json 未装配时 add 仍成功（目录是主落点），
// 但结果文本注明绑定失败——绑定联动不静默丢。
func TestMcpManageNotAttached(t *testing.T) {
	srv, _, _ := newTestServer(t, nil) // 不 AttachAssistantConfig
	out := execMcpManage(t, srv, `{"action":"add","server":{"id":"x","transport":"sse","url":"http://x"}}`)
	if strings.HasPrefix(out, "错误") {
		t.Fatalf("未装配配置时目录写入仍应成功: %s", out)
	}
	if !strings.Contains(out, "绑定") || !strings.Contains(out, "失败") {
		t.Fatalf("绑定失败应如实告知:\n%s", out)
	}
}

// assistantCfgPathOf 取 srv 的 assistant.json 路径（测试断言读盘用）。
func assistantCfgPathOf(t *testing.T, srv *Server) string {
	t.Helper()
	srv.assistantCfgMu.Lock()
	defer srv.assistantCfgMu.Unlock()
	if srv.assistantCfgPath == "" {
		t.Fatal("assistant.json 路径未装配")
	}
	return srv.assistantCfgPath
}
