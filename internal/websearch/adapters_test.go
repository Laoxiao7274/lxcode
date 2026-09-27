package websearch

import (
	"context"
	"strings"
	"testing"
)

// 适配器测试的统一手法：把 base_url 指向 httptest 服务器，
// 断言「发出去的请求」与「映射回来的结果」——不联网、不花钱、可重复。

func TestTavilyRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"answer":"摘要答案","results":[
		{"title":"标题A","url":"https://a.com","content":"  片段   A  "},
		{"title":"","url":"https://b.com","content":"片段B"},
		{"url":"https://c.com","content":"第三条"}
	]}`)

	p := newTavily()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "golang 泛型", Options{
		NumResults:    2,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com", "-b.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" {
		t.Errorf("方法 = %s，期望 POST", cap.Method)
	}
	if cap.Path != "/search" {
		t.Errorf("路径 = %s，期望 /search", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer k" {
		t.Errorf("Authorization = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["query"] != "golang 泛型" {
		t.Errorf("query = %v", body["query"])
	}
	if body["max_results"] != float64(2) {
		t.Errorf("max_results 应受 numResults 限制，实际 %v", body["max_results"])
	}
	if body["time_range"] != "week" {
		t.Errorf("time_range = %v", body["time_range"])
	}
	if body["include_answer"] != "basic" {
		t.Errorf("include_answer = %v", body["include_answer"])
	}
	if !strings.Contains(toString(body["include_domains"]), "a.com") {
		t.Errorf("include_domains = %v", body["include_domains"])
	}
	if !strings.Contains(toString(body["exclude_domains"]), "b.com") {
		t.Errorf("exclude_domains = %v", body["exclude_domains"])
	}

	// 映射：answer 透传、snippet 折叠空白、空标题回落 URL、条数受 numResults 限制。
	if resp.Answer != "摘要答案" {
		t.Errorf("answer = %q", resp.Answer)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(resp.Results))
	}
	if resp.Results[0].Snippet != "片段 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[1].Title != "https://b.com" {
		t.Errorf("空标题应回落 URL，实际 %q", resp.Results[1].Title)
	}
}

func TestTavilyMissingKey(t *testing.T) {
	p := newTavily()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	pe := assertProviderError(t, err, KindCredential, 0)
	if !strings.Contains(pe.Message, "未配置 API key") {
		t.Errorf("错误消息应说明缺 key: %s", pe.Message)
	}
}

func TestTavilyStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"detail":"rate limited"}`)
	p := newTavily()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

func TestBraveRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"web":{"results":[
		{"title":"标题A","url":"https://a.com","description":"描述 A"},
		{"title":"标题B","url":"https://b.com","description":"描述 B"}
	]}}`)

	p := newBrave()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "brave-key", BaseURL: srv.URL}, "查询", Options{
		NumResults:    1,
		RecencyFilter: "day",
		DomainFilter:  []string{"a.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "GET" {
		t.Errorf("方法 = %s，期望 GET", cap.Method)
	}
	if cap.Path != "/web/search" {
		t.Errorf("路径 = %s", cap.Path)
	}
	if got := cap.Header.Get("X-Subscription-Token"); got != "brave-key" {
		t.Errorf("鉴权头 = %q", got)
	}
	// 域名过滤拼进查询串。
	if q := cap.Query.Get("q"); !strings.Contains(q, "site:a.com") {
		t.Errorf("查询串应含 site: 过滤，实际 %q", q)
	}
	// 有过滤时多取一些（过滤会砍掉结果，只取 num 会导致不足）。
	if c := cap.Query.Get("count"); c != "20" {
		t.Errorf("有过滤时 count 应为 20，实际 %q", c)
	}
	if f := cap.Query.Get("freshness"); f != "pd" {
		t.Errorf("day 应映射为 pd，实际 %q", f)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1", len(resp.Results))
	}
	if resp.Answer == "" {
		t.Error("纯 SERP 渠道应合成 answer")
	}
}

// 域名过滤必须二次过滤：搜索引擎对 site: 的解析各家不同，
// 只靠查询串约束会漏进别的域名。
func TestBraveFiltersResultsByDomain(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"web":{"results":[
		{"title":"A","url":"https://a.com","description":"x"},
		{"title":"B","url":"https://other.com","description":"y"}
	]}}`)
	p := newBrave()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{
		DomainFilter: []string{"a.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	for _, r := range resp.Results {
		if !strings.Contains(r.URL, "a.com") {
			t.Errorf("域名过滤失效，漏进 %q", r.URL)
		}
	}
}

func TestSearXNGRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{
		"results":[{"title":"T1","url":"https://a.com","content":"内容 1"},
		           {"title":"T2","url":"https://b.com","content":"内容 2"}],
		"answers":["引擎直出答案"]
	}`)

	p := newSearxng()
	resp, err := p.Search(context.Background(), ChannelConfig{BaseURL: srv.URL}, "查询词", Options{
		NumResults:    5,
		RecencyFilter: "month",
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Path != "/search" {
		t.Errorf("路径 = %s", cap.Path)
	}
	if cap.Query.Get("format") != "json" {
		t.Errorf("必须显式要求 json 输出（实例默认给 HTML），实际 %q", cap.Query.Get("format"))
	}
	if cap.Query.Get("time_range") != "month" {
		t.Errorf("time_range = %q", cap.Query.Get("time_range"))
	}
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(resp.Results))
	}
	// answer = 引擎直出答案 + 结果合成块。
	if !strings.Contains(resp.Answer, "引擎直出答案") {
		t.Errorf("answer 应含引擎直出答案: %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "Source: T1 (https://a.com)") {
		t.Errorf("answer 应含结果出处: %q", resp.Answer)
	}
}

// SearXNG 的配置判定只看实例地址（不需要 key）。
func TestSearXNGConfigured(t *testing.T) {
	p := newSearxng()
	if p.Configured(ChannelConfig{}) {
		t.Error("无地址时应为未就绪")
	}
	if !p.Configured(ChannelConfig{BaseURL: "https://searx.example.com"}) {
		t.Error("有地址时应为就绪（无需 key）")
	}
	if p.Configured(ChannelConfig{BaseURL: "https://x", Disabled: true}) {
		t.Error("停用时应为未就绪")
	}
}

func TestSearXNGMissingBaseURL(t *testing.T) {
	p := newSearxng()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindConfig, 0)
}

func TestExaRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"results":[
		{"title":"标题","url":"https://a.com","highlights":["高亮片段一","高亮片段二"]},
		{"url":"https://b.com","text":"正文内容"}
	]}`)

	p := newExa()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "exa-key", BaseURL: srv.URL}, "查询", Options{
		NumResults:    5,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Path != "/search" {
		t.Errorf("路径 = %s", cap.Path)
	}
	if got := cap.Header.Get("x-api-key"); got != "exa-key" {
		t.Errorf("鉴权头 = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["type"] != "auto" {
		t.Errorf("type = %v，期望 auto", body["type"])
	}
	if _, ok := body["startPublishedDate"]; !ok {
		t.Error("week 应映射出 startPublishedDate")
	}
	if !strings.Contains(toString(body["includeDomains"]), "a.com") {
		t.Errorf("includeDomains = %v", body["includeDomains"])
	}
	contents, ok := body["contents"].(map[string]any)
	if !ok {
		t.Fatalf("contents 缺失: %v", body["contents"])
	}
	if contents["highlights"] != true {
		t.Errorf("contents.highlights 应为 true，实际 %v", contents["highlights"])
	}

	// 答案由 highlights 合成；没有 highlights 的条目退回 text。
	if !strings.Contains(resp.Answer, "高亮片段一") {
		t.Errorf("answer 应含 highlights: %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "正文内容") {
		t.Errorf("answer 应含 text 回落: %q", resp.Answer)
	}
}

func TestJinaRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"code":200,"data":[
		{"title":"标题A","url":"https://a.com","description":"描述 A"},
		{"title":"标题A","url":"https://a.com","description":"重复项应去重"},
		{"title":"标题B","url":"https://b.com","description":"描述 B"}
	]}`)

	p := newJina()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "jina-key", BaseURL: srv.URL + "/"}, "查询词", Options{
		DomainFilter: []string{"a.com", "-b.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer jina-key" {
		t.Errorf("鉴权头 = %q", got)
	}
	if got := cap.Header.Get("X-Respond-With"); got != "no-content" {
		t.Errorf("X-Respond-With = %q，期望 no-content", got)
	}
	// 查询串是路径段（URL 编码），不是 q 参数。
	if cap.Query.Get("count") == "" {
		t.Error("count 参数缺失")
	}
	// 排除项拼进查询串。
	if !strings.Contains(cap.Path, "-site") && !strings.Contains(unescapePath(cap.Path), "b.com") {
		t.Errorf("路径应含排除项: %s", cap.Path)
	}
	// 去重：两个 a.com 只留一个。
	if len(resp.Results) != 1 {
		t.Errorf("结果数 = %d，期望 1（去重 + 排除 b.com）", len(resp.Results))
	}
}

// Jina 的裸数组响应形态也要认。
func TestJinaBareArrayResponse(t *testing.T) {
	srv, _ := captureServer(t, 200, `[{"title":"A","url":"https://a.com","description":"d"}]`)
	p := newJina()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL + "/"}, "q", Options{})
	if err != nil {
		t.Fatalf("裸数组响应应被接受: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d", len(resp.Results))
	}
}

// 信封 code≠200 视为渠道侧失败（invalid-response，可降级）。
func TestJinaEnvelopeFailure(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"code":401,"data":[]}`)
	p := newJina()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL + "/"}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 0)
}

func TestJinaMissingKey(t *testing.T) {
	p := newJina()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

// DuckDuckGo：解析容器与锚点，跳过广告位，解出跳转链接的真实目标。
func TestDuckDuckGoParsing(t *testing.T) {
	page := `<html><body>
	<div class="result result--ad"><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fad.com">广告</a></div>
	<div class="result results_links">
		<h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fa.com%2Fpage&amp;rut=x">标题 A</a></h2>
		<a class="result__snippet" href="x">片段 <b>A</b></a>
	</div>
	<div class="result results_links">
		<h2 class="result__title"><a class="result__a" href="https://b.com/direct">标题 B</a></h2>
		<a class="result__snippet">片段 B</a>
	</div>
	</body></html>`
	srv, cap := captureServer(t, 200, page)

	p := newDuckDuckGo()
	resp, err := p.Search(context.Background(), ChannelConfig{BaseURL: srv.URL}, "查询", Options{NumResults: 10})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "GET" {
		t.Errorf("方法 = %s", cap.Method)
	}
	if !strings.Contains(cap.Header.Get("Accept"), "text/html") {
		t.Errorf("Accept 应为 text/html，实际 %q", cap.Header.Get("Accept"))
	}
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2（广告位应跳过）: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].URL != "https://a.com/page" {
		t.Errorf("应解出 uddg 里的真实地址，实际 %q", resp.Results[0].URL)
	}
	if resp.Results[0].Title != "标题 A" {
		t.Errorf("标题应去标签解实体，实际 %q", resp.Results[0].Title)
	}
	if resp.Results[0].Snippet != "片段 A" {
		t.Errorf("片段应去标签，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[1].URL != "https://b.com/direct" {
		t.Errorf("非跳转链接应原样使用，实际 %q", resp.Results[1].URL)
	}
}

// 解析出 0 条 = 反爬页或结构变更 → invalid-response（触发降级），
// 不能静默返回空结果（那会把「被墙」当成「没搜到」）。
func TestDuckDuckGoNoParseableResults(t *testing.T) {
	srv, _ := captureServer(t, 200, `<html><body>请稍后再试</body></html>`)
	p := newDuckDuckGo()
	_, err := p.Search(context.Background(), ChannelConfig{BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestDecodeDDGURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"//duckduckgo.com/l/?uddg=https%3A%2F%2Fa.com%2Fx&rut=1", "https://a.com/x"},
		{"https://b.com/direct", "https://b.com/direct"},
		{"javascript:void(0)", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := decodeDDGURL(c.in); got != c.want {
			t.Errorf("decodeDDGURL(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, toString(item))
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
}

func unescapePath(p string) string {
	// httptest 服务器收到的 Path 已解码一次，这里只做百分号形态的宽松匹配。
	return p
}
