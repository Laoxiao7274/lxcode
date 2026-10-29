package websearch

import (
	"context"
	"strings"
	"testing"
)

// 本批（perplexity / kagi / ollama / anysearch / valyu）的适配器测试。
// 手法与 adapters_test.go 一致：base_url 指向 httptest，断言请求形状与响应映射，
// 不联网、不花钱。错误分类统一断言 Kind（降级链只认它）。

// ---------- Perplexity ----------

func TestPerplexityRequestShape(t *testing.T) {
	// 答案里引用了 [2]，所以即使 numResults=1 也要保留前 2 条引用，
	// 否则正文的角标在结果列表里找不到对应项。
	srv, cap := captureServer(t, 200, `{
		"choices":[{"message":{"content":"答案正文，见 [2]。"}}],
		"citations":["https://a.com","https://b.com","https://c.com"]
	}`)

	p := newPerplexity()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "pplx-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    1,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com", "-b.com", "不是域名"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" {
		t.Errorf("方法 = %s，期望 POST", cap.Method)
	}
	if cap.Path != "/chat/completions" {
		t.Errorf("路径 = %s，期望 /chat/completions（Perplexity 的搜索长在对话接口上）", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer pplx-key" {
		t.Errorf("Authorization = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["model"] != "sonar" {
		t.Errorf("model = %v，期望 sonar", body["model"])
	}
	if body["search_recency_filter"] != "week" {
		t.Errorf("search_recency_filter = %v", body["search_recency_filter"])
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages 形状不符: %v", body["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "查询词" {
		t.Errorf("messages[0] = %v", first)
	}
	// 原生域名参数：排除项必须带 "-" 前缀（剥掉会把排除变成包含），
	// 非域名项在本地丢弃。
	domains := toString(body["search_domain_filter"])
	if !strings.Contains(domains, "a.com") {
		t.Errorf("search_domain_filter 应含 a.com: %v", domains)
	}
	if !strings.Contains(domains, "-b.com") {
		t.Errorf("search_domain_filter 应保留 -b.com 排除语义: %v", domains)
	}
	if strings.Contains(domains, "不是域名") {
		t.Errorf("非法域名项应被丢弃: %v", domains)
	}

	if resp.Answer != "答案正文，见 [2]。" {
		t.Errorf("answer = %q", resp.Answer)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2（按答案最大引用编号对齐）", len(resp.Results))
	}
	if resp.Results[0].Title != "Source 1" || resp.Results[0].URL != "https://a.com" {
		t.Errorf("字符串引用应映射成 Source N + URL: %+v", resp.Results[0])
	}
}

func TestPerplexityObjectCitation(t *testing.T) {
	// 引用也可能是对象形态 {title,url}（上游两种都认）。
	srv, _ := captureServer(t, 200, `{
		"choices":[{"message":{"content":"答案"}}],
		"citations":[{"title":"标题A","url":"https://a.com"}]
	}`)
	p := newPerplexity()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{NumResults: 1})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1", len(resp.Results))
	}
	if resp.Results[0].Title != "标题A" {
		t.Errorf("对象引用的标题应被采用，实际 %q", resp.Results[0].Title)
	}
}

func TestPerplexityMissingKey(t *testing.T) {
	p := newPerplexity()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	pe := assertProviderError(t, err, KindCredential, 0)
	if !strings.Contains(pe.Message, "未配置 API key") {
		t.Errorf("错误消息应说明缺 key: %s", pe.Message)
	}
}

func TestPerplexityStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error":"rate limited"}`)
	p := newPerplexity()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// ---------- Kagi ----------

func TestKagiRequestShape(t *testing.T) {
	// 三种字段名形态各一条，验证 firstString 回落链。
	srv, cap := captureServer(t, 200, `{"data":{"search":[
		{"title":"标题A","url":"https://a.com","snippet":"片段 A"},
		{"name":"标题B","href":"https://b.com","description":"描述 B"},
		{"title":"标题C","link":"https://other.com","markdown":"正文 C"}
	]}}`)

	p := newKagi()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "kagi-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:   2,
		DomainFilter: []string{"a.com", "b.com"},
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
	if got := cap.Header.Get("Authorization"); got != "Bearer kagi-key" {
		t.Errorf("Authorization = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["limit"] != float64(2) {
		t.Errorf("limit = %v，期望 2", body["limit"])
	}
	// Kagi 无域名参数 → 过滤拼进查询串。
	if q, _ := body["query"].(string); !strings.Contains(q, "site:a.com") {
		t.Errorf("查询串应含 site: 过滤，实际 %q", q)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2（other.com 被过滤掉）: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Title != "标题A" || resp.Results[0].Snippet != "片段 A" {
		t.Errorf("title/snippet 字段应被识别: %+v", resp.Results[0])
	}
	if resp.Results[1].URL != "https://b.com" || resp.Results[1].Title != "标题B" {
		t.Errorf("href/name 回落应生效: %+v", resp.Results[1])
	}
	if resp.Answer == "" {
		t.Error("纯 SERP 渠道应合成 answer")
	}
}

// data 直接是条目数组（没有 search 包裹）也要认。
func TestKagiFlatDataArray(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"data":[{"title":"A","url":"https://a.com","snippet":"s"}]}`)
	p := newKagi()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1", len(resp.Results))
	}
}

// HTTP 200 + 信封 errors 数组 = 渠道侧失败，必须当失败处理
// （静默返回空结果会把「渠道坏了」显示成「没搜到」）。
func TestKagiEnvelopeErrors(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"errors":[{"message":"insufficient credits"}]}`)
	p := newKagi()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	pe := assertProviderError(t, err, KindInvalidResponse, 200)
	if !strings.Contains(pe.Message, "insufficient credits") {
		t.Errorf("错误消息应带上渠道侧原因: %s", pe.Message)
	}
}

func TestKagiMissingKey(t *testing.T) {
	p := newKagi()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestKagiStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 401, `{"error":"bad key"}`)
	p := newKagi()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindCredential, 401)
}

// ---------- Ollama ----------

func TestOllamaRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"results":[
		{"title":"标题A","url":"https://a.com","content":"  正文   A  "},
		{"title":"标题B","url":"https://other.com","content":"正文 B"}
	]}`)

	p := newOllama()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "ollama-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    50, // 超发：Ollama 侧上限 10，应被钳住
		DomainFilter:  []string{"a.com"},
		RecencyFilter: "week", // Ollama 无此参数 → 忽略
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Path != "/web_search" {
		t.Errorf("路径 = %s，期望 /web_search", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer ollama-key" {
		t.Errorf("Authorization = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["max_results"] != float64(ollamaMaxResults) {
		t.Errorf("max_results 应被钳到 %d，实际 %v", ollamaMaxResults, body["max_results"])
	}
	if _, ok := body["recency_filter"]; ok {
		t.Error("Ollama 无时间范围参数，不应发出该字段")
	}
	if q, _ := body["query"].(string); !strings.Contains(q, "site:a.com") {
		t.Errorf("查询串应含 site: 过滤，实际 %q", q)
	}

	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1（other.com 应被过滤）", len(resp.Results))
	}
	if resp.Results[0].Snippet != "正文 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
}

// 任一条结果字段类型不对 → 整次响应判 invalid-response（上游严格校验同款）。
func TestOllamaStrictValidation(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"results":[{"title":"A","url":"","content":"c"}]}`)
	p := newOllama()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestOllamaMissingKey(t *testing.T) {
	p := newOllama()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestOllamaStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error":"too many requests"}`)
	p := newOllama()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// ---------- AnySearch ----------

func TestAnysearchRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"code":0,"data":{
		"results":[
			{"title":"标题A","url":"https://a.com","snippet":"  片段   A  ","content":"正文 A"},
			{"title":"标题B","url":"https://other.com","snippet":"片段 B"}
		],
		"metadata":{"total":2}
	}}`)

	p := newAnysearch()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "as-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:   5,
		DomainFilter: []string{"a.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Path != "/search" {
		t.Errorf("路径 = %s，期望 /search", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer as-key" {
		t.Errorf("Authorization = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["max_results"] != float64(5) {
		t.Errorf("max_results = %v，期望 5", body["max_results"])
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1（other.com 应被过滤）", len(resp.Results))
	}
	if resp.Results[0].Snippet != "片段 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Answer == "" {
		t.Error("纯 SERP 渠道应合成 answer")
	}
}

// 信封 code≠0 → invalid-response（可降级）。
func TestAnysearchEnvelopeCode(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"code":401,"data":{"results":[],"metadata":{}}}`)
	p := newAnysearch()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestAnysearchMissingKey(t *testing.T) {
	p := newAnysearch()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestAnysearchStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"code":429,"message":"quota"}`)
	p := newAnysearch()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// ---------- Valyu ----------

func TestValyuRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"success":true,"results":[
		{"title":"标题A","url":"https://a.com","description":"描述 A","content":"正文 A"},
		{"url":"https://b.com","description":"描述 B"}
	]}`)

	p := newValyu()
	resp, err := p.Search(context.Background(), ChannelConfig{APIKey: "valyu-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    2,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com", "-b.com", "不是域名"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Path != "/search" {
		t.Errorf("路径 = %s，期望 /search", cap.Path)
	}
	if got := cap.Header.Get("x-api-key"); got != "valyu-key" {
		t.Errorf("x-api-key = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["max_num_results"] != float64(2) {
		t.Errorf("max_num_results = %v，期望 2", body["max_num_results"])
	}
	if !strings.Contains(toString(body["included_sources"]), "a.com") {
		t.Errorf("included_sources = %v", body["included_sources"])
	}
	if !strings.Contains(toString(body["excluded_sources"]), "b.com") {
		t.Errorf("excluded_sources = %v（排除项应去掉 - 前缀）", body["excluded_sources"])
	}
	if strings.Contains(toString(body["included_sources"]), "不是域名") {
		t.Errorf("非法域名应被丢弃: %v", body["included_sources"])
	}
	// week → 起始日期（YYYY-MM-DD）。
	start, _ := body["start_date"].(string)
	if len(start) != len("2006-01-02") {
		t.Errorf("start_date 应为 YYYY-MM-DD，实际 %q", start)
	}

	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2", len(resp.Results))
	}
	// 第一条有 content，snippet 取 content；第二条只有 description，回落它。
	if resp.Results[0].Snippet != "正文 A" {
		t.Errorf("snippet 应优先取 content，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[1].Snippet != "描述 B" {
		t.Errorf("无 content 时应回落 description，实际 %q", resp.Results[1].Snippet)
	}
	// 无标题回落 Source N。
	if resp.Results[1].Title != "Source 2" {
		t.Errorf("空标题应回落 Source N，实际 %q", resp.Results[1].Title)
	}
}

// success≠true → invalid-response（信封自带业务成败标记）。
func TestValyuEnvelopeFailure(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"success":false,"results":[]}`)
	p := newValyu()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestValyuMissingKey(t *testing.T) {
	p := newValyu()
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestValyuStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 401, `{"error":"unauthorized"}`)
	p := newValyu()
	_, err := p.Search(context.Background(), ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindCredential, 401)
}

// ---------- 渠道元数据 ----------

// 构造函数必须给出可用的元数据：id/label/docURL 齐备，付费渠道标 needsKey。
// 漏了 needsKey 会让「没配 key」的渠道在降级链里被当成就绪，白试一轮。
func TestBatchAChannelMetadata(t *testing.T) {
	cases := []struct {
		provider Provider
		wantID   string
		wantEnv  string
	}{
		{newPerplexity(), "perplexity", "PERPLEXITY_API_KEY"},
		{newKagi(), "kagi", "KAGI_API_KEY"},
		{newOllama(), "ollama", "OLLAMA_API_KEY"},
		{newAnysearch(), "anysearch", "ANYSEARCH_API_KEY"},
		{newValyu(), "valyu", "VALYU_API_KEY"},
	}
	for _, c := range cases {
		if c.provider.ID() != c.wantID {
			t.Errorf("id = %q，期望 %q", c.provider.ID(), c.wantID)
		}
		if c.provider.EnvVar() != c.wantEnv {
			t.Errorf("%s 的 EnvVar = %q，期望 %q", c.wantID, c.provider.EnvVar(), c.wantEnv)
		}
		if c.provider.Label() == "" || c.provider.Desc() == "" || c.provider.DocURL() == "" {
			t.Errorf("%s 的元数据不全: label=%q desc=%q docURL=%q",
				c.wantID, c.provider.Label(), c.provider.Desc(), c.provider.DocURL())
		}
		if !c.provider.NeedsKey() {
			t.Errorf("%s 需要 API key，NeedsKey 应为 true", c.wantID)
		}
		if c.provider.NeedsBaseURL() {
			t.Errorf("%s 不需要自填实例地址", c.wantID)
		}
		// 未配置 key 时不应就绪（否则降级链会把它排进来白试一轮）。
		if c.provider.Configured(ChannelConfig{}) {
			t.Errorf("%s 无 key 时应为未就绪", c.wantID)
		}
		if !c.provider.Configured(ChannelConfig{APIKey: "k"}) {
			t.Errorf("%s 有 key 时应为就绪", c.wantID)
		}
	}
}
