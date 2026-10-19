// 组合键在**会话级模型选择**上的内核契约：
//   - chat.model 接受组合键（`provider::id`）与纯 id（存量引用）；
//   - 同名不同 provider 的模型在一条会话里是两个独立选择（不互相覆盖）；
//   - 选中的规范键落库、重启后照旧解析（存量行存的是纯 id，零迁移）。
package agent

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// newCompositeModelSession 起一条带两个「同名不同提供商」模型的会话。
func newCompositeModelSession(t *testing.T) (*Session, *config.Registry) {
	t.Helper()
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	for _, p := range []string{"p1", "p2"} {
		if err := reg.Add(config.ModelConfig{
			ID: "gpt-4o", Provider: p, BaseURL: "http://localhost:2", Model: "gpt-4o-" + p,
			Format: config.FormatOpenAI, Enabled: true,
		}); err != nil {
			t.Fatalf("Add %s: %v", p, err)
		}
	}
	return s, reg
}

func TestSetModelAcceptsCompositeKey(t *testing.T) {
	s, _ := newCompositeModelSession(t)

	got, err := s.SetModel("p2::gpt-4o")
	if err != nil || got != "p2::gpt-4o" {
		t.Fatalf("组合键 SetModel: %q %v", got, err)
	}
	if s.ModelID() != "p2::gpt-4o" {
		t.Fatalf("实际模型应是 p2 那条: %q", s.ModelID())
	}
	if s.SelectedModel() != "p2::gpt-4o" {
		t.Fatalf("会话选择值应是规范键: %q", s.SelectedModel())
	}
	// 换一家：纯 id 形式不被接受（provider 缺失就是没指明是哪一家）
	if _, err := s.SetModel("gpt-4o"); err == nil {
		t.Fatal("裸 id 不该命中带 provider 的条目（跨 provider 回落是猜）")
	}
	// 请求级/Agent 绑定路径走同一条解析：显式组合键取到的是那一家
	m, err := s.modelForRequest(nil, "p1::gpt-4o")
	if err != nil || m.Key() != "p1::gpt-4o" {
		t.Fatalf("请求级组合键: %+v %v", m, err)
	}
	// 纯 id 兜不到任何一条 → 如实报错，不静默回落到 default
	if _, err := s.modelByID("gpt-4o", "测试"); err == nil {
		t.Fatal("裸 id 应报错")
	}
}

func TestLegacySessionModelStillResolves(t *testing.T) {
	// 存量 sessions.model 存的是纯 id（条目无 provider）：零迁移照旧解析。
	s, reg := newCompositeModelSession(t)
	if err := reg.Add(config.ModelConfig{
		ID: "legacy", BaseURL: "http://localhost:2", Model: "legacy-x",
		Format: config.FormatOpenAI, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.SetModel("legacy")
	if err != nil || got != "legacy" {
		t.Fatalf("存量纯 id SetModel: %q %v", got, err)
	}
	if s.ModelID() != "legacy" {
		t.Fatalf("存量模型解析: %q", s.ModelID())
	}
}

// 子会话继承父会话的选择时走的是同一个 SetModel：组合键必须原样传下去。
func TestDispatchInheritsCompositeKey(t *testing.T) {
	s, reg := newCompositeModelSession(t)
	// 没做过会话级选择时，Agent 绑定接受组合键（agents.model 同一条解析路径）
	ac := &sessiondata.AgentContext{Def: sessiondata.AgentDef{ID: "a1", Name: "A", Model: "p1::gpt-4o"}}
	m, err := s.modelFor(ac)
	if err != nil || m.Key() != "p1::gpt-4o" {
		t.Fatalf("Agent 绑定的组合键: %+v %v", m, err)
	}
	// 选定 p2 后 Agent 绑定让位（优先级不变，只是值也变成了组合键）
	if _, err := s.SetModel("p2::gpt-4o"); err != nil {
		t.Fatal(err)
	}
	if got := s.SelectedModel(); got != "p2::gpt-4o" {
		t.Fatalf("父会话选择: %q", got)
	}
	m, err = s.modelFor(ac)
	if err != nil || m.Key() != "p2::gpt-4o" {
		t.Fatalf("会话选择应压过 Agent 绑定: %+v %v", m, err)
	}
	// default 角色绑组合键：另一条没做过选择的会话经角色解析到它
	if err := reg.SetRole(config.RoleDefault, "p1::gpt-4o"); err != nil {
		t.Fatal(err)
	}
	fresh := New(reg, tools.New(), nil)
	t.Cleanup(fresh.Close)
	m2, err := fresh.modelFor(nil)
	if err != nil || m2.Key() != "p1::gpt-4o" {
		t.Fatalf("default 角色的组合键: %+v %v", m2, err)
	}
}
