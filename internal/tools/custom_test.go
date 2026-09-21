package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// binarySpec 造一个 binary 工具条目（参数面固定：必填 pattern + 可选 path）。
func binarySpec(command string) sessiondata.ToolSpec {
	return sessiondata.ToolSpec{
		ID: "probe", Desc: "测试用自定义工具", Risk: "low", Source: "binary", Command: command,
		Params: []sessiondata.ToolParam{
			{Name: "pattern", Type: "regex", Required: true, Desc: "检索正则"},
			{Name: "path", Type: "string", Desc: "检索根目录"},
		},
	}
}

func mustCustomDef(t *testing.T, spec sessiondata.ToolSpec) *Def {
	t.Helper()
	d, err := CustomDef(spec)
	if err != nil {
		t.Fatalf("CustomDef(%s): %v", spec.ID, err)
	}
	return d
}

// TestCustomDefSchemaFromParams：目录里的参数简写类型必须映射成合法 JSON Schema
// （int→integer、regex→string、array→array），required 只收必填项。
func TestCustomDefSchemaFromParams(t *testing.T) {
	spec := binarySpec("probe --all")
	spec.Params = []sessiondata.ToolParam{
		{Name: "pattern", Type: "regex", Required: true, Desc: "正则"},
		{Name: "limit", Type: "int"},
		{Name: "deep", Type: "bool"},
		{Name: "tags", Type: "array"},
	}
	d := mustCustomDef(t, spec)
	var schema struct {
		Type       string                       `json:"type"`
		Properties map[string]map[string]string `json:"properties"`
		Required   []string                     `json:"required"`
	}
	if err := json.Unmarshal(d.Parameters, &schema); err != nil {
		t.Fatalf("schema 不是合法 JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("schema type = %q，应为 object", schema.Type)
	}
	want := map[string]string{"pattern": "string", "limit": "integer", "deep": "boolean", "tags": "array"}
	for name, typ := range want {
		if got := schema.Properties[name]["type"]; got != typ {
			t.Errorf("参数 %s 的 schema 类型 = %q，应为 %q", name, got, typ)
		}
	}
	if schema.Properties["pattern"]["description"] != "正则" {
		t.Errorf("参数说明丢失: %+v", schema.Properties["pattern"])
	}
	if len(schema.Required) != 1 || schema.Required[0] != "pattern" {
		t.Errorf("required = %v，应只有 pattern", schema.Required)
	}
}

// TestCustomDefRejectsBadSpec：坏目录条目必须在注册时就报错（而不是等模型调用），
// 且错误自解释——调用方据此跳过并记日志。
func TestCustomDefRejectsBadSpec(t *testing.T) {
	cases := []struct {
		name string
		spec func(s sessiondata.ToolSpec) sessiondata.ToolSpec
		want string
	}{
		{"非 binary 来源", func(s sessiondata.ToolSpec) sessiondata.ToolSpec { s.Source = "builtin"; return s }, "binary"},
		{"缺 command", func(s sessiondata.ToolSpec) sessiondata.ToolSpec { s.Command = ""; return s }, "command"},
		{"占位符未声明", func(s sessiondata.ToolSpec) sessiondata.ToolSpec { s.Command = "rg {nope}"; return s }, "{nope}"},
		{"参数名重复", func(s sessiondata.ToolSpec) sessiondata.ToolSpec {
			s.Params = append(s.Params, sessiondata.ToolParam{Name: "pattern"})
			return s
		}, "重复"},
		{"参数名为空", func(s sessiondata.ToolSpec) sessiondata.ToolSpec {
			s.Params = append(s.Params, sessiondata.ToolParam{Name: " "})
			return s
		}, "不能为空"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CustomDef(c.spec(binarySpec("rg {pattern}")))
			if err == nil {
				t.Fatal("应当报错")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息应包含 %q，实际: %v", c.want, err)
			}
		})
	}
}

// TestRenderCommand：模板渲染是自定义工具的安全核心——值原样单参（含空格不切分）、
// 支持 --flag={p} 内联、缺参/未知参/非标量都自解释报错。
func TestRenderCommand(t *testing.T) {
	spec := binarySpec("rg --json {pattern} {path}")
	cases := []struct {
		name    string
		command string
		args    string
		want    []string
		wantErr string
	}{
		{"整参替换", "rg --json {pattern} {path}", `{"pattern":"a b","path":"src"}`, []string{"rg", "--json", "a b", "src"}, ""},
		{"内联替换", "rg --path={path} {pattern}", `{"pattern":"x","path":"a b"}`, []string{"rg", "--path=a b", "x"}, ""},
		{"只给必填", "rg {pattern}", `{"pattern":"x"}`, []string{"rg", "x"}, ""},
		{"缺参", "rg {pattern} {path}", `{"path":"src"}`, nil, "缺少参数 {pattern}"},
		{"未知参", "rg {pattern}", `{"pattern":"x","limit":"3"}`, nil, "不支持参数"},
		{"数组参", "rg {pattern}", `{"pattern":["a","b"]}`, nil, "不是标量"},
		{"null 参", "rg {pattern}", `{"pattern":null}`, nil, "null"},
		{"坏 JSON", "rg {pattern}", `{"pattern":`, nil, "解析失败"},
		{"数字与布尔", "rg --max={pattern} --deep={path}", `{"pattern":3,"path":true}`, []string{"rg", "--max=3", "--deep=true"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := spec
			s.Command = c.command
			argv, err := renderCommand(s, c.command, json.RawMessage(c.args))
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("应报含 %q 的错误，实际: %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("渲染失败: %v", err)
			}
			if strings.Join(argv, "\x00") != strings.Join(c.want, "\x00") {
				t.Fatalf("argv = %q，应为 %q", argv, c.want)
			}
		})
	}
}

// TestCustomDefRiskGate：高危工具必须带确认文本（且含渲染后的完整命令——
// 用户批准前要看到实际跑什么）；低危自动执行（无确认文本）。
func TestCustomDefRiskGate(t *testing.T) {
	low := mustCustomDef(t, binarySpec("rg {pattern}"))
	if low.Risk != RiskLow {
		t.Fatalf("risk = %v，应为 low", low.Risk)
	}
	if got := low.Confirm(context.Background(), json.RawMessage(`{"pattern":"x"}`)); got != "" {
		t.Fatalf("低危工具不应要求确认，得到: %q", got)
	}
	if !low.Mutates {
		t.Fatal("自定义工具起进程即变更外部世界，Mutates 必须为 true（strict 只读模式据此拒绝）")
	}

	highSpec := binarySpec("rg {pattern}")
	highSpec.Risk = "high"
	high := mustCustomDef(t, highSpec)
	if high.Risk != RiskHigh {
		t.Fatalf("risk = %v，应为 high", high.Risk)
	}
	got := high.Confirm(context.Background(), json.RawMessage(`{"pattern":"secret"}`))
	if !strings.Contains(got, "rg secret") {
		t.Fatalf("确认文本应含渲染后的完整命令，实际: %q", got)
	}
	// 参数不完整时也要给出可判断的文本（不能静默放行）
	if got := high.Confirm(context.Background(), json.RawMessage(`{}`)); !strings.Contains(got, "建议拒绝") {
		t.Fatalf("参数不完整时确认文本应提示拒绝，实际: %q", got)
	}
}

// TestCustomDefExec：真的起进程并回填输出。
func TestCustomDefExec(t *testing.T) {
	d := mustCustomDef(t, binarySpec("go version"))
	out, err := d.Exec(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if !strings.Contains(out, "go version go") {
		t.Fatalf("输出不含 go version 字样: %q", out)
	}
}

// TestCustomDefExecWorkDir：子进程 cwd 必须是会话工作目录（项目会话 = 项目根）——
// 用 go env GOMOD 判定：仓库里有 go.mod，临时目录里打印 NUL//dev/null。
func TestCustomDefExecWorkDir(t *testing.T) {
	d := mustCustomDef(t, binarySpec("go env GOMOD"))
	ctx := WithWorkDir(context.Background(), t.TempDir())
	out, err := d.Exec(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if strings.Contains(out, "go.mod") {
		t.Fatalf("子进程未在会话工作目录执行（打印出了仓库的 go.mod）: %q", out)
	}
}

// TestCustomDefExecNotes：退出码与启动失败都要以文本回填（模型据此换方法），
// 而不是让整轮中断。
func TestCustomDefExecNotes(t *testing.T) {
	// 非零退出码：go 的未定义 flag → 退出码 2
	bad := mustCustomDef(t, binarySpec("go --definitely-not-a-flag"))
	out, err := bad.Exec(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("非零退出码不是执行错误: %v", err)
	}
	if !strings.Contains(out, "[退出码 2]") {
		t.Fatalf("应回填退出码，实际: %q", out)
	}
	// 启动失败：可执行文件不存在
	missing := mustCustomDef(t, binarySpec("lxcode-no-such-binary-xyz"))
	out, err = missing.Exec(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("启动失败也应以文本回填: %v", err)
	}
	if !strings.Contains(out, "[启动失败") {
		t.Fatalf("应回填启动失败，实际: %q", out)
	}
}

// TestRegistrySetDynamic：动态段整体替换（删除即消失）、内置段不动、同名跳过。
func TestRegistrySetDynamic(t *testing.T) {
	r := New()
	builtinCount := len(r.Order())

	a := mustCustomDef(t, binarySpec("rg {pattern}"))
	a.Name = "rg"
	b := mustCustomDef(t, binarySpec("fd {pattern}"))
	b.Name = "fd"

	if skipped := r.SetDynamic([]*Def{a}); len(skipped) != 0 {
		t.Fatalf("不应跳过任何工具: %v", skipped)
	}
	if _, ok := r.Get("rg"); !ok {
		t.Fatal("动态工具 rg 未注册")
	}
	if len(r.LLMTools()) != builtinCount+1 {
		t.Fatalf("wire 声明数 = %d，应为 %d", len(r.LLMTools()), builtinCount+1)
	}
	// 同名内置：跳过并返回（内置实现优先）
	shadow := mustCustomDef(t, binarySpec("bash"))
	shadow.Name = "bash"
	skipped := r.SetDynamic([]*Def{shadow, b})
	if len(skipped) != 1 || skipped[0] != "bash" {
		t.Fatalf("应跳过与内置同名的 bash，实际: %v", skipped)
	}
	if _, ok := r.Get("rg"); ok {
		t.Fatal("整体替换后 rg 应消失（目录是事实源）")
	}
	if _, ok := r.Get("fd"); !ok {
		t.Fatal("fd 应已注册")
	}
	if d, ok := r.Get("bash"); !ok || d.Description == "" || strings.Contains(d.Description, "测试用") {
		t.Fatal("内置 bash 应保持内置实现")
	}
	// 清空动态段
	r.SetDynamic(nil)
	if len(r.Order()) != builtinCount {
		t.Fatalf("清空后工具数 = %d，应为 %d", len(r.Order()), builtinCount)
	}
}
