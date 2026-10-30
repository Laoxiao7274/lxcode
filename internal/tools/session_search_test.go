package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// TestSessionSearchNotWired：未注入实现时给出可读错误（而不是 panic）。
func TestSessionSearchNotWired(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("session_search", `{"pattern":"x"}`))
	if !strings.Contains(got, "未启用") {
		t.Fatalf("未接线应报\"会话存储未启用\": %q", got)
	}
}

// TestSessionSearchWired：注入后查询参数正确传递、结果透传。
func TestSessionSearchWired(t *testing.T) {
	r := New()
	var got sessiondata.SearchQuery
	r.SetSessionSearch(func(_ context.Context, q sessiondata.SearchQuery) (string, error) {
		got = q
		return "找到 1 条历史消息", nil
	})

	res := r.Execute(context.Background(), call("session_search", `{"pattern":"端口|防火墙","max":20,"role":"user","context":4}`))
	if !strings.Contains(res, "找到 1 条历史消息") {
		t.Fatalf("结果未透传: %q", res)
	}
	if got.Pattern != "端口|防火墙" || got.Max != 20 || got.Role != "user" || got.Context != 4 {
		t.Fatalf("参数未正确传递: %+v", got)
	}
}

// TestSessionSearchDefaultsAndCaps：默认 max 30 / context 2、上限 max 100 / context 5、
// 空 pattern 与非法 role 拦截。
func TestSessionSearchDefaultsAndCaps(t *testing.T) {
	r := New()
	var got sessiondata.SearchQuery
	r.SetSessionSearch(func(_ context.Context, q sessiondata.SearchQuery) (string, error) {
		got = q
		return "", nil
	})

	// 默认 max 与默认上下文（默认不为 0：命中行常常只是提问，答案在它后面几条）
	r.Execute(context.Background(), call("session_search", `{"pattern":"x"}`))
	if got.Max != 30 {
		t.Fatalf("默认 max 应为 30: %d", got.Max)
	}
	if got.Context != sessionSearchDefaultContext {
		t.Fatalf("默认 context 应为 %d: %d", sessionSearchDefaultContext, got.Context)
	}
	// 超上限钳制
	r.Execute(context.Background(), call("session_search", `{"pattern":"x","max":9999,"context":99}`))
	if got.Max != 100 {
		t.Fatalf("超上限应钳到 100: %d", got.Max)
	}
	if got.Context != sessionSearchMaxContext {
		t.Fatalf("context 超上限应钳到 %d: %d", sessionSearchMaxContext, got.Context)
	}
	// 显式 context=0 是「只要命中本身」——与「没给」必须区分开（所以 schema 用指针）
	r.Execute(context.Background(), call("session_search", `{"pattern":"x","context":0}`))
	if got.Context != 0 {
		t.Fatalf("显式 context=0 应保持 0（不能被默认值顶掉）: %d", got.Context)
	}
	// 空 pattern
	if got := r.Execute(context.Background(), call("session_search", `{"pattern":""}`)); !strings.Contains(got, "不能为空") {
		t.Fatalf("空 pattern 应报错: %q", got)
	}
	// 非法 role 直接报错而不是静默当成「全部」——静默会让模型以为过滤生效了
	if got := r.Execute(context.Background(), call("session_search", `{"pattern":"x","role":"system"}`)); !strings.Contains(got, "role") {
		t.Fatalf("非法 role 应报错: %q", got)
	}
}

// TestSessionSearchFilterParams：session/days 新过滤参数透传（过滤判定的
// 真伪在 store 侧测试，这里只验参数链路）。
func TestSessionSearchFilterParams(t *testing.T) {
	r := New()
	var got sessiondata.SearchQuery
	r.SetSessionSearch(func(_ context.Context, q sessiondata.SearchQuery) (string, error) {
		got = q
		return "", nil
	})
	r.Execute(context.Background(), call("session_search", `{"pattern":"x","session":"abc","days":7}`))
	if got.Session != "abc" || got.Days != 7 {
		t.Fatalf("session/days 未透传: %+v", got)
	}
	// 不给 = 空值（默认全库语义）
	r.Execute(context.Background(), call("session_search", `{"pattern":"x"}`))
	if got.Session != "" || got.Days != 0 {
		t.Fatalf("默认应全库（Session 空 / Days 0）: %+v", got)
	}
}

// TestSessionSearchErrorPropagates：实现返回的错误变成自解释文本。
func TestSessionSearchErrorPropagates(t *testing.T) {
	r := New()
	r.SetSessionSearch(func(context.Context, sessiondata.SearchQuery) (string, error) {
		return "", errors.New("模式不是合法正则: missing closing )")
	})
	got := r.Execute(context.Background(), call("session_search", `{"pattern":"("}`))
	if !strings.Contains(got, "正则") {
		t.Fatalf("坏正则应透传错误信息: %q", got)
	}
}

// TestSessionSearchIsLowRisk：只读检索不占确认门。
func TestSessionSearchIsLowRisk(t *testing.T) {
	d, ok := New().Get("session_search")
	if !ok {
		t.Fatal("session_search 未注册")
	}
	if d.Risk != RiskLow {
		t.Fatal("session_search 应为低危（只读）")
	}
	if d.Confirm != nil {
		t.Fatal("低危工具不该有确认门")
	}
}

// TestSessionSearchWorkspace：扫描范围解析——默认取 ctx 注入的当前项目、
// all_projects=true 放开全库、显式 workspace 优先级最高、未分组注入空串退回全库。
func TestSessionSearchWorkspace(t *testing.T) {
	r := New()
	var got sessiondata.SearchQuery
	r.SetSessionSearch(func(_ context.Context, q sessiondata.SearchQuery) (string, error) {
		got = q
		return "", nil
	})

	// 注入当前项目：默认限定它
	r.Execute(WithWorkspace(context.Background(), "proj-a"), call("session_search", `{"pattern":"x"}`))
	if got.Workspace != "proj-a" {
		t.Fatalf("注入的当前项目应作为默认过滤: %+v", got)
	}
	// all_projects=true：放开全库（Workspace 空，覆盖注入值）
	r.Execute(WithWorkspace(context.Background(), "proj-a"), call("session_search", `{"pattern":"x","all_projects":true}`))
	if got.Workspace != "" {
		t.Fatalf("all_projects=true 应放开全库: %+v", got)
	}
	// 显式 workspace 优先级高于注入值与 all_projects
	r.Execute(WithWorkspace(context.Background(), "proj-a"), call("session_search", `{"pattern":"x","workspace":"proj-b"}`))
	if got.Workspace != "proj-b" {
		t.Fatalf("显式 workspace 应优先于注入值: %+v", got)
	}
	r.Execute(WithWorkspace(context.Background(), "proj-a"), call("session_search", `{"pattern":"x","workspace":"proj-c","all_projects":true}`))
	if got.Workspace != "proj-c" {
		t.Fatalf("显式 workspace 应优先于 all_projects: %+v", got)
	}
	// 未分组会话（注入空串）：退回全库——「当前项目」不存在，空值=不限语义
	r.Execute(WithWorkspace(context.Background(), ""), call("session_search", `{"pattern":"x"}`))
	if got.Workspace != "" {
		t.Fatalf("未分组会话（注入空串）应退回全库: %+v", got)
	}
	// 未注入（单测直调）：同样全库（不编造一个项目）
	r.Execute(context.Background(), call("session_search", `{"pattern":"x"}`))
	if got.Workspace != "" {
		t.Fatalf("未注入应全库（Workspace 空）: %+v", got)
	}
}

// TestSessionSearchScopeNote：非全库扫描应经 ctx 带范围说明（接线侧取它放进
// 结果头部）；全库扫描说明为空（结果不掺入「限定」字样）。
func TestSessionSearchScopeNote(t *testing.T) {
	r := New()
	var gotCtx context.Context
	r.SetSessionSearch(func(ctx context.Context, q sessiondata.SearchQuery) (string, error) {
		gotCtx = ctx // 工具包装过的 ctx（范围说明在里面）——接线侧同款取法
		return "结果", nil
	})

	// 限定当前项目：范围说明随 ctx 传出（接线侧 SearchScope 取它放进结果头部）
	r.Execute(WithWorkspace(context.Background(), "proj-a"), call("session_search", `{"pattern":"x"}`))
	if scope := SearchScope(gotCtx); scope == "" || !strings.Contains(scope, "proj-a") {
		t.Fatalf("限定项目时应带范围说明: %q", scope)
	}
	if gotCtx == nil || gotCtx == context.Background() {
		t.Fatalf("工具应包装 ctx 后再调实现")
	}
	// 全库：说明为空
	r.Execute(WithWorkspace(context.Background(), "proj-a"), call("session_search", `{"pattern":"x","all_projects":true}`))
	if scope := SearchScope(gotCtx); scope != "" {
		t.Fatalf("全库扫描不应带范围说明: %q", scope)
	}
	// 未分组（注入空串）退回全库：同样无说明
	r.Execute(WithWorkspace(context.Background(), ""), call("session_search", `{"pattern":"x"}`))
	if scope := SearchScope(gotCtx); scope != "" {
		t.Fatalf("未分组退回全库不应带范围说明: %q", scope)
	}
}
