// workdirLine 的语境措辞钉子：助手语境不能说「归属一个项目」——助手会话
// 没有项目归属，工作目录是它的专属目录（<sessions>/../assistant）。主域
// 语境措辞保持不变（项目根 = 工作目录）。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func TestWorkdirLineAssistantContext(t *testing.T) {
	dir := `C:\data\lxcode\assistant`
	// 助手语境：不说「归属一个项目」，说出专属目录与产出位置
	a := workdirLine(dir, true)
	if strings.Contains(a, "归属一个项目") {
		t.Fatalf("助手语境不应说「归属一个项目」:\n%s", a)
	}
	if !strings.Contains(a, dir) || !strings.Contains(a, "专属目录") {
		t.Fatalf("助手语境应说明专属目录与路径:\n%s", a)
	}
	// 助手语境但未装配（空 = 旧行为，中性句）
	if got := workdirLine("", true); !strings.Contains(got, "启动后端的目录") {
		t.Fatalf("未装配时应回落中性句:\n%s", got)
	}
	// 主域语境：措辞保持不变
	p := workdirLine(dir, false)
	if !strings.Contains(p, "归属一个项目") || !strings.Contains(p, dir) {
		t.Fatalf("主域语境应保持项目措辞:\n%s", p)
	}
	// composeSystemPrompt 按 Space 推导：助手语境走专属目录句
	s := newAgentSession(t)
	ac := assistantACFixtures().assistant
	prompt := composeSystemPrompt(s.tools, dir, ac, ac.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil, ActivitySnapshot{})
	if strings.Contains(prompt, "归属一个项目") {
		t.Fatalf("助手 Agent 的提示词不应出现项目措辞:\n%s", prompt)
	}
	if !strings.Contains(prompt, "专属目录") {
		t.Fatalf("助手 Agent 的提示词应说明专属目录:\n%s", prompt)
	}
	// 主域语境（IsMain）走项目句
	mainAC := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true}}
	mprompt := composeSystemPrompt(s.tools, dir, mainAC, mainAC.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}, nil, ActivitySnapshot{})
	if !strings.Contains(mprompt, "归属一个项目") {
		t.Fatalf("主域提示词应保持项目措辞:\n%s", mprompt)
	}
}

// assistantACFixtures 提供测试用的助手 Agent 语境。
func assistantACFixtures() struct{ assistant *sessiondata.AgentContext } {
	return struct{ assistant *sessiondata.AgentContext }{
		assistant: &sessiondata.AgentContext{
			Def: sessiondata.AgentDef{ID: "assistant", Name: "助手", Space: sessiondata.SpaceAssistant,
				Tools: []string{"read_file"}},
		},
	}
}
