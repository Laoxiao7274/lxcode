// todo-first 行为指令的测试：主会话与子会话组装出的系统提示词都必须
// 包含同一段 todo-first 关键句（基础层 = systemPromptFooter，两条组装
// 路径——BuildSystemPrompt / ComposeSystemPrompt——都在末尾追加它），
// 且 todo 工具的 description 含「动手前先建清单」文案（模型对工具
// description 的遵从度高，与提示词层互为冗余）。
package agent

import (
	"strconv"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// todoFirstKey 是提示词层的行为指令关键句（改措辞时这里同步改）。
// strict 档 2026-12 改写为 todo 契约的轮次纪律（每轮审视 / 不攒批 / 收尾一致）。
const todoFirstKey = "每轮开始先审视任务清单：本轮是新任务就用 todo 工具先出清单"

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
	if !strings.Contains(d.Description, "维护当前任务清单") {
		t.Fatalf("todo 工具 description 缺少「维护当前任务清单」：%s", d.Description)
	}
	if !strings.Contains(d.Description, "每完成一步立即更新状态") {
		t.Fatalf("todo 工具 description 缺少「先列清单再执行」语义：%s", d.Description)
	}
}

// ---- 三档模板（用户可选——config.TodoPrompt*）----

func TestTodoPromptModesInMainPrompt(t *testing.T) {
	s := newAgentSession(t)
	// strict（默认档，缺省参数 = strict）：现有关键句在场
	p := BuildSystemPrompt(s.tools, "", WorktreeInfo{}, false, ProjectDocs{})
	if !strings.Contains(p, todoFirstKey) {
		t.Fatal("strict 主会话提示词缺少 todo-first 关键句")
	}
	// normal：常规版关键句在场；strict 硬约束与 strict 专属措辞缺席
	p = BuildSystemPrompt(s.tools, "", WorktreeInfo{}, false, ProjectDocs{}, config.TodoPromptNormal)
	if !strings.Contains(p, "按任务复杂度用清单：需要多步工具调用才能完成的任务") {
		t.Fatal("normal 主会话提示词缺少常规版关键句")
	}
	if strings.Contains(p, todoFirstKey) || strings.Contains(p, "只有两三步也要先建") || strings.Contains(p, "每轮开始先审视任务清单") {
		t.Fatal("normal 档不应包含 strict 的硬约束措辞")
	}
	if !strings.Contains(p, "不为凑数拆分") {
		t.Fatal("normal 档缺少「条数与复杂度相称」语义")
	}
	// off：两档文案都不出现，编号顺延（守则不应留空号）
	p = BuildSystemPrompt(s.tools, "", WorktreeInfo{}, false, ProjectDocs{}, config.TodoPromptOff)
	if strings.Contains(p, todoFirstKey) || strings.Contains(p, "按任务复杂度用清单") {
		t.Fatal("off 档不应包含任何 todo-first 指令")
	}
	// 编号顺延 = 不留空号（off 档少一条，后面的条目整体前移）。
	// 断言连续性而不是写死「第 N 条」：守则会继续增删，写死编号的测试会在
	// 每次改守则时误报，而真正要钉的是「不出现 1,2,4,5 这种断号」。
	if got := ruleNumbers(p); !consecutiveFromOne(got) {
		t.Fatalf("off 档守则编号应顺延且不留空号，实际 %v", got)
	}
	if !strings.Contains(p, ". 修改代码") {
		t.Fatal("off 档「修改代码」条目应存在")
	}
}

func TestTodoPromptModesInChildPrompt(t *testing.T) {
	s := newAgentSession(t)
	// 子会话同源：两条组装路径 × 三档各断言一次
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", IsMain: false, Tools: []string{"read_file"}}}
	cases := []struct {
		mode    string
		wantKey string
		banKey  string
	}{
		{config.TodoPromptStrict, todoFirstKey, "按任务复杂度用清单"},
		{config.TodoPromptNormal, "按任务复杂度用清单：需要多步工具调用才能完成的任务", todoFirstKey},
		{config.TodoPromptOff, "", todoFirstKey},
	}
	for _, c := range cases {
		for _, path := range []struct {
			name   string
			prompt string
		}{
			{"BuildSystemPrompt(isChild)", BuildSystemPrompt(s.tools, "", WorktreeInfo{}, true, ProjectDocs{}, c.mode)},
			{"ComposeSystemPrompt(isChild)", ComposeSystemPrompt(s.tools, "", ac, ac.Def.Tools, WorktreeInfo{}, true, ProjectDocs{}, c.mode)},
		} {
			if c.wantKey != "" && !strings.Contains(path.prompt, c.wantKey) {
				t.Fatalf("%s 档子会话（%s）缺少关键句 %q", c.mode, path.name, c.wantKey)
			}
			if strings.Contains(path.prompt, c.banKey) {
				t.Fatalf("%s 档子会话（%s）不应包含 %q", c.mode, path.name, c.banKey)
			}
		}
	}
}

// off 档是「整条不出现」而不是「留一个空号」：strict 与 off 的守则条数差 1。
func TestTodoPromptOffDropsExactlyOneRule(t *testing.T) {
	s := newAgentSession(t)
	strict := BuildSystemPrompt(s.tools, "", WorktreeInfo{}, false, ProjectDocs{}, config.TodoPromptStrict)
	off := BuildSystemPrompt(s.tools, "", WorktreeInfo{}, false, ProjectDocs{}, config.TodoPromptOff)
	if strings.Count(strict, "\n")-strings.Count(off, "\n") != 1 {
		t.Fatalf("off 档应恰好少一条守则：strict %d 行 / off %d 行",
			strings.Count(strict, "\n"), strings.Count(off, "\n"))
	}
}

// 会话档位：Send 经 WithTodoPrompt 存进会话（空 = 不覆盖），TodoPromptMode
// 归一缺省。子会话派发继承（dispatch.go 两处 SendWait 都带 WithTodoPrompt）。
func TestSessionTodoPromptMode(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	fake := &fakeStream{script: [][]llm.StreamEvent{
		textResult("第一轮"),
		textResult("第二轮"),
	}}
	s.stream = fake.stream
	if got := s.TodoPromptMode(); got != config.TodoPromptStrict {
		t.Fatalf("缺省档位应为 strict，got %q", got)
	}
	if err := s.Send("你好", WithTodoPrompt(config.TodoPromptOff)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if got := s.TodoPromptMode(); got != config.TodoPromptOff {
		t.Fatalf("WithTodoPrompt 应覆盖会话档位，got %q", got)
	}
	// 不带参数的内部开轮（通告 flushNotices 等）不重置用户档位
	if err := s.Send("再来一句"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	if got := s.TodoPromptMode(); got != config.TodoPromptOff {
		t.Fatalf("不带参数的 Send 不应重置档位，got %q", got)
	}
}

// ruleNumbers 抽出提示词里「工作守则」段的条目编号（按出现顺序）。
//
// 只认行首 `数字. `：工具清单是 `- ` 开头，正文里的序号不会被误认。
func ruleNumbers(prompt string) []int {
	var nums []int
	inRules := false
	for _, line := range strings.Split(prompt, "\n") {
		if line == "工作守则：" {
			inRules = true
			continue
		}
		if !inRules {
			continue
		}
		dot := strings.Index(line, ". ")
		if dot <= 0 {
			continue
		}
		n, err := strconv.Atoi(line[:dot])
		if err != nil {
			continue
		}
		nums = append(nums, n)
	}
	return nums
}

// consecutiveFromOne 报告编号是否恰好是 1..N（无空号、无重复）。
func consecutiveFromOne(nums []int) bool {
	if len(nums) == 0 {
		return false
	}
	for i, n := range nums {
		if n != i+1 {
			return false
		}
	}
	return true
}
