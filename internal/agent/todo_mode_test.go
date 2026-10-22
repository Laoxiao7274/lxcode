// todo off 档的行为钉子：选了「关闭」之后，任何清单指令性/催促性措辞都不得
// 到达模型——四处口径：
//
//	(a) 系统提示词无 todo 守则条（既有 todo_prompt_test 覆盖，这里复核 off/strict 对照）；
//	(b) 发给模型的 todo 工具 Description 不含指令性措辞；
//	(c) 轮首不注入清单快照（真实轮次走一遍，看模型实际收到的消息）；
//	(d) 轮末不产生清单收尾提示（被动通告队列为空）。
//
// strict 档是正对照：同一条链路上描述含指令措辞、快照会注入、收尾会入队。
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// todoDescBannedInOff 是 off 档下不得出现在 todo 工具描述里的指令性/催促性措辞。
var todoDescBannedInOff = []string{
	"每轮开始先审视",
	"只有两三步也要先建",
	"每完成一步立即更新",
	"不要另起炉灶",
	"不漏步骤",
}

// (b) 真实请求路径：streamRound 组装的 wire 声明里 todo 描述随会话档位变化。
func TestOffTodoToolDescriptionInWire(t *testing.T) {
	cases := []struct {
		mode       string
		wantBanned bool // true = 指令性措辞必须缺席
		wantPhrase string
	}{
		{config.TodoPromptStrict, false, "每轮开始先审视清单"},
		{config.TodoPromptOff, true, "维护当前任务清单（全量替换式写入）"},
	}
	for _, c := range cases {
		s, reg := newTestSession(t)
		bindDefault(t, reg)
		fake := &fakeStream{script: [][]llm.StreamEvent{textResult("好的")}}
		s.stream = fake.stream
		if err := s.Send("你好", WithTodoPrompt(c.mode)); err != nil {
			t.Fatalf("%s: Send: %v", c.mode, err)
		}
		waitFor(t, func() bool { return !s.Busy() })

		// 与 streamRound 同一口径（白名单 nil = 全量）取 wire 声明
		wire := llmToolsFiltered(s.tools, nil, s.TodoPromptMode())
		var desc string
		for _, tw := range wire {
			if tw.Name == "todo" {
				desc = tw.Description
			}
		}
		if desc == "" {
			t.Fatalf("%s 档：todo 必须仍在 wire 声明里（工具不删）", c.mode)
		}
		if !strings.Contains(desc, c.wantPhrase) {
			t.Fatalf("%s 档 todo 描述缺少 %q：%s", c.mode, c.wantPhrase, desc)
		}
		if c.wantBanned {
			for _, ban := range todoDescBannedInOff {
				if strings.Contains(desc, ban) {
					t.Fatalf("off 档 todo 描述不得含指令性措辞 %q：%s", ban, desc)
				}
			}
		}
	}
}

// (c) 轮首快照：strict 档注入、off 档跳过——直接断言函数分支（口径唯一）。
func TestInjectTodoSnapshotRespectsOffMode(t *testing.T) {
	// strict（缺省）：注入现状摘要（正对照）
	s := New(nil, tools.New(), nil)
	s.todos = []tools.TodoItem{{Content: "改代码", Status: "active"}, {Content: "跑测试", Status: "pending"}}
	before := len(s.History().Messages)
	s.injectTodoSnapshot()
	msgs := s.History().Messages
	if len(msgs) != before+1 {
		t.Fatalf("strict 档应注入轮首清单快照，got %d 条", len(msgs)-before)
	}
	if !strings.Contains(msgs[len(msgs)-1].Content, "先对照此清单行动") {
		t.Fatalf("strict 档快照应带「先对照此清单行动」：%+v", msgs[len(msgs)-1])
	}

	// off：不注入（未完成清单 + 全 done 收敛态都不注入）
	for _, items := range [][]tools.TodoItem{
		{{Content: "改代码", Status: "active"}},
		{{Content: "已完事项", Status: "done"}},
	} {
		s = New(nil, tools.New(), nil)
		s.todoPrompt = config.TodoPromptOff
		s.todos = items
		before = len(s.History().Messages)
		s.injectTodoSnapshot()
		if got := len(s.History().Messages); got != before {
			t.Fatalf("off 档不应注入清单快照（清单 %+v），实际多出 %d 条", items, got-before)
		}
	}
}

// (c)+(d) 真实轮次：off 档下一轮跑完，模型收到的消息里没有清单快照、
// 被动通告队列里没有清单收尾提示；strict 档两者都有（正对照）。
func TestOffModeNoTodoInjectionInRealTurn(t *testing.T) {
	runTurn := func(t *testing.T, mode string) (*Session, *fakeStream) {
		t.Helper()
		s, reg := newTestSession(t)
		bindDefault(t, reg)
		fake := &fakeStream{script: [][]llm.StreamEvent{textResult("交答复")}}
		s.stream = fake.stream
		s.todos = []tools.TodoItem{{Content: "跑测试", Status: "active"}}
		if err := s.Send("继续", WithTodoPrompt(mode)); err != nil {
			t.Fatalf("Send: %v", err)
		}
		waitFor(t, func() bool { return !s.Busy() })
		return s, fake
	}

	// strict：模型收到的消息里有轮首快照；收尾时清单未完成 → 被动通告入队
	s, fake := runTurn(t, config.TodoPromptStrict)
	if !messagesContain(fake.lastMsgs, "先对照此清单行动") {
		t.Fatalf("strict 档应把轮首清单快照发给模型：%+v", fake.lastMsgs)
	}
	s.mu.Lock()
	passive := len(s.passiveNotices)
	s.mu.Unlock()
	if passive == 0 {
		t.Fatal("strict 档轮末应入队清单收尾提示（正对照）")
	}

	// off：两条注入都缺席
	s, fake = runTurn(t, config.TodoPromptOff)
	if messagesContain(fake.lastMsgs, "先对照此清单行动") || messagesContain(fake.lastMsgs, "[当前任务清单]") {
		t.Fatalf("off 档不应把清单快照发给模型：%+v", fake.lastMsgs)
	}
	s.mu.Lock()
	passive = len(s.passiveNotices)
	s.mu.Unlock()
	if passive != 0 {
		t.Fatalf("off 档轮末不应产生清单收尾提示，got %d 条", passive)
	}
}

// messagesContain 报告消息序列里是否有任一内容包含 key。
func messagesContain(msgs []llm.Message, key string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, key) {
			return true
		}
	}
	return false
}

// 工具可用性：off 档下 todo 仍在白名单里可执行、写回生效（不删工具）。
func TestOffModeTodoStillCallable(t *testing.T) {
	r := tools.New()
	var got []tools.TodoItem
	ctx := tools.WithTodoSink(context.Background(), func(items []tools.TodoItem) { got = items })
	wire := r.LLMToolsForMode(config.TodoPromptOff)
	var found bool
	for _, tw := range wire {
		if tw.Name == "todo" {
			found = true
		}
	}
	if !found {
		t.Fatal("off 档 todo 必须仍在 wire 声明里")
	}
	var call llm.ToolCall
	call.Function.Name = "todo"
	call.Function.Arguments = `{"items":[{"content":"看代码","status":"active"}]}`
	out := r.Execute(ctx, call)
	if !strings.Contains(out, "看代码") || len(got) != 1 {
		t.Fatalf("off 档 todo 仍应可执行并写回: out=%q sink=%+v", out, got)
	}
}
