// M4 工具目录 → 注册表的同步契约：目录是事实源，增删改都必须反映到
// 注册表（否则「目录里有、模型看不见」或「目录删了、模型还在调」）。
package server

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestCatalogToolsSyncRegistry：catalog.tools.add 建的自定义工具必须立刻进
// 注册表（wire 声明可见），删除后必须消失。
func TestCatalogToolsSyncRegistry(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	builtinCount := len(srv.treg.Order())

	resp := client.call(protocol.MethodCatalogToolAdd, protocol.ToolAddParams{
		Tool: protocol.ToolEntry{
			ID: "smoke-bin", Desc: "冒烟二进制工具", Risk: "high", Source: "binary",
			Command: "go version", Custom: true,
			Params: []protocol.ToolParamEntry{{Name: "input", Type: "string", Desc: "输入"}},
		},
	})
	if resp.Error != nil {
		t.Fatalf("tools.add 失败: %v", resp.Error)
	}
	def, ok := srv.treg.Get("smoke-bin")
	if !ok {
		t.Fatal("新建的 binary 工具未注册进注册表（模型看不见 = 建了白建）")
	}
	if def.Risk != tools.RiskHigh {
		t.Fatalf("风险等级未映射: %v", def.Risk)
	}
	if !def.Mutates {
		t.Fatal("自定义工具必须标记 Mutates（strict 只读模式据此拒绝）")
	}
	if got := def.Confirm(context.Background(), []byte(`{"input":"x"}`)); !strings.Contains(got, "go version") {
		t.Fatalf("高危工具的确认文本应含渲染后的命令，实际: %q", got)
	}
	if !hasTool(srv.treg.LLMTools(), "smoke-bin") {
		t.Fatal("动态工具未进 wire 声明")
	}

	// 删除 → 注册表同步移除（模型不该再看到它）
	if resp := client.call(protocol.MethodCatalogToolRemove, protocol.ToolRemoveParams{ID: "smoke-bin"}); resp.Error != nil {
		t.Fatalf("tools.remove 失败: %v", resp.Error)
	}
	if _, ok := srv.treg.Get("smoke-bin"); ok {
		t.Fatal("删除后注册表里仍有该工具（模型还会去调它）")
	}
	if n := len(srv.treg.Order()); n != builtinCount {
		t.Fatalf("删除后工具数 = %d，应回到 %d", n, builtinCount)
	}
}

// TestCatalogToolsSeedSkipped：种子里 source=binary 但没配 command 的条目
// （ripgrep/browser 是「声明了没装」的形态）必须被跳过而不是让同步失败——
// 内置 9 个工具照常在。
func TestCatalogToolsSeedSkipped(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	for _, name := range []string{"ripgrep", "browser"} {
		if _, ok := srv.treg.Get(name); ok {
			t.Fatalf("%s 未配置 command，不该被注册（跳过即可，注册了模型会去调一个跑不起来的工具）", name)
		}
	}
	for _, name := range []string{"read_file", "search", "session_search", "read_skill", "edit", "write_file", "bash", "todo", "agent.dispatch"} {
		if _, ok := srv.treg.Get(name); !ok {
			t.Fatalf("内置工具 %s 丢失", name)
		}
	}
}

// TestCatalogToolsUpdateSyncRegistry：改 command 后注册表里的定义必须跟着变
// （否则用户改了命令，模型还在跑旧命令）。
func TestCatalogToolsUpdateSyncRegistry(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	entry := protocol.ToolEntry{
		ID: "smoke-upd", Desc: "冒烟工具", Risk: "low", Source: "binary",
		Command: "go version", Custom: true,
		Params: []protocol.ToolParamEntry{{Name: "input", Type: "string"}},
	}
	if resp := client.call(protocol.MethodCatalogToolAdd, protocol.ToolAddParams{Tool: entry}); resp.Error != nil {
		t.Fatalf("tools.add 失败: %v", resp.Error)
	}
	entry.Command = "go env GOMOD"
	if resp := client.call(protocol.MethodCatalogToolUpdate, protocol.ToolAddParams{Tool: entry}); resp.Error != nil {
		t.Fatalf("tools.update 失败: %v", resp.Error)
	}
	def, ok := srv.treg.Get("smoke-upd")
	if !ok {
		t.Fatal("更新后工具丢失")
	}
	out, err := def.Exec(context.Background(), []byte(`{"input":"x"}`))
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	// 旧命令（go version）会打印版本号；新命令（go env GOMOD）打印仓库 go.mod 路径
	if !strings.Contains(out, "go.mod") {
		t.Fatalf("注册表未跟上 command 的更新（跑的还是旧命令）: %q", out)
	}
}

func hasTool(list []llm.Tool, name string) bool {
	for _, tw := range list {
		if tw.Name == name {
			return true
		}
	}
	return false
}
