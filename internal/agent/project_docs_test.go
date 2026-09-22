// 项目守则（项目根 AGENTS.md）注入的契约：位置体现优先级、必须标来源、
// 没有就不加噪声、未分组会话绝不注入（严格项目级）。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

const docsMarker = "提交信息一律用中文，禁止英文动词开头。"

// TestProjectDocsInjectedAfterAgentLayers：项目约定必须排在 Agent 自身四层
// 之后（用户拍板「项目约定比 Agent 自己的低」），且在工具清单之前。
func TestProjectDocsInjectedAfterAgentLayers(t *testing.T) {
	s := newAgentSession(t)
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{
		ID: "coder", Name: "代码 Agent", Tools: []string{"read_file"},
		Protocol: "定制协议：只写 Go。", Prompt: "自定义段：先读再改。",
	}}
	docs := ProjectDocs{Path: `C:\proj\demo\AGENTS.md`, Content: docsMarker}
	prompt := ComposeSystemPrompt(s.tools, `C:\proj\demo`, ac, ac.Def.Tools, docs)

	idx := func(sub string) int {
		i := strings.Index(prompt, sub)
		if i < 0 {
			t.Fatalf("提示词缺少 %q:\n%s", sub, prompt)
		}
		return i
	}
	protocolAt := idx("定制协议：只写 Go。")
	customAt := idx("自定义段：先读再改。")
	docsAt := idx(docsMarker)
	toolsAt := idx("可用工具：")

	if !(protocolAt < docsAt && customAt < docsAt) {
		t.Fatal("项目约定必须排在 Agent 自身四层之后（优先级更低）")
	}
	if docsAt > toolsAt {
		t.Fatal("项目约定应排在环境说明与工具清单之前")
	}
	// 来源与优先级必须显式（模型据此分清「项目约定」与「用户指令」）
	if !strings.Contains(prompt, `C:\proj\demo\AGENTS.md`) {
		t.Fatal("必须标明守则来源路径")
	}
	if !strings.Contains(prompt, "优先级低于上面的 Agent 设定") {
		t.Fatal("必须显式声明优先级低于 Agent 设定")
	}
	if !strings.Contains(prompt, "以用户为准") {
		t.Fatal("必须声明与用户当前指令冲突时以用户为准")
	}
}

// TestProjectDocsAbsentNoNoise：没有守则（未分组会话、项目没写、文件为空）
// 时不该出现任何相关噪声——提示词与加注入前一致。
func TestProjectDocsAbsentNoNoise(t *testing.T) {
	s := newAgentSession(t)
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Tools: []string{"read_file"}}}

	base := ComposeSystemPrompt(s.tools, "/proj/demo", ac, ac.Def.Tools, ProjectDocs{})
	if strings.Contains(base, "项目约定") {
		t.Fatal("没有守则时不该出现「项目约定」段")
	}
	empty := ComposeSystemPrompt(s.tools, "/proj/demo", ac, ac.Def.Tools, ProjectDocs{Path: "/proj/demo/AGENTS.md", Content: "   \n\n"})
	if empty != base {
		t.Fatal("守则内容为空白时，提示词应与无守则时逐字节一致")
	}
	// 旧语境（无 Agent）同样适用
	if p := BuildSystemPrompt(s.tools, "/proj/demo", ProjectDocs{}); strings.Contains(p, "项目约定") {
		t.Fatal("无 Agent 语境下没有守则时不该出现「项目约定」段")
	}
}

// TestProjectDocsNoteSurfaced：读不到/超大时如实告知（Note），而不是静默当没有——
// 用户明明写了守则却没生效，得让模型和用户都看得见原因。
func TestProjectDocsNoteSurfaced(t *testing.T) {
	s := newAgentSession(t)
	prompt := BuildSystemPrompt(s.tools, "/proj/demo", ProjectDocs{
		Path: "/proj/demo/AGENTS.md", Note: "文件 2097152 字节，超过 1048576 字节上限，已跳过",
	})
	if !strings.Contains(prompt, "项目约定") || !strings.Contains(prompt, "已跳过") {
		t.Fatalf("跳过原因应出现在提示词里:\n%s", prompt)
	}
}

// TestProjectDocsTruncated：超长守则截断并注明（模型要知道「后面还有」）。
func TestProjectDocsTruncated(t *testing.T) {
	s := newAgentSession(t)
	long := strings.Repeat("规则。", projectDocsCap) // 远超上限
	prompt := BuildSystemPrompt(s.tools, "/proj/demo", ProjectDocs{Path: "/proj/demo/AGENTS.md", Content: long})
	if !strings.Contains(prompt, "已截断") {
		t.Fatal("超长守则应截断并注明")
	}
	// 注入正文不得超过上限（留出注明行的余量）
	if got := len(prompt); got > len(systemPromptHeader)+len(systemPromptFooter)+projectDocsCap+4096 {
		t.Fatalf("截断后提示词仍过大: %d 字节", got)
	}
}

// TestProjectDocsScopeProjectOnly：读取器只在有项目工作目录时被调用——
// 未分组会话（workDir 为空）绝不读盘（否则会把后端进程目录的 AGENTS.md，
// 比如 lxcode 自己的仓库守则，塞进用户无关的对话）。
func TestProjectDocsScopeProjectOnly(t *testing.T) {
	s := newAgentSession(t)
	calls := 0
	s.SetProjectDocs(func(workDir string) ProjectDocs {
		calls++
		return ProjectDocs{Path: workDir + "/AGENTS.md", Content: docsMarker}
	})
	if got := s.projectDocsFor(""); got.Content != "" {
		t.Fatalf("未分组会话不该注入守则: %+v", got)
	}
	if calls != 0 {
		t.Fatalf("未分组会话不该触发读取（实际调用 %d 次）", calls)
	}
	if got := s.projectDocsFor("/proj/demo"); got.Content != docsMarker {
		t.Fatalf("项目会话应读到守则: %+v", got)
	}
	if calls != 1 {
		t.Fatalf("项目会话应触发一次读取（实际 %d 次）", calls)
	}
}

// TestProjectDocsPerRoundFreshness：守则每轮现读——改了立刻生效（长命会话里
// 「我刚改了 AGENTS.md 它却不知道」是更糟的体验）。
func TestProjectDocsPerRoundFreshness(t *testing.T) {
	s := newAgentSession(t)
	current := "第一版守则"
	s.SetProjectDocs(func(string) ProjectDocs {
		return ProjectDocs{Path: "/proj/demo/AGENTS.md", Content: current}
	})
	if got := s.projectDocsFor("/proj/demo").Content; got != "第一版守则" {
		t.Fatalf("首次读取: %q", got)
	}
	current = "第二版守则"
	if got := s.projectDocsFor("/proj/demo").Content; got != "第二版守则" {
		t.Fatalf("守则改动后应立刻生效: %q", got)
	}
}
