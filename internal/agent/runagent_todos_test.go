// runagent_todos_test.go —— RunAgentTask（合并进程拉起子会话的那条路）收尾的
// 清单终态钉子：DispatchEndEvent.FinalTodos 必须带上子会话定格清单——与
// runDispatch 同一条纪律（todo 契约批③）。漏了它，「子会话结束后最后一项没
// 显示完成」在合并进程路上重现。
package agent

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestRunAgentTaskDispatchEndCarriesFinalTodos：子会话跑一轮 todo 更新后给结论，
// 收尾的 DispatchEndEvent 应带定格清单（未经模型收敛的残留项原样——不假装完成）。
func TestRunAgentTaskDispatchEndCarriesFinalTodos(t *testing.T) {
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
	events := make(chan Event, 64)
	capture := func(ev Event) {
		select {
		case events <- ev:
		default:
		}
	}
	s := New(reg, tools.New(), capture)
	t.Cleanup(s.Close)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main": {Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true}},
		"merger": {Def: sessiondata.AgentDef{ID: "merger", Name: "合并 Agent", Enabled: true,
			Tools: []string{"todo"}, Approval: "auto"}},
	}})

	// 假流：子会话第一轮调 todo（更新后仍留一项 active——模型没收敛），第二轮给结论。
	s.SetStream(func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		round := 0
		for _, msg := range msgs {
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				round++
			}
		}
		go func() {
			defer close(ch)
			if round == 0 {
				tc := llm.ToolCall{ID: "runagent-todo-1"}
				tc.Function.Name = "todo"
				tc.Function.Arguments = `{"items":[{"content":"分析冲突","status":"done"},{"content":"跑验证","status":"active"}]}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
				return
			}
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "合并结论。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "合并结论。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})

	res, err := s.RunAgentTask(context.Background(), "merger", "合并任务", "", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("RunAgentTask 失败: %v", err)
	}
	if !strings.Contains(res, "合并结论") {
		t.Fatalf("结论应回传，got %q", res)
	}
	// 等收尾事件落袋
	var end *DispatchEndEvent
	for i := 0; i < 200 && end == nil; i++ {
		select {
		case ev := <-events:
			if e, ok := ev.(DispatchEndEvent); ok {
				end = &e
			}
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if end == nil {
		t.Fatal("应收到 DispatchEndEvent")
	}
	if len(end.FinalTodos) != 2 {
		t.Fatalf("FinalTodos 应带子会话定格清单，got %+v", end.FinalTodos)
	}
	if end.FinalTodos[0].Status != "done" || end.FinalTodos[1].Status != "active" {
		t.Fatalf("残留项应原样定格（不假装完成），got %+v", end.FinalTodos)
	}
}
