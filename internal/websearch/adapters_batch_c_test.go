package websearch

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 本文件是 C 批渠道（search1api / searchinfinity / querit / tinyfish / parallel）
// 的适配器测试。手法与 adapters_test.go 一致：把 base_url 指向 httptest 服务器，
// 断言「发出去的请求」与「映射回来的结果」——不联网、不花钱、可重复。

// batchCObject 取嵌套对象字段（把类型断言的噪音收在一处）。
// 名字带 batchC 前缀是刻意的：同包并行开发时，通用名会与别人的测试文件撞名。
func batchCObject(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("字段 %s 不是对象: %v", key, m[key])
	}
	return v
}

// ---------------------------------------------------------------------------
// Search1API
// ---------------------------------------------------------------------------

func TestSearch1APIRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"searchParameters":{"q":"x"},"results":[
		{"title":"标题A","link":"https://a.com","snippet":"  片段   A  ","content":"正文"},
		{"title":"","link":"https://b.com","snippet":"片段B"},
		{"title":"标题C","link":"https://c.com","snippet":"片段C"}
	]}`)

	p := newSearch1API()
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
	// crawl_results 恒为 0 = 不启用 Deep Search（每抓一页多花一个积分）。
	if body["crawl_results"] != float64(0) {
		t.Errorf("crawl_results 应为 0，实际 %v", body["crawl_results"])
	}
	if body["time_range"] != "week" {
		t.Errorf("time_range = %v", body["time_range"])
	}
	if !strings.Contains(toString(body["include_sites"]), "a.com") {
		t.Errorf("include_sites = %v", body["include_sites"])
	}
	if !strings.Contains(toString(body["exclude_sites"]), "b.com") {
		t.Errorf("exclude_sites = %v", body["exclude_sites"])
	}

	// 映射：snippet 折叠空白、空标题回落 URL、条数受 numResults 限制、answer 由结果合成。
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2（受 numResults 限制）", len(resp.Results))
	}
	if resp.Results[0].Snippet != "片段 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[1].Title != "https://b.com" {
		t.Errorf("空标题应回落 URL，实际 %q", resp.Results[1].Title)
	}
	if !strings.Contains(resp.Answer, "Source: 标题A (https://a.com)") {
		t.Errorf("answer 应由结果合成，实际 %q", resp.Answer)
	}
}

func TestSearch1APIMissingKey(t *testing.T) {
	p := newSearch1API()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	pe := assertProviderError(t, err, KindCredential, 0)
	if !strings.Contains(pe.Message, "未配置 API key") {
		t.Errorf("错误消息应说明缺 key: %s", pe.Message)
	}
}

func TestSearch1APIStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error":"rate limited"}`)
	p := newSearch1API()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// 响应里没有 results 数组 = 渠道结构变更，必须报错而不是静默返回空结果。
func TestSearch1APIUnexpectedShape(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"searchParameters":{}}`)
	p := newSearch1API()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

// ---------------------------------------------------------------------------
// Searchinfinity
// ---------------------------------------------------------------------------

func TestSearchInfinityRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"ResponseMetadata":{"RequestId":"r1"},"Result":{"ResultCount":2,"WebResults":[
		{"Title":"标题A","Url":"https://a.com","Snippet":"原始片段","Summary":"  模型摘要  A "},
		{"Title":"","Url":"https://b.com","Snippet":"片段 B","Summary":""}
	]}}`)

	p := newSearchInfinity()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "查询", Options{
		NumResults:    5,
		RecencyFilter: "month",
		DomainFilter:  []string{"a.com", "-b.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" {
		t.Errorf("方法 = %s，期望 POST", cap.Method)
	}
	// 端点带路径前缀，不是裸根。
	if cap.Path != "/search_api/web_search" {
		t.Errorf("路径 = %s，期望 /search_api/web_search", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer k" {
		t.Errorf("Authorization = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["Query"] != "查询" {
		t.Errorf("Query = %v", body["Query"])
	}
	if body["Count"] != float64(5) {
		t.Errorf("Count = %v，期望 5", body["Count"])
	}
	filter := batchCObject(t, body, "Filter")
	if filter["Sites"] != "a.com" {
		t.Errorf("Filter.Sites = %v，期望 a.com", filter["Sites"])
	}
	if filter["BlockHosts"] != "b.com" {
		t.Errorf("Filter.BlockHosts = %v，期望 b.com", filter["BlockHosts"])
	}
	if body["TimeRange"] != "OneMonth" {
		t.Errorf("TimeRange = %v，期望 OneMonth", body["TimeRange"])
	}

	// 摘要优先于原始片段；没有摘要的条目回落 Snippet；空标题回落 URL。
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(resp.Results))
	}
	if resp.Results[0].Snippet != "模型摘要 A" {
		t.Errorf("应优先用 Summary，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[1].Snippet != "片段 B" {
		t.Errorf("无 Summary 应回落 Snippet，实际 %q", resp.Results[1].Snippet)
	}
	if resp.Results[1].Title != "https://b.com" {
		t.Errorf("空标题应回落 URL，实际 %q", resp.Results[1].Title)
	}
	if !strings.Contains(resp.Answer, "Source: 标题A (https://a.com)") {
		t.Errorf("answer 应由结果合成，实际 %q", resp.Answer)
	}
}

// 渠道侧的域名上限是 5 个：超出部分要丢弃而不是原样发出去（发出去会被拒）。
func TestSearchInfinityDomainCap(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"Result":{"WebResults":[]}}`)
	p := newSearchInfinity()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{
		DomainFilter: []string{"a.com", "b.com", "c.com", "d.com", "e.com", "f.com", "g.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	body := decodeBody(t, cap.Body)
	sites, _ := batchCObject(t, body, "Filter")["Sites"].(string)
	if got := strings.Count(sites, "|") + 1; got != 5 {
		t.Errorf("Sites 应有 5 个域名（上限），实际 %d: %q", got, sites)
	}
	if strings.Contains(sites, "f.com") {
		t.Errorf("超限域名应被丢弃，实际 %q", sites)
	}
}

// 业务错误码 700901 = 无效 key：必须翻成凭据类错误（不降级），而不是当成成功。
func TestSearchInfinityBusinessError(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"ResponseMetadata":{"Error":{"CodeN":700901,"Code":"invalid_api_key","Message":"invalid api key"}}}`)
	p := newSearchInfinity()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindCredential, 401)
}

// 看不懂的业务码归 KindUnknown（不触发降级）——不能让它冒充「换个渠道就好」。
func TestSearchInfinityUnknownBusinessCode(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"ResponseMetadata":{"Error":{"CodeN":123456,"Code":"weird","Message":"boom"}}}`)
	p := newSearchInfinity()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindUnknown, 0)
}

func TestSearchInfinityUnexpectedShape(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"ResponseMetadata":{"RequestId":"r1"}}`)
	p := newSearchInfinity()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestSearchInfinityMissingKey(t *testing.T) {
	p := newSearchInfinity()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestSearchInfinityStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error":"too many requests"}`)
	p := newSearchInfinity()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// ---------------------------------------------------------------------------
// Querit
// ---------------------------------------------------------------------------

func TestQueritRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"error_code":200,"search_id":1,"results":{"result":[
		{"url":"https://a.com","title":"标题A","snippet":"  片段   A  "},
		{"url":"https://b.com","snippet":"片段B"}
	]}}`)

	p := newQuerit()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "查询", Options{
		NumResults:    1,
		RecencyFilter: "day",
		DomainFilter:  []string{"a.com", "-b.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" {
		t.Errorf("方法 = %s，期望 POST", cap.Method)
	}
	if cap.Path != "/v1/search" {
		t.Errorf("路径 = %s，期望 /v1/search", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer k" {
		t.Errorf("Authorization = %q", got)
	}
	if got := cap.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q，期望 application/json", got)
	}
	body := decodeBody(t, cap.Body)
	if body["query"] != "查询" {
		t.Errorf("query = %v", body["query"])
	}
	if body["count"] != float64(1) {
		t.Errorf("count = %v，期望 1", body["count"])
	}
	filters := batchCObject(t, body, "filters")
	sites := batchCObject(t, filters, "sites")
	if !strings.Contains(toString(sites["include"]), "a.com") {
		t.Errorf("filters.sites.include = %v", sites["include"])
	}
	if !strings.Contains(toString(sites["exclude"]), "b.com") {
		t.Errorf("filters.sites.exclude = %v", sites["exclude"])
	}
	timeRange := batchCObject(t, filters, "timeRange")
	if timeRange["date"] != "d1" {
		t.Errorf("filters.timeRange.date = %v，期望 d1", timeRange["date"])
	}

	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1（受 numResults 限制）", len(resp.Results))
	}
	if resp.Results[0].Snippet != "片段 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[0].Title != "标题A" {
		t.Errorf("标题 = %q", resp.Results[0].Title)
	}
	if !strings.Contains(resp.Answer, "Source: 标题A (https://a.com)") {
		t.Errorf("answer 应由结果合成，实际 %q", resp.Answer)
	}
}

// error_code 是字符串形态的 "200" 也算成功（上游用 Number() 归一）。
func TestQueritStringErrorCode(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"error_code":"200","results":{"result":[{"url":"https://a.com","title":"A"}]}}`)
	p := newQuerit()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	if err != nil {
		t.Fatalf("字符串 error_code 应被接受: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1", len(resp.Results))
	}
}

func TestQueritBusinessError(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"error_code":401,"error_msg":"unauthorized"}`)
	p := newQuerit()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindCredential, 401)
}

func TestQueritUnexpectedShape(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"error_code":200,"results":{}}`)
	p := newQuerit()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestQueritMissingKey(t *testing.T) {
	p := newQuerit()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestQueritStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error_code":429}`)
	p := newQuerit()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// ---------------------------------------------------------------------------
// TinyFish
// ---------------------------------------------------------------------------

func TestTinyFishRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"query":"q","results":[
		{"position":1,"title":"标题A","url":"https://a.com","snippet":"  片段   A  "},
		{"position":2,"title":"","url":"https://b.com","snippet":"片段B"}
	],"total_results":2,"page":0}`)

	p := newTinyFish()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "查询", Options{
		NumResults:    5,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com", "-b.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "GET" {
		t.Errorf("方法 = %s，期望 GET", cap.Method)
	}
	// 端点是裸主机名，查询串直接挂在其后（没有路径段）。
	if cap.Path != "/" {
		t.Errorf("路径 = %s，期望 /", cap.Path)
	}
	if got := cap.Header.Get("X-API-Key"); got != "k" {
		t.Errorf("鉴权头 X-API-Key = %q（不是 Authorization）", got)
	}
	if cap.Query.Get("query") != "查询" {
		t.Errorf("query = %q", cap.Query.Get("query"))
	}
	if got := cap.Query.Get("include_domains"); got != "a.com" {
		t.Errorf("include_domains = %q（逗号连接）", got)
	}
	if got := cap.Query.Get("exclude_domains"); got != "b.com" {
		t.Errorf("exclude_domains = %q（逗号连接）", got)
	}
	if got := cap.Query.Get("recency_minutes"); got != "10080" {
		t.Errorf("week 应映射为 10080 分钟，实际 %q", got)
	}
	// 首页不带 page 参数（只有翻页才带）。
	if got := cap.Query.Get("page"); got != "" {
		t.Errorf("首页不该带 page 参数，实际 %q", got)
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
	if !strings.Contains(resp.Answer, "Source: 标题A (https://a.com)") {
		t.Errorf("answer 应由结果合成，实际 %q", resp.Answer)
	}
}

// 单页只有 10 条：需求 > 10 时必须再翻一页，且两页结果按 URL 去重。
func TestTinyFishPaginationAndDedupe(t *testing.T) {
	srv, cap := captureServer(t, 200, batchCTinyFishPageJSON(10))
	p := newTinyFish()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{NumResults: 12})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	// 第二次请求带 page=1，证明确实翻了页。
	if got := cap.Query.Get("page"); got != "1" {
		t.Errorf("第二页应带 page=1，实际 %q", got)
	}
	// 两页返回同一批 URL：去重后只剩 10 条（不足 12 是正常的）。
	if len(resp.Results) != 10 {
		t.Fatalf("结果数 = %d，期望 10（去重后）", len(resp.Results))
	}
	seen := map[string]bool{}
	for _, r := range resp.Results {
		if seen[r.URL] {
			t.Errorf("结果未去重: %s", r.URL)
		}
		seen[r.URL] = true
	}
}

func TestTinyFishMissingKey(t *testing.T) {
	p := newTinyFish()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestTinyFishStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 401, `{"error":"invalid api key"}`)
	p := newTinyFish()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindCredential, 401)
}

// batchCTinyFishPageJSON 造一页 n 条结果的响应（URL 稳定，便于测去重）。
func batchCTinyFishPageJSON(n int) string {
	var b strings.Builder
	b.WriteString(`{"results":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"title":"T` + strconv.Itoa(i) + `","url":"https://page` + strconv.Itoa(i) + `.com","snippet":"s"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// ---------------------------------------------------------------------------
// Parallel
// ---------------------------------------------------------------------------

func TestParallelRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"results":[
		{"url":"https://a.com","title":"标题A","excerpts":["摘录一","摘录二"]},
		{"url":"https://b.com","excerpts":["只有摘录"]}
	]}`)

	p := newParallel()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "研究问题", Options{
		NumResults:    5,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com", "-b.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" {
		t.Errorf("方法 = %s，期望 POST", cap.Method)
	}
	if cap.Path != "/v1/search" {
		t.Errorf("路径 = %s，期望 /v1/search", cap.Path)
	}
	if got := cap.Header.Get("x-api-key"); got != "k" {
		t.Errorf("鉴权头 x-api-key = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["objective"] != "研究问题" {
		t.Errorf("objective = %v", body["objective"])
	}
	queries, ok := body["search_queries"].([]any)
	if !ok || len(queries) != 1 || queries[0] != "研究问题" {
		t.Errorf("search_queries = %v", body["search_queries"])
	}
	advanced := batchCObject(t, body, "advanced_settings")
	if advanced["max_results"] != float64(5) {
		t.Errorf("advanced_settings.max_results = %v，期望 5", advanced["max_results"])
	}
	policy := batchCObject(t, advanced, "source_policy")
	if !strings.Contains(toString(policy["include_domains"]), "a.com") {
		t.Errorf("source_policy.include_domains = %v", policy["include_domains"])
	}
	if !strings.Contains(toString(policy["exclude_domains"]), "b.com") {
		t.Errorf("source_policy.exclude_domains = %v", policy["exclude_domains"])
	}
	afterDate, _ := policy["after_date"].(string)
	if _, err := time.Parse("2006-01-02", afterDate); err != nil {
		t.Errorf("after_date 应为 YYYY-MM-DD，实际 %q", afterDate)
	}

	// snippet 只取第一条摘录；标题回落 "Source N"；answer 用全部摘录拼。
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(resp.Results))
	}
	if resp.Results[0].Snippet != "摘录一" {
		t.Errorf("snippet 应只取第一条摘录，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[1].Title != "Source 2" {
		t.Errorf("空标题应回落 Source N，实际 %q", resp.Results[1].Title)
	}
	if !strings.Contains(resp.Answer, "摘录一 摘录二") {
		t.Errorf("answer 应拼接全部摘录，实际 %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "Source: 标题A (https://a.com)") {
		t.Errorf("answer 应含出处，实际 %q", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "Source: Source 2 (https://b.com)") {
		t.Errorf("answer 里空标题也应回落 Source N，实际 %q", resp.Answer)
	}
}

// snippet 要截到 200 字符：Parallel 的 excerpt 是整段正文，不截会吃掉上下文预算。
func TestParallelSnippetTruncation(t *testing.T) {
	long := strings.Repeat("很", 300)
	srv, _ := captureServer(t, 200, `{"results":[{"url":"https://a.com","title":"A","excerpts":["`+long+`"]}]}`)
	p := newParallel()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d", len(resp.Results))
	}
	// 按 rune 截断：300 个汉字截到 200 个字符（而不是按字节切出乱码）。
	if got := len([]rune(resp.Results[0].Snippet)); got != parallelSnippetChars {
		t.Errorf("snippet 长度 = %d 个字符，期望 %d", got, parallelSnippetChars)
	}
}

// 没有 results 数组不算错（上游此处不抛错）：空结果就是空结果。
func TestParallelEmptyResults(t *testing.T) {
	srv, _ := captureServer(t, 200, `{}`)
	p := newParallel()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	if err != nil {
		t.Fatalf("缺 results 不应报错: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("结果数 = %d，期望 0", len(resp.Results))
	}
}

func TestParallelMissingKey(t *testing.T) {
	p := newParallel()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestParallelStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error":"rate limited"}`)
	p := newParallel()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}
