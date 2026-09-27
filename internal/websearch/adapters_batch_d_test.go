package websearch

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// batch D（bocha / xcrawl / brightdata / mistral-search / firecrawl）的适配器测试。
//
// 与 adapters_test.go 同一套手法：把 base_url 指向 httptest 服务器，
// 断言「发出去的请求」与「映射回来的结果」——不联网、不花钱、可重复。
//
// 本批有三个渠道有私有设置（brightdata 的 SERP zone、mistral 的模型与档位、
// firecrawl 的 API 版本），它们是 OptionSpec 声明的设置项。**适配器只认 ch.Options**
// ——环境变量回退与默认值都发生在 effectiveChannel 里，所以相关用例必须走
// searchVia（走真实解析路径）才测得到那两层；直接构造 ChannelConfig 就调 Search
// 只能测到「显式填在配置里」这一条路。
//
// 环境变量仍要用 t.Setenv 钉住：它们既是回退来源，也是「配置留空」时的实际取值，
// 不钉就会被开发机上的环境变量污染。

// searchVia 走真实解析路径（配置 → 环境变量 → 默认值）再交给适配器。
func searchVia(t *testing.T, p Provider, ch ChannelConfig, query string, opts Options) (Response, error) {
	t.Helper()
	return p.Search(context.Background(), effectiveChannel(p, ch), query, opts)
}

// pinMistralSearchEnv 把 mistral 的两个设置项的环境变量回退钉到「未设置」。
func pinMistralSearchEnv(t *testing.T) {
	t.Helper()
	t.Setenv(mistralSearchToolOption.EnvVar, "")
	t.Setenv(mistralSearchModelOption.EnvVar, "")
}

// pinFirecrawlEnv 把 Firecrawl 的 API 版本环境变量钉到「未设置」。
func pinFirecrawlEnv(t *testing.T) {
	t.Helper()
	t.Setenv(firecrawlAPIVersionOption.EnvVar, "")
}

// ---------------------------------------------------------------- 博查 Bocha

func TestBochaRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"code":200,"msg":"ok","data":{"webPages":{"value":[
		{"name":"标题A","url":"https://a.com/1","summary":"  摘要   A  "},
		{"title":"标题B","link":"https://b.com/2","snippet":"摘要B"},
		{"href":"https://a.com/3","content":"无标题条目"}
	]}}}`)

	p := newBocha()
	resp, err := searchVia(t, p, ChannelConfig{APIKey: "bocha-key", BaseURL: srv.URL}, "查询词", Options{
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
	if cap.Path != "/v1/web-search" {
		t.Errorf("路径 = %s，期望 /v1/web-search", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer bocha-key" {
		t.Errorf("Authorization = %q", got)
	}

	body := decodeBody(t, cap.Body)
	if body["query"] != "查询词" {
		t.Errorf("query = %v", body["query"])
	}
	if body["count"] != float64(5) {
		t.Errorf("count = %v，期望 5", body["count"])
	}
	if body["freshness"] != "oneWeek" {
		t.Errorf("week 应映射为 oneWeek，实际 %v", body["freshness"])
	}
	if body["summary"] != true {
		t.Errorf("summary 应为 true（snippet 的唯一来源），实际 %v", body["summary"])
	}

	// 域名过滤只有本地一条路（博查没有域名参数）：b.com 被排除、a.com 两条留下；
	// 空标题回落 URL（不是 "Source N"）。
	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Title != "标题A" || resp.Results[0].URL != "https://a.com/1" {
		t.Errorf("第一条映射错: %+v", resp.Results[0])
	}
	if resp.Results[0].Snippet != "摘要 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	if resp.Results[1].Title != "https://a.com/3" {
		t.Errorf("空标题应回落 URL，实际 %q", resp.Results[1].Title)
	}
	if !strings.Contains(resp.Answer, "Source: 标题A (https://a.com/1)") {
		t.Errorf("纯 SERP 渠道应合成 answer，实际 %q", resp.Answer)
	}
}

// 博查的失败是「HTTP 200 + 信封 code≠200」：必须报 invalid-response 触发降级，
// 不能静默解成空结果（那会把「key 无效」显示成「没搜到」）。
func TestBochaEnvelopeCodeFailure(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"code":403,"msg":"invalid api key","data":{}}`)
	p := newBocha()
	// key 用够长的假值：Redact 会把短 key 在消息里出现的任何位置都替换掉
	// （"k" 会把 "invalid api key" 打成 "invalid api ***ey"），断言就失去意义了。
	_, err := searchVia(t, p, ChannelConfig{APIKey: "bocha-key-1234567890", BaseURL: srv.URL}, "q", Options{})
	pe := assertProviderError(t, err, KindInvalidResponse, 200)
	if !strings.Contains(pe.Message, "invalid api key") {
		t.Errorf("错误消息应带上信封的 msg: %s", pe.Message)
	}
}

// data.webPages.value 不是数组 = 渠道改了结构 → 降级，不能当成「没搜到」。
func TestBochaMissingWebPages(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"code":200,"data":{"webPages":{}}}`)
	p := newBocha()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestBochaMissingKey(t *testing.T) {
	p := newBocha()
	_, err := searchVia(t, p, ChannelConfig{}, "q", Options{})
	pe := assertProviderError(t, err, KindCredential, 0)
	if !strings.Contains(pe.Message, "未配置 API key") {
		t.Errorf("错误消息应说明缺 key: %s", pe.Message)
	}
}

func TestBochaStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 429, `{"error":"too many requests"}`)
	p := newBocha()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// ------------------------------------------------------------------ XCrawl

func TestXcrawlRequestShape(t *testing.T) {
	srv, cap := captureServer(t, 200, `{"search_metadata":{"status":"completed"},"organic_results":[
		{"title":"标题A","link":"https://a.com/1","snippet":"  片段   A  "},
		{"title":null,"link":"/goto?url=x","snippet":null}
	]}`)

	p := newXcrawl()
	resp, err := searchVia(t, p, ChannelConfig{APIKey: "xcrawl-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults: 5,
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" {
		t.Errorf("方法 = %s，期望 POST", cap.Method)
	}
	if cap.Path != "/v1/serp" {
		t.Errorf("路径 = %s，期望 /v1/serp", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer xcrawl-key" {
		t.Errorf("Authorization = %q", got)
	}
	body := decodeBody(t, cap.Body)
	if body["engine"] != "google_search" {
		t.Errorf("engine = %v，期望 google_search", body["engine"])
	}
	if body["q"] != "查询词" {
		t.Errorf("q = %v", body["q"])
	}

	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Snippet != "片段 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	// 相对链接（跳转壳）必须按实际生效的端点绝对化，
	// 否则调用方拿到的是一个不可访问的相对路径。
	wantAbs := srv.URL + "/goto?url=x"
	if resp.Results[1].URL != wantAbs {
		t.Errorf("相对链接应绝对化，实际 %q，期望 %q", resp.Results[1].URL, wantAbs)
	}
	if resp.Results[1].Title != wantAbs {
		t.Errorf("空标题应回落绝对化后的 URL，实际 %q", resp.Results[1].Title)
	}
	if resp.Answer == "" {
		t.Error("纯 SERP 渠道应合成 answer")
	}
}

// SERP 任务没跑完（status≠completed）时结果不完整，不能当成功。
func TestXcrawlIncompleteStatus(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"search_metadata":{"status":"processing"},"organic_results":[]}`)
	p := newXcrawl()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

// link 缺失 = 结构不符 → 降级（上游对每一条都是硬校验）。
func TestXcrawlMissingLink(t *testing.T) {
	srv, _ := captureServer(t, 200, `{"search_metadata":{"status":"completed"},"organic_results":[{"title":"无链接"}]}`)
	p := newXcrawl()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestXcrawlMissingKey(t *testing.T) {
	p := newXcrawl()
	_, err := searchVia(t, p, ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestXcrawlStatusClassification(t *testing.T) {
	srv, _ := captureServer(t, 401, `{"message":"unauthorized"}`)
	p := newXcrawl()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindCredential, 401)
}

// -------------------------------------------------------------- Bright Data

func TestBrightdataRequestShape(t *testing.T) {
	t.Setenv(brightdataZoneOption.EnvVar, "my_serp_zone")
	srv, cap := captureServer(t, 200, `{"organic":[
		{"link":"https://a.com/1","title":"标题A","description":"  描述  A "},
		{"link":"https://b.com/2","title":"域外","description":"应被过滤"},
		{"link":"https://a.com/3","description":"无标题"}
	]}`)

	p := newBrightdata()
	resp, err := searchVia(t, p, ChannelConfig{APIKey: "bd-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    5,
		RecencyFilter: "day",
		DomainFilter:  []string{"a.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" || cap.Path != "/request" {
		t.Errorf("请求 = %s %s，期望 POST /request", cap.Method, cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer bd-key" {
		t.Errorf("Authorization = %q", got)
	}

	body := decodeBody(t, cap.Body)
	if body["zone"] != "my_serp_zone" {
		t.Errorf("zone = %v", body["zone"])
	}
	if body["format"] != "raw" {
		t.Errorf("format = %v，期望 raw", body["format"])
	}
	if body["data_format"] != "parsed_light" {
		t.Errorf("data_format = %v，期望 parsed_light", body["data_format"])
	}
	// 被代理的目标地址：域名过滤用 site: 表达（Bright Data 背后就是 Google）。
	rawURL, _ := body["url"].(string)
	target, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("body.url 不是合法地址: %q", rawURL)
	}
	if target.Host != "www.google.com" {
		t.Errorf("代理目标应为 Google，实际 %q", target.Host)
	}
	q := target.Query()
	if q.Get("q") != "查询词 site:a.com" {
		t.Errorf("q = %q，期望含 site: 过滤", q.Get("q"))
	}
	// 一次 SERP 请求按次计费、与 num 无关，所以多要 5 条余量；上限 20。
	if q.Get("num") != "10" {
		t.Errorf("num = %q，期望 10（5+5 余量）", q.Get("num"))
	}
	if q.Get("tbs") != "qdr:d" {
		t.Errorf("day 应映射为 qdr:d，实际 %q", q.Get("tbs"))
	}
	if q.Get("brd_json") != "1" {
		t.Errorf("必须带 brd_json=1（否则拿到的是 HTML），实际 %q", q.Get("brd_json"))
	}

	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Snippet != "描述 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	// 空标题回落 "Source N"（已接受条数 +1），不是 URL。
	if resp.Results[1].Title != "Source 2" {
		t.Errorf("空标题应回落 Source 2，实际 %q", resp.Results[1].Title)
	}
}

// Bright Data 会把自己的错误塞进 200 响应体：这是「已计费却拿不到结果」，
// 必须报错降级，不能显示成「网上没有答案」。
func TestBrightdataErrorEnvelope(t *testing.T) {
	t.Setenv(brightdataZoneOption.EnvVar, "my_serp_zone")
	srv, _ := captureServer(t, 200, `{"error":"zone not found","code":"zone_missing"}`)
	p := newBrightdata()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	pe := assertProviderError(t, err, KindInvalidResponse, 200)
	if !strings.Contains(pe.Message, "zone not found") {
		t.Errorf("错误消息应带上上游说明: %s", pe.Message)
	}
}

func TestBrightdataMissingZone(t *testing.T) {
	t.Setenv(brightdataZoneOption.EnvVar, "")
	p := newBrightdata()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k"}, "q", Options{})
	pe := assertProviderError(t, err, KindConfig, 0)
	if !strings.Contains(pe.Message, brightdataZoneOption.EnvVar) {
		t.Errorf("错误消息应点名环境变量: %s", pe.Message)
	}
	if p.Configured(ChannelConfig{APIKey: "k"}) {
		t.Error("缺 zone 时不该算就绪（半配好的渠道不该进降级链）")
	}
}

func TestBrightdataMissingKey(t *testing.T) {
	t.Setenv(brightdataZoneOption.EnvVar, "my_serp_zone")
	p := newBrightdata()
	// 刻意不走 searchVia：这一条测的是「配置里没有 key」，
	// 走解析路径会把开发机上真设了的 BRIGHTDATA_API_KEY 当成本用例的输入。
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

// zone 名是账号内的标识符，不是 URL：字符集不对就该在本地拦住，
// 否则 Bright Data 会回一个读起来像凭据问题的通用 400。
func TestBrightdataZoneValidation(t *testing.T) {
	cases := []struct {
		zone string
		want bool
	}{
		{"my_serp_zone", true},
		{"zone-1", true},
		{"", false},
		{"bad zone", false},
		{"zone/name", false},
	}
	for _, c := range cases {
		if got := brightdataZoneValid(c.zone); got != c.want {
			t.Errorf("brightdataZoneValid(%q) = %v，期望 %v", c.zone, got, c.want)
		}
	}

	t.Setenv(brightdataZoneOption.EnvVar, "bad zone")
	srv, _ := captureServer(t, 200, `{"organic":[]}`)
	p := newBrightdata()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindConfig, 0)
}

func TestBrightdataStatusClassification(t *testing.T) {
	t.Setenv(brightdataZoneOption.EnvVar, "my_serp_zone")
	srv, _ := captureServer(t, 429, `{"error":"rate limited"}`)
	p := newBrightdata()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// ------------------------------------------------------------- Mistral 搜索

func TestMistralSearchRequestShape(t *testing.T) {
	pinMistralSearchEnv(t)
	srv, cap := captureServer(t, 200, `{"outputs":[
		{"type":"message.output","content":[
			{"type":"text","text":"这是答案"},
			{"type":"tool_reference","url":"https://a.com/1","title":"标题A","description":"描述 A"},
			{"type":"tool_reference","url":"https://a.com/1","title":"重复来源"},
			{"type":"tool_reference","url":"ftp://x.com/f","title":"非 http 来源"}
		]},
		{"type":"tool.execution","content":"过程，应被忽略"}
	]}`)

	p := newMistralSearch()
	resp, err := searchVia(t, p, ChannelConfig{APIKey: "m-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    3,
		RecencyFilter: "week",
		DomainFilter:  []string{"a.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" || cap.Path != "/v1/conversations" {
		t.Errorf("请求 = %s %s，期望 POST /v1/conversations", cap.Method, cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer m-key" {
		t.Errorf("Authorization = %q", got)
	}

	body := decodeBody(t, cap.Body)
	if body["stream"] != false {
		t.Errorf("stream = %v，期望 false", body["stream"])
	}
	if body["model"] != mistralSearchDefaultModel {
		t.Errorf("model = %v，期望 %s", body["model"], mistralSearchDefaultModel)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v，期望一个元素", body["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["type"] != mistralSearchDefaultTool {
		t.Errorf("tools[0] = %v，期望 type=%s", tools[0], mistralSearchDefaultTool)
	}

	inputs, ok := body["inputs"].([]any)
	if !ok || len(inputs) != 1 {
		t.Fatalf("inputs = %v，期望一个元素", body["inputs"])
	}
	input, ok := inputs[0].(map[string]any)
	if !ok || input["role"] != "user" {
		t.Fatalf("inputs[0] = %v，期望 role=user", inputs[0])
	}
	prompt, _ := input["content"].(string)
	for _, want := range []string{"past week", "Prefer up to 3 distinct sources.", "Only use sources from: a.com.", "查询词"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("提示词应含 %q，实际 %q", want, prompt)
		}
	}

	// 答案来自模型正文，结果来自引用；同一来源引用两次只留一条，非 http 来源丢掉。
	if resp.Answer != "这是答案" {
		t.Errorf("answer = %q，期望模型正文", resp.Answer)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1（去重 + 丢非 http）: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].URL != "https://a.com/1" || resp.Results[0].Title != "标题A" {
		t.Errorf("结果映射错: %+v", resp.Results[0])
	}
}

func TestMistralSearchToolOverride(t *testing.T) {
	pinMistralSearchEnv(t)
	t.Setenv(mistralSearchToolOption.EnvVar, mistralSearchPremiumTool)
	srv, cap := captureServer(t, 200, `{"outputs":[{"type":"message.output","content":"答案"}]}`)
	p := newMistralSearch()
	if _, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{}); err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	body := decodeBody(t, cap.Body)
	tools, _ := body["tools"].([]any)
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != mistralSearchPremiumTool {
		t.Errorf("档位覆盖失效: %v", tool["type"])
	}
}

// 非法档位报错而不是回落默认：web_search_premium 更贵，静默换档等于悄悄改计费。
func TestMistralSearchInvalidTool(t *testing.T) {
	pinMistralSearchEnv(t)
	t.Setenv(mistralSearchToolOption.EnvVar, "web_search_ultra")
	p := newMistralSearch()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k"}, "q", Options{})
	assertProviderError(t, err, KindConfig, 0)
}

// 响应既没有答案也没有来源 = 渠道结构变了 → 降级。
func TestMistralSearchEmptyOutputs(t *testing.T) {
	pinMistralSearchEnv(t)
	srv, _ := captureServer(t, 200, `{"outputs":[]}`)
	p := newMistralSearch()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindInvalidResponse, 200)
}

func TestMistralSearchMissingKey(t *testing.T) {
	pinMistralSearchEnv(t)
	p := newMistralSearch()
	// 同 TestBrightdataMissingKey：刻意不走 searchVia（要的是「配置里没有 key」）。
	_, err := p.Search(context.Background(), ChannelConfig{}, "q", Options{})
	assertProviderError(t, err, KindCredential, 0)
}

func TestMistralSearchStatusClassification(t *testing.T) {
	pinMistralSearchEnv(t)
	srv, _ := captureServer(t, 429, `{"message":"rate limit"}`)
	p := newMistralSearch()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}

// ---------------------------------------------------------------- Firecrawl

func TestFirecrawlRequestShape(t *testing.T) {
	pinFirecrawlEnv(t)
	srv, cap := captureServer(t, 200, `{"success":true,"data":{"web":[
		{"url":"https://a.com/1","title":"标题A","description":"  描述  A "},
		{"metadata":{"sourceURL":"https://a.com/2","title":"标题B","description":"描述 B"}},
		{"url":"https://b.com/3","title":"域外"}
	]}}`)

	p := newFirecrawl()
	resp, err := searchVia(t, p, ChannelConfig{APIKey: "fc-key", BaseURL: srv.URL}, "查询词", Options{
		NumResults:    5,
		RecencyFilter: "month",
		DomainFilter:  []string{"a.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Method != "POST" {
		t.Errorf("方法 = %s，期望 POST", cap.Method)
	}
	if cap.Path != "/v2/search" {
		t.Errorf("路径 = %s，期望 /v2/search（版本段来自默认 v2）", cap.Path)
	}
	if got := cap.Header.Get("Authorization"); got != "Bearer fc-key" {
		t.Errorf("Authorization = %q", got)
	}

	body := decodeBody(t, cap.Body)
	if body["query"] != "查询词" {
		t.Errorf("query = %v", body["query"])
	}
	if body["limit"] != float64(5) {
		t.Errorf("limit = %v，期望 5", body["limit"])
	}
	if !strings.Contains(toString(body["sources"]), "web") {
		t.Errorf("sources = %v，期望含 web", body["sources"])
	}
	if !strings.Contains(toString(body["includeDomains"]), "a.com") {
		t.Errorf("includeDomains = %v", body["includeDomains"])
	}
	// include 与 exclude 互斥：两个都给时 Firecrawl 的行为未定义。
	if _, present := body["excludeDomains"]; present {
		t.Errorf("有 includeDomains 时不该发 excludeDomains: %v", body["excludeDomains"])
	}
	if body["tbs"] != "qdr:m" {
		t.Errorf("month 应映射为 qdr:m，实际 %v", body["tbs"])
	}

	if len(resp.Results) != 2 {
		t.Fatalf("结果数 = %d，期望 2（b.com 被过滤）: %+v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].Snippet != "描述 A" {
		t.Errorf("snippet 应折叠空白，实际 %q", resp.Results[0].Snippet)
	}
	// 条目字段可能平铺，也可能嵌在 metadata 里，两处都要认。
	if resp.Results[1].URL != "https://a.com/2" || resp.Results[1].Title != "标题B" {
		t.Errorf("metadata 形态映射错: %+v", resp.Results[1])
	}
	if resp.Answer == "" {
		t.Error("纯 SERP 渠道应合成 answer")
	}
}

// 只有排除项时走 excludeDomains（上游的互斥规则）。
func TestFirecrawlExcludeOnlyDomains(t *testing.T) {
	pinFirecrawlEnv(t)
	srv, cap := captureServer(t, 200, `{"success":true,"data":{"web":[
		{"url":"https://a.com/1","title":"A"},
		{"url":"https://b.com/2","title":"B"}
	]}}`)
	p := newFirecrawl()
	resp, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{
		DomainFilter: []string{"-b.com"},
	})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	body := decodeBody(t, cap.Body)
	if _, present := body["includeDomains"]; present {
		t.Errorf("只有排除项时不该发 includeDomains: %v", body["includeDomains"])
	}
	if !strings.Contains(toString(body["excludeDomains"]), "b.com") {
		t.Errorf("excludeDomains = %v", body["excludeDomains"])
	}
	if len(resp.Results) != 1 || resp.Results[0].URL != "https://a.com/1" {
		t.Errorf("排除项应生效: %+v", resp.Results)
	}
}

// 自建实例通常不带 key：key 可选，且不该发空的 Authorization 头。
func TestFirecrawlKeylessSelfHosted(t *testing.T) {
	pinFirecrawlEnv(t)
	srv, cap := captureServer(t, 200, `{"success":true,"data":{"web":[{"url":"https://a.com/1","title":"A"}]}}`)
	p := newFirecrawl()
	resp, err := searchVia(t, p, ChannelConfig{BaseURL: srv.URL}, "q", Options{})
	if err != nil {
		t.Fatalf("无 key 的自建实例应可搜索: %v", err)
	}
	if got := cap.Header.Get("Authorization"); got != "" {
		t.Errorf("无 key 时不该发 Authorization，实际 %q", got)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("结果数 = %d，期望 1", len(resp.Results))
	}
	// 就绪判定只看实例地址（上游 isFirecrawlAvailable 同款）。
	if !p.Configured(ChannelConfig{BaseURL: "https://fc.example.com"}) {
		t.Error("有地址、无 key 时应为就绪")
	}
	if p.Configured(ChannelConfig{}) {
		t.Error("无地址时应为未就绪")
	}
}

// success=false 是渠道明确的失败：不能映射成「网上没有结果」。
func TestFirecrawlEnvelopeFailure(t *testing.T) {
	pinFirecrawlEnv(t)
	srv, _ := captureServer(t, 200, `{"success":false,"error":"invalid api key"}`)
	p := newFirecrawl()
	// key 用够长的假值：短 key 会被 Redact 在消息里连带替换掉，断言就没意义了。
	_, err := searchVia(t, p, ChannelConfig{APIKey: "fc-key-1234567890", BaseURL: srv.URL}, "q", Options{})
	pe := assertProviderError(t, err, KindInvalidResponse, 200)
	if !strings.Contains(pe.Message, "invalid api key") {
		t.Errorf("错误消息应带上渠道说明: %s", pe.Message)
	}
}

func TestFirecrawlMissingBaseURL(t *testing.T) {
	p := newFirecrawl()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k"}, "q", Options{})
	pe := assertProviderError(t, err, KindConfig, 0)
	if !strings.Contains(pe.Message, "未配置实例地址") {
		t.Errorf("错误消息应说明缺实例地址: %s", pe.Message)
	}
}

func TestFirecrawlInvalidAPIVersion(t *testing.T) {
	t.Setenv(firecrawlAPIVersionOption.EnvVar, "v3")
	p := newFirecrawl()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: "https://fc.example.com"}, "q", Options{})
	assertProviderError(t, err, KindConfig, 0)
}

func TestFirecrawlStatusClassification(t *testing.T) {
	pinFirecrawlEnv(t)
	srv, _ := captureServer(t, 429, `{"error":"rate limited"}`)
	p := newFirecrawl()
	_, err := searchVia(t, p, ChannelConfig{APIKey: "k", BaseURL: srv.URL}, "q", Options{})
	assertProviderError(t, err, KindQuota, 429)
}
