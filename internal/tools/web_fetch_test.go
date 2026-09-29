package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/websearch"
)

// webFetchCall 构造一次 web_fetch 调用（坏 JSON 也允许，用来测修复路径）。
func webFetchCall(args string) llm.ToolCall {
	var c llm.ToolCall
	c.Function.Name = WebFetchToolName
	c.Function.Arguments = args
	return c
}

func TestWebFetchToolIsLowRiskReadOnly(t *testing.T) {
	r := New()
	d, ok := r.Get(WebFetchToolName)
	if !ok {
		t.Fatalf("应注册 %s 工具", WebFetchToolName)
	}
	// 抓取是只读操作：strict 只读模式下必须可用——标成变更类会让
	// 「只读模式」连读文档都做不到（与 web_search 同款判定）。
	if d.Risk != RiskLow {
		t.Errorf("%s 应为低危（自动执行），实际 %v", WebFetchToolName, d.Risk)
	}
	if d.Mutates {
		t.Errorf("%s 不该标记为变更外部世界", WebFetchToolName)
	}
	if r.IsMutating(WebFetchToolName) {
		t.Error("strict 只读模式不该拒绝 web_fetch")
	}
	if r.Confirm(context.Background(), webFetchCall(`{"url":"https://example.com"}`)) != "" {
		t.Error("web_fetch 不该需要人工确认")
	}
}

// TestWebFetchBlocksPrivateTargetsAtToolBoundary：工具边界上的 SSRF 保证。
//
// 这是 web_fetch 与 web_search 最大的安全差异：搜索渠道地址是用户自己配的
// （可信，可以指向内网自建 SearXNG），而抓取面对的是**模型给的任意 URL**
// ——它可能被网页内容里的提示注入诱导去读本机服务（127.0.0.1:7789 就是
// 这个后端自己）。必须在工具这一层就挡住。
func TestWebFetchBlocksPrivateTargetsAtToolBoundary(t *testing.T) {
	r := New()
	for _, raw := range []string{
		`{"url":"http://127.0.0.1:7789/rpc"}`,
		`{"url":"http://localhost/admin"}`,
		`{"url":"http://169.254.169.254/latest/meta-data/"}`,
		`{"url":"http://192.168.1.1/"}`,
		`{"url":"file:///C:/Windows/win.ini"}`,
	} {
		out := r.Execute(context.Background(), webFetchCall(raw))
		if !strings.Contains(out, "拒绝访问") && !strings.Contains(out, "只支持") {
			t.Errorf("内网/非 http 地址 %s 必须在工具层被拒绝，实际: %s", raw, out)
		}
	}
}

func TestWebFetchRejectsEmptyURL(t *testing.T) {
	r := New()
	out := r.Execute(context.Background(), webFetchCall(`{"url":"   "}`))
	if !strings.Contains(out, "url 不能为空") {
		t.Errorf("空 url 应被拒绝，实际: %s", out)
	}
}

// 坏 JSON 走既有的一次保守修复；修不动则自解释报错（不能静默返回空串）。
func TestWebFetchBadJSONArgs(t *testing.T) {
	r := New()
	out := r.Execute(context.Background(), webFetchCall(`{"url": }`))
	if out == "" {
		t.Error("坏参数应给出结果（修复或报错），不能是空串")
	}
}

// TestFormatFetchResultRendersContext：三件必须显式告知的事。
//
// 1. 最终地址（重定向可能换站点，出处影响可信度）；
// 2. 正文为空 vs 抓取失败（两种结论，处置不同）；
// 3. 被截断（不注明的话模型以为读到了全文，据此下结论）。
func TestFormatFetchResultRendersContext(t *testing.T) {
	full := formatFetchResult(websearch.FetchResult{
		URL: "https://example.com/a", Title: "标题", ContentType: "text/html", Text: "正文内容",
	})
	for _, want := range []string{"https://example.com/a", "标题", "正文内容"} {
		if !strings.Contains(full, want) {
			t.Errorf("结果应含 %q:\n%s", want, full)
		}
	}

	truncated := formatFetchResult(websearch.FetchResult{
		URL: "https://example.com/a", Text: "正文", Truncated: true,
	})
	if !strings.Contains(truncated, "已截断") || !strings.Contains(truncated, "max_chars") {
		t.Errorf("被截断必须注明并指出怎么要更多（否则模型以为读到了全文）:\n%s", truncated)
	}

	empty := formatFetchResult(websearch.FetchResult{
		URL: "https://example.com/spa", ContentType: "text/html", Text: "   ",
	})
	if !strings.Contains(empty, "没有提取出正文") {
		t.Errorf("抓到页面但没正文要如实说明（模型该换来源）:\n%s", empty)
	}
}
