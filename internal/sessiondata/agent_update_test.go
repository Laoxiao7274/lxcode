package sessiondata

import (
	"strings"
	"testing"
)

func baseDef() AgentDef {
	return AgentDef{ID: "tester", Name: "测试 Agent", Enabled: true, Custom: true}
}

func always(bool2 bool) func(string) bool {
	return func(string) bool { return bool2 }
}

func TestValidateAgentFieldUpdate(t *testing.T) {
	t.Run("合法更新通过（改模板+白名单+技能+描述）", func(t *testing.T) {
		prompt := "你是测试 Agent。"
		desc := "验证与测试"
		u := AgentFieldUpdate{Prompt: &prompt, Desc: &desc, Tools: []string{"read_file"}, Skills: []string{"gsap"}}
		if err := ValidateAgentFieldUpdate(baseDef(), u, always(true), always(true)); err != nil {
			t.Fatalf("合法更新不应报错: %v", err)
		}
	})

	t.Run("主 Agent 拒改", func(t *testing.T) {
		main := baseDef()
		main.IsMain = true
		prompt := "x"
		if err := ValidateAgentFieldUpdate(main, AgentFieldUpdate{Prompt: &prompt}, always(true), always(true)); err == nil {
			t.Fatal("主 Agent 的字段级更新应拒绝")
		}
	})

	t.Run("模板空白拒绝", func(t *testing.T) {
		for _, p := range []string{"", "   \n\t "} {
			p := p
			if err := ValidateAgentFieldUpdate(baseDef(), AgentFieldUpdate{Prompt: &p}, always(true), always(true)); err == nil {
				t.Fatalf("空白模板应拒绝: %q", p)
			}
		}
	})

	t.Run("白名单清空拒绝", func(t *testing.T) {
		if err := ValidateAgentFieldUpdate(baseDef(), AgentFieldUpdate{Tools: []string{}}, always(true), always(true)); err == nil {
			t.Fatal("清空白名单应拒绝")
		}
	})

	t.Run("未知工具名拒绝（逐个列出）", func(t *testing.T) {
		known := map[string]bool{"read_file": true}
		exists := func(name string) bool { return known[name] }
		err := ValidateAgentFieldUpdate(baseDef(), AgentFieldUpdate{Tools: []string{"read_file", "nope", "also_nope"}}, exists, always(true))
		if err == nil {
			t.Fatal("未知工具名应拒绝")
		}
		for _, want := range []string{"nope", "also_nope"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("错误应列出 %s: %v", want, err)
			}
		}
		if strings.Contains(err.Error(), "read_file") {
			t.Fatalf("存在的工具不该被点名: %v", err)
		}
	})

	t.Run("未知技能 id 拒绝", func(t *testing.T) {
		err := ValidateAgentFieldUpdate(baseDef(), AgentFieldUpdate{Skills: []string{"ghost-skill"}}, always(true), always(false))
		if err == nil || !strings.Contains(err.Error(), "ghost-skill") {
			t.Fatalf("未知技能应拒绝并列名: %v", err)
		}
	})

	t.Run("技能清空合法（不绑技能是合法配置）", func(t *testing.T) {
		if err := ValidateAgentFieldUpdate(baseDef(), AgentFieldUpdate{Skills: []string{}}, always(true), always(true)); err != nil {
			t.Fatalf("清空技能绑定不应报错: %v", err)
		}
	})
}

