package websearch

import (
	"context"
	"strings"
	"testing"
)

// 本文件覆盖 SERP 系五家（serper/serpapi/serpbase/serply/serpdive）。
// 手法与 adapters_test.go 一致：base_url 指向 httptest 服务器，
// 只断言「我们发出去的请求」与「我们把响应映射成什么」——不联网、不花钱。

// —— Serper ——

func TestSerperRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"organic":[
		{"title":"  标题 A  ","link":"https://a.com/1","snippet":"  片段   A  "},
		{"title":"标题 X","link":"https://blocked.com/x","snippet":"应被排除项砍掉"},
		{"title":"","link":"https://b.com/2","snippet":"片段 B"}
	]}`)

	p := newSerper()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "serper-key", BaseURL: srv.URL}, "golang 泛型", Options{
		NumResults:    2,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com", "b.com", "-blocked.com"},
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
	if got := cap.Header.Get("X-API-KEY"); got != "serper-key" {
		t.Errorf("X-API-KEY = %q", got)
	}
	if got := cap.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q，期望 application/json", got)
	}

	body := decodeBody(t, cap.Body)
	q := toString(body["q"])
	for _, want := range []string{"golang 泛型", "site:a.com", "site:b.com", "-site:blocked.com"} {
		if !strings.Contains(q, want) {
			t.Errorf("查询串应含 %q，实际 %q", want, q)
		}
	}
	// 有域名过滤时多取 5 条（上游 serper.ts:135）：2 + 5。
	if body["num"] != float64(7) {
		t.Errorf("num = %v，期望 7（numResults + 5）", body["num"])
	}
	if body["tbs"] != "qdr:w" {
		t.Errorf("week 应映射为 qdr:w，实际 %v", body["tbs"])
	}

	// 映射：标题 TrimSpace、snippet 折叠空白、被排除域名不进结果、
	// 空标题回落 Source N（下标按**已接受**条数算，与上游一致）。
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2（blocked.com 应被排除）: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Title != "标题 A" {
		t.Errorf("标题应 TrimSpace，实际 %q", resp.Results[0].Title)
	}
	if resp.Results[0].Snippet != "片段 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[1].Title != "Source 2" {
		t.Errorf("空标题应回落 Source 2，实际 %q", resp.Results[1].Title)
	}
	if resp.Results[1].URL != "https://b.com/2" {
		t.Errorf("URL = %q", resp.Results[1].URL)
	}
	if !strings.Contains(resp.Answer, "Source: 标题 A (https://a.com/1)") {
		t.Errorf("纯 SERP 渠道应合成 answer，实际 %q", resp.Answer)
	}
}

// organic 字段缺席或不是数组 = 信封形状不符（上游 serper.ts:120-125 判无效响应），
// 不能静默返回空结果——那会把「渠道坏了」当成「没搜到」。
func TestSerperInvalidEnvelope(t *testing.T) {
	cases := map[string]string{
		"缺少 organic":  `{"searchParameters":{"q":"x"}}`,
		"organic 非数组": `{"organic":"nope"}`,
	}
	for name, respBody := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := captureServer(t, 200, respBody)
			p := newSerper()
			_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
			assertProviderError(t, err, KindInvalidResponse, 200)
		})
	}
}

func TestSerperStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"message":"rate limited"}`)
	p := newSerper()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

func TestSerperMissingKey(t *testing.T) {
	p := newSerper()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	pe := assertProviderError(t, err, KindCredential, 0)
	if !strings.Contains(pe.Message, "未配置 API key") {
		t.Errorf("错误消息应说明缺 key: %s", pe.Message)
	}
}

// —— SerpApi ——

func TestSerpApiRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"organic_results":[
		{"title":"  标题 A  ","link":"https://a.com/1","snippet":"片段 A"},
		{"title":"标题 B","link":"https://b.com/2","snippet":"片段 B"}
	]}`)

	p := newSerpapi()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "serpapi-key", BaseURL: srv.URL}, "查询词", Options{
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
	if cap.Path != "/search.json" {
		t.Errorf("路径 = %s，期望 /search.json", cap.Path)
	}
	if got := cap.Query.Get("engine"); got != "google" {
		t.Errorf("engine = %q，期望 google", got)
	}
	// 鉴权走 query 参数（上游 serpapi.ts:140 的设计）——key 会出现在 URL 里。
	if got := cap.Query.Get("api_key"); got != "serpapi-key" {
		t.Errorf("api_key = %q", got)
	}
	if got := cap.Query.Get("num"); got != "6" {
		t.Errorf("num = %q，期望 6（1 + 5，有域名过滤时多取）", got)
	}
	if got := cap.Query.Get("tbs"); got != "qdr:d" {
		t.Errorf("day 应映射为 qdr:d，实际 %q", got)
	}
	if q := cap.Query.Get("q"); !strings.Contains(q, "site:a.com") {
		t.Errorf("查询串应含 site: 过滤，实际 %q", q)
	}

	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1", len(resp.Results))
	}
	if resp.Results[0].Title != "标题 A" {
		t.Errorf("标题应 TrimSpace，实际 %q", resp.Results[0].Title)
	}
	if resp.Results[0].Snippet != "片段 A" {
		t.Errorf("snippet = %q", resp.Results[0].Snippet)
	}
}

// 信封 error 字段（HTTP 200 + error）归 invalid-response：可降级，但不能当成功。
func TestSerpApiEnvelopeError(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"error":"Invalid API key"}`)
	p := newSerpapi()
	// key 用长字符串：Redact 会把 key 的每个出现都抹掉，用 "k" 这类单字符
	// key 会把消息里的 "key" 一起打成 "***ey"（脱敏是对的，是测试夹具不真实）。
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "serpapi-key-abc123", BaseURL: srv.URL}, "q", Options{})
	pe := assertProviderError(t, err, KindInvalidResponse, 200)
	if !strings.Contains(pe.Message, "Invalid API key") {
		t.Errorf("错误消息应带上渠道原因: %s", pe.Message)
	}
}

func TestSerpApiMissingOrganicResults(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"search_metadata":{"status":"Success"}}`)
	p := newSerpapi()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestSerpApiStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 401, `{"error":"Invalid API key"}`)
	p := newSerpapi()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindCredential, 401)
}

func TestSerpApiMissingKey(t *testing.T) {
	p := newSerpapi()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

// —— SerpBase ——

func TestSerpBaseRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"organic_results":[
		{"title":"标题 A","url":"https://a.com/1","description":"描述 A"},
		{"title":"","link":"https://b.com/2","snippet":"片段 B"},
		{"title":"标题 C","link":"https://blocked.com/3","snippet":"应被排除项砍掉"}
	]}`)

	p := newSerpbase()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "serpbase-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    5,
		RecencyFilter: "month",
		DomainFilter:  []string{"a.com", "b.com", "-blocked.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "GET" {
		t.Errorf("方法 = %s，期望 GET", cap.Method)
	}
	if cap.Path != "/google/search" {
		t.Errorf("路径 = %s，期望 /google/search", cap.Path)
	}
	// 鉴权同样走 query 参数（上游 serpbase.ts:163 明确说明这是该端点的设计）。
	if got := cap.Query.Get("api_key"); got != "serpbase-key" {
		t.Errorf("api_key = %q", got)
	}
	// SerpBase 不做「有过滤就多取」的补偿（上游 serpbase.ts:164 直接发 numResults）。
	if got := cap.Query.Get("num"); got != "5" {
		t.Errorf("num = %q，期望 5（不因域名过滤而增加）", got)
	}
	if got := cap.Query.Get("tbs"); got != "qdr:m" {
		t.Errorf("month 应映射为 qdr:m，实际 %q", got)
	}
	if q := cap.Query.Get("q"); !strings.Contains(q, "site:a.com") {
		t.Errorf("查询串应含 site: 过滤，实际 %q", q)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2: %+v", len(resp.Results), resp.Results)
	}
	// url 字段与 link 字段同义，snippet 与 description 同义。
	if resp.Results[0].URL != "https://a.com/1" || resp.Results[0].Snippet != "描述 A" {
		t.Errorf("url/description 形态映射错误: %+v", resp.Results[0])
	}
	if resp.Results[1].URL != "https://b.com/2" || resp.Results[1].Snippet != "片段 B" {
		t.Errorf("link/snippet 形态映射错误: %+v", resp.Results[1])
	}
	if resp.Results[1].Title != "Source 2" {
		t.Errorf("空标题应回落 Source 2，实际 %q", resp.Results[1].Title)
	}
}

// 同一个 API 在版本演进中换过结果数组字段名（上游 serpbase.ts:147 的 ?? 链），
// 三种都要认——只认一种会让另一版部署静默返回空结果。
func TestSerpBaseAlternateResultKeys(t *testing.T) {
	cases := map[string]string{
		"organic": `{"organic":[{"title":"T1","link":"https://a.com"}]}`,
		"results": `{"results":[{"title":"T2","link":"https://b.com"}]}`,
	}
	for name, respBody := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := captureServer(t, 200, respBody)
			p := newSerpbase()
			resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
			if err != nil {
				t.Fatalf("搜索失败: %v", err)
			}
			if len(resp.Results) != 1 {
				t.Fatalf("结果数 = %d，期望 1", len(resp.Results))
			}
		})
	}
}

func TestSerpBaseEnvelopeErrorWithStatus(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"error":"quota exceeded","status":429}`)
	p := newSerpbase()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	pe := assertProviderError(t, err, KindInvalidResponse, 200)
	if !strings.Contains(pe.Message, "quota exceeded") || !strings.Contains(pe.Message, "429") {
		t.Errorf("错误消息应含原因与 status: %s", pe.Message)
	}
}

func TestSerpBaseStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error":"too many requests"}`)
	p := newSerpbase()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

func TestSerpBaseMissingKey(t *testing.T) {
	p := newSerpbase()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

// —— Serply ——

func TestSerplyRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"results":[
		{"title":"  标题 A  ","link":"https://a.com/1","description":"  描述   A  "},
		{"title":"伪协议","link":"javascript:void(0)","description":"应跳过"},
		{"title":"","link":"https://b.com/2","description":"描述 B"}
	]}`)

	p := newSerply()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "serply-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    2,
		RecencyFilter: "year",
		DomainFilter:  []string{"a.com", "b.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "GET" {
		t.Errorf("方法 = %s，期望 GET", cap.Method)
	}
	if cap.Path != "/search" {
		t.Errorf("路径 = %s，期望 /search", cap.Path)
	}
	if got := cap.Header.Get("X-Api-Key"); got != "serply-key" {
		t.Errorf("X-Api-Key = %q", got)
	}
	if got := cap.Query.Get("num"); got != "7" {
		t.Errorf("num = %q，期望 7（2 + 5）", got)
	}
	if got := cap.Query.Get("tbs"); got != "qdr:y" {
		t.Errorf("year 应映射为 qdr:y，实际 %q", got)
	}
	if q := cap.Query.Get("q"); !strings.Contains(q, "site:a.com") {
		t.Errorf("查询串应含 site: 过滤，实际 %q", q)
	}

	// javascript: 那条被挡掉且不占下标：Source 编号只数已接受的结果。
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Title != "标题 A" || resp.Results[0].Snippet != "描述 A" {
		t.Errorf("首条映射错误: %+v", resp.Results[0])
	}
	if resp.Results[1].URL != "https://b.com/2" || resp.Results[1].Title != "Source 2" {
		t.Errorf("第二条应跳过伪协议后编号为 Source 2: %+v", resp.Results[1])
	}
}

func TestSerplyEnvelopeDetail(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"detail":"Invalid API key"}`)
	p := newSerply()
	// 同 TestSerpApiEnvelopeError：key 要够长，否则脱敏会把消息里的 "key" 一起抹掉。
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "serply-key-abc123", BaseURL: srv.URL}, "q", Options{})
	pe := assertProviderError(t, err, KindInvalidResponse, 200)
	if !strings.Contains(pe.Message, "Invalid API key") {
		t.Errorf("错误消息应带上渠道原因: %s", pe.Message)
	}
}

func TestSerplyStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"detail":"rate limited"}`)
	p := newSerply()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

func TestSerplyMissingKey(t *testing.T) {
	p := newSerply()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

// —— SERPdive ——

func TestSerpdiveRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"query":"q","model":"krill","answer":null,"results":[
		{"url":"https://a.com/1","title":"标题 A","content":"  内容   A  "},
		{"url":"https://b.com/2","title":"","content":"内容 B"}
	]}`)

	p := newSerpdive()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "serpdive-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    2,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com", "b.com"},
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
	if got := cap.Header.Get("Authorization"); got != "Bearer serpdive-key" {
		t.Errorf("Authorization = %q", got)
	}

	body := decodeBody(t, cap.Body)
	if body["model"] != "krill" {
		t.Errorf("model = %v，期望 krill（免费档，不替用户花钱）", body["model"])
	}
	// max_results 是「上限」不是「下限」（上游 serpdive.ts:225），照 numResults 发。
	if body["max_results"] != float64(2) {
		t.Errorf("max_results = %v，期望 2", body["max_results"])
	}
	// krill 档没有答案合成能力，连 answer 都不发（上游 serpdive.ts:228）。
	if v, ok := body["answer"]; ok {
		t.Errorf("krill 档不应发送 answer 字段，实际 %v", v)
	}
	q := toString(body["query"])
	if !strings.Contains(q, "past week") {
		t.Errorf("week 应作为查询串提示（SERPdive 无原生时间参数），实际 %q", q)
	}
	if !strings.Contains(q, "site:a.com") {
		t.Errorf("查询串应含 site: 过滤，实际 %q", q)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(resp.Results))
	}
	if resp.Results[0].Title != "标题 A" || resp.Results[0].Snippet != "内容 A" {
		t.Errorf("首条映射错误: %+v", resp.Results[0])
	}
	if resp.Results[1].Title != "Source 2" {
		t.Errorf("空标题应回落 Source 2，实际 %q", resp.Results[1].Title)
	}
	// answer 为 null → 由结果合成。
	if !strings.Contains(resp.Answer, "Source: 标题 A (https://a.com/1)") {
		t.Errorf("无 API 答案时应合成 answer，实际 %q", resp.Answer)
	}
}

// max_results 压到 10（上游 serpdive.ts:225）：要更多也拿不到更多。
func TestSerpdiveMaxResultsCap(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"results":[]}`)
	p := newSerpdive()
	if _, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{NumResults: 20}); err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	body := decodeBody(t, cap.Body)
	if body["max_results"] != float64(10) {
		t.Errorf("max_results = %v，期望 10（上限）", body["max_results"])
	}
}

func TestSerpdiveUsesAPIAnswer(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"answer":"API 合成答案","results":[
		{"url":"https://a.com","title":"A","content":"c"}
	]}`)
	p := newSerpdive()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if resp.Answer != "API 合成答案" {
		t.Errorf("有 API 答案时应优先用它，实际 %q", resp.Answer)
	}
}

func TestSerpdiveStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error":"rate limited"}`)
	p := newSerpdive()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

func TestSerpdiveMissingKey(t *testing.T) {
	p := newSerpdive()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

// —— 本批次共享助手 ——

// 四家的 tbs 映射必须与上游逐字一致（qdr:d/w/m/y），且只认四种取值。
func TestSerpFamilyTBSMapping(t *testing.T) {
	want := map[string]string{"day": "qdr:d", "week": "qdr:w", "month": "qdr:m", "year": "qdr:y"}
	for k, v := range want {
		if got := serpFamilyTBS[k]; got != v {
			t.Errorf("serpFamilyTBS[%q] = %q，期望 %q", k, got, v)
		}
	}
	if _, ok := serpFamilyTBS[""]; ok {
		t.Error("空 recencyFilter 不应命中映射（否则会发一个空 tbs）")
	}
	if _, ok := serpFamilyTBS["hour"]; ok {
		t.Error("未支持的取值不应命中映射")
	}
}

// 请求条数：无过滤时就是 num；有过滤时 +5 且封顶 20。
func TestSerpFamilyRequestCount(t *testing.T) {
	cases := []struct {
		name   string
		num    int
		filter []string
		want   int
	}{
		{"无过滤", 5, nil, 5},
		{"无过滤但空切片", 5, []string{}, 5},
		{"有过滤加五", 5, []string{"a.com"}, 10},
		{"有过滤封顶 20", 18, []string{"a.com"}, 20},
		{"有过滤且已达上限", 20, []string{"a.com"}, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serpFamilyRequestCount(c.num, c.filter); got != c.want {
				t.Errorf("serpFamilyRequestCount(%d, %v) = %d，期望 %d", c.num, c.filter, got, c.want)
			}
		})
	}
}
