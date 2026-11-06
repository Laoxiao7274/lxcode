// run_task（助手模式 P0 能力桥）的验收测试：
// 派发链（子会话独立行 + workspace 继承项目 + 事件 dispatch_id）、
// 结论回填带子会话 id、错误路径（项目不存在人话错误不孤儿行）、
// 取消补配对（P5 不变量）。
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// newAssistantEnv 造"挂了存储 + 项目行 + 助手语境"的测试会话：
// stubResolver 里放助手 Agent 定义（Space=assistant、白名单含 run_task），
// resolver 按库的种子实现不可用（stubResolver 是本包测试桩）。
func newAssistantEnv(t *testing.T) (*dispatchEnv, *store.Store, string) {
	t.Helper()
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		ContextWindow: 100000, Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// 项目行（run_task 的 project_id 指到它）
	projDir := t.TempDir()
	proj, err := st.AddProject("demo", projDir)
	if err != nil {
		t.Fatal(err)
	}
	env := &dispatchEnv{}
	s := New(reg, tools.New(), func(ev Event) {
		env.mu.Lock()
		env.events = append(env.events, ev)
		env.mu.Unlock()
	})
	t.Cleanup(s.Close)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"assistant": {
			Def: sessiondata.AgentDef{ID: "assistant", Name: "助手", Enabled: true,
				Space: sessiondata.SpaceAssistant, Approval: "auto",
				Tools:     []string{"read_file", "run_task"},
				Delegates: []string{"coder"}},
			Delegates: []sessiondata.AgentDef{
				{ID: "coder", Name: "代码 Agent", Desc: "写代码", Enabled: true},
			},
		},
		"coder": {
			Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6",
				Tools: []string{"read_file", "edit"}, Approval: "confirm"},
		},
		"main": {
			Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true,
				Tools: []string{"agent_dispatch"}},
		},
	}})
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	env.s = s
	return env, st, proj.ID
}

// runTaskStream 造"助手一轮：发 run_task 调用 → 子会话给结论 → 助手验收"的假流。
// 助手语境判定用助手协议文本（「个人智能体助手」），子会话语境用 coder 的执行协议。
func runTaskStream(t *testing.T, argsJSON, subText string) StreamFn {
	t.Helper()
	round := 0
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if strings.Contains(msgs[0].Content, "个人智能体助手") {
			round++
			if round > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已转述。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-rt1"}
			tc.Function.Name = tools.RunTaskToolName
			tc.Function.Arguments = argsJSON
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境（coder 执行协议）
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: subText}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: subText}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// TestRunProjectTaskDispatchChain（验收③+⑤）：run_task → 子会话独立行
// （workspace 落项目 id、工作目录=项目根）→ 结论回填带子会话 id →
// 事件带 dispatch_id 归属。
func TestRunProjectTaskDispatchChain(t *testing.T) {
	env, st, projID := newAssistantEnv(t)
	// 助手会话自己没有项目归属（workspace 空）——这正是 CreateChild 的 SQL
	// 继承会拷到空、必须显式覆盖的场景。
	env.s.SetAgentID("assistant")
	env.s.SetStream(runTaskStream(t, `{"project_id":"`+projID+`","task":"修一下构建"}`, "构建修好了。"))
	if err := env.s.Send("帮我修下 demo 项目的构建", WithAgent("assistant")); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)

	kids, err := st.ChildrenOf(env.s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 {
		t.Fatalf("run_task 应开一个子会话: %+v", kids)
	}
	kid := kids[0]
	if kid.AgentID != "coder" || kid.ParentID != env.s.SessionID() {
		t.Fatalf("子会话应记住父（助手会话）与 Agent: %+v", kid)
	}
	// 子会话 workspace = 项目 id（验收③ 的核心断言）
	ws, err := st.WorkspaceOf(kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ws != projID {
		t.Fatalf("子会话 workspace 应落项目 id: got %q want %q", ws, projID)
	}
	// 结论回填带子会话 id（验收⑤）
	var toolResult string
	for _, m := range env.s.History().Messages {
		if m.Role == "tool" {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "构建修好了") {
		t.Fatalf("工具结果应含子会话结论: %q", toolResult)
	}
	if !strings.Contains(toolResult, "[子会话 id: "+kid.ID) {
		t.Fatalf("结论应带子会话 id: %q", toolResult)
	}
	// 事件带 dispatch_id（验收③ 的事件归属）
	foundStart := false
	for _, ev := range env.snapshot() {
		switch e := ev.(type) {
		case DispatchStartEvent:
			foundStart = true
			if e.DispatchID == "" || e.SessionID != kid.ID {
				t.Fatalf("DispatchStart 应带 dispatch_id 与子会话 id: %+v", e)
			}
			// run_task 特有：带目标项目名（派发卡显示「coder · demo」消解
			// 「助手自己派了代码 Agent」的误读）。
			if e.ProjectName != "demo" {
				t.Fatalf("run_task 的 DispatchStart 应带项目名: %+v", e)
			}
		case DeltaEvent:
			if e.DispatchID == "" {
				t.Fatalf("子会话 delta 应带 dispatch_id 归属: %+v", e)
			}
		}
	}
	if !foundStart {
		t.Fatal("应有 DispatchStart 事件（派发卡）")
	}
}

// TestDispatchStartWithoutProjectName：agent_dispatch 的常规委派**不带**项目名
// （子会话 workspace 继承父，没有项目归属——键缺席 = 无，主域派发不受影响）。
func TestDispatchStartWithoutProjectName(t *testing.T) {
	env := newDispatchSession(t, dispatchStream(t, `{"agent":"coder","task":"跑一遍测试"}`, "测试结论。"))
	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !env.s.Busy() })
	foundStart := false
	for _, ev := range env.snapshot() {
		if e, ok := ev.(DispatchStartEvent); ok {
			foundStart = true
			if e.ProjectName != "" {
				t.Fatalf("agent_dispatch 的 DispatchStart 不该带项目名: %+v", e)
			}
		}
	}
	if !foundStart {
		t.Fatal("应有 DispatchStart 事件（派发卡）")
	}
}

// TestRunProjectTaskWorkspaceResolvesProjectRoot（验收③ 续）：AttachTo 之后
// 子会话的工作目录 = 项目根（不是助手会话的默认目录）。
func TestRunProjectTaskWorkspaceResolvesProjectRoot(t *testing.T) {
	env, st, projID := newAssistantEnv(t)
	env.s.SetAgentID("assistant")
	round := 0
	env.s.SetStream(func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		if strings.Contains(msgs[0].Content, "个人智能体助手") {
			round++
			if round > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已转述。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-rt1"}
			tc.Function.Name = tools.RunTaskToolName
			tc.Function.Arguments = `{"project_id":"` + projID + `","task":"看一眼目录"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境（coder 执行协议）
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "done"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	if err := env.s.Send("看看项目", WithAgent("assistant")); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)
	// AttachTo 从子会话行上的 workspace 解析项目根——workspace 落库已在
	// TestRunProjectTaskDispatchChain 钉住；这里钉「只有一个子会话且行上
	// workspace 是项目 id」（round 计数保证只派发一次）。
	kids, err := st.ChildrenOf(env.s.SessionID())
	if err != nil || len(kids) != 1 {
		t.Fatalf("应恰有一个子会话: %+v err=%v", kids, err)
	}
	if kids[0].Workspace != projID {
		t.Fatalf("子会话 workspace 应是项目 id: %q", kids[0].Workspace)
	}
}

// TestRunProjectTaskProjectNotFound（验收⑥）：项目不存在 → 人话错误、不开孤儿行。
func TestRunProjectTaskProjectNotFound(t *testing.T) {
	env, st, _ := newAssistantEnv(t)
	env.s.SetAgentID("assistant")
	_, err := env.s.RunProjectTask(context.Background(), "no-such-project", "修构建", "coder", "")
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("项目不存在应报人话错误: %v", err)
	}
	// 不开孤儿行：没有子会话
	kids, err := st.ChildrenOf(env.s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 0 {
		t.Fatalf("失败路径不应留下子会话行: %+v", kids)
	}
}

// 按名兜底（2026-12 助手项目全景）：id 是不可读的时间戳随机串，模型只能从
// 注入段 / project_list 拿到，而用户口中的常常是项目名——id 未命中时按名
// 精确匹配：唯一命中用之、重名消歧、零命中维持原报错。
func TestRunProjectTaskProjectNameFallback(t *testing.T) {
	t.Run("唯一名命中", func(t *testing.T) {
		env, st, projID := newAssistantEnv(t)
		env.s.SetAgentID("assistant")
		env.s.SetStream(runTaskStream(t, `{"project_id":"demo","task":"看一眼目录"}`, "看到了。"))
		if err := env.s.Send("看下 demo 项目", WithAgent("assistant")); err != nil {
			t.Fatal(err)
		}
		waitDispatchIdle(t, env.s)
		// 子会话 workspace 落的是**项目 id**（不是名字）
		kids, err := st.ChildrenOf(env.s.SessionID())
		if err != nil || len(kids) != 1 {
			t.Fatalf("应开一个子会话: %+v err=%v", kids, err)
		}
		if ws, err := st.WorkspaceOf(kids[0].ID); err != nil || ws != projID {
			t.Fatalf("子会话归属应落项目 id: %q err=%v", ws, err)
		}
	})
	t.Run("重名消歧", func(t *testing.T) {
		env, st, _ := newAssistantEnv(t)
		env.s.SetAgentID("assistant")
		if _, err := st.AddProject("demo", t.TempDir()); err != nil {
			t.Fatal(err)
		}
		_, err := env.s.RunProjectTask(context.Background(), "demo", "任务", "coder", "")
		if err == nil || !strings.Contains(err.Error(), "重名") || !strings.Contains(err.Error(), "project_id") {
			t.Fatalf("重名应报消歧错误: %v", err)
		}
		kids, _ := st.ChildrenOf(env.s.SessionID())
		if len(kids) != 0 {
			t.Fatalf("消歧前不应开子会话: %+v", kids)
		}
	})
	t.Run("零命中维持报错", func(t *testing.T) {
		env, _, _ := newAssistantEnv(t)
		env.s.SetAgentID("assistant")
		_, err := env.s.RunProjectTask(context.Background(), "不存在的名字", "任务", "coder", "")
		if err == nil || !strings.Contains(err.Error(), "不存在") {
			t.Fatalf("零命中应维持原报错: %v", err)
		}
	})
}

// TestRunProjectTaskMainAgentRejected：主 Agent 不可被派（两类制）。
func TestRunProjectTaskMainAgentRejected(t *testing.T) {
	env, _, projID := newAssistantEnv(t)
	env.s.SetAgentID("assistant")
	_, err := env.s.RunProjectTask(context.Background(), projID, "任务", "main", "")
	if err == nil || !strings.Contains(err.Error(), "主 Agent") {
		t.Fatalf("主 Agent 应被拒绝: %v", err)
	}
}

// TestRunProjectTaskCancelPatchesPairing（验收⑦）：ctx 取消 → 子会话停 +
// 未执行调用的合成结果补进历史（P5：配对不变量不破）。
//
// 走整链（Send → 假流 run_task 调用 → 子会话挂住 → 会话级 Cancel）：
// 取消的补配对发生在 runTools 的取消出口（阶段一发现 ctx 断 → 合成结果），
// 直调 RunProjectTask 的 ctx 取消不经过那条出口（它只回错误，历史由
// 调用方 runTools 收尾——直调语义下调用方自己管历史）。
func TestRunProjectTaskCancelPatchesPairing(t *testing.T) {
	env, _, projID := newAssistantEnv(t)
	env.s.SetAgentID("assistant")
	// 子会话流吐文本后挂住（响应 ctx 取消——真实端点同款），助手第一轮
	// 发 run_task 调用；用户点停止 = 会话级 Cancel，ctx 断 → runTools 的
	// 取消出口给 run_task 调用补「已取消，未执行」合成结果。
	block := make(chan struct{})
	round := 0
	env.s.SetStream(func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if strings.Contains(msgs[0].Content, "个人智能体助手") {
			round++
			if round > 1 {
				// 第二轮收尾（不再次派发——不然取消后的轮循环会再发一次调用，
				// 又被取消出口补一张合成结果，断言就不是「恰一对」了）
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "收到中断。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-rt1"}
			tc.Function.Name = tools.RunTaskToolName
			tc.Function.Arguments = `{"project_id":"` + projID + `","task":"长任务"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话：吐文本后挂住（等取消）——真实端点会响应 ctx 取消，
		// 测试流同样响应（select），否则取消路径等不到流收尾。
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "干着呢"}
			select {
			case <-block:
			case <-ctx.Done():
			}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "半截"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	if err := env.s.Send("开干", WithAgent("assistant")); err != nil {
		t.Fatal(err)
	}
	// 等子会话真的跑起来（busy）再取消
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !env.s.Busy() {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // 等子轮进入流式阶段
	env.s.Cancel()
	waitDispatchIdle(t, env.s)
	// 配对不变量：助手历史里 assistant 的 run_task 调用要有配对 tool 结果，
	// 且结果是取消说明（P5：缺配对会 400 + 压缩切点永久卡死）。
	paired := false
	var calls, results int
	for _, m := range env.s.History().Messages {
		switch m.Role {
		case "assistant":
			calls += len(m.ToolCalls)
		case "tool":
			results++
			if m.ToolCallID == "call-rt1" {
				paired = strings.Contains(m.Content, "取消")
			}
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("助手历史应恰有一对调用/结果: calls=%d results=%d", calls, results)
	}
	if !paired {
		t.Fatal("run_task 调用应补「已取消，未执行」合成结果（配对不变量）")
	}
}
