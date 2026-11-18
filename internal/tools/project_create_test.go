// project_create（助手项目全景三件套之三）的验收：
// 未装配报人话错误、接线后参数校验（name 禁路径分隔符/../空、parent 必填）、
// 注册声明（高危 + Mutates + ConfirmForced 硬闸回调存在且命中）、
// 成功路径的返回文本带 project_id 与 run_task 提示。
package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProjectCreateNotWired(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call(ProjectCreateToolName, `{"name":"demo","parent":"C:/repo"}`))
	if !strings.Contains(got, "未装配") {
		t.Fatalf("未装配应报人话错误: %q", got)
	}
}

func TestProjectCreateParamValidation(t *testing.T) {
	r := New()
	r.SetProjectCreate(func(context.Context, string, string) (ProjectCreateResult, error) {
		t.Fatal("校验不过不应到达接线实现")
		return ProjectCreateResult{}, nil
	})
	cases := []struct {
		name string
		args string
		want string
	}{
		{"name 空白", `{"name":"  ","parent":"C:/repo"}`, "不能为空"},
		{"name 带斜杠", `{"name":"a/b","parent":"C:/repo"}`, "路径分隔符"},
		{"name 带反斜杠", `{"name":"a\\b","parent":"C:/repo"}`, "路径分隔符"},
		{"name 带父目录点", `{"name":"../x","parent":"C:/repo"}`, ".."},
		{"parent 空", `{"name":"demo","parent":" "}`, "必填"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Execute(context.Background(), call(ProjectCreateToolName, tc.args))
			if !strings.Contains(got, tc.want) {
				t.Fatalf("应拒绝并说明原因 %q: %q", tc.want, got)
			}
		})
	}
}

func TestProjectCreateWired(t *testing.T) {
	r := New()
	var gotName, gotParent string
	r.SetProjectCreate(func(_ context.Context, name, parent string) (ProjectCreateResult, error) {
		gotName, gotParent = name, parent
		return ProjectCreateResult{ID: "p-9", Name: name, Path: parent + "/" + name}, nil
	})
	got := r.Execute(context.Background(), call(ProjectCreateToolName, `{"name":"demo","parent":"C:/repo"}`))
	if gotName != "demo" || gotParent != "C:/repo" {
		t.Fatalf("接线实现应收到 name/parent: %q %q", gotName, gotParent)
	}
	for _, want := range []string{"p-9", "run_task", "C:/repo/demo"} {
		if !strings.Contains(got, want) {
			t.Fatalf("输出缺 %q:\n%s", want, got)
		}
	}
}

func TestProjectCreateImplError(t *testing.T) {
	r := New()
	r.SetProjectCreate(func(context.Context, string, string) (ProjectCreateResult, error) {
		return ProjectCreateResult{}, errors.New("目标目录已存在且非空")
	})
	got := r.Execute(context.Background(), call(ProjectCreateToolName, `{"name":"demo","parent":"C:/repo"}`))
	if !strings.Contains(got, "非空") {
		t.Fatalf("实现错误应透传: %q", got)
	}
}

// 注册存在性：名字进注册表、高危 + Mutates、ConfirmForced 命中（硬闸回调存在）。
func TestProjectCreateRegistered(t *testing.T) {
	r := New()
	d, ok := r.Get(ProjectCreateToolName)
	if !ok {
		t.Fatal("project_create 应注册进内置清单")
	}
	if d.Risk != RiskHigh || !d.Mutates {
		t.Fatalf("project_create 应是高危 + 变更: risk=%v mutates=%v", d.Risk, d.Mutates)
	}
	if d.ConfirmForced == nil {
		t.Fatal("project_create 必须声明 ConfirmForced（auto 档硬闸）")
	}
	prompt := r.ConfirmForced(context.Background(), call(ProjectCreateToolName, `{"name":"demo","parent":"C:/repo"}`))
	if prompt == "" || !strings.Contains(prompt, "demo") || !strings.Contains(prompt, "C:/repo") {
		t.Fatalf("强制确认文案应带 name 与 parent: %q", prompt)
	}
	if prompt := r.ConfirmForced(context.Background(), call(ProjectListToolName, `{}`)); prompt != "" {
		t.Fatalf("project_list 不应有强制确认面: %q", prompt)
	}
}
