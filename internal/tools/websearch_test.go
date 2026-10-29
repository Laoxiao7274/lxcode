package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/websearch"
)

// webSearchCall 构造一次 web_search 调用（坏 JSON 也允许，用来测修复路径）。
func webSearchCall(args string) llm.ToolCall {
	var c llm.ToolCall
	c.Function.Name = WebSearchToolName
	c.Function.Arguments = args
	return c
}

// stubSearch 挂一个可控的搜索实现（返回给定的响应或错误）。
func stubSearch(r *Registry, resp websearch.Response, err error) {
	r.SetWebSearch(func(context.Context, string, websearch.Options) (websearch.Response, error) {
		return resp, err
	})
}

func TestWebSearchToolIsLowRiskReadOnly(t *testing.T) {
	r := New()
	d, ok := r.Get(WebSearchToolName)
	if !ok {
		t.Fatalf("应注册 %s 工具", WebSearchToolName)
	}
	// 搜索是只读操作：strict 只读模式下必须可用——标成变更类会让
	// 「只读模式」连查资料都做不到。
	if d.Risk != RiskLow {
		t.Errorf("%s 应为低危（自动执行），实际 %v", WebSearchToolName, d.Risk)
	}
	if d.Mutates {
		t.Errorf("%s 不该标记为变更外部世界", WebSearchToolName)
	}
	if r.IsMutating(WebSearchToolName) {
		t.Error("strict 只读模式不该拒绝 web_search")
	}
	if r.Confirm(context.Background(), webSearchCall(`{"query":"x"}`)) != "" {
		t.Error("web_search 不该需要人工确认")
	}
}

// 未装配搜索服务时（如直接跑内核的调用方）必须自解释报错，不能 panic。
func TestWebSearchUnwiredReportsClearly(t *testing.T) {
	r := New()
	out := r.Execute(context.Background(), webSearchCall(`{"query":"golang"}`))
	if !strings.Contains(out, "未装配") {
		t.Errorf("未装配应给出明确说明，实际: %s", out)
	}
}

func TestWebSearchRejectsEmptyQuery(t *testing.T) {
	r := New()
	r.SetWebSearch(func(context.Context, string, websearch.Options) (websearch.Response, error) {
		t.Fatal("空查询不该走到搜索实现")
		return websearch.Response{}, nil
	})
	out := r.Execute(context.Background(), webSearchCall(`{"query":"   "}`))
	if !strings.Contains(out, "query 不能为空") {
		t.Errorf("空查询应被拒绝，实际: %s", out)
	}
}

func TestWebSearchRejectsBadRecency(t *testing.T) {
	r := New()
	r.SetWebSearch(func(context.Context, string, websearch.Options) (websearch.Response, error) {
		t.Fatal("非法 recency 不该走到搜索实现")
		return websearch.Response{}, nil
	})
	out := r.Execute(context.Background(), webSearchCall(`{"query":"x","recency":"hour"}`))
	if !strings.Contains(out, "recency") {
		t.Errorf("非法 recency 应被拒绝并说明取值，实际: %s", out)
	}
}

// 参数必须原样传到搜索实现：条数/时间范围/域名过滤三项都不能丢。
func TestWebSearchPassesOptionsThrough(t *testing.T) {
	r := New()
	var got websearch.Options
	var gotQuery string
	r.SetWebSearch(func(_ context.Context, q string, o websearch.Options) (websearch.Response, error) {
		gotQuery, got = q, o
		return websearch.Response{Provider: "tavily", Query: q, Results: []websearch.Result{
			{Title: "标题", URL: "https://a.com", Snippet: "片段"},
		}}, nil
	})
	out := r.Execute(context.Background(), webSearchCall(
		`{"query":"golang 泛型","num_results":7,"recency":"week","domains":["a.com","-b.com"]}`))
	if gotQuery != "golang 泛型" {
		t.Errorf("查询未透传: %q", gotQuery)
	}
	if got.NumResults != 7 || got.RecencyFilter != "week" {
		t.Errorf("选项未透传: %+v", got)
	}
	if len(got.DomainFilter) != 2 {
		t.Errorf("域名过滤未透传: %v", got.DomainFilter)
	}
	// 输出必须带渠道出处与结果正文。
	if !strings.Contains(out, "tavily") {
		t.Errorf("结果应标明作答渠道: %s", out)
	}
	if !strings.Contains(out, "https://a.com") || !strings.Contains(out, "片段") {
		t.Errorf("结果应含链接与摘要: %s", out)
	}
}

// 空结果与失败是两种不同结论：空结果要让模型换关键词，失败要如实报告。
func TestWebSearchEmptyResultsHint(t *testing.T) {
	r := New()
	stubSearch(r, websearch.Response{Provider: "brave", Query: "q"}, nil)
	out := r.Execute(context.Background(), webSearchCall(`{"query":"q"}`))
	if !strings.Contains(out, "没有搜到结果") {
		t.Errorf("空结果应给出换关键词的指引，实际: %s", out)
	}
}

func TestWebSearchErrorSurfacesToModel(t *testing.T) {
	r := New()
	stubSearch(r, websearch.Response{}, &websearch.ProviderError{
		Provider: "tavily", Kind: websearch.KindQuota, Status: 429, Message: "超出额度",
	})
	out := r.Execute(context.Background(), webSearchCall(`{"query":"q"}`))
	// 工具错误回填模型（三层错误的 L1），不中断整轮。
	if !strings.Contains(out, "错误") || !strings.Contains(out, "超出额度") {
		t.Errorf("渠道失败应如实回填模型，实际: %s", out)
	}
}

func TestWebSearchAnswerRendered(t *testing.T) {
	r := New()
	stubSearch(r, websearch.Response{
		Provider: "perplexity",
		Query:    "q",
		Answer:   "综合答案正文",
		Results:  []websearch.Result{{Title: "T", URL: "https://a.com"}},
	}, nil)
	out := r.Execute(context.Background(), webSearchCall(`{"query":"q"}`))
	if !strings.Contains(out, "综合答案正文") {
		t.Errorf("自带答案应渲染进结果: %s", out)
	}
}

// 装配后替换实现（server 重建搜索服务时重挂）必须安全。
func TestWebSearchCanBeReplaced(t *testing.T) {
	r := New()
	stubSearch(r, websearch.Response{Provider: "first"}, nil)
	first := r.Execute(context.Background(), webSearchCall(`{"query":"q"}`))
	stubSearch(r, websearch.Response{Provider: "second"}, nil)
	second := r.Execute(context.Background(), webSearchCall(`{"query":"q"}`))
	if !strings.Contains(first, "first") || !strings.Contains(second, "second") {
		t.Errorf("替换注入实现应生效: first=%q second=%q", first, second)
	}
}

// 坏 JSON 走既有的一次保守修复；修不动则自解释报错（不能静默返回空串）。
func TestWebSearchBadJSONArgs(t *testing.T) {
	r := New()
	stubSearch(r, websearch.Response{Provider: "p"}, nil)
	out := r.Execute(context.Background(), webSearchCall(`{"query": }`))
	if out == "" {
		t.Error("坏参数应给出结果（修复或报错），不能是空串")
	}
}
