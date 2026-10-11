// todo-first 行为指令的测试：主会话与子会话组装出的系统提示词都必须
// 包含同一段 todo-first 关键句（基础层 = systemPromptFooter，两条组装
// 路径——BuildSystemPrompt / ComposeSystemPrompt——都在末尾追加它），
// 且 todo 工具的 description 含「动手前先建清单」文案（模型对工具
// description 的遵从度高，与提示词层互为冗余）。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// todoFirstKey 是提示词层的行为指令关键句（改措辞时这里同步改）。
const todoFirstKey = "动手前先建清单：凡是需要调用工具（写文件、改代码、跑命令）的任务，开始前先用 todo 工具列出步骤清单"

func TestTodoFirstInMainPrompt(t *testing.T) {
	s := newAgentSession(t)
	// 主会话：全局默认路径（无 Agent 语境）
	if p := BuildSystemPrompt(s.tools, "", WorktreeInfo{}, false, ProjectDocs{}); !strings.Contains(p, todoFirstKey) {
		t.Fatal("主会话（BuildSystemPrompt）提示词缺少 todo-first 关键句")
	}
	// 主会话：Agent 四层组合路径（定制协议整段替换也不影响——守则在基础层）
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true}}
	if p := ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, false, ProjectDocs{}); !strings.Contains(p, todoFirstKey) {
		t.Fatal("主会话（ComposeSystemPrompt）提示词缺少 todo-first 关键句")
	}
}

func TestTodoFirstInChildPrompt(t *testing.T) {
	s := newAgentSession(t)
	// 子会话：BuildSystemPrompt 的 isChild=true 路径（未分组子会话回落）
	if p := BuildSystemPrompt(s.tools, "", WorktreeInfo{}, true, ProjectDocs{}); !strings.Contains(p, todoFirstKey) {
		t.Fatal("子会话（BuildSystemPrompt isChild）提示词缺少 todo-first 关键句")
	}
	// 子会话：dispatch/WithAgent 的真实路径（子 Agent = 独立会话，同一套组合）
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", IsMain: false, Tools: []string{"read_file"}}}
	if p := ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, true, ProjectDocs{}); !strings.Contains(p, todoFirstKey) {
		t.Fatal("子会话（ComposeSystemPrompt isChild）提示词缺少 todo-first 关键句")
	}
}

func TestTodoToolDescriptionSaysBuildFirst(t *testing.T) {
	s := newAgentSession(t)
	d, ok := s.tools.Get("todo")
	if !ok {
		t.Fatal("todo 工具未注册")
	}
	if !strings.Contains(d.Description, "动手前先建清单") {
		t.Fatalf("todo 工具 description 缺少「动手前先建清单」：%s", d.Description)
	}
	if !strings.Contains(d.Description, "再执行") {
		t.Fatalf("todo 工具 description 缺少「先列清单再执行」语义：%s", d.Description)
	}
}
