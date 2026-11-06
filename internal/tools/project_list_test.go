// project_list（助手项目全景三件套之二）的验收：未装配报人话错误、
// 注入后格式正确（id（name）：path，N 个会话，最近活动）、空清单也是有效回答。
package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func TestProjectListNotWired(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call(ProjectListToolName, `{}`))
	if !strings.Contains(got, "未装配") {
		t.Fatalf("未装配应报人话错误: %q", got)
	}
}

func TestProjectListWired(t *testing.T) {
	r := New()
	r.SetProjectList(func(context.Context) ([]sessiondata.ProjectStats, error) {
		return []sessiondata.ProjectStats{
			{ProjectMeta: sessiondata.ProjectMeta{ID: "p-1", Name: "lxcode", Path: "C:/repo/lxcode"},
				Sessions: 3, UpdatedAt: "2026-12-20 10:30"},
			{ProjectMeta: sessiondata.ProjectMeta{ID: "p-2", Name: "site", Path: "C:/repo/site"}},
		}, nil
	})
	got := r.Execute(context.Background(), call(ProjectListToolName, `{}`))
	for _, want := range []string{
		"共 2 个项目",
		"- p-1（lxcode）：C:/repo/lxcode，3 个会话，最近活动 2026-12-20 10:30",
		"- p-2（site）：C:/repo/site，0 个会话，无活动",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("输出缺 %q:\n%s", want, got)
		}
	}
}

func TestProjectListEmptyAndError(t *testing.T) {
	r := New()
	r.SetProjectList(func(context.Context) ([]sessiondata.ProjectStats, error) {
		return nil, nil
	})
	if got := r.Execute(context.Background(), call(ProjectListToolName, `{}`)); !strings.Contains(got, "还没有注册任何项目") {
		t.Fatalf("空清单应给有效回答: %q", got)
	}
	r.SetProjectList(func(context.Context) ([]sessiondata.ProjectStats, error) {
		return nil, errors.New("database offline")
	})
	if got := r.Execute(context.Background(), call(ProjectListToolName, `{}`)); !strings.Contains(got, "offline") {
		t.Fatalf("实现错误应透传: %q", got)
	}
}

// 注册存在性：名字进注册表、描述点明 run_task 的 project_id 从这里取。
func TestProjectListRegistered(t *testing.T) {
	r := New()
	d, ok := r.Get(ProjectListToolName)
	if !ok {
		t.Fatal("project_list 应注册进内置清单")
	}
	if d.Risk != RiskLow {
		t.Fatalf("project_list 应是低危: %v", d.Risk)
	}
	if !strings.Contains(d.Description, "run_task") || !strings.Contains(d.Description, "project_id") {
		t.Fatalf("描述应指明 run_task 的 project_id 来源: %q", d.Description)
	}
}
