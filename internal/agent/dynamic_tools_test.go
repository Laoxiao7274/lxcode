// M4 动态工具（自定义/binary）在提示词层的契约：
// 注册表里的工具必须出现在清单里（不回落到 Description 就会被静默漏掉），
// 白名单勾了但没注册的工具必须被点名（否则模型以为它有）。
package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// dynamicDef 造一个注册表里的动态工具（描述带换行——清单只取首行）。
func dynamicDef(t *testing.T, name, desc string) *tools.Def {
	t.Helper()
	d, err := tools.CustomDef(sessiondata.ToolSpec{
		ID: name, Desc: desc, Risk: "low", Source: "binary", Command: "rg {pattern}",
		Params: []sessiondata.ToolParam{{Name: "pattern", Type: "regex", Required: true}},
	})
	if err != nil {
		t.Fatalf("CustomDef(%s): %v", name, err)
	}
	return d
}

// TestPromptListsDynamicTool：目录里的自定义工具进了注册表就必须出现在工具清单里
// （白名单勾了它，模型却不知道它存在 = 白勾）。
func TestPromptListsDynamicTool(t *testing.T) {
	s := newAgentSession(t)
	if skipped := s.tools.SetDynamic([]*tools.Def{dynamicDef(t, "rg_deep", "深度检索\n示例: rg_deep --all")}); len(skipped) != 0 {
		t.Fatalf("不应跳过: %v", skipped)
	}

	// 无 Agent 语境的默认提示词（BuildSystemPrompt 路径）
	base := BuildSystemPrompt(s.tools, "", ProjectDocs{})
	if !strings.Contains(base, "rg_deep") {
		t.Fatal("动态工具未出现在默认工具清单里（静默漏掉）")
	}
	if !strings.Contains(base, "深度检索") {
		t.Fatal("动态工具的一行说明应回落到 Description")
	}
	if strings.Contains(base, "示例: rg_deep --all") {
		t.Fatal("清单一行一条，不该带 Description 的后续行")
	}

	// Agent 语境（ComposeSystemPrompt 路径 + 白名单过滤）
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{
		ID: "coder", Name: "代码 Agent", Tools: []string{"read_file", "rg_deep"},
	}}
	prompt := ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, ProjectDocs{})
	if !strings.Contains(prompt, "rg_deep") {
		t.Fatal("白名单里的动态工具未出现在 Agent 提示词里")
	}
	if !strings.Contains(prompt, "低危") {
		t.Fatal("动态工具的一行应带风险说明")
	}
	if strings.Contains(prompt, "bash：") {
		t.Fatal("白名单外的工具不该出现在清单里")
	}
}

// TestPromptWarnsMissingWhitelistTool：白名单里勾了但注册表没有（自定义工具没配
// command、MCP 服务器没启用）必须如实告知——否则模型会去调一个不存在的工具。
func TestPromptWarnsMissingWhitelistTool(t *testing.T) {
	s := newAgentSession(t)
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{
		ID: "coder", Name: "代码 Agent", Tools: []string{"read_file", "ripgrep"},
	}}
	prompt := ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, ProjectDocs{})
	if !strings.Contains(prompt, "ripgrep") || !strings.Contains(prompt, "当前不可用") {
		t.Fatalf("未注册的白名单工具应被点名，实际提示词片段:\n%s", tail(prompt, 400))
	}
	// 全部可用时不该出现这句噪声
	ok := &sessiondata.AgentContext{Def: sessiondata.AgentDef{
		ID: "coder", Name: "代码 Agent", Tools: []string{"read_file", "edit"},
	}}
	if p := ComposeSystemPrompt(s.tools, "", ok, ok.Def.Tools, ProjectDocs{}); strings.Contains(p, "当前不可用") {
		t.Fatal("白名单全部可用时不该出现「不可用」提示")
	}
}

// TestPromptWireDeclaresDynamicTool：动态工具必须进 wire 声明（模型据此发起调用）。
func TestPromptWireDeclaresDynamicTool(t *testing.T) {
	s := newAgentSession(t)
	s.tools.SetDynamic([]*tools.Def{dynamicDef(t, "rg_deep", "深度检索")})
	var found bool
	for _, tw := range s.tools.LLMTools() {
		if tw.Name != "rg_deep" {
			continue
		}
		found = true
		if !json.Valid(tw.Parameters) {
			t.Fatalf("动态工具的参数 schema 非法: %s", tw.Parameters)
		}
	}
	if !found {
		t.Fatal("动态工具未进 wire 声明")
	}
}

// TestBuiltinToolsHaveCuratedLine：内置工具必须有手写的一句话摘要
// （systemPromptTools——「有什么、风险等级」的守则层表述）。Description 回落
// 只服务动态工具：内置工具落到回落上就等于丢了这条表述，而 BuildSystemPrompt
// 仍会列出它（测试不会红）——所以这条守卫必须单独钉住。
func TestBuiltinToolsHaveCuratedLine(t *testing.T) {
	for _, name := range tools.New().Order() {
		if _, ok := systemPromptTools[name]; !ok {
			t.Fatalf("内置工具 %s 缺少 systemPromptTools 摘要（回落到 Description 会丢失风险等级表述）", name)
		}
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
