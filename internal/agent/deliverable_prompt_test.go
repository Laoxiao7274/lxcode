// 产物交付引导（2026-12 产物交付改造）的提示词钉子：
//   - 全局守则新条目（三层共用）对三种角色都生效，且对没有 share_file 的
//     主 Agent 不误导（守则里带「没有 share_file 时引导…」的兜底半句）；
//   - share_file 工具摘要带触发条件（产出文件类产物时用它交付）；
//   - 助手协议带「专属目录文件用户无法直接访问，只有 share_file 能渲染」句；
//   - 主协议带「汇总/转述引导看附件卡、不复述路径」句。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// TestDeliverableRuleInGlobalFooter：新守则条目在守则段，三档 todo 模板下都在
//（守则编号随 off 档顺延——条目本身不许缺席）。
func TestDeliverableRuleInGlobalFooter(t *testing.T) {
	key := "产出文件类交付物"
	for _, mode := range []string{config.TodoPromptStrict, config.TodoPromptNormal, config.TodoPromptOff} {
		footer := systemPromptFooter(mode)
		if !strings.Contains(footer, key) {
			t.Fatalf("%s 档守则缺少产物交付条目（%q）", mode, key)
		}
		// 对没有 share_file 的 Agent 不误导：守则必须带无工具时的引导半句，
		// 而不能写成「你有 share_file 必须用」这种无条件要求。
		var rule string
		for _, line := range strings.Split(footer, "\n") {
			if strings.Contains(line, key) {
				rule = line
			}
		}
		if !strings.Contains(rule, "没有 share_file 时") {
			t.Fatalf("产物交付条目应带无工具引导半句: %s", rule)
		}
		if !strings.Contains(rule, "纯文本结论不需要") {
			t.Fatalf("产物交付条目应带纯文本豁免: %s", rule)
		}
	}
}

// TestDeliverableRuleInAllPromptPaths：守则是三层共用的——两条组装路径（全局
// 与 Agent 语境）都必须带上。
func TestDeliverableRuleInAllPromptPaths(t *testing.T) {
	s := newAgentSession(t)
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Tools: []string{"read_file", "share_file"}}}
	prompts := map[string]string{
		"BuildSystemPrompt":      BuildSystemPrompt(s.tools, "", WorktreeInfo{}, false, ProjectDocs{}),
		"ComposeSystemPrompt":    ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil),
		"ComposeSystemPrompt(子)": ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, true, ProjectDocs{}, nil),
	}
	for name, p := range prompts {
		if !strings.Contains(p, "产出文件类交付物") {
			t.Fatalf("%s 缺少产物交付守则", name)
		}
	}
}

// TestShareFileToolSummaryHasTrigger：share_file 的行内摘要带触发条件——
// 只说「能共享」不说「什么时候该用」，模型产出文件后还是只报路径。
func TestShareFileToolSummaryHasTrigger(t *testing.T) {
	summary := systemPromptTools["share_file"]
	if !strings.Contains(summary, "share_file：") {
		t.Fatalf("摘要应存在: %q", summary)
	}
	for _, want := range []string{"文件类产物", "交付"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("share_file 摘要应带触发条件（%q 缺席）: %s", want, summary)
		}
	}
}

// TestAssistantProtocolDeliverableLine：助手协议带专属目录交付句——助手产出的
// 文件用户无法直接访问，只有 share_file 能渲染到对话。
func TestAssistantProtocolDeliverableLine(t *testing.T) {
	p := DefaultProtocol(ProtocolRoleAssistant)
	for _, want := range []string{"专属目录", "只有 share_file", "渲染到对话"} {
		if !strings.Contains(p, want) {
			t.Fatalf("助手协议缺少产物交付引导（%q 缺席）:\n%s", want, p)
		}
	}
}

// TestMainProtocolDispatchAttachmentLine：主协议带转述引导——引导用户看派发卡
// 附件、不复述路径；子会话没共享时要求它补 share_file。主 Agent 不持有
// share_file，这句说的全是转述/派发动作，与它的工具面自洽。
func TestMainProtocolDispatchAttachmentLine(t *testing.T) {
	p := DefaultProtocol(ProtocolRoleMain)
	for _, want := range []string{"[附件]", "附件卡", "不复述路径", "share_file"} {
		if !strings.Contains(p, want) {
			t.Fatalf("主协议缺少产物交付引导（%q 缺席）:\n%s", want, p)
		}
	}
}
