// 工作区说明注入的契约（2026-10 直连架构 + 轮末自动提交已移除）：项目会话说
// 「直接在用户的项目目录工作」+ 分支名 +「系统不会自动提交、显式要求才提交」的
// 语义，**没有**「独立工作树/合并」的表述；未分组会话零注入；子会话说「共享」。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

const testWorktreeBranch = "main"

// TestWorktreePromptProjectSession：项目会话（设了工作区信息）的提示词含「直接在
// 用户的项目目录」、分支名与「不会自动提交」语义；不再有独立工作树/合并进程/merge_request
// 的表述；未分组会话（零值）零注入。
func TestWorktreePromptProjectSession(t *testing.T) {
	s := newAgentSession(t)
	wt := WorktreeInfo{Branch: testWorktreeBranch, Base: "0123456"}
	prompt := BuildSystemPrompt(s.tools, "/proj/demo", wt, false, ProjectDocs{})
	if !strings.Contains(prompt, "直接在用户的项目目录") {
		t.Fatalf("项目会话提示词缺少「直接在用户的项目目录」:\n%s", prompt)
	}
	if !strings.Contains(prompt, testWorktreeBranch) {
		t.Fatalf("项目会话提示词缺少分支名:\n%s", prompt)
	}
	if !strings.Contains(prompt, "自动") || !strings.Contains(prompt, "提交") {
		t.Fatalf("项目会话提示词缺少「自动提交」语义:\n%s", prompt)
	}
	if !strings.Contains(prompt, "workspace_status") || !strings.Contains(prompt, "workspace_sync") || !strings.Contains(prompt, "workspace_rollback") {
		t.Fatalf("项目会话提示词缺少三个 workspace 工具的指引:\n%s", prompt)
	}
	for _, banned := range []string{"独立 Git 工作树", "独立工作树", "合并进程", "集成分支"} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("项目会话提示词不该再出现 %q:\n%s", banned, prompt)
		}
	}
	// merge_request 只允许以「遗留/已退役」的口径出现（工具清单里保留如实说明）。
	if idx := strings.Index(prompt, "merge_request"); idx >= 0 {
		if !strings.Contains(prompt[idx:], "遗留") && !strings.Contains(prompt[idx:], "退役") {
			t.Fatalf("merge_request 只能以遗留口径出现:\n%s", prompt)
		}
	}
	// 「共享」曾是旧工作树架构的措辞（"在共享的检出里工作"）——直连后项目会话
	// 提示词不该再出现**工作区说明层**的「共享」。share_file 的工具摘要合法地
	// 含「共享到对话」（那是工具清单不是工作区说明），所以钉的是旧句式。
	if strings.Contains(prompt, "在共享的检出") || strings.Contains(prompt, "共享的检出") {
		t.Fatalf("项目会话（非子会话）不该说「共享的检出」:\n%s", prompt)
	}

	// 未分组会话（零值）：工作区说明零注入。（判据用 worktreeLine 特有的
	// 措辞与分支名，不能用裸「工作树」——workspace_publish 的工具摘要里
	// 合法地出现「会话工作树」，那是工具清单不是工作区说明注入。）
	plain := BuildSystemPrompt(s.tools, "/proj/demo", WorktreeInfo{}, false, ProjectDocs{})
	if strings.Contains(plain, "项目目录里工作") || strings.Contains(plain, testWorktreeBranch) {
		t.Fatalf("未分组会话不该注入工作区说明:\n%s", plain)
	}

	// 两条提示词路径必须都注入——漏一条会出现「同一会话不同提示词」。
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Tools: []string{"read_file"}}}
	composed := ComposeSystemPrompt(s.tools, "/proj/demo", ac, ac.Def.Tools, wt, false, ProjectDocs{})
	if !strings.Contains(composed, "直接在用户的项目目录") || !strings.Contains(composed, "不会**自动提交") {
		t.Fatalf("ComposeSystemPrompt 未注入工作区说明:\n%s", composed)
	}
	// ComposeSystemPrompt 的旧语境回落（ac == nil）同样要注入。
	fallback := ComposeSystemPrompt(s.tools, "/proj/demo", nil, nil, wt, false, ProjectDocs{})
	if !strings.Contains(fallback, "直接在用户的项目目录") {
		t.Fatalf("ComposeSystemPrompt(ac=nil) 未注入工作区说明:\n%s", fallback)
	}
}

// TestWorktreePromptPushDiscipline：项目会话注入推送纪律（推送只能走 workspace_sync、
// 永不直接 git push、本体仓库发版走 push-release.mjs）；未分组会话（零值）零注入。
func TestWorktreePromptPushDiscipline(t *testing.T) {
	s := newAgentSession(t)
	wt := WorktreeInfo{Branch: testWorktreeBranch, Base: "0123456"}
	prompt := BuildSystemPrompt(s.tools, "/proj/demo", wt, false, ProjectDocs{})
	for _, want := range []string{"推送纪律", "永远不要直接执行 `git push`", "scripts/push-release.mjs"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("项目会话提示词缺少推送纪律 %q:\n%s", want, prompt)
		}
	}
	// 未分组会话：没有工作区说明，也就没有推送纪律（零注入的既有契约）。
	plain := BuildSystemPrompt(s.tools, "/proj/demo", WorktreeInfo{}, false, ProjectDocs{})
	if strings.Contains(plain, "推送纪律") {
		t.Fatalf("未分组会话不该注入推送纪律:\n%s", plain)
	}
}

// TestWorktreePromptChildSession：子会话注入「共享」措辞，且不说「独立分支」/「独立工作树」。
func TestWorktreePromptChildSession(t *testing.T) {
	s := newAgentSession(t)
	wt := WorktreeInfo{Branch: testWorktreeBranch, Base: "0123456"}
	prompt := BuildSystemPrompt(s.tools, "/proj/demo", wt, true, ProjectDocs{})
	if !strings.Contains(prompt, "共享") {
		t.Fatalf("子会话提示词缺少「共享」措辞:\n%s", prompt)
	}
	if strings.Contains(prompt, "独立分支") || strings.Contains(prompt, "独立工作树") {
		t.Fatalf("子会话不该说「独立分支/独立工作树」:\n%s", prompt)
	}
	if !strings.Contains(prompt, testWorktreeBranch) {
		t.Fatalf("子会话提示词缺少分支名:\n%s", prompt)
	}
}

// TestWorktreePromptNoGit：项目目录不是 git 仓库的降级态——如实告知「改动不会被
// 提交、不要执行 git 操作」，且不能声称自动提交（自动提交已移除，更不能假称存在）。
func TestWorktreePromptNoGit(t *testing.T) {
	s := newAgentSession(t)
	wt := WorktreeInfo{NoGit: true}
	prompt := BuildSystemPrompt(s.tools, "/proj/demo", wt, false, ProjectDocs{})
	if !strings.Contains(prompt, "不是 Git 仓库") {
		t.Fatalf("非 git 项目会话提示词应如实告知:\n%s", prompt)
	}
	if strings.Contains(prompt, "系统会自动提交") {
		t.Fatalf("非 git 项目会话不该声称自动提交:\n%s", prompt)
	}
	// 工作区说明层不该出现「Git 分支 <名>」的表述（没有分支可说）。
	if strings.Contains(prompt, "（Git 分支 ") {
		t.Fatalf("非 git 项目会话不该声称工作在某分支:\n%s", prompt)
	}
	// 子会话同样告知（非 git 标志随父会话继承）。
	child := BuildSystemPrompt(s.tools, "/proj/demo", wt, true, ProjectDocs{})
	if !strings.Contains(child, "不是 Git 仓库") {
		t.Fatalf("非 git 子会话提示词应如实告知:\n%s", child)
	}
}
