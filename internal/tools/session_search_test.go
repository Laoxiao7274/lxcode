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
