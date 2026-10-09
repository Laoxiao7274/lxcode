// Git 工作树说明注入的契约：项目会话给出分支名与「每轮自动提交」语义；未分组会话
// 零注入；子会话说「共享」而不是「独立分支」。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

const testWorktreeBranch = "lxcode/session-abc"

// TestWorktreePromptProjectSession：项目会话（设了 worktree 信息）的提示词含分支名与
// 「自动提交」语义；未分组会话（零值）零注入。
func TestWorktreePromptProjectSession(t *testing.T) {
	s := newAgentSession(t)
	wt := WorktreeInfo{Branch: testWorktreeBranch, Base: "0123456"}
	prompt := BuildSystemPrompt(s.tools, "/proj/demo", wt, false, ProjectDocs{})
	if !strings.Contains(prompt, testWorktreeBranch) {
		t.Fatalf("项目会话提示词缺少分支名:\n%s", prompt)
	}
	if !strings.Contains(prompt, "自动") || !strings.Contains(prompt, "提交") {
		t.Fatalf("项目会话提示词缺少「自动提交」语义:\n%s", prompt)
	}
	if strings.Contains(prompt, "共享") {
		t.Fatalf("项目会话（非子会话）不该说「共享」:\n%s", prompt)
	}

	// 未分组会话（零值）：工作树说明零注入。
	plain := BuildSystemPrompt(s.tools, "/proj/demo", WorktreeInfo{}, false, ProjectDocs{})
	if strings.Contains(plain, "工作树") || strings.Contains(plain, testWorktreeBranch) {
		t.Fatalf("未分组会话不该注入工作树说明:\n%s", plain)
	}

	// 两条提示词路径必须都注入——漏一条会出现「同一会话不同提示词」。
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Tools: []string{"read_file"}}}
	composed := ComposeSystemPrompt(s.tools, "/proj/demo", ac, ac.Def.Tools, wt, false, ProjectDocs{})
	if !strings.Contains(composed, testWorktreeBranch) || !strings.Contains(composed, "自动") {
		t.Fatalf("ComposeSystemPrompt 未注入工作树说明:\n%s", composed)
	}
	// ComposeSystemPrompt 的旧语境回落（ac == nil）同样要注入。
	fallback := ComposeSystemPrompt(s.tools, "/proj/demo", nil, nil, wt, false, ProjectDocs{})
	if !strings.Contains(fallback, testWorktreeBranch) {
		t.Fatalf("ComposeSystemPrompt(ac=nil) 未注入工作树说明:\n%s", fallback)
	}
}

// TestWorktreePromptChildSession：子会话注入「共享」措辞，且不说「独立分支」/「独立 Git 工作树」。
func TestWorktreePromptChildSession(t *testing.T) {
	s := newAgentSession(t)
	wt := WorktreeInfo{Branch: testWorktreeBranch, Base: "0123456"}
	prompt := BuildSystemPrompt(s.tools, "/proj/demo", wt, true, ProjectDocs{})
	if !strings.Contains(prompt, "共享") {
		t.Fatalf("子会话提示词缺少「共享」措辞:\n%s", prompt)
	}
	if strings.Contains(prompt, "独立分支") || strings.Contains(prompt, "独立 Git 工作树") {
		t.Fatalf("子会话不该说「独立分支/独立工作树」:\n%s", prompt)
	}
	if !strings.Contains(prompt, testWorktreeBranch) {
		t.Fatalf("子会话提示词缺少分支名:\n%s", prompt)
	}
}
