// 会话级模型（Session.SetModel）的内核契约：
//   - 优先级：请求级 > 会话级选择 > Agent 绑定 > default 角色；
//   - 切换只影响本会话（另一条会话的 ModelID 不动）——这是作用域 bug 的核心验收；
//   - 落库 + AttachTo 恢复（重启后打开同一条会话还是用户选的模型）；
//   - 校验：不存在的模型/停用的模型如实报错，不静默回落；
//   - 子会话继承父会话的会话级选择（默认角色后来被改也不漂移）。
package agent

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// newModelTestSession 起一个带 m1(default)/m2/m3(停用) 的会话。
func newModelTestSession(t *testing.T) (*Session, *config.Registry) {
	t.Helper()
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	for _, id := range []string{"m2", "m3"} {
		if err := reg.Add(config.ModelConfig{
			ID: id, BaseURL: "http://localhost:2", Model: "test-" + id,
			Format: config.FormatOpenAI, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	m3, _ := reg.Get("m3")
	m3.Enabled = false
	if err := reg.Update(m3); err != nil {
		t.Fatal(err)
	}
	return s, reg
}

func TestSessionModelScopeIsolation(t *testing.T) {
	a, reg := newModelTestSession(t)
	b := New(reg, tools.New(), nil)
	t.Cleanup(b.Close)

	// 两条会话初始都解析到 default 角色
	if got := a.ModelID(); got != "m1" {
		t.Fatalf("初始应回落 default: %q", got)
	}
	if got := b.ModelID(); got != "m1" {
		t.Fatalf("初始应回落 default: %q", got)
	}

	// A 切到 m2：只改 A（作用域隔离）
	if got, err := a.SetModel("m2"); err != nil || got != "m2" {
		t.Fatalf("SetModel 失败: %q %v", got, err)
	}
	if got := a.ModelID(); got != "m2" {
		t.Fatalf("A 应是 m2: %q", got)
	}
	if got := b.ModelID(); got != "m1" {
		t.Fatalf("B 不该跟着变（模型选择是会话级）: %q", got)
	}
	// 全局 default 角色没被改：新会话仍继承 m1
	c := New(reg, tools.New(), nil)
	t.Cleanup(c.Close)
	if got := c.ModelID(); got != "m1" {
		t.Fatalf("SetModel 不该改全局 default 角色: %q", got)
	}
}

func TestSessionModelPriority(t *testing.T) {
	s, reg := newModelTestSession(t)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main":  {Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true}},
		"coder": {Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Model: "m2"}},
	}})
	if _, err := s.SetModel("m3"); err == nil {
		t.Fatal("停用的模型应被拒绝")
	}
	if _, err := s.SetModel("不存在"); err == nil {
		t.Fatal("不存在的模型应被拒绝")
	}
	if _, err := s.SetModel(""); err == nil {
		t.Fatal("空模型 id 应被拒绝（不提供隐式回默认）")
	}
	if got := s.ModelID(); got != "m1" {
		t.Fatalf("失败的 SetModel 不该改动状态: %q", got)
	}

	// Agent 绑定优先于 default
	s.SetAgentID("coder")
	if got := s.ModelID(); got != "m2" {
		t.Fatalf("Agent 绑定应优先于 default: %q", got)
	}
	// 会话级选择优先于 Agent 绑定
	if _, err := s.SetModel("m1"); err != nil {
		t.Fatal(err)
	}
	if got := s.ModelID(); got != "m1" {
		t.Fatalf("会话级选择应优先于 Agent 绑定: %q", got)
	}
	// 请求级（WithModel）优先于会话级——streamRound 的解析入口
	ac, err := s.resolveAgent("coder")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.modelForRequest(ac, "m2")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "m2" {
		t.Fatalf("请求级指定应最优先: %q", m.ID)
	}
	_ = reg
}

func TestSessionModelPersistsAndRestores(t *testing.T) {
	// 用一个最小 Persistence 桩记录落库值（AttachTo 走同一套恢复路径）
	st := &modelStub{}
	s := New(nil, tools.New(), nil)
	s.st = st
	s.id = "sess-1"
	reg, err := config.Load(t.TempDir() + "/models.json")
	if err != nil {
		t.Fatal(err)
	}
	s.reg = reg
	if err := reg.Add(config.ModelConfig{ID: "m1", BaseURL: "http://localhost:1", Model: "m1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{ID: "m2", BaseURL: "http://localhost:2", Model: "m2", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetModel("m2"); err != nil {
		t.Fatal(err)
	}
	if st.saved != "m2" {
		t.Fatalf("应落库选中的模型: %q", st.saved)
	}

	// AttachTo 恢复：另一份 Session 附着同一 id，读回 m2
	st2 := &modelStub{saved: "m2"}
	s2 := New(reg, tools.New(), nil)
	s2.SetAgentID("")
	if err := s2.AttachTo(st2, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if got := s2.SelectedModel(); got != "m2" {
		t.Fatalf("附着后应恢复落库的模型: %q", got)
	}
	if got := s2.ModelID(); got != "m2" {
		t.Fatalf("恢复后解析出的模型应是 m2: %q", got)
	}
}

// modelStub 只实现模型选择落库/读回（其余内嵌 nil——未预期调用 panic 即失败）。
type modelStub struct {
	Persistence
	saved     string
	loadErr   error
	workspace string
}

func (p *modelStub) Load(string) ([]llm.Message, error) { return nil, nil }
func (p *modelStub) WorkspaceOf(string) (string, error) { return p.workspace, nil }
func (p *modelStub) ProjectByID(string) (sessiondata.ProjectMeta, bool, error) {
	return sessiondata.ProjectMeta{}, false, nil
}
func (p *modelStub) ContextUsageOf(string) (sessiondata.ContextUsage, bool, error) {
	return sessiondata.ContextUsage{}, false, nil
}
func (p *modelStub) SaveContextUsage(string, sessiondata.ContextUsage) error { return nil }
func (p *modelStub) ApprovalOf(string) (string, error)                       { return "", nil }
func (p *modelStub) SaveApproval(string, string) error                       { return nil }
func (p *modelStub) TodosOf(string) ([]sessiondata.TodoItem, bool, error)    { return nil, false, nil }
func (p *modelStub) SaveTodos(string, []sessiondata.TodoItem) error          { return nil }
func (p *modelStub) SaveSessionModel(_ string, model string) error {
	p.saved = model
	return nil
}
func (p *modelStub) SessionModelOf(string) (string, error) { return p.saved, p.loadErr }

// TestChildSessionInheritsParentModel：子会话继承父会话的**会话级**模型选择，但
// 继承值**只进内存不落库**（2026-12 语义修订）：落库会让续跑/回放把它当成子会话
// 自己的选择，此后子 Agent 的模型绑定永远被压死（用户实测的 bug）。父会话之后改
// 默认角色/切模型都不影响已开的子会话（继承的是死值，不漂移）。
func TestChildSessionInheritsParentModel(t *testing.T) {
	env, st := newChildSessionEnv(t)
	reg := env.s.reg
	// 加一个 m2 供父会话选择（default 角色仍是 m1）
	if err := reg.Add(config.ModelConfig{
		ID: "m2", BaseURL: "http://127.0.0.1:2/v1", Model: "m2", Enabled: true,
		ContextWindow: 100000, Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.s.SetModel("m2"); err != nil {
		t.Fatal(err)
	}
	env.s.SetStream(dispatchStream(t, `{"agent":"coder","task":"跑一遍测试"}`, "子会话结论"))
	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)

	kids, err := st.ChildrenOf(env.s.SessionID())
	if err != nil || len(kids) != 1 {
		t.Fatalf("应有 1 个子会话: %+v err=%v", kids, err)
	}
	// **不落库**（旧断言是 = "m2"，锁的是落库继承——那正是压死 Agent 绑定的根因）：
	// 继承的父模型不是子会话自己的选择，store 的 model 列必须保持空。
	if got, err := st.SessionModelOf(kids[0].ID); err != nil || got != "" {
		t.Fatalf("子会话继承父模型不应落库（model 列应为空）: %q err=%v", got, err)
	}
}

// childModelResolver 造一份名单：main（可委派 coder）+ coder（model 可指定绑定）。
func childModelResolver(coderModel string) *stubResolver {
	return &stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main": {
			Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true,
				Tools: []string{"agent_dispatch"}, Delegates: []string{"coder"}},
			Delegates: []sessiondata.AgentDef{
				{ID: "coder", Name: "代码 Agent", Desc: "写代码", Enabled: true},
			},
		},
		"coder": {
			Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6",
				Tools: []string{"read_file"}, Approval: "confirm", Model: coderModel},
		},
	}}
}

// TestChildSessionModelPriority：派发链模型解析的完整优先级——
// 子会话自己的选择 > 子 Agent 绑定 > 父会话会话级选择（内存继承）> default 角色。
func TestChildSessionModelPriority(t *testing.T) {
	env, st := newChildSessionEnv(t)
	reg := env.s.reg
	for _, id := range []string{"m2", "m3"} {
		if err := reg.Add(config.ModelConfig{
			ID: id, BaseURL: "http://127.0.0.1:2/v1", Model: id, Enabled: true,
			ContextWindow: 100000, Capabilities: config.Capabilities{Tools: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 父会话选 m2
	if _, err := env.s.SetModel("m2"); err != nil {
		t.Fatal(err)
	}

	// 场景①：父选 m2 + 子 Agent 绑定 m3 → 子会话实际用 m3，且 store 的 model 列
	// 不被写成继承值（用户报的 bug：绑定模型不生效、子 Agent 跟随主会话选择）。
	resolver := childModelResolver("m3")
	env.s.SetAgentResolver(resolver)
	env.s.SetStream(dispatchStream(t, `{"agent":"coder","task":"跑一遍测试"}`, "场景一结论"))
	if err := env.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env.s)
	kids, err := st.ChildrenOf(env.s.SessionID())
	if err != nil || len(kids) != 1 {
		t.Fatalf("应有 1 个子会话: %+v err=%v", kids, err)
	}
	if got, err := st.SessionModelOf(kids[0].ID); err != nil || got != "" {
		t.Fatalf("子会话 model 列不应被写成父会话的 m2: %q err=%v", got, err)
	}
	// 续跑路径（模拟重启后重新附着）：仍按绑定解析出 m3，不依赖落库的继承值
	probe := New(reg, tools.New(), nil)
	probe.SetAgentResolver(resolver)
	if err := probe.AttachTo(st, kids[0].ID); err != nil {
		t.Fatal(err)
	}
	probe.SetAgentID("coder")
	if got := probe.ModelID(); got != "m3" {
		t.Fatalf("续跑后子 Agent 绑定 m3 应压过父会话选择 m2: %q", got)
	}

	// 场景②：父没选 + 子没绑 → default（m1）。开新父会话（无会话级选择）。
	env2, st2 := newChildSessionEnv(t)
	resolver2 := childModelResolver("")
	env2.s.SetAgentResolver(resolver2)
	env2.s.SetStream(dispatchStream(t, `{"agent":"coder","task":"跑一遍测试"}`, "场景二结论"))
	if err := env2.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env2.s)
	kids2, err := st2.ChildrenOf(env2.s.SessionID())
	if err != nil || len(kids2) != 1 {
		t.Fatalf("场景②应有 1 个子会话: %+v err=%v", kids2, err)
	}
	probe2 := New(env2.s.reg, env2.s.tools, nil)
	probe2.SetAgentResolver(resolver2)
	if err := probe2.AttachTo(st2, kids2[0].ID); err != nil {
		t.Fatal(err)
	}
	probe2.SetAgentID("coder")
	if got := probe2.ModelID(); got != "m1" {
		t.Fatalf("父没选+子没绑应回落 default m1: %q", got)
	}

	// 场景③：父选 m1（继承）+ 子没绑 → 内存继承生效，**不落库**。
	env3, st3 := newChildSessionEnv(t)
	env3.s.SetAgentResolver(childModelResolver(""))
	if _, err := env3.s.SetModel("m1"); err != nil {
		t.Fatal(err)
	}
	env3.s.SetStream(dispatchStream(t, `{"agent":"coder","task":"跑一遍测试"}`, "场景三结论"))
	if err := env3.s.Send("把测试跑了"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, env3.s)
	kids3, err := st3.ChildrenOf(env3.s.SessionID())
	if err != nil || len(kids3) != 1 {
		t.Fatalf("场景③应有 1 个子会话: %+v err=%v", kids3, err)
	}
	if got, err := st3.SessionModelOf(kids3[0].ID); err != nil || got != "" {
		t.Fatalf("继承的父模型不应落库: %q err=%v", got, err)
	}
}
