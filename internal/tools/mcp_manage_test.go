// mcp_manage 的工具层测试：注入桩 ops 验证 action 分派、参数校验、未装配
// 报错、add 的 bind 透传与对账回调，以及高危确认门（Risk/Mutates/Confirm
// 文案——运行期 gate 由 agent.runTools 的既有路径按 Risk 驱动）。
package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func fakeMcServers() []sessiondata.McServerSpec {
	return []sessiondata.McServerSpec{
		{ID: "exa", Desc: "Exa 搜索", Transport: "sse", URL: "http://127.0.0.1:15714/sse", Enabled: true, Custom: true},
		{ID: "fs", Desc: "文件系统", Transport: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem"}, Enabled: false, Custom: true},
	}
}

func fakeMcpCounts() map[string]int { return map[string]int{"exa": 3, "fs": 11} }

// newMcpManageRegistry 起一个注入了桩 ops 的注册表；记录 add/update/remove
// 的调用（透传断言用）。
func newMcpManageRegistry(t *testing.T) (*Registry, *mcpOpsRecorder) {
	t.Helper()
	r := New()
	rec := &mcpOpsRecorder{counts: fakeMcpCounts(), list: fakeMcServers()}
	r.SetMcpManageOps(McpManageOps{
		List: func() ([]sessiondata.McServerSpec, error) { return rec.list, nil },
		Get: func(id string) (sessiondata.McServerSpec, error) {
			for _, m := range rec.list {
				if m.ID == id {
					return m, nil
				}
			}
			return sessiondata.McServerSpec{}, errNotFound{id}
		},
		ToolCounts: func() (map[string]int, error) { return rec.counts, nil },
		Add: func(m sessiondata.McServerSpec, bind bool) (string, error) {
			rec.added = append(rec.added, m)
			rec.addBinds = append(rec.addBinds, bind)
			rec.list = append(rec.list, m)
			return "已安装 " + m.ID, nil
		},
		Update: func(m sessiondata.McServerSpec) (string, error) {
			rec.updated = append(rec.updated, m)
			return "已更新 " + m.ID, nil
		},
		Remove: func(id string) (string, error) {
			rec.removed = append(rec.removed, id)
			return "已删除 " + id, nil
		},
		SyncAndBroadcast: func(reason string) { rec.reasons = append(rec.reasons, reason) },
	})
	return r, rec
}

type errNotFound struct{ id string }

func (e errNotFound) Error() string { return "MCP 服务器 " + e.id + " 不存在" }

type mcpOpsRecorder struct {
	list     []sessiondata.McServerSpec
	counts   map[string]int
	added    []sessiondata.McServerSpec
	addBinds []bool
	updated  []sessiondata.McServerSpec
	removed  []string
	reasons  []string
}

func TestMcpManageListAndGet(t *testing.T) {
	r, _ := newMcpManageRegistry(t)

	t.Run("list：清单含 id/传输/端点/状态/工具数", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(McpManageToolName, `{"action":"list"}`))
		for _, want := range []string{"exa", "fs", "sse", "stdio", "http://127.0.0.1:15714/sse", "停用", "3 个"} {
			if !strings.Contains(out, want) {
				t.Fatalf("列表缺 %s:\n%s", want, out)
			}
		}
		// 空目录有引导文案（模型知道 add 是装新服务器的口）
	})

	t.Run("get：单读含 args/env，未知 id 报错列可用项", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(McpManageToolName, `{"action":"get","server":{"id":"fs"}}`))
		for _, want := range []string{"npx", "@modelcontextprotocol/server-filesystem", "11 个"} {
			if !strings.Contains(out, want) {
				t.Fatalf("单读缺 %s:\n%s", want, out)
			}
		}
		out = r.Execute(context.Background(), callOf(McpManageToolName, `{"action":"get","server":{"id":"ghost"}}`))
		if !strings.Contains(out, "不存在") || !strings.Contains(out, "exa") {
			t.Fatalf("未知 id 应报错并列可用项:\n%s", out)
		}
	})

	t.Run("空目录：引导用 add 安装", func(t *testing.T) {
		r2 := New()
		r2.SetMcpManageOps(McpManageOps{List: func() ([]sessiondata.McServerSpec, error) { return nil, nil }})
		out := r2.Execute(context.Background(), callOf(McpManageToolName, `{"action":"list"}`))
		if !strings.Contains(out, "没有配置任何 MCP 服务器") || !strings.Contains(out, "add") {
			t.Fatalf("空目录应引导安装:\n%s", out)
		}
	})
}

func TestMcpManageValidation(t *testing.T) {
	r, rec := newMcpManageRegistry(t)
	for _, tc := range []struct {
		name, args, want string
	}{
		{"未知 action", `{"action":"reinstall"}`, "未知 action"},
		{"add 缺 id", `{"action":"add","server":{"transport":"sse","url":"http://x"}}`, "需要提供 server.id"},
		{"add 缺 transport", `{"action":"add","server":{"id":"x","url":"http://x"}}`, "需要提供 server.transport"},
		{"stdio 缺 command", `{"action":"add","server":{"id":"x","transport":"stdio"}}`, "需要提供 server.command"},
		{"sse 缺 url", `{"action":"add","server":{"id":"x","transport":"sse"}}`, "需要提供 server.url"},
		{"update 同套校验", `{"action":"update","server":{"id":"x","transport":"stdio"}}`, "需要提供 server.command"},
		{"remove 缺 id", `{"action":"remove"}`, "需要提供 server.id"},
		{"remove 未知 id", `{"action":"remove","server":{"id":"ghost"}}`, "不存在"},
		{"enable 缺 id", `{"action":"enable"}`, "需要提供 server.id"},
	} {
		out := r.Execute(context.Background(), callOf(McpManageToolName, tc.args))
		if !strings.HasPrefix(out, "错误") || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: 应报 %q，得 %s", tc.name, tc.want, out)
		}
	}
	// 全部被拦在校验层：没有任何写落地、没有触发对账。
	if len(rec.added)+len(rec.updated)+len(rec.removed) != 0 || len(rec.reasons) != 0 {
		t.Fatalf("校验拒绝不应落地任何写操作: added=%d updated=%d removed=%d reasons=%v",
			len(rec.added), len(rec.updated), len(rec.removed), rec.reasons)
	}
}

func TestMcpManageNotAssembled(t *testing.T) {
	r := New() // 不注入 ops
	out := r.Execute(context.Background(), callOf(McpManageToolName, `{"action":"list"}`))
	if !strings.Contains(out, "未装配") {
		t.Fatalf("未注入 ops 应报「未装配」:\n%s", out)
	}
}

func TestMcpManageAddAndLifecycle(t *testing.T) {
	r, rec := newMcpManageRegistry(t)

	t.Run("add：形状透传 + bind 默认 true + 对账回调", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(McpManageToolName,
			`{"action":"add","server":{"id":"demo","desc":"演示","transport":"stdio","command":"npx","args":["-y","demo"],"env":{"KEY":"v"}}}`))
		if strings.HasPrefix(out, "错误") {
			t.Fatalf("合法 add 不应报错: %s", out)
		}
		if len(rec.added) != 1 {
			t.Fatalf("Add 应被调用一次: %+v", rec.added)
		}
		m := rec.added[0]
		if m.ID != "demo" || m.Transport != "stdio" || m.Command != "npx" || len(m.Args) != 2 || m.Env["KEY"] != "v" {
			t.Fatalf("Add 收到的形状不对: %+v", m)
		}
		// 新装的默认启用、custom（用户侧条目）
		if !m.Enabled || !m.Custom {
			t.Fatalf("新装服务器应启用且 custom: %+v", m)
		}
		if !rec.addBinds[0] {
			t.Fatal("bind 缺省应为 true（透传给 server 侧做绑定联动）")
		}
		if len(rec.reasons) != 1 || rec.reasons[0] != "update" {
			t.Fatalf("add 后应触发对账回调一次: %v", rec.reasons)
		}
	})

	t.Run("add bind=false 显式跳过绑定", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(McpManageToolName,
			`{"action":"add","server":{"id":"demo2","transport":"sse","url":"http://x"},"bind":false}`))
		if strings.HasPrefix(out, "错误") {
			t.Fatalf("合法 add 不应报错: %s", out)
		}
		if rec.addBinds[len(rec.addBinds)-1] {
			t.Fatal("bind=false 应原样透传")
		}
	})

	t.Run("enable/disable：取现值翻转 enabled 走 update", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(McpManageToolName, `{"action":"disable","server":{"id":"demo"}}`))
		if strings.HasPrefix(out, "错误") {
			t.Fatalf("disable 不应报错: %s", out)
		}
		last := rec.updated[len(rec.updated)-1]
		if last.ID != "demo" || last.Enabled {
			t.Fatalf("disable 应翻转 enabled: %+v", last)
		}
		// 现值其余字段保留（不是全量替换被清空）
		if last.Transport != "stdio" || last.Command != "npx" {
			t.Fatalf("disable 应保留其余配置: %+v", last)
		}
		out = r.Execute(context.Background(), callOf(McpManageToolName, `{"action":"enable","server":{"id":"demo"}}`))
		if strings.HasPrefix(out, "错误") {
			t.Fatalf("enable 不应报错: %s", out)
		}
		if last := rec.updated[len(rec.updated)-1]; !last.Enabled {
			t.Fatalf("enable 应翻转 enabled: %+v", last)
		}
	})

	t.Run("remove：存在性检查后删除 + 对账回调带 remove", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(McpManageToolName, `{"action":"remove","server":{"id":"demo"}}`))
		if strings.HasPrefix(out, "错误") {
			t.Fatalf("remove 不应报错: %s", out)
		}
		if len(rec.removed) != 1 || rec.removed[0] != "demo" {
			t.Fatalf("Remove 收到的 id 不对: %v", rec.removed)
		}
		if rec.reasons[len(rec.reasons)-1] != "remove" {
			t.Fatalf("remove 的对账 reason 应为 remove: %v", rec.reasons)
		}
	})
}

func TestMcpManageConfirmGate(t *testing.T) {
	// 高危确认门的结构断言：Risk=high + Mutates=true（strict 档拒绝、confirm 档
	// 弹确认——由 agent.runTools 按 Risk/Mutates 驱动）+ Confirm 文案写明后果。
	d, ok := New().Get(McpManageToolName)
	if !ok {
		t.Fatal("mcp_manage 应已注册")
	}
	if d.Risk != RiskHigh || !d.Mutates {
		t.Fatalf("mcp_manage 应为高危可变更: risk=%v mutates=%v", d.Risk, d.Mutates)
	}

	t.Run("add 的确认文案写明将执行的命令/端点", func(t *testing.T) {
		prompt := d.Confirm(context.Background(), []byte(
			`{"action":"add","server":{"id":"demo","transport":"stdio","command":"npx","args":["-y","demo"]}}`))
		for _, want := range []string{"demo", "在本机执行命令", "npx -y demo", "自动绑定", "确认执行"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("确认文案缺 %q:\n%s", want, prompt)
			}
		}
		prompt = d.Confirm(context.Background(), []byte(
			`{"action":"add","server":{"id":"remote","transport":"sse","url":"http://x/sse"}}`))
		if !strings.Contains(prompt, "连接端点 http://x/sse") {
			t.Fatalf("sse 的确认文案应写明端点:\n%s", prompt)
		}
	})

	t.Run("remove 的确认文案写明级联后果", func(t *testing.T) {
		prompt := d.Confirm(context.Background(), []byte(`{"action":"remove","server":{"id":"exa"}}`))
		for _, want := range []string{"exa", "一并撤下", "失效"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("确认文案缺 %q:\n%s", want, prompt)
			}
		}
	})

	t.Run("只读动作的确认文案明示无操作", func(t *testing.T) {
		prompt := d.Confirm(context.Background(), []byte(`{"action":"list"}`))
		if !strings.Contains(prompt, "只读查询") {
			t.Fatalf("list 的确认文案应明示只读:\n%s", prompt)
		}
	})
}
