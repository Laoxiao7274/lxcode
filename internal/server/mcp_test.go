package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/mcp"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// 假 MCP 端点（Streamable HTTP）：握手 + 列举 + 调用。
//
// 用 HTTP 而不是 stdio 子进程：本包测试没有「自我 re-exec」的 TestMain 钩子
// （那个手法在 internal/mcp 里用掉了），而 httptest 能覆盖同一条装配链——
// 传输层的 stdio 细节由 mcp 包自己的测试钉住。
func fakeMCPEndpoint(t *testing.T, tools []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if req.ID == nil { // 通知
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": mcp.ProtocolVersion,
				"serverInfo":      map[string]any{"name": "fakesrv", "version": "1.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": tools}
		case "tools/call":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = map[string]any{"content": []map[string]any{
				{"type": "text", "text": "srv 收到 " + p.Name},
			}}
		}
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 端到端：加 MCP 服务器 → 工具物化进目录 → 注册进注册表 → 模型能调、能执行。
//
// 这是**接线**的验收：只测 internal/mcp 证明不了「物化 + 注册 + 调用口」这三段
// 真的接上了（三段里任何一段断掉，功能都是不可见的半截）。
func TestMCPServerEndToEnd(t *testing.T) {
	endpoint := fakeMCPEndpoint(t, []map[string]any{
		{
			"name":        "web.search", // 点号：物化时必须被净化
			"description": "联网搜索",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q": map[string]any{"type": "string", "description": "查询词"},
				},
				"required": []string{"q"},
			},
		},
	})
	srv, client, _ := newTestServer(t, nil)

	// 1) 建服务器（sse = Streamable HTTP 端点）
	var addRes protocol.McServerEntry
	callOK(t, client, protocol.MethodCatalogMcpAdd, protocol.McServerAddParams{
		Server: protocol.McServerEntry{
			ID: "exa", Desc: "Exa", Transport: "sse", URL: endpoint.URL, Enabled: true, Custom: true,
		},
	}, &addRes)

	// 2) 工具必须已被物化并注册（净化后的名字）
	list := srv.treg.Order()
	wantName := mcp.ExposedName("exa", "web.search") // exa_web_search
	if !hasName(list, wantName) {
		t.Fatalf("物化后的工具 %q 应已注册，实际注册表: %v", wantName, list)
	}

	// 3) 目录里也要有这条（用户要能在 Agent 编辑器里勾它）
	specs, err := srv.st.ListTools()
	if err != nil {
		t.Fatal(err)
	}
	var found *sessiondata.ToolSpec
	for i := range specs {
		if specs[i].ID == wantName {
			found = &specs[i]
		}
	}
	if found == nil {
		t.Fatalf("目录里应有 %q，实际 %v", wantName, toolIDs(specs))
	}
	if found.Source != "mcp" || found.Server != "exa" {
		t.Errorf("目录条目的来源字段不对: %+v", *found)
	}
	// 一律高危（注解不可信，不给服务器自降级的机会）
	if found.Risk != "high" {
		t.Errorf("MCP 工具必须按高危处理，实际 risk=%q", found.Risk)
	}
	// 参数表从 inputSchema 推出来（详情层展示）
	if len(found.Params) != 1 || found.Params[0].Name != "q" || !found.Params[0].Required {
		t.Errorf("参数表应由 schema 推出: %+v", found.Params)
	}

	// 4) 真的能执行（走注册表 → mcpCall → manager → HTTP）
	def, ok := srv.treg.Get(wantName)
	if !ok {
		t.Fatalf("注册表里应有 %q", wantName)
	}
	out, err := def.Exec(srv.Ctx(), json.RawMessage(`{"q":"go"}`))
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if !strings.Contains(out, "srv 收到 web.search") {
		t.Errorf("应把原名传给服务器，实际: %q", out)
	}

	// 5) 列表带运行期状态（界面据此显示「已连接」）
	var servers []protocol.McServerEntry
	callOK(t, client, protocol.MethodCatalogMcpList, nil, &servers)
	if len(servers) != 1 {
		t.Fatalf("服务器数 = %d", len(servers))
	}
	if servers[0].Status != mcp.StateConnected {
		t.Errorf("状态应为 connected，实际 %q（错误: %s）", servers[0].Status, servers[0].LastError)
	}
	if servers[0].ToolCount != 1 {
		t.Errorf("工具数 = %d，期望 1", servers[0].ToolCount)
	}

	// 6) 停用服务器 = 能力挂起：工具从注册表消失
	var updated protocol.McServerEntry
	callOK(t, client, protocol.MethodCatalogMcpUpdate, protocol.McServerAddParams{
		Server: protocol.McServerEntry{
			ID: "exa", Desc: "Exa", Transport: "sse", URL: endpoint.URL, Enabled: false, Custom: true,
		},
	}, &updated)
	if hasName(srv.treg.Order(), wantName) {
		t.Error("停用后工具应从注册表撤下（能力挂起）")
	}
	var after []protocol.McServerEntry
	callOK(t, client, protocol.MethodCatalogMcpList, nil, &after)
	if after[0].Status != mcp.StateStopped {
		t.Errorf("停用后状态应为 stopped，实际 %q", after[0].Status)
	}

	// 7) 删除服务器：工具彻底消失
	callOK(t, client, protocol.MethodCatalogMcpRemove, protocol.McServerRemoveParams{ID: "exa"}, nil)
	specs, _ = srv.st.ListTools()
	for _, sp := range specs {
		if sp.Server == "exa" {
			t.Errorf("删除服务器后不该还有它的目录条目: %+v", sp)
		}
	}
}

// 连不上的服务器不该让 catalog 请求失败，也不该拖住它——状态要如实上报。
func TestMCPServerUnreachableReportsError(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	// 指向一个必然连不上的地址
	var entry protocol.McServerEntry
	callOK(t, client, protocol.MethodCatalogMcpAdd, protocol.McServerAddParams{
		Server: protocol.McServerEntry{
			ID: "dead", Desc: "连不上的", Transport: "sse",
			URL: "http://127.0.0.1:1/mcp", Enabled: true, Custom: true,
		},
	}, &entry)

	var servers []protocol.McServerEntry
	callOK(t, client, protocol.MethodCatalogMcpList, nil, &servers)
	if len(servers) != 1 {
		t.Fatalf("服务器数 = %d", len(servers))
	}
	if servers[0].Status != mcp.StateError {
		t.Errorf("状态应为 error，实际 %q", servers[0].Status)
	}
	if servers[0].LastError == "" {
		t.Error("失败时必须给出原因（界面要显示给用户）")
	}
	// 目录里不该有它的工具
	specs, _ := srv.st.ListTools()
	for _, sp := range specs {
		if sp.Server == "dead" {
			t.Errorf("连不上的服务器不该物化出工具: %+v", sp)
		}
	}
}

// callOK 发请求并断言成功，把 result 解到 out（out 为 nil 时只断言成功）。
func callOK(t *testing.T, c *wsTestClient, method string, params any, out any) {
	t.Helper()
	resp := c.call(method, params)
	if resp == nil {
		t.Fatalf("%s 无应答（超时）", method)
	}
	if resp.Error != nil {
		t.Fatalf("%s 失败: %+v", method, resp.Error)
	}
	if out != nil {
		// Result 已被 WS 客户端解成 any，再编回去解进目标类型（省一套断言辅助）
		b, err := json.Marshal(resp.Result)
		if err != nil {
			t.Fatalf("%s 结果再编码失败: %v", method, err)
		}
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s 结果解析失败: %v（原始 %s）", method, err, string(b))
		}
	}
}

// hasName 判断名字是否在清单里（避免与 server_test.go 的既有辅助函数撞名）。
func hasName(list []string, want string) bool {
	for _, x := range list {
		if x == want {
			return true
		}
	}
	return false
}

func toolIDs(specs []sessiondata.ToolSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.ID)
	}
	return out
}
