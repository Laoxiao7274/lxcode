// agent_read / agent_write 的工具层测试：注入桩 ops 验证列表/单读格式、
// 参数校验、字段级更新的传递，以及 agent_write 的高危确认门（Risk/Mutates/
// Confirm 文案——运行期 gate 由 agent.runTools 的既有路径按 Risk 驱动，
// 见 internal/agent/tools.go 的 Confirm 分支）。
package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func fakeAgents() []sessiondata.AgentDef {
	return []sessiondata.AgentDef{
		{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true, Tools: []string{"agent_dispatch"}, Skills: []string{}, Delegates: []string{"coder"}},
		{ID: "tester", Name: "测试 Agent", Enabled: true, Approval: "confirm",
			Prompt: "你是测试 Agent。",
			Desc:   "验证与测试",
			Tools:  []string{"read_file", "bash"},
			Skills: []string{"gsap"},
		},
	}
}

func callOf(name, args string) llm.ToolCall {
	var c llm.ToolCall
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

func newAgentCatalogRegistry(t *testing.T, ops AgentCatalogOps) *Registry {
	t.Helper()
	r := New()
	r.SetAgentCatalogOps(ops)
	return r
}

func TestAgentReadListsAndReads(t *testing.T) {
	r := newAgentCatalogRegistry(t, AgentCatalogOps{
		List: func() ([]sessiondata.AgentDef, error) { return fakeAgents(), nil },
	})

	t.Run("列表：含 id/白名单/技能，不含模板全文", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(AgentReadToolName, `{}`))
		for _, want := range []string{"main", "tester", "agent_dispatch", "read_file", "gsap"} {
			if !strings.Contains(out, want) {
				t.Fatalf("列表缺 %s:\n%s", want, out)
			}
		}
		if strings.Contains(out, "你是测试 Agent。") {
			t.Fatalf("列表视图不应带模板全文（上下文考虑）:\n%s", out)
		}
	})

	t.Run("单读：模板全文在场", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(AgentReadToolName, `{"id":"tester"}`))
		if !strings.Contains(out, "你是测试 Agent。") || !strings.Contains(out, "委派名单") {
			t.Fatalf("单读应含模板全文与委派名单:\n%s", out)
		}
	})

	t.Run("未知 id 报错并列出可用项", func(t *testing.T) {
		out := r.Execute(context.Background(), callOf(AgentReadToolName, `{"id":"ghost"}`))
		if !strings.Contains(out, "不存在") || !strings.Contains(out, "tester") {
			t.Fatalf("未知 id 应报错并列可用项:\n%s", out)
		}
	})

	t.Run("未装配报自解释错误", func(t *testing.T) {
		r2 := New() // 不注入 ops
		out := r2.Execute(context.Background(), callOf(AgentReadToolName, `{}`))
		if !strings.Contains(out, "未装配") {
			t.Fatalf("未注入 ops 应报「未装配」:\n%s", out)
		}
	})
}

func TestAgentWriteFieldLevelUpdate(t *testing.T) {
	var got struct {
		id string
		u  sessiondata.AgentFieldUpdate
	}
	r := newAgentCatalogRegistry(t, AgentCatalogOps{
		List: func() ([]sessiondata.AgentDef, error) { return fakeAgents(), nil },
		Update: func(id string, u sessiondata.AgentFieldUpdate) (string, error) {
			got.id, got.u = id, u
			return "已更新 Agent「" + id + "」的配置并落库——该 Agent 下次被派发就按新配置运行。", nil
		},
	})

	t.Run("字段级写只传提供的字段", func(t *testing.T) {
		prompt := "新的模板"
		out := r.Execute(context.Background(), callOf(AgentWriteToolName,
			`{"id":"tester","prompt":"新的模板","tools":["read_file"]}`))
		if strings.HasPrefix(out, "错误") {
			t.Fatalf("合法更新不应报错: %s", out)
		}
		if got.id != "tester" || got.u.Prompt == nil || *got.u.Prompt != prompt {
			t.Fatalf("Update 收到的参数不对: id=%s u=%+v", got.id, got.u)
		}
		if got.u.Desc != nil || got.u.Skills != nil {
			t.Fatalf("未提供的字段应保持 nil: %+v", got.u)
		}
		if len(got.u.Tools) != 1 || got.u.Tools[0] != "read_file" {
			t.Fatalf("白名单应全量替换: %v", got.u.Tools)
		}
	})

	t.Run("缺 id / 无字段都被工具层拦下", func(t *testing.T) {
		for _, tc := range []struct {
			name, args, want string
		}{
			{"缺 id", `{"prompt":"x"}`, "缺少 id"},
			{"没有任何字段", `{"id":"tester"}`, "没有提供任何要改的字段"},
		} {
			out := r.Execute(context.Background(), callOf(AgentWriteToolName, tc.args))
			if !strings.Contains(out, "错误") || !strings.Contains(out, tc.want) {
				t.Fatalf("%s: 应报 %q，得 %s", tc.name, tc.want, out)
			}
		}
	})

	t.Run("空白数组 = 显式清空，原样传给落库层（拒与许是校验层的事）", func(t *testing.T) {
		r.Execute(context.Background(), callOf(AgentWriteToolName, `{"id":"tester","tools":[]}`))
		if got.u.Tools == nil || len(got.u.Tools) != 0 {
			t.Fatalf("空白数组应作为显式清空传递: %+v", got.u.Tools)
		}
		// 清空的拒绝在共享校验层（sessiondata）与 store 落库路径测试里钉住。
	})
}

func TestAgentWriteConfirmGate(t *testing.T) {
	// 高危确认门的结构断言：Risk=high + Mutates=true（strict 档拒绝、confirm 档
	// 弹确认——由 agent.runTools 按 Risk/Mutates 驱动）+ Confirm 文案列字段。
	d, ok := New().Get(AgentWriteToolName)
	if !ok {
		t.Fatal("agent_write 应已注册")
	}
	if d.Risk != RiskHigh || !d.Mutates {
		t.Fatalf("agent_write 应为高危可变更: risk=%v mutates=%v", d.Risk, d.Mutates)
	}
	rd, _ := New().Get(AgentReadToolName)
	if rd.Risk != RiskLow || rd.Mutates {
		t.Fatalf("agent_read 应为低危只读: risk=%v mutates=%v", rd.Risk, rd.Mutates)
	}

	t.Run("确认文案列明目标与字段", func(t *testing.T) {
		prompt := d.Confirm(context.Background(), []byte(`{"id":"tester","prompt":"新的模板","tools":["bash"]}`))
		for _, want := range []string{"tester", "系统提示词模板", "工具白名单", "下次被派发"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("确认文案缺 %q:\n%s", want, prompt)
			}
		}
	})

	t.Run("无字段时确认文案明示空转", func(t *testing.T) {
		prompt := d.Confirm(context.Background(), []byte(`{"id":"tester"}`))
		if !strings.Contains(prompt, "不会产生变化") {
			t.Fatalf("空字段确认文案应明示空转:\n%s", prompt)
		}
	})
}
