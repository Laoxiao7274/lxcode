// 派发结论的正确性 + 死循环判据（2026-09-29 用户实测与拍板）。
//
// 用户实测：「第一个子 Agent 没有返回结果，主 Agent 拿它的会话 id 重新做了一遍」。
// 根因两条，缺一条就复现：
//  1. SendWait 在 done 关闭时**恒返回 nil**（即使这一轮以错误收尾），真正的错误记在
//     tap.err 里。runDispatch 只看 err 就会静默走进结论分支，把错误丢掉。
//  2. lastAssistantText 往回扫"最近一条有正文的"——多轮工具循环里模型每轮先写一句
//     开场白再发工具调用（"I'll start by …"），往回扫就把那句**没兑现的承诺**当成
//     结论回填。主 Agent 既不知道子 Agent 没做完，又看到"接着这次进度继续时填
//     session 参数"，于是自己又派了一遍。
//
// 用户拍板：**去掉轮数上限**（maxToolRounds=16），死循环改判"同一批调用重复"——
// 轮数区分不了「卡住」与「任务本来就长」（真库实测：通读仓库的 researcher 16 轮
// 每轮参数都不同，被误杀在半路）。见 repeat.go。
package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestLastAssistantTextTakesOnlyLastAssistant 钉住"开场白不是结论"。
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
	msgs = append(msgs, llm.Message{Role: "assistant", Content: " 报告正文 "})
	if got := lastAssistantText(msgs); got != "报告正文" {
		t.Fatalf("正常收尾应取最后一条的正文，得到 %q", got)
	}
}

// repeatDispatchStream 造假流：主轮派发一次；子会话按 sub 决定每轮的调用与收尾。
// sub(round) 返回该轮的工具参数与"是否收尾"（final 非空 = 收尾并给出结论）。
func repeatDispatchStream(sub func(round int) (args string, final string)) StreamFn {
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
			args, final := sub(subRound)
			if final != "" {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: final}, FinishReason: llm.FinishStop}}
				return
			}
			tc := llm.ToolCall{ID: fmt.Sprintf("sub-%d", subRound)}
			tc.Function.Name = "read_file"
			tc.Function.Arguments = args
			msg := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}
			if subRound == 1 {
				// 第一轮：开场白 + 工具调用（正是那条曾被误当结论的文本）
				msg.Content = "I'll start by surveying the top-level structure."
			}
			ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: msg, FinishReason: llm.FinishToolCalls}}
		}()
		return ch, nil
	}
}

// newRepeatDispatchSession 造「主 Agent 派发 → 子 Agent 用 read_file 干活」的会话。
func newRepeatDispatchSession(t *testing.T, stream StreamFn) (*Session, *toolEventCapture) {
	t.Helper()
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
	s.SetStream(stream)
	if err := s.SendWait(context.Background(), "派活"); err != nil {
		t.Fatal(err)
	}
	return s, cap
}

func dispatchResult(t *testing.T, cap *toolEventCapture) ToolResultEvent {
	t.Helper()
	var out *ToolResultEvent
	for i := range cap.results {
		if cap.results[i].Name == "agent_dispatch" {
			out = &cap.results[i]
		}
	}
	if out == nil {
		t.Fatalf("应有 agent_dispatch 的工具结果: %+v", cap.results)
	}
	return *out
}

// TestLongTaskWithDistinctCallsNotStopped 是**去掉轮数上限**的回归钉子：
// 子会话跑 20 轮（> 原上限 16）**每轮参数都不同** → 必须正常跑到结论。
//
// 真库实测的反面教材（20260929-133600-48b9）：通读仓库的 researcher 跑了 16 轮，
// 每轮参数都不同，却被轮数上限误杀在半路——最后一条消息是工具调用（正文为空），
// 没有结论，主 Agent 于是拿它的会话 id 又派了一遍。
func TestLongTaskWithDistinctCallsNotStopped(t *testing.T) {
	const rounds = 20
	_, cap := newRepeatDispatchSession(t, repeatDispatchStream(func(round int) (string, string) {
		if round > rounds {
			return "", "勘察完成：共读了 20 个文件。"
		}
		return fmt.Sprintf(`{"path":"file-%d.txt"}`, round), ""
	}))
	res := dispatchResult(t, cap)
	if res.IsError {
		t.Fatalf("20 轮**参数各不相同**是正常长任务，不该被判死循环: %+v", res)
	}
	if !strings.Contains(res.Content, "勘察完成") {
		t.Fatalf("应回填子会话的结论，得到 %q", res.Content)
	}
}

// TestDispatchRepeatLoopReportsErrorNotPreamble：子会话**同参数原地打转**时，
// 回填给主 Agent 的必须是**错误**（不是中间轮的开场白），且不能是"成功"。
//
// 这条同时钉住两件事：① repeatGuard 在 repeatStop 次判死循环并停轮；
// ② runDispatch 把子会话的这一轮错误如实回填（SendWait 返回 nil 也要看 tap.err）。
func TestDispatchRepeatLoopReportsErrorNotPreamble(t *testing.T) {
	_, cap := newRepeatDispatchSession(t, repeatDispatchStream(func(int) (string, string) {
		return `{"path":"same.txt"}`, "" // 每一轮一字不差
	}))
	res := dispatchResult(t, cap)
	if !res.IsError {
		t.Fatalf("同参数死循环时子会话以错误收尾，回填必须是错误: %+v", res)
	}
	if !strings.Contains(res.Content, "死循环") {
		t.Fatalf("应如实说明判定死循环，得到 %q", res.Content)
	}
	if strings.Contains(res.Content, "I'll start by") {
		t.Fatalf("开场白不许被当成结论回填: %q", res.Content)
	}
}
