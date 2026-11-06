// assistant 工具白名单（ToolWhitelist，2026-12 用户拍板：助手默认工具完全可配）
// 的 server 侧验收：
//   - resolver 合并序：whitelist 完整覆盖（勾 bash → 白名单含 bash）/ whitelist
//     空 → 内置 + 旧 tools 追加（旧行为不回归）/ whitelist 非空时旧 tools 忽略
//    （防双源叠加）/ MCP 恒最后追加 / 悬空 id 过滤；
//   - set 校验：whitelist 只查存在性（bash 可勾入）/ bash 在旧 tools 仍被拒
//    （旧语义不回归）/ get 应答带 builtinDefaults；
//   - patch 边界：toolWhitelist 不在 update_assistant_config 的可改形状里；
//   - RemoveTool 引用检查：删被 whitelist 引用的工具被拒。
package server

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// newWhitelistStack 起一个装配了 assistant.json + 一条自定义工具的测试栈。
func newWhitelistStack(t *testing.T) (*Server, *wsTestClient, string) {
	t.Helper()
	srv, client, _ := newTestServer(t, nil)
	path := filepath.Join(t.TempDir(), "assistant.json")
	srv.AttachAssistantConfig(path)
	if err := srv.st.AddTool(sessiondata.ToolSpec{ID: "my_query", Desc: "自定义查询", Risk: "low", Source: "binary", Command: "myquery {q}", Custom: true}); err != nil {
		t.Fatal(err)
	}
	return srv, client, path
}

func resolverOf(srv *Server) *assistantAgentResolver {
	return &assistantAgentResolver{st: srv.st, configFn: srv.assistantConfig, modFn: srv.modulesSnapshot}
}

func saveCfg(t *testing.T, path string, a config.Assistant) {
	t.Helper()
	if err := config.SaveAssistant(path, a); err != nil {
		t.Fatal(err)
	}
}

// TestWhitelistOverrides：whitelist 非空 = 完整覆盖——勾 bash（+ read_file）
// 后白名单就是这两项（内置 14 项不回来），悬空 id（nope）被过滤。
func TestWhitelistOverrides(t *testing.T) {
	srv, _, path := newWhitelistStack(t)
	saveCfg(t, path, config.Assistant{ToolWhitelist: []string{"bash", "read_file", "nope"}})
	srv.AttachAssistantConfig(path)
	r := resolverOf(srv)
	ac, _ := r.Resolve("assistant")
	// nope 不在目录（内置条目都在 tools 表，nope 是纯悬空），被过滤；bash 是
	// seedTools 的内置条目，合法放行。
	want := []string{"bash", "read_file"}
	if !slices.Equal(ac.Def.Tools, want) {
		t.Fatalf("whitelist 应完整覆盖（悬空 id 过滤）: got %v want %v", ac.Def.Tools, want)
	}
	// 常量本体不被写坏。
	if len(assistantAgentDef.Tools) != 14 {
		t.Fatalf("内置常量 Tools 不该被写坏: %v", assistantAgentDef.Tools)
	}
}

// TestWhitelistEmptyKeepsLegacyTools：whitelist 空 → 内置 14 项 + 旧 cfg.Tools
// 追加（custom binary 命中、内置名被滤——旧行为不回归）。
func TestWhitelistEmptyKeepsLegacyTools(t *testing.T) {
	srv, _, path := newWhitelistStack(t)
	saveCfg(t, path, config.Assistant{Tools: []string{"my_query", "bash"}})
	srv.AttachAssistantConfig(path)
	r := resolverOf(srv)
	ac, _ := r.Resolve("assistant")
	for _, base := range assistantAgentDef.Tools {
		if !slices.Contains(ac.Def.Tools, base) {
			t.Fatalf("内置白名单应保留: %q 缺席于 %v", base, ac.Def.Tools)
		}
	}
	if !slices.Contains(ac.Def.Tools, "my_query") {
		t.Fatalf("旧 tools 通道应照常追加: %v", ac.Def.Tools)
	}
	if slices.Contains(ac.Def.Tools, "bash") {
		t.Fatalf("旧 tools 通道内置名 bash 仍应被滤（旧语义不回归）: %v", ac.Def.Tools)
	}
}

// TestWhitelistShadowsLegacyTools：whitelist 非空时旧 cfg.Tools 被忽略——
// 双源叠加会让「勾掉的工具经旧通道回来」。
func TestWhitelistShadowsLegacyTools(t *testing.T) {
	srv, _, path := newWhitelistStack(t)
	saveCfg(t, path, config.Assistant{
		ToolWhitelist: []string{"read_file"},
		Tools:         []string{"my_query"}, // 与 whitelist 并存——应被忽略
	})
	srv.AttachAssistantConfig(path)
	r := resolverOf(srv)
	ac, _ := r.Resolve("assistant")
	if !slices.Equal(ac.Def.Tools, []string{"read_file"}) {
		t.Fatalf("whitelist 非空时旧 tools 应被忽略: %v", ac.Def.Tools)
	}
}

// TestWhitelistMCPAlwaysAppended：MCP 工具**恒最后追加**——whitelist 覆盖
// 不含 MCP（mcpServers 是独立语义的动态面，与静态面正交）；whitelist 空时
// 旧行为同款（内置 + MCP）。
func TestWhitelistMCPAlwaysAppended(t *testing.T) {
	srv, _, path := newWhitelistStack(t)
	if err := srv.st.AddMcServer(sessiondata.McServerSpec{ID: "exa", Desc: "Exa", Transport: "sse", URL: "http://127.0.0.1:1/sse", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := srv.st.AddTool(sessiondata.ToolSpec{ID: "exa_web_search", Desc: "联网搜索", Risk: "high", Source: "mcp", Server: "exa", Custom: true}); err != nil {
		t.Fatal(err)
	}
	r := resolverOf(srv)

	// ① whitelist 非空：静态面 = whitelist，MCP 在最后追加。
	saveCfg(t, path, config.Assistant{ToolWhitelist: []string{"read_file"}, MCPServers: []string{"exa"}})
	srv.AttachAssistantConfig(path)
	ac, _ := r.Resolve("assistant")
	want := []string{"read_file", "exa_web_search"}
	if !slices.Equal(ac.Def.Tools, want) {
		t.Fatalf("whitelist + MCP 恒追加: got %v want %v", ac.Def.Tools, want)
	}

	// ② whitelist 空：内置 + MCP（旧行为）。
	saveCfg(t, path, config.Assistant{MCPServers: []string{"exa"}})
	srv.AttachAssistantConfig(path)
	ac, _ = r.Resolve("assistant")
	for _, base := range assistantAgentDef.Tools {
		if !slices.Contains(ac.Def.Tools, base) {
			t.Fatalf("内置白名单应保留: %q 缺席于 %v", base, ac.Def.Tools)
		}
	}
	if ac.Def.Tools[len(ac.Def.Tools)-1] != "exa_web_search" {
		t.Fatalf("MCP 工具应在尾部追加: %v", ac.Def.Tools)
	}
}

// TestSetWhitelistAcceptsBuiltin：set 的 whitelist 路径只查存在性——内置
// bash 可勾入（完全可配的核心场景）；旧 tools 路径的 bash 仍被拒（旧语义
// 不回归）；get 应答带 builtinDefaults。
func TestSetWhitelistAcceptsBuiltin(t *testing.T) {
	_, client, path := newWhitelistStack(t)

	// ① whitelist 勾 bash → 落盘 + 应答回带。
	resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", ToolWhitelist: []string{"bash", "read_file"},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("whitelist 勾 bash 应放行: %+v", resp)
	}
	res := decode[protocol.AssistantConfigResult](t, resp.Result)
	if !slices.Equal(res.ToolWhitelist, []string{"bash", "read_file"}) {
		t.Fatalf("应答应回带 toolWhitelist: %+v", res)
	}
	if len(res.BuiltinDefaults) != 14 {
		t.Fatalf("应答应带 builtinDefaults（内置默认事实源）: %v", res.BuiltinDefaults)
	}
	if a := config.LoadAssistant(path); !slices.Equal(a.ToolWhitelist, []string{"bash", "read_file"}) {
		t.Fatalf("whitelist 未落盘: %+v", a)
	}

	// ② 同一份白名单里塞不存在的 id → 拒（存在性校验）。
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", ToolWhitelist: []string{"read_file", "nope"},
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("whitelist 塞悬空 id 应报参数错误: %+v", resp)
	}
	if msg := resp.Error.Message; !strings.Contains(msg, "nope") {
		t.Fatalf("错误文案应含无效 id: %q", msg)
	}

	// ③ 旧 tools 路径的 bash 仍被拒（旧语义不回归）。
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", Tools: []string{"bash"},
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("旧 tools 路径的 bash 应仍被拒: %+v", resp)
	}
	if msg := resp.Error.Message; !strings.Contains(msg, "bash") {
		t.Fatalf("错误文案应含被拒 id: %q", msg)
	}

	// ④ get 应答带 builtinDefaults。
	res = decode[protocol.AssistantConfigResult](t, client.call(protocol.MethodAssistantConfigGet, map[string]any{}).Result)
	if len(res.BuiltinDefaults) != 14 || res.BuiltinDefaults[0] != "read_file" {
		t.Fatalf("get 应答应带 builtinDefaults: %v", res.BuiltinDefaults)
	}
}

// TestRemoveToolBlockedByWhitelistRef：删被 whitelist 引用的工具被拒
//（checkAssistantToolRef 扫 whitelist）——白名单也是一份引用面。
func TestRemoveToolBlockedByWhitelistRef(t *testing.T) {
	_, client, path := newWhitelistStack(t)
	// whitelist 引用 my_query（custom binary，合法白名单条目）。
	if resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", ToolWhitelist: []string{"read_file", "my_query"},
	}); resp == nil || resp.Error != nil {
		t.Fatalf("绑定 set 失败: %+v", resp)
	}
	if a := config.LoadAssistant(path); len(a.ToolWhitelist) != 2 {
		t.Fatalf("绑定未落盘: %+v", a)
	}
	resp := client.call(protocol.MethodCatalogToolRemove, protocol.ToolRemoveParams{ID: "my_query"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("被 whitelist 引用的工具应拒删: %+v", resp)
	}
	if msg := resp.Error.Message; !strings.Contains(msg, "白名单") {
		t.Fatalf("错误文案应说明是白名单引用: %q", msg)
	}
	// 取消勾选（全量替换清空 whitelist）→ 删除放行。
	if resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{Persona: "x"}); resp == nil || resp.Error != nil {
		t.Fatalf("解除引用 set 失败: %+v", resp)
	}
	if resp := client.call(protocol.MethodCatalogToolRemove, protocol.ToolRemoveParams{ID: "my_query"}); resp == nil || resp.Error != nil {
		t.Fatalf("解除引用后应可删除: %+v", resp)
	}
}

// TestPatchShapeExcludesToolWhitelist（防自我提权钉子·白名单版）：
// update_assistant_config 的 patch 形状不含 toolWhitelist——白名单是助手的
// 全部可执行面，auto 档下自改 = 结构性自我提权。
func TestPatchShapeExcludesToolWhitelist(t *testing.T) {
	src := []byte(`{"name":"n","toolWhitelist":["bash","edit","write_file"]}`)
	var p tools.AssistantConfigPatch
	if err := json.Unmarshal(src, &p); err != nil {
		t.Fatal(err)
	}
	if p.Name == nil {
		t.Fatalf("name 应照常解析: %+v", p)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "toolWhitelist") {
		t.Fatalf("patch 序列化不应出现 toolWhitelist 键（结构性不可自改）: %s", out)
	}
}
