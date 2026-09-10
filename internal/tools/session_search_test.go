package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSessionSearchNotWired：未注入实现时给出可读错误（而不是 panic）。
func TestSessionSearchNotWired(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("session_search", `{"pattern":"x"}`))
	if !strings.Contains(got, "未启用") {
		t.Fatalf("未接线应报\"会话存储未启用\": %q", got)
	}
}

// TestSessionSearchWired：注入后 pattern/max 正确传递、结果透传。
func TestSessionSearchWired(t *testing.T) {
	r := New()
	var gotPattern string
	var gotMax int
	r.SetSessionSearch(func(ctx context.Context, pattern string, max int) (string, error) {
		gotPattern, gotMax = pattern, max
		return "找到 1 条历史消息", nil
	})

	res := r.Execute(context.Background(), call("session_search", `{"pattern":"端口|防火墙","max":20}`))
	if !strings.Contains(res, "找到 1 条历史消息") {
		t.Fatalf("结果未透传: %q", res)
	}
	if gotPattern != "端口|防火墙" || gotMax != 20 {
		t.Fatalf("参数未正确传递: pattern=%q max=%d", gotPattern, gotMax)
	}
}

// TestSessionSearchDefaultsAndCaps：默认 30、上限 100、空 pattern 拦截。
func TestSessionSearchDefaultsAndCaps(t *testing.T) {
	r := New()
	var gotMax int
	r.SetSessionSearch(func(ctx context.Context, pattern string, max int) (string, error) {
		gotMax = max
		return "", nil
	})

	// 默认 max
	r.Execute(context.Background(), call("session_search", `{"pattern":"x"}`))
	if gotMax != 30 {
		t.Fatalf("默认 max 应为 30: %d", gotMax)
	}
	// 超上限钳到 100
	r.Execute(context.Background(), call("session_search", `{"pattern":"x","max":9999}`))
	if gotMax != 100 {
		t.Fatalf("超上限应钳到 100: %d", gotMax)
	}
	// 空 pattern
	if got := r.Execute(context.Background(), call("session_search", `{"pattern":""}`)); !strings.Contains(got, "不能为空") {
		t.Fatalf("空 pattern 应报错: %q", got)
	}
}

// TestSessionSearchErrorPropagates：实现返回的错误变成自解释文本。
func TestSessionSearchErrorPropagates(t *testing.T) {
	r := New()
	r.SetSessionSearch(func(ctx context.Context, pattern string, max int) (string, error) {
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
