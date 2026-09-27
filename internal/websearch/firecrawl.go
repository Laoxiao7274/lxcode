package websearch

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// 上游没有硬编码 Firecrawl 地址：端点必须由用户填（自建实例或官方云
// api.firecrawl.dev 都可以），所以本渠道没有默认 base 常量。
const firecrawlDefaultAPIVersion = "v2"

// firecrawlAPIVersionOption 是 API 版本的设置项声明。
//
// 官方云与自建实例的路径前缀不同（/{version}/search），版本填错会得到一个
// 看不懂的 404，所以它是有限枚举而不是自由文本。
var firecrawlAPIVersionOption = OptionSpec{
	Key:         "api_version",
	Label:       "API 版本",
	Placeholder: firecrawlDefaultAPIVersion,
	Hint:        "官方云用 v2；自建老版本实例可能只支持 v1（填错会 404）",
	EnvVar:      "FIRECRAWL_API_VERSION",
	Default:     firecrawlDefaultAPIVersion,
	Choices:     []string{"v1", "v2"},
}

// firecrawlTimeout 比包默认长：官方云要现抓页面，自建实例可能更慢，上游给 60s。
const firecrawlTimeout = 60 * time.Second

// firecrawlRecencyTBS 是 recencyFilter → Google tbs 时间过滤（firecrawl.ts:264-272）。
// Firecrawl 把它透传给底层引擎，属于引擎侧过滤。
var firecrawlRecencyTBS = map[string]string{
	"day":   "qdr:d",
	"week":  "qdr:w",
	"month": "qdr:m",
	"year":  "qdr:y",
}

func newFirecrawl() Provider {
	// key 与端点两个开关的取值理由：
	//   needsKey=false  自建实例通常不带 key，上游只在有 key 时才发 Authorization
	//                   （firecrawl.ts:337-338），所以 key 是可选项；
	//   needsBaseURL=true 端点必须用户填——上游没有硬编码地址，
	//                   官方云与自建实例走的是同一个字段。
	return &firecrawlProvider{base: base{
		id:           "firecrawl",
		label:        "Firecrawl",
		desc:         "自建/云端 Firecrawl 的 /search 端点：返回网页结果，支持原生域名过滤",
		docURL:       "https://docs.firecrawl.dev/",
		envVar:       "FIRECRAWL_API_KEY",
		needsKey:     false,
		needsBaseURL: true,
		options:      []OptionSpec{firecrawlAPIVersionOption},
	}}
}

type firecrawlProvider struct{ base }

// Configured 覆写：Firecrawl 的就绪判定只看实例地址（上游 isFirecrawlAvailable
// 同款，firecrawl.ts:378-380）——key 可选，缺 key 不是「未就绪」。
func (p *firecrawlProvider) Configured(ch ChannelConfig) bool {
	return ch.Enabled() && ch.BaseURL != ""
}

// Search 对齐上游 firecrawl.ts:382-398：
// POST {base}/{version}/search，body {query,limit,sources,includeDomains?,...}，
// 响应 {success,data:{web:[{url,title,description,metadata}]}}。
//
// 两处细节照上游：① includeDomains 与 excludeDomains 互斥（只在没有 include
// 时才发 exclude，firecrawl.ts:279-280）；② 响应信封必须 success=true，
// 否则「Firecrawl 明确说失败了」会被映射成「网上没有结果」。
func (p *firecrawlProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.BaseURL == "" {
		return Response{}, NewProviderError(p.id, KindConfig, 0,
			"未配置实例地址（官方云 https://api.firecrawl.dev 或自建实例地址）", ch.APIKey, nil)
	}
	version, err := firecrawlAPIVersion(ch)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{
		"query":   query,
		"limit":   num,
		"sources": []string{"web"},
	}
	switch {
	case len(include) > 0:
		body["includeDomains"] = include
	case len(exclude) > 0:
		// 两个都给时 Firecrawl 的行为没有定义，上游刻意只在没有 include 时发
		// exclude（firecrawl.ts:279-280），这里照搬。
		body["excludeDomains"] = exclude
	}
	if tbs, ok := firecrawlRecencyTBS[opts.RecencyFilter]; ok {
		body["tbs"] = tbs
	}

	headers := map[string]string{}
	if ch.APIKey != "" {
		headers["Authorization"] = "Bearer " + ch.APIKey
	}
	req, err := newJSONRequest(ctx, http.MethodPost, ch.BaseURL+"/"+version+"/search", headers, body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var env map[string]any
	if err := requestJSON(WithRequestTimeout(ctx, firecrawlTimeout), p.id, ch.APIKey, req, &env); err != nil {
		return Response{}, err
	}
	web, err := firecrawlWebResults(env)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	seen := map[string]bool{}
	for _, item := range web {
		u := firecrawlFirstString(item["url"], firecrawlMeta(item, "sourceURL"), firecrawlMeta(item, "url"))
		if u == "" || seen[u] || !MatchesDomainFilter(u, include, exclude) {
			continue
		}
		seen[u] = true
		title := firecrawlFirstString(item["title"], firecrawlMeta(item, "title"))
		if title == "" {
			title = u
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   title,
			URL:     u,
			Snippet: firecrawlFirstString(item["description"], item["snippet"], firecrawlMeta(item, "description")),
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// 纯 SERP 渠道：答案由结果合成（上游 firecrawl.ts:395 同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// firecrawlWebResults 校验响应信封并取出 web 结果数组。
//
// success 字段是成败判据（上游 firecrawl.ts:362-368）：false 时把 error 当渠道侧
// 失败报出来，缺席也当结构不符——不这样判的话，一次明确的失败会被静默映射成
// 「网上没有结果」，用户永远查不出问题。
func firecrawlWebResults(env map[string]any) ([]map[string]any, error) {
	success, ok := env["success"].(bool)
	if !ok {
		return nil, fmt.Errorf("响应缺少 success 布尔字段")
	}
	if !success {
		reason := "unknown error"
		if s, ok := env["error"].(string); ok && strings.TrimSpace(s) != "" {
			reason = strings.TrimSpace(s)
		}
		return nil, fmt.Errorf("渠道报告失败: %s", reason)
	}

	var web []any
	switch data := env["data"].(type) {
	case []any:
		// 旧版本把结果直接放在 data 数组里，两种形态都要认（firecrawl.ts:303-305）。
		web = data
	case map[string]any:
		arr, ok := data["web"].([]any)
		if !ok {
			return nil, fmt.Errorf("data.web 不是数组")
		}
		web = arr
	default:
		return nil, fmt.Errorf("data 既不是数组也不是对象")
	}

	items := make([]map[string]any, 0, len(web))
	for _, it := range web {
		if m, ok := it.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items, nil
}

// firecrawlMeta 取 item.metadata.<key>：Firecrawl 的条目字段可能平铺在条目上，
// 也可能嵌在 metadata 里，两处都要认（firecrawl.ts:313-317）。
func firecrawlMeta(item map[string]any, key string) any {
	m, ok := item["metadata"].(map[string]any)
	if !ok {
		return nil
	}
	return m[key]
}

// firecrawlFirstString 返回第一个非空字符串（上游 firstString 同款：
// 同一语义的字段名有多个来源，按优先级取第一个有值的）。
func firecrawlFirstString(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok {
			if t := strings.TrimSpace(s); t != "" {
				return t
			}
		}
	}
	return ""
}

// firecrawlAPIVersion 解析 API 版本（默认 v2）。
//
// 上游把版本放在 web-search.json 的 firecrawlApiVersion（firecrawl.ts:132-147）；
// 本包把它做成声明式设置项。非法值报错而不是回落默认：用户显式配了 v1 却被
// 静默按 v2 发请求，会得到一个看不懂的 404。
//
// 面板给的是下拉（Choices），非法值只可能来自手写配置或环境变量。
func firecrawlAPIVersion(ch ChannelConfig) (string, error) {
	v := strings.ToLower(strings.TrimSpace(ch.Options[firecrawlAPIVersionOption.Key]))
	if v == "" {
		return firecrawlDefaultAPIVersion, nil
	}
	if v != "v1" && v != "v2" {
		return "", fmt.Errorf("%s 只支持 v1 或 v2（实际 %q）", firecrawlAPIVersionOption.Label, v)
	}
	return v, nil
}
