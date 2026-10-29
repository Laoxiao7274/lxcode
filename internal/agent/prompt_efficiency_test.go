// 效率纪律四条的钉子（2026-12，参考 DeepSeek 自家 preset 的「工作方式与效率」段）。
//
// 为什么钉在守则层：这四条是**所有 Agent 共用**的（主会话 / 子会话 / 各定制
// Agent 都读同一份 systemPromptFooter）——写进某个 Agent 的模板只会影响那一个，
// 而「先便宜后贵」「别死磕」「别顺手改」是通用工作方式，不是某个角色的职责。
//
// 四条各自要钉的语义（措辞可改，语义不许丢）：
//  1. 验证由便宜到贵：针对性检查 → 最后跑一次全量，不反复全量；
//  2. 并行无副作用操作：独立且无副作用的调用同批发出；
//  3. 不死磕：同一问题两次未解决必须换策略；
//  4. 改动范围最小：只动必需部分，范围外问题记入总结。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// efficiencyKeys 是四条效率纪律的关键句片段（改措辞时这里同步改）。
var efficiencyKeys = []struct {
	name string
	key  string
}{
	{"验证由便宜到贵", "验证由便宜到贵"},
	{"并行无副作用操作", "并行无副作用操作"},
	{"不死磕（两次阈值）", "尝试两次仍未解决时，必须换策略"},
	{"改动范围最小", "改动范围最小"},
}

// TestEfficiencyRulesInGlobalFooter 钉「四条都在全局守则里」——三档 todo 模板
// 只影响第 5 条，四条效率纪律在任何档位都必须存在。
func TestEfficiencyRulesInGlobalFooter(t *testing.T) {
	for _, mode := range []string{config.TodoPromptStrict, config.TodoPromptNormal, config.TodoPromptOff} {
		footer := systemPromptFooter(mode)
		for _, c := range efficiencyKeys {
			if !strings.Contains(footer, c.key) {
				t.Fatalf("%s 档守则缺少效率纪律「%s」（关键句 %q）", mode, c.name, c.key)
			}
		}
	}
}

// TestEfficiencyRulesInBothPromptPaths 钉两条组装路径都带上（主会话与子会话
// 走的是不同的组装函数，只改一处会让另一半静默失效）。
func TestEfficiencyRulesInBothPromptPaths(t *testing.T) {
	s := newAgentSession(t)
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Tools: []string{"read_file"}}}
	prompts := map[string]string{
		"BuildSystemPrompt(主)":             BuildSystemPrompt(s.tools, "", WorktreeInfo{}, false, ProjectDocs{}),
		"BuildSystemPrompt(子)":             BuildSystemPrompt(s.tools, "", WorktreeInfo{}, true, ProjectDocs{}),
		"ComposeSystemPrompt(主)":           ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}),
		"ComposeSystemPrompt(子)":           ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, true, ProjectDocs{}),
		"ComposeSystemPrompt(主, custom模板)": ComposeSystemPrompt(s.tools, "", withCustomPrompt(ac), ac.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}),
	}
	for name, p := range prompts {
		for _, c := range efficiencyKeys {
			if !strings.Contains(p, c.key) {
				t.Fatalf("%s 缺少效率纪律「%s」", name, c.name)
			}
		}
	}
}

// TestEfficiencyRulesStayGeneric 钉「措辞通用」：守则是所有 Agent 读的，
// 写死某个 Agent 的职责（如「你是 coder」「交给测试 Agent 跑全量」）会让
// 另一个 Agent 读到自相矛盾的指令。
func TestEfficiencyRulesStayGeneric(t *testing.T) {
	footer := systemPromptFooter(config.TodoPromptStrict)
	// 四条效率纪律必须出现在守则段（而不是散落在某个 Agent 模板里）
	if !strings.Contains(footer, "工作守则：") {
		t.Fatal("效率纪律应挂在工作守则段")
	}
	// 验证那条必须保留「按职责分工」的边界——否则测试 Agent 会读到
	// 「至少到能编译」这种对自己不成立的底线
	var verifyRule string
	for _, line := range strings.Split(footer, "\n") {
		if strings.Contains(line, "验证由便宜到贵") {
			verifyRule = line
		}
	}
	if verifyRule == "" {
		t.Fatal("缺少「验证由便宜到贵」条目")
	}
	if !strings.Contains(verifyRule, "职责分工") {
		t.Fatalf("验证纪律应保留职责分工边界（不与编译底线/测试 Agent 的职责打架）: %s", verifyRule)
	}
}

// withCustomPrompt 造一个带自定义提示词模板的 Agent 载荷（组合路径整段替换
// 模板，但守则在基础层——这条正是要钉的：模板替换不影响守则）。
func withCustomPrompt(ac *sessiondata.AgentContext) *sessiondata.AgentContext {
	cp := *ac
	cp.Def.Prompt = "你是一个只做检索的 Agent，不要写代码。"
	return &cp
}
