// M3 agent.dispatch 的内核测试：主 Agent 派发 → 子上下文隔离执行 →
// 结果回填主历史；委派名单校验；事件归属标记。
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// dispatchEnv 是 dispatchSession 的可观测面（事件捕获）。
type dispatchEnv struct {
	s      *Session
	events []Event
	mu     sync.Mutex
}

func (e *dispatchEnv) snapshot() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Event(nil), e.events...)
}

// newDispatchSession 构造挂 resolver 的会话（main 委派 coder）+ 可编程假流。
func newDispatchSession(t *testing.T, stream StreamFn) *dispatchEnv {
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
	env := &dispatchEnv{}
	s := New(reg, tools.New(), func(ev Event) {
		env.mu.Lock()
		env.events = append(env.events, ev)
		env.mu.Unlock()
	})
	s.SetStream(stream)
	t.Cleanup(s.Close)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main": {
			Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true,
				Tools: []string{"agent.dispatch"}, Delegates: []string{"coder"}},
			Delegates: []sessiondata.AgentDef{
				{ID: "coder", Name: "代码 Agent", Desc: "写代码", Enabled: true},
			},
		},
		"coder": {
			Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6",
				Tools: []string{"read_file", "edit"}, Approval: "confirm"},
		},
		"outsider": {
			Def: sessiondata.AgentDef{ID: "outsider", Name: "名单外", Enabled: true, Tools: []string{}},
		},
	}})
	env.s = s
	return env
}

// waitDispatchIdle 等会话空闲（轮跑完）。
func waitDispatchIdle(t *testing.T, s *Session) {
	t.Helper()
	for i := 0; i < 300 && s.Busy(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatchSubContextIsolation(t *testing.T) {
	var mainCalls, subCalls int
	var subPrompt, subFirstMsg string
	env := newDispatchSession(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if strings.Contains(msgs[0].Content, "主 Agent（调度中枢）") {
			// 主轮：第一轮发起 dispatch；拿到工具结果后收尾（模拟验收汇总）
			mainCalls++
			if mainCalls == 1 {
				tc := llm.ToolCall{ID: "call-1"}
				tc.Function.Name = "agent.dispatch"
				tc.Function.Arguments = `{"agent":"coder","task":"读 README 并总结","context":"项目根在当前目录"}`
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls,
					}}
				}()
				return ch, nil
			}
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "已验收。"}, FinishReason: llm.FinishStop,
				}}
			}()
			return ch, nil
		}
		// 子轮
		subCalls++
		subPrompt = msgs[0].Content
		subFirstMsg = msgs[1].Content
		if subCalls == 1 {
			tc := llm.ToolCall{ID: "sub-1"}
			tc.Function.Name = "read_file"
			tc.Function.Arguments = `{"path":"README.md"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls,
				}}
			}()
			return ch, nil
		}
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "README 总结完成：这是一个 agent 项目。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "README 总结完成：这是一个 agent 项目。"}, FinishReason: llm.FinishStop,
			}}
		}()
		return ch, nil
	})

	if err := env.s.Send("帮我总结 README"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)

	if mainCalls != 2 || subCalls != 2 {
		t.Fatalf("调用数不符 main=%d sub=%d", mainCalls, subCalls)
	}
	// 隔离验证：子上下文第一条 = 任务消息（不是主历史）
	if !strings.Contains(subFirstMsg, "读 README 并总结") || !strings.Contains(subFirstMsg, "项目根在当前目录") {
		t.Fatalf("子上下文应只含任务+背景: %q", subFirstMsg)
	}
	if strings.Contains(subFirstMsg, "帮我总结 README") {
		t.Fatalf("子上下文不应含主历史: %q", subFirstMsg)
	}
	// 子提示词 = 执行协议（子 Agent 四层组合）
	if !strings.Contains(subPrompt, "你是执行 Agent") {
		t.Fatal("子提示词应为执行协议")
	}
	// 主历史：user + dispatch 工具结果（子结论回填）——子中间消息不进主历史
	var toolResultMsg string
	var subToolMsgs int
	for _, m := range env.s.History().Messages {
		if m.Role == "tool" {
			toolResultMsg = m.Content
			subToolMsgs++
		}
	}
	if subToolMsgs != 1 {
		t.Fatalf("主历史应只有一条工具结果（子中间消息不进主历史）: %d", subToolMsgs)
	}
	if !strings.Contains(toolResultMsg, "README 总结完成") {
		t.Fatalf("子结论应回填主历史（工具结果）: %q", toolResultMsg)
	}
	// 事件序：DispatchStart → 子 delta（带 DispatchID=call-1）→ DispatchEnd
	var sawStart, sawSubDelta, sawSubDone, sawEnd bool
	for _, ev := range env.snapshot() {
		if e, ok := ev.(DispatchStartEvent); ok {
			sawStart = e.DispatchID == "call-1" && e.AgentID == "coder" && e.AgentColor != ""
		}
		if e, ok := ev.(DeltaEvent); ok && e.DispatchID == "call-1" {
			sawSubDelta = strings.Contains(e.Text, "README 总结完成")
		}
		if e, ok := ev.(TurnDoneEvent); ok && e.DispatchID == "call-1" {
			sawSubDone = true
		}
		if e, ok := ev.(DispatchEndEvent); ok {
			sawEnd = e.DispatchID == "call-1" && !e.IsError && strings.Contains(e.Result, "README 总结完成")
		}
	}
	if !sawStart || !sawSubDelta || !sawSubDone || !sawEnd {
		t.Fatalf("事件序缺失 start=%v subDelta=%v subDone=%v end=%v", sawStart, sawSubDelta, sawSubDone, sawEnd)
	}
}

func TestDispatchGuards(t *testing.T) {
	run := func(t *testing.T, target string, wantErr string) {
		t.Helper()
		var result string
		env := newDispatchSession(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
			ch := make(chan llm.StreamEvent, 4)
			if strings.Contains(msgs[0].Content, "调度中枢") {
				tc := llm.ToolCall{ID: "c1"}
				tc.Function.Name = "agent.dispatch"
				tc.Function.Arguments = `{"agent":"` + target + `","task":"x"}`
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
				}()
				return ch, nil
			}
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "ok"}}}
			}()
			return ch, nil
		})
		if err := env.s.Send("派活"); err != nil {
			t.Fatal(err)
		}
		waitDispatchIdle(t, env.s)
		for _, m := range env.s.History().Messages {
			if m.Role == "tool" {
				result = m.Content
			}
		}
		if !strings.Contains(result, wantErr) {
			t.Fatalf("目标 %s 应拒绝（%s）: %q", target, wantErr, result)
		}
	}
	t.Run("名单外拒绝", func(t *testing.T) { run(t, "outsider", "不在有效委派名单内") })
	t.Run("主 Agent 不可被委派", func(t *testing.T) { run(t, "main", "主 Agent 不可被委派") })
	t.Run("不存在的 Agent", func(t *testing.T) { run(t, "nobody", "Agent 不存在") })
}

// JSON 参数解析路径（runTools 调用位的展开形状）。
func TestDispatchCallUnmarshal(t *testing.T) {
	var p struct {
		Agent   string `json:"agent"`
		Task    string `json:"task"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal([]byte(`{"agent":"coder","task":"t","context":"c"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Agent != "coder" || p.Task != "t" || p.Context != "c" {
		t.Fatalf("解析失真: %+v", p)
	}
}
