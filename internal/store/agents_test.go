// Agent 注册表与拓展目录的持久化测试（M1）：种子幂等、CRUD 往返、
// 引用校验删除（拒绝并列出引用方）、主 Agent 结构保护。
package store

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func openAgentStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSeedAgentsCatalog(t *testing.T) {
	s := openAgentStore(t)
	agents, err := s.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) < 2 {
		t.Fatalf("种子 Agent 不足: %d", len(agents))
	}
	if !agents[0].IsMain {
		t.Fatal("主 Agent 应排首位")
	}
	if agents[0].ID != "main" || len(agents[0].Delegates) == 0 {
		t.Fatalf("主 Agent 形状不对: %+v", agents[0])
	}
	// 主 Agent 的 custom=0（结构成员不可删）
	if agents[0].Custom {
		t.Fatal("主 Agent 应为 custom=0（结构成员）")
	}
	tools, err := s.ListTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) < 7 {
		t.Fatalf("内置工具种子不足: %d", len(tools))
	}
	var hasBash bool
	for _, x := range tools {
		if x.ID == "bash" {
			hasBash = true
			if x.Custom {
				t.Fatal("内置工具应为 custom=0")
			}
		}
	}
	if !hasBash {
		t.Fatal("bash 工具缺失")
	}
	mods, err := s.ListModules()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) < 5 {
		t.Fatalf("内置模块种子不足: %d", len(mods))
	}
	// 另一库再开一次：种子路径幂等（数量稳定）
	s2, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	agents2, _ := s2.ListAgents()
	if len(agents2) != len(agents) {
		t.Fatalf("同版种子数量应稳定: %d vs %d", len(agents2), len(agents))
	}
}

func TestAgentCRUDRoundtrip(t *testing.T) {
	s := openAgentStore(t)
	a := sessiondata.AgentDef{
		ID: "reviewer", Name: "审查 Agent", Desc: "只读审查",
		Color: "#7c3aed", Model: "", Tools: []string{"read_file", "search"},
		Workflow: "", Skills: []string{"gsap"}, Delegates: []string{},
		Approval: "strict", Enabled: true, Prompt: "只读不写",
	}
	if err := s.AddAgent(a); err != nil {
		t.Fatal(err)
	}
	// id 冲突拒绝
	if err := s.AddAgent(a); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("id 冲突应拒绝: %v", err)
	}
	// 读回——数组字段往返
	got, err := s.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	var found *sessiondata.AgentDef
	for i := range got {
		if got[i].ID == "reviewer" {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatal("读回失败")
	}
	if len(found.Tools) != 2 || found.Tools[0] != "read_file" || len(found.Skills) != 1 || found.Skills[0] != "gsap" {
		t.Fatalf("数组字段往返失真: %+v", found)
	}
	if !found.Enabled || found.Approval != "strict" {
		t.Fatalf("标量字段往返失真: %+v", found)
	}
	// 更新
	found.Desc = "改过的描述"
	found.Delegates = []string{"coder"}
	if err := s.UpdateAgent(*found); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.ListAgents()
	for _, x := range got2 {
		if x.ID == "reviewer" {
			if x.Desc != "改过的描述" || len(x.Delegates) != 1 {
				t.Fatalf("更新失真: %+v", x)
			}
		}
	}
	// 校验：空名拒绝
	if err := s.AddAgent(sessiondata.AgentDef{ID: "x", Name: " "}); err == nil {
		t.Fatal("空名应拒绝")
	}
	// 删除
	if err := s.RemoveAgent("reviewer"); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRemoveGuards(t *testing.T) {
	s := openAgentStore(t)
	// 主 Agent 不可删
	if err := s.RemoveAgent("main"); err == nil || !strings.Contains(err.Error(), "主 Agent") {
		t.Fatalf("主 Agent 删除应拒绝: %v", err)
	}
	// 被 delegates 引用的 Agent 不可删（错误列出引用方）
	if err := s.RemoveAgent("coder"); err == nil || !strings.Contains(err.Error(), "main") {
		t.Fatalf("被引用删除应拒绝并列出引用方: %v", err)
	}
	// 主 Agent 不可自建第二个
	if err := s.AddAgent(sessiondata.AgentDef{ID: "main2", Name: "伪主", IsMain: true}); err == nil || !strings.Contains(err.Error(), "主 Agent") {
		t.Fatalf("自建主 Agent 应拒绝: %v", err)
	}
}

func TestModuleCRUDAndRefs(t *testing.T) {
	s := openAgentStore(t)
	m := sessiondata.ModuleSpec{ID: "deploy-checklist", Kind: "process", Desc: "发布检查清单", Body: "# 发布\n\n- 构建过\n- 测试过", Custom: true}
	if err := s.AddModule(m); err != nil {
		t.Fatal(err)
	}
	// kind 校验
	if err := s.AddModule(sessiondata.ModuleSpec{ID: "x", Kind: "bogus", Desc: "x"}); err == nil {
		t.Fatal("非法 kind 应拒绝")
	}
	// 内置模块只读（无论是否被引用——结构保护优先于引用校验）
	if err := s.RemoveModule("plan-execute-verify"); err == nil || !strings.Contains(err.Error(), "内置") {
		t.Fatalf("内置模块删除应拒绝: %v", err)
	}
	// 内置模块被 workflow 引用也是「内置」先拒绝（种子 main 的 workflow=plan-execute-verify）
	if err := s.RemoveModule("minimal-change"); err == nil || !strings.Contains(err.Error(), "内置") {
		t.Fatalf("内置模块删除应拒绝（内置优先于引用）: %v", err)
	}
	// 自定义模块被引用 → 拒绝并列出引用方
	if err := s.AddModule(sessiondata.ModuleSpec{ID: "my-flow", Kind: "process", Desc: "自建流程", Body: "# 流程", Custom: true}); err != nil {
		t.Fatal(err)
	}
	agents, _ := s.ListAgents()
	for i, a := range agents {
		if a.ID == "coder" {
			agents[i].Workflow = "my-flow"
			if err := s.UpdateAgent(agents[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.RemoveModule("my-flow"); err == nil || !strings.Contains(err.Error(), "coder") {
		t.Fatalf("被引用模块删除应拒绝并列出引用方: %v", err)
	}
	// 解除引用后可删
	for i, a := range agents {
		if a.ID == "coder" {
			agents[i].Workflow = "minimal-change"
			if err := s.UpdateAgent(agents[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.RemoveModule("my-flow"); err != nil {
		t.Fatal(err)
	}
}

func TestToolCRUDAndRefs(t *testing.T) {
	s := openAgentStore(t)
	tt := sessiondata.ToolSpec{
		ID: "my-tool", Desc: "自定义工具", Risk: "low", Source: "binary",
		Command: "my-tool {input} --json", Custom: true,
		Params: []sessiondata.ToolParam{{Name: "input", Type: "string", Required: true}},
	}
	if err := s.AddTool(tt); err != nil {
		t.Fatal(err)
	}
	// risk 校验
	if err := s.AddTool(sessiondata.ToolSpec{ID: "y", Desc: "d", Risk: "mid", Source: "binary"}); err == nil {
		t.Fatal("非法 risk 应拒绝")
	}
	// 内置只读（无论是否被引用——结构保护优先）
	if err := s.RemoveTool("bash"); err == nil || !strings.Contains(err.Error(), "内置") {
		t.Fatalf("内置工具删除应拒绝: %v", err)
	}
	// 内置工具被白名单引用也是「内置」先拒绝（种子 coder 引用 read_file）
	if err := s.RemoveTool("read_file"); err == nil || !strings.Contains(err.Error(), "内置") {
		t.Fatalf("内置工具删除应拒绝（内置优先于引用）: %v", err)
	}
	// 自定义工具被引用 → 拒绝并列出引用方
	agents, _ := s.ListAgents()
	for i, a := range agents {
		if a.ID == "coder" {
			agents[i].Tools = append(agents[i].Tools, "my-tool")
			if err := s.UpdateAgent(agents[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.RemoveTool("my-tool"); err == nil || !strings.Contains(err.Error(), "coder") {
		t.Fatalf("被引用工具删除应拒绝并列出引用方: %v", err)
	}
	// 解除引用后可删
	for i, a := range agents {
		if a.ID == "coder" {
			agents[i].Tools = []string{"read_file", "search", "edit", "write_file", "bash", "todo"}
			if err := s.UpdateAgent(agents[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.RemoveTool("my-tool"); err != nil {
		t.Fatal(err)
	}
	// 往返 params
	if err := s.AddTool(tt); err != nil {
		t.Fatal(err)
	}
	tools, _ := s.ListTools()
	for _, x := range tools {
		if x.ID == "my-tool" {
			if len(x.Params) != 1 || x.Params[0].Name != "input" || !x.Params[0].Required {
				t.Fatalf("params 往返失真: %+v", x.Params)
			}
		}
	}
}

func TestMcServerCRUDAndCascade(t *testing.T) {
	s := openAgentStore(t)
	m := sessiondata.McServerSpec{ID: "filesystem", Transport: "stdio", Command: "npx", Args: []string{"-y", "@mcp/fs"}, Env: map[string]string{"ROOT": "/"}, Enabled: true, Custom: true}
	if err := s.AddMcServer(m); err != nil {
		t.Fatal(err)
	}
	// transport 校验
	if err := s.AddMcServer(sessiondata.McServerSpec{ID: "x", Transport: "grpc"}); err == nil {
		t.Fatal("非法 transport 应拒绝")
	}
	// sse 无 url 拒绝
	if err := s.AddMcServer(sessiondata.McServerSpec{ID: "x2", Transport: "sse"}); err == nil {
		t.Fatal("sse 缺 url 应拒绝")
	}
	// 挂一个工具到该服务器 → 级联删除带走它
	if err := s.AddTool(sessiondata.ToolSpec{ID: "mcp:fs-read", Desc: "fs 读", Risk: "low", Source: "mcp", Server: "filesystem", Custom: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMcServer("filesystem"); err != nil {
		t.Fatal(err)
	}
	tools, _ := s.ListTools()
	for _, x := range tools {
		if x.ID == "mcp:fs-read" {
			t.Fatal("级联删除应带走服务器的工具")
		}
	}
	// 重建服务器与工具；Agent 引用其工具时删服务器被拒（引用校验前置）
	if err := s.AddMcServer(m); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTool(sessiondata.ToolSpec{ID: "mcp:fs-read", Desc: "fs 读", Risk: "low", Source: "mcp", Server: "filesystem", Custom: true}); err != nil {
		t.Fatal(err)
	}
	agents, _ := s.ListAgents()
	for i, a := range agents {
		if a.ID == "coder" {
			agents[i].Tools = append(agents[i].Tools, "mcp:fs-read")
			if err := s.UpdateAgent(agents[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.RemoveMcServer("filesystem"); err == nil || !strings.Contains(err.Error(), "coder") {
		t.Fatalf("被引用工具的服务器删除应拒绝: %v", err)
	}
}
