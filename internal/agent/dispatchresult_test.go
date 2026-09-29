// 派发结论的正确性（2026-09-29 用户实测）：「第一个子 Agent 没有返回结果，
// 主 Agent 拿它的会话 id 重新做了一遍」。
//
// 根因两条，缺一条就复现：
//  1. SendWait 在 done 关闭时**恒返回 nil**（即使这一轮以错误收尾——比如工具循环
//     达上限），真正的错误记在 tap.err 里。runDispatch 只看 err 就会静默走进结论
//     分支，把「达上限」丢掉。
//  2. lastAssistantText 往回扫"最近一条有正文的"——多轮工具循环里模型每轮先写一句
//     开场白再发工具调用（"I'll start by …"），往回扫就把那句**没兑现的承诺**当成
//     结论回填。主 Agent 既不知道子 Agent 没做完，又看到"接着这次进度继续时填
//     session 参数"，于是自己又派了一遍。
package agent

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestLastAssistantTextTakesOnlyLastAssistant 钉住"开场白不是结论"。
// 中间轮的开场白（后面还跟着工具调用）不许被当成子会话的结论回填。
func TestLastAssistantTextTakesOnlyLastAssistant(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "勘察一下"},
		{Role: "assistant", Content: "I'll start by surveying the top-level structure."},
		{Role: "tool", Content: "total 108 ..."},
		{Role: "assistant", Content: "   "}, // 纯工具调用轮：正文为空
		{Role: "tool", Content: "more"},
	}
	if got := lastAssistantText(msgs); got != "" {
		t.Fatalf("最后一条 assistant 没有正文时应回空串（开场白不是结论），得到 %q", got)
	}
	// 正常收尾：最后一条 assistant 就是结论
	msgs = append(msgs, llm.Message{Role: "assistant", Content: " 报告正文 "})
	if got := lastAssistantText(msgs); got != "报告正文" {
		t.Fatalf("正常收尾应取最后一条的正文，得到 %q", got)
	}
}

// maxRoundsStream 造假流：主轮派发一次；子会话**每轮都发工具调用**，第一轮先给一句
// 开场白——于是子会话必然撞上 maxToolRounds 且没有文本结论。
func maxRoundsStream() StreamFn {
	mainRound, subRound := 0, 0
	return func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		isMain := strings.Contains(msgs[0].Content, "主 Agent（调度中枢）")
		go func() {
			defer close(ch)
			if isMain {
				mainRound++
				if mainRound > 1 {
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "收到。"}, FinishReason: llm.FinishStop}}
					return
				}
				tc := llm.ToolCall{ID: "call-r1"}
				tc.Function.Name = "agent_dispatch"
				tc.Function.Arguments = `{"agent":"coder","task":"勘察一下"}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
				return
			}
			subRound++
			tc := llm.ToolCall{ID: "sub-" + strconv.Itoa(subRound)}
			tc.Function.Name = "read_file"
			tc.Function.Arguments = `{"path":"nope.txt"}`
			msg := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}
			if subRound == 1 {
				// 第一轮：开场白 + 工具调用（正是那条被误当结论的文本）
				msg.Content = "I'll start by surveying the top-level structure."
			}
			ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: msg, FinishReason: llm.FinishToolCalls}}
		}()
		return ch, nil
	}
}

// TestDispatchMaxRoundsReportsErrorNotPreamble 钉住用户实测的那条：子会话撞上工具循环
// 上限时，回填给主 Agent 的必须是**错误 + 上限说明**，不是中间轮的开场白。
func TestDispatchMaxRoundsReportsErrorNotPreamble(t *testing.T) {
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	cap := &toolEventCapture{}
	s := New(reg, tools.New(), cap.handle)
	t.Cleanup(s.Close)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main": {
			Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true,
				Tools: []string{"agent_dispatch"}, Delegates: []string{"coder"}},
			Delegates: []sessiondata.AgentDef{
				{ID: "coder", Name: "代码 Agent", Desc: "勘察", Enabled: true},
			},
		},
		"coder": {
			Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6",
				Tools: []string{"read_file"}},
		},
	}})
	s.SetStream(maxRoundsStream())
	if err := s.SendWait(context.Background(), "派活"); err != nil {
		t.Fatal(err)
	}

	var dispatchRes *ToolResultEvent
	for i := range cap.results {
		if cap.results[i].Name == "agent_dispatch" {
			dispatchRes = &cap.results[i]
		}
	}
	if dispatchRes == nil {
		t.Fatalf("应有 agent_dispatch 的工具结果: %+v", cap.results)
	}
	if !dispatchRes.IsError {
		t.Fatalf("子会话达工具循环上限时应回**错误**（否则主 Agent 以为拿到了结论）: %+v", dispatchRes)
	}
	if !strings.Contains(dispatchRes.Content, "工具循环达上限") {
		t.Fatalf("应如实说明达上限，得到 %q", dispatchRes.Content)
	}
	if strings.Contains(dispatchRes.Content, "I'll start by") {
		t.Fatalf("开场白不许被当成结论回填: %q", dispatchRes.Content)
	}
}
