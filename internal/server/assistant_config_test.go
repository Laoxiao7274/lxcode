// 助手配置（assistant.json）的 server 侧验收：resolver merge（空配置=纯内置、
// persona 灌 Prompt 第三层、skills/workflow 现读过滤无效 id）、协议端到端
//（get/set + 广播 + 落盘 + 无效 id 报错）。
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

// newAssistantConfigStack 起一个装配了 assistant.json 的完整 WS 服务端（含真实客户端）。
func newAssistantConfigStack(t *testing.T) (*Server, *wsTestClient, string) {
	t.Helper()
	srv, client, _ := newTestServer(t, nil)
	path := filepath.Join(t.TempDir(), "assistant.json")
	srv.AttachAssistantConfig(path)
	return srv, client, path
}

// TestAssistantResolverMergeEmptyConfig：空配置 = 纯内置（Name/Prompt/白名单
// 与常量本体一致，常量没被写坏）。
func TestAssistantResolverMergeEmptyConfig(t *testing.T) {
	srv, _, _ := newAssistantConfigStack(t)
	r := &assistantAgentResolver{st: srv.st, configFn: srv.assistantConfig, modFn: srv.modulesSnapshot}
	ac, ok := r.Resolve("assistant")
	if !ok {
		t.Fatal("assistant 应可解析")
	}
	if ac.Def.Name != "助手" || ac.Def.Prompt != "" || len(ac.Def.Skills) != 0 || ac.Def.Workflow != "" {
		t.Fatalf("空配置应回落内置值: %+v", ac.Def)
	}
	// 常量本体不被写坏（只读 merge 的硬边界）。
	if assistantAgentDef.Name != "助手" || assistantAgentDef.Prompt != "" {
		t.Fatalf("merge 不该改内置常量: %+v", assistantAgentDef)
	}
}

// TestAssistantResolverMergePersona：persona 灌进 Def.Prompt（第三层追加）；
// Name 覆盖显示名。
func TestAssistantResolverMergePersona(t *testing.T) {
	srv, _, _ := newAssistantConfigStack(t)
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := config.SaveAssistant(path, config.Assistant{Name: "小灵", Persona: "说话轻松幽默，回答简洁"}); err != nil {
		t.Fatal(err)
	}
	// 直接把配置指向这个文件（AttachAssistantConfig 重装配——测试专用路径）。
	srv.AttachAssistantConfig(path)
	r := &assistantAgentResolver{st: srv.st, configFn: srv.assistantConfig, modFn: srv.modulesSnapshot}
	ac, _ := r.Resolve("assistant")
	if ac.Def.Name != "小灵" {
		t.Fatalf("Name 应被覆盖: %q", ac.Def.Name)
	}
	if ac.Def.Prompt != "说话轻松幽默，回答简洁" {
		t.Fatalf("persona 应灌进 Prompt: %q", ac.Def.Prompt)
	}
	// ResolveByName 按新名命中。
	if _, ok := r.ResolveByName("小灵"); !ok {
		t.Fatal("新显示名应可按名解析")
	}
}

// TestAssistantResolverMergeSkillsFilter：skills/workflow 现读 modules 匹配——
// 无效 id 静默过滤（set 时已校验，这里兜手改文件的窗口），有效 id 灌 ac。
func TestAssistantResolverMergeSkillsFilter(t *testing.T) {
	srv, _, _ := newAssistantConfigStack(t)
	mods, err := srv.st.ListModules()
	if err != nil {
		t.Fatal(err)
	}
	var workflowID, skillID string
	for _, m := range mods {
		if m.Kind == "process" && workflowID == "" {
			workflowID = m.ID
		}
		if m.Kind == "skill" && skillID == "" {
			skillID = m.ID
		}
	}
	if workflowID == "" || skillID == "" {
		t.Skip("种子目录缺模块，无法测过滤")
	}
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := config.SaveAssistant(path, config.Assistant{
		Workflow: workflowID, Skills: []string{skillID, "不存在的技能"},
	}); err != nil {
		t.Fatal(err)
	}
	srv.AttachAssistantConfig(path)
	r := &assistantAgentResolver{st: srv.st, configFn: srv.assistantConfig, modFn: srv.modulesSnapshot}
	ac, _ := r.Resolve("assistant")
	if ac.Workflow == nil || ac.Workflow.ID != workflowID {
		t.Fatalf("workflow 应灌进 ac: %+v", ac.Workflow)
	}
	if len(ac.Skills) != 1 || ac.Skills[0].ID != skillID {
		t.Fatalf("无效技能 id 应被过滤: %+v", ac.Skills)
	}
}

// TestAssistantConfigProtocolRoundtrip：get/set 端到端 + set 后广播 + 落盘。
func TestAssistantConfigProtocolRoundtrip(t *testing.T) {
	_, client, path := newAssistantConfigStack(t)
	// get：空配置
	resp := client.call(protocol.MethodAssistantConfigGet, map[string]any{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("get 失败: %+v", resp)
	}
	res := decode[protocol.AssistantConfigResult](t, resp.Result)
	if res.Name != "" || res.Persona != "" {
		t.Fatalf("空配置 get 应全空: %+v", res)
	}
	// set + 广播 + 落盘
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Name: "小灵", Persona: "轻松幽默", Skills: []string{"gsap"}, Workflow: "research-first",
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("set 失败: %+v", resp)
	}
	if ev := client.waitEvent(protocol.EventAssistantConfigChanged); ev == nil {
		t.Fatal("set 后应广播 assistant.configChanged")
	}
	res = decode[protocol.AssistantConfigResult](t, client.call(protocol.MethodAssistantConfigGet, map[string]any{}).Result)
	if res.Name != "小灵" || res.Persona != "轻松幽默" || len(res.Skills) != 1 {
		t.Fatalf("set 后 get 应返回新值: %+v", res)
	}
	if a := config.LoadAssistant(path); a.Name != "小灵" {
		t.Fatalf("配置未落盘: %+v", a)
	}
}

// TestAssistantConfigSetInvalidModule：Skills/Workflow 的 id 必须存在于 modules
// 表（现读校验，无效 id 报人话错误）。
func TestAssistantConfigSetInvalidModule(t *testing.T) {
	_, client, _ := newAssistantConfigStack(t)
	resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", Skills: []string{"不存在的技能"},
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("无效技能 id 应报参数错误: %+v", resp)
	}
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", Workflow: "不存在的模板",
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("无效模板 id 应报参数错误: %+v", resp)
	}
	// 报错文案带无效 id（人话错误，不是裸 code）。
	if msg := resp.Error.Message; msg == "" {
		t.Fatal("错误应有说明文案")
	}
}

// TestAssistantConfigSetClears：全量替换语义——set 不带 skills/workflow = 清空绑定。
func TestAssistantConfigSetClears(t *testing.T) {
	_, client, path := newAssistantConfigStack(t)
	if resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", Skills: []string{"gsap"}, Workflow: "research-first",
	}); resp == nil || resp.Error != nil {
		t.Fatalf("首次 set 失败: %+v", resp)
	}
	if resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x",
	}); resp == nil || resp.Error != nil {
		t.Fatalf("清空 set 失败: %+v", resp)
	}
	if a := config.LoadAssistant(path); len(a.Skills) != 0 || a.Workflow != "" {
		t.Fatalf("全量替换应清掉未带字段: %+v", a)
	}
}

// TestAssistantConfigUnattached：未装配路径时 get 全空、set 报「未装配」。
func TestAssistantConfigUnattached(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	_ = srv
	resp := client.call(protocol.MethodAssistantConfigGet, map[string]any{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("未装配 get 应仍可用（全空）: %+v", resp)
	}
	res := decode[protocol.AssistantConfigResult](t, resp.Result)
	if res.Name != "" || res.Persona != "" {
		t.Fatalf("未装配应回落全空: %+v", res)
	}
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{Persona: "x"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("未装配 set 应报错: %+v", resp)
	}
}

// TestAssistantAgentMergeDefIsolation：merge 后的 Def 是副本——多次 Resolve 各自
// 独立（Persona 追加不会越加越长）。
func TestAssistantAgentMergeDefIsolation(t *testing.T) {
	srv, _, _ := newAssistantConfigStack(t)
	r := &assistantAgentResolver{st: srv.st, configFn: srv.assistantConfig, modFn: srv.modulesSnapshot}
	first, _ := r.Resolve("assistant")
	second, _ := r.Resolve("assistant")
	if first.Def.Prompt != second.Def.Prompt {
		t.Fatalf("两次解析应得同一份 persona（追加不叠加）: %q vs %q", first.Def.Prompt, second.Def.Prompt)
	}
}

// TestAssistantResolverMergeMCPTools（MCP 服务器绑定）：配置声明服务器 id →
// Def.Tools 合入该服务器已物化的工具（<server>_<tool> 暴露名）；合入是新建
// 切片（内置常量底层不被写坏——防 append 共享底层的跨会话污染）；服务器停用
// /条目撤下 → 工具名悬空（白名单里名字在、注册表没有 → compose 层的
// missingTools 提示兜住）；st=nil 跳过合入。
func TestAssistantResolverMergeMCPTools(t *testing.T) {
	srv, _, _ := newAssistantConfigStack(t)
	// 目录里挂一个 MCP 服务器及其工具（物化后的真实形状——照 mcp_test 同款）
	if err := srv.st.AddMcServer(sessiondata.McServerSpec{ID: "exa", Desc: "Exa", Transport: "sse", URL: "http://127.0.0.1:1/sse", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := srv.st.AddTool(sessiondata.ToolSpec{ID: "exa_web_search", Desc: "联网搜索", Risk: "high", Source: "mcp", Server: "exa", Custom: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := config.SaveAssistant(path, config.Assistant{MCPServers: []string{"exa", "不存在的服务器"}}); err != nil {
		t.Fatal(err)
	}
	srv.AttachAssistantConfig(path)
	r := &assistantAgentResolver{st: srv.st, configFn: srv.assistantConfig, modFn: srv.modulesSnapshot}

	// ① 合入：MCP 工具名出现在 Def.Tools，内置白名单原样保留
	ac, _ := r.Resolve("assistant")
	if !slices.Contains(ac.Def.Tools, "exa_web_search") {
		t.Fatalf("声明的 MCP 服务器的工具应合入白名单: %v", ac.Def.Tools)
	}
	for _, base := range assistantAgentDef.Tools {
		if !slices.Contains(ac.Def.Tools, base) {
			t.Fatalf("内置白名单不应被动: %q 缺席于 %v", base, ac.Def.Tools)
		}
	}
	// ② 新建切片：内置常量的 Tools 底层不被写坏（长度/内容与常量本体一致，
	// 且二次解析后常量仍纯内置——append 共享底层的回归钉子）。
	if len(assistantAgentDef.Tools) != 14 {
		t.Fatalf("内置常量 Tools 不该被写坏（len=%d）: %v", len(assistantAgentDef.Tools), assistantAgentDef.Tools)
	}
	for i := 0; i < 3; i++ {
		r.Resolve("assistant")
	}
	if len(assistantAgentDef.Tools) != 14 {
		t.Fatalf("多次解析后常量 Tools 被写坏: %v", assistantAgentDef.Tools)
	}
	// ③ 服务器停用（工具条目仍在目录）→ 白名单悬空名照旧合入——消费点在
	// 注册表/compose 层，这里只负责目录过滤。真正的悬空降级用「目录条目被
	// 级联删除」验证：
	if err := srv.st.RemoveMcServer("exa"); err != nil {
		t.Fatal(err)
	}
	ac2, _ := r.Resolve("assistant")
	if slices.Contains(ac2.Def.Tools, "exa_web_search") {
		t.Fatalf("服务器删除后目录条目被级联撤下，不应再合入: %v", ac2.Def.Tools)
	}
	// 但声明仍在（用户的声明原样保留——声明与目录解耦）。
	if cfg := r.config(); len(cfg.MCPServers) != 2 {
		t.Fatalf("配置声明不该被读路径改写: %v", cfg.MCPServers)
	}
	// ④ st=nil：跳过合入（纯内存模式降级）。
	rNil := &assistantAgentResolver{configFn: srv.assistantConfig}
	acNil, _ := rNil.Resolve("assistant")
	if slices.Contains(acNil.Def.Tools, "exa_web_search") {
		t.Fatal("st=nil 不应合入 MCP 工具")
	}
}

// TestAssistantConfigSetMCPServers：set 现读 mcp_servers 表校验——无效 id 报
// 人话错误；有效 id 落盘 + get 回带。
func TestAssistantConfigSetMCPServers(t *testing.T) {
	srv, client, path := newAssistantConfigStack(t)
	if err := srv.st.AddMcServer(sessiondata.McServerSpec{ID: "exa", Desc: "Exa", Transport: "sse", URL: "http://127.0.0.1:1/sse", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// 无效 id → 参数错误 + 文案带 id
	resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", MCPServers: []string{"nope"},
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("无效 MCP 服务器 id 应报参数错误: %+v", resp)
	}
	if msg := resp.Error.Message; !strings.Contains(msg, "nope") {
		t.Fatalf("错误文案应含无效 id: %q", msg)
	}
	// 有效 id → 落盘 + get 回带
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", MCPServers: []string{"exa"},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("set 失败: %+v", resp)
	}
	res := decode[protocol.AssistantConfigResult](t, resp.Result)
	if len(res.MCPServers) != 1 || res.MCPServers[0] != "exa" {
		t.Fatalf("set 应答应回带 mcpServers: %+v", res)
	}
	if a := config.LoadAssistant(path); len(a.MCPServers) != 1 || a.MCPServers[0] != "exa" {
		t.Fatalf("mcpServers 未落盘: %+v", a)
	}
	// 全量替换：不带 mcpServers 的 set = 清空绑定
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{Persona: "x"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("清空 set 失败: %+v", resp)
	}
	if a := config.LoadAssistant(path); len(a.MCPServers) != 0 {
		t.Fatalf("全量替换应清掉 mcpServers: %+v", a)
	}
}

// TestUpdateAssistantConfigPatchShape（防自我提权钉子）：update_assistant_config
// 的 patch 形状只有 Name/Persona/Skills/Workflow 四字段——不含 mcpServers，
// 不含 tools（自定义命令工具绑定，2026-12：助手 Approval=auto 且自定义工具
// 不经确认门直接执行，自绑 = 结构性自我提权），也不含 toolWhitelist（工具
// 白名单，2026-12 完全可配后它是助手的全部可执行面——自改 = 提权）。
func TestUpdateAssistantConfigPatchShape(t *testing.T) {
	src := []byte(`{"name":"n","persona":"p","skills":["s"],"workflow":"w","mcpServers":["exa"],"tools":["my_query"],"toolWhitelist":["bash","edit"]}`)
	var p tools.AssistantConfigPatch
	if err := json.Unmarshal(src, &p); err != nil {
		t.Fatal(err)
	}
	if p.Name == nil || p.Persona == nil || p.Skills == nil || p.Workflow == nil {
		t.Fatalf("四字段应照常解析: %+v", p)
	}
	// mcpServers/tools/toolWhitelist 无法表达进 patch（无该字段）——结构上不可自改。
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "mcpServers") {
		t.Fatalf("patch 序列化不应出现 mcpServers 键: %s", out)
	}
	if strings.Contains(string(out), "tools") {
		t.Fatalf("patch 序列化不应出现 tools 键: %s", out)
	}
	if strings.Contains(string(out), "toolWhitelist") {
		t.Fatalf("patch 序列化不应出现 toolWhitelist 键: %s", out)
	}
}

// TestAssistantResolverMergeCustomTools（自定义命令工具绑定）：配置声明工具 id →
// Def.Tools 合入 custom && binary 的条目；内置名 bash（手改文件塞进来的）被
// 静默过滤；非 custom 条目被过滤；目录条目删除后声明悬空（不再合入）；合入
// 是新建切片（内置常量底层不被写坏）；st=nil 跳过合入。
func TestAssistantResolverMergeCustomTools(t *testing.T) {
	srv, _, _ := newAssistantConfigStack(t)
	// 目录里挂一个自定义命令工具（custom binary）
	if err := srv.st.AddTool(sessiondata.ToolSpec{ID: "my_query", Desc: "自定义查询", Risk: "low", Source: "binary", Command: "myquery {q}", Custom: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "assistant.json")
	if err := config.SaveAssistant(path, config.Assistant{
		Tools: []string{"my_query", "bash", "read_file"},
	}); err != nil {
		t.Fatal(err)
	}
	srv.AttachAssistantConfig(path)
	r := &assistantAgentResolver{st: srv.st, configFn: srv.assistantConfig, modFn: srv.modulesSnapshot}

	// ① 合入：custom binary 命中；内置名 bash / read_file（source=builtin，
	//    非自定义）被静默过滤——三闸的第三闸（运行期兜底）。
	ac, _ := r.Resolve("assistant")
	if !slices.Contains(ac.Def.Tools, "my_query") {
		t.Fatalf("声明的自定义工具应合入白名单: %v", ac.Def.Tools)
	}
	if slices.Contains(ac.Def.Tools, "bash") {
		t.Fatalf("内置写类工具（bash）不应合入: %v", ac.Def.Tools)
	}
	// 注：read_file 本来就在内置白名单里（合法），这里只验证「声明不引入新名字」。
	for _, base := range assistantAgentDef.Tools {
		if !slices.Contains(ac.Def.Tools, base) {
			t.Fatalf("内置白名单不应被动: %q 缺席于 %v", base, ac.Def.Tools)
		}
	}
	// ② 新建切片：内置常量的 Tools 底层不被写坏。
	if len(assistantAgentDef.Tools) != 14 {
		t.Fatalf("内置常量 Tools 不该被写坏（len=%d）: %v", len(assistantAgentDef.Tools), assistantAgentDef.Tools)
	}
	for i := 0; i < 3; i++ {
		r.Resolve("assistant")
	}
	if len(assistantAgentDef.Tools) != 14 {
		t.Fatalf("多次解析后常量 Tools 被写坏: %v", assistantAgentDef.Tools)
	}
	// ③ 目录条目删除后悬空：解除绑定（改配置文件）→ 删除放行 → 声明与目录
	// 都不再含该 id，resolver 不再合入（按目录过滤的副作用）。
	if err := config.SaveAssistant(path, config.Assistant{Tools: nil}); err != nil {
		t.Fatal(err)
	}
	if err := srv.st.RemoveTool("my_query"); err != nil {
		t.Fatalf("解除绑定后应可删除: %v", err)
	}
	ac2, _ := r.Resolve("assistant")
	if slices.Contains(ac2.Def.Tools, "my_query") {
		t.Fatalf("工具删除后不应再合入: %v", ac2.Def.Tools)
	}
	if cfg := r.config(); len(cfg.Tools) != 0 {
		t.Fatalf("配置声明不该被读路径改写: %v", cfg.Tools)
	}
	// ④ st=nil：跳过合入（纯内存模式降级）。
	srv2, _, _ := newAssistantConfigStack(t)
	path2 := filepath.Join(t.TempDir(), "assistant.json")
	if err := config.SaveAssistant(path2, config.Assistant{Tools: []string{"my_query"}}); err != nil {
		t.Fatal(err)
	}
	srv2.AttachAssistantConfig(path2)
	rNil := &assistantAgentResolver{configFn: srv2.assistantConfig}
	acNil, _ := rNil.Resolve("assistant")
	if slices.Contains(acNil.Def.Tools, "my_query") {
		t.Fatal("st=nil 不应合入自定义工具")
	}
}

// TestAssistantConfigSetTools：set 现读 tools 表校验——只放行 custom binary；
// 内置名 bash 被拒（人话错误点名条目）；有效 id 落盘 + get 回带 + 全量替换清空。
func TestAssistantConfigSetTools(t *testing.T) {
	srv, client, path := newAssistantConfigStack(t)
	if err := srv.st.AddTool(sessiondata.ToolSpec{ID: "my_query", Desc: "自定义查询", Risk: "low", Source: "binary", Command: "myquery {q}", Custom: true}); err != nil {
		t.Fatal(err)
	}
	// 内置名 bash → 参数错误 + 文案点名（内置条目也在 tools 表里，靠
	// custom/source 拒绝——这就是为什么校验不能只查存在性）。
	resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", Tools: []string{"bash"},
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("内置工具应被拒: %+v", resp)
	}
	if msg := resp.Error.Message; !strings.Contains(msg, "bash") {
		t.Fatalf("错误文案应含被拒 id: %q", msg)
	}
	// 不存在的 id → 同样报参数错误。
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", Tools: []string{"nope"},
	})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("无效工具 id 应报参数错误: %+v", resp)
	}
	if msg := resp.Error.Message; !strings.Contains(msg, "nope") {
		t.Fatalf("错误文案应含无效 id: %q", msg)
	}
	// 有效 id → 落盘 + get 回带。
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", Tools: []string{"my_query"},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("set 失败: %+v", resp)
	}
	res := decode[protocol.AssistantConfigResult](t, resp.Result)
	if len(res.Tools) != 1 || res.Tools[0] != "my_query" {
		t.Fatalf("set 应答应回带 tools: %+v", res)
	}
	if a := config.LoadAssistant(path); len(a.Tools) != 1 || a.Tools[0] != "my_query" {
		t.Fatalf("tools 未落盘: %+v", a)
	}
	// 全量替换：不带 tools 的 set = 清空绑定。
	resp = client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{Persona: "x"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("清空 set 失败: %+v", resp)
	}
	if a := config.LoadAssistant(path); len(a.Tools) != 0 {
		t.Fatalf("全量替换应清掉 tools: %+v", a)
	}
}

// TestRemoveToolBlockedByAssistantBinding（盲区补口钉子）：工具被 assistant.json
// 的 tools 绑定时，catalog.tools.remove 被拒（报「先解除助手绑定」）——
// agents 表白名单之外的第二份引用面。解除绑定后删除放行。
func TestRemoveToolBlockedByAssistantBinding(t *testing.T) {
	srv, client, path := newAssistantConfigStack(t)
	if err := srv.st.AddTool(sessiondata.ToolSpec{ID: "my_query", Desc: "自定义查询", Risk: "low", Source: "binary", Command: "myquery {q}", Custom: true}); err != nil {
		t.Fatal(err)
	}
	// 绑定（走 set——过校验的真实路径）。
	if resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{
		Persona: "x", Tools: []string{"my_query"},
	}); resp == nil || resp.Error != nil {
		t.Fatalf("绑定 set 失败: %+v", resp)
	}
	if a := config.LoadAssistant(path); len(a.Tools) != 1 {
		t.Fatalf("绑定未落盘: %+v", a)
	}
	// 删除被拒：文案点名助手绑定。
	resp := client.call(protocol.MethodCatalogToolRemove, protocol.ToolRemoveParams{ID: "my_query"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("被助手绑定的工具应拒删: %+v", resp)
	}
	if msg := resp.Error.Message; !strings.Contains(msg, "助手") {
		t.Fatalf("错误文案应说明是助手绑定: %q", msg)
	}
	// 工具仍在目录。
	list, err := srv.st.ListTools()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(list, func(x sessiondata.ToolSpec) bool { return x.ID == "my_query" }) {
		t.Fatal("拒删后工具应仍在目录")
	}
	// 解除绑定（全量替换清空 tools）→ 删除放行。
	if resp := client.call(protocol.MethodAssistantConfigSet, protocol.AssistantConfigSetParams{Persona: "x"}); resp == nil || resp.Error != nil {
		t.Fatalf("解除绑定 set 失败: %+v", resp)
	}
	if resp := client.call(protocol.MethodCatalogToolRemove, protocol.ToolRemoveParams{ID: "my_query"}); resp == nil || resp.Error != nil {
		t.Fatalf("解除绑定后应可删除: %+v", resp)
	}
}
