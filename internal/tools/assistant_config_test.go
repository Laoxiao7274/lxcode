package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// regWithAssistantConfig 起一个带 update_assistant_config 的注册表（可选注入保存口）。
func regWithAssistantConfig(t *testing.T, fn AssistantConfigSaveFn) *Registry {
	t.Helper()
	r := New()
	if fn != nil {
		r.SetAssistantConfigFn(fn)
	}
	return r
}

func mustExec(t *testing.T, r *Registry, args string) string {
	t.Helper()
	var call llm.ToolCall
	call.Function.Name = AssistantConfigToolName
	call.Function.Arguments = args
	return r.Execute(context.Background(), call)
}

func TestAssistantConfigToolNotAttached(t *testing.T) {
	r := regWithAssistantConfig(t, nil)
	out := mustExec(t, r, `{"name":"小灵"}`)
	if got := "助手配置写入未装配"; !contains(out, got) {
		t.Fatalf("未装配应回自解释错误: %q", out)
	}
}

func TestAssistantConfigToolSavesMergedPatch(t *testing.T) {
	r := regWithAssistantConfig(t, func(p AssistantConfigPatch) error {
		// 部分更新语义：只带 name = 只改 name（skills/workflow nil 不动——
		// server 侧闭包负责读当前合并，这里钉「工具层把 nil 透传」）。
		if p.Name == nil || *p.Name != "小灵" {
			t.Fatalf("应透传 name: %+v", p)
		}
		if p.Persona != nil || p.Skills != nil || p.Workflow != nil {
			t.Fatalf("未指定的字段应保持 nil: %+v", p)
		}
		return nil
	})
	out := mustExec(t, r, `{"name":"小灵"}`)
	if !contains(out, "已保存") || !contains(out, "小灵") {
		t.Fatalf("成功结果应说明改了什么: %q", out)
	}
}

func TestAssistantConfigToolSaveError(t *testing.T) {
	r := regWithAssistantConfig(t, func(AssistantConfigPatch) error {
		return errors.New("模块 id 不存在: xxx")
	})
	out := mustExec(t, r, `{"persona":"轻松"}`)
	if !contains(out, "模块 id 不存在") {
		t.Fatalf("保存失败应把原因回给模型: %q", out)
	}
}

func TestAssistantConfigToolNoFields(t *testing.T) {
	r := regWithAssistantConfig(t, func(AssistantConfigPatch) error {
		t.Fatal("空参数不应触发写盘")
		return nil
	})
	out := mustExec(t, r, `{}`)
	if !contains(out, "至少给一个") {
		t.Fatalf("空参数应回人话错误: %q", out)
	}
}

func TestAssistantConfigToolPersonaClear(t *testing.T) {
	r := regWithAssistantConfig(t, func(p AssistantConfigPatch) error {
		if p.Persona == nil || *p.Persona != "" {
			t.Fatalf("应透传空串（清除语义）: %+v", p)
		}
		return nil
	})
	out := mustExec(t, r, `{"persona":""}`)
	if !contains(out, "已保存") {
		t.Fatalf("清除也应成功: %q", out)
	}
}

func TestAssistantConfigToolRegistered(t *testing.T) {
	r := New()
	d, ok := r.Get(AssistantConfigToolName)
	if !ok {
		t.Fatal("update_assistant_config 应注册进注册表")
	}
	if d.Risk != RiskLow {
		t.Fatalf("应是低危: %v", d.Risk)
	}
	if d.Mutates {
		t.Fatalf("不应标记 Mutates（只写配置文件，strict 只读档下它也不弹门）: %v", d.Mutates)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
