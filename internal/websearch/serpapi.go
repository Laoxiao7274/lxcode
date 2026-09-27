package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// serpapiDefaultBase 是 SerpApi 的默认 API 根（可被 base_url 覆盖）。
// 端点为 {base}/search.json（上游 serpapi.ts:10 的 SERPAPI_SEARCH_URL）。
const serpapiDefaultBase = "https://serpapi.com"

func newSerpapi() Provider {
	return &serpapiProvider{base: base{
		id:       "serpapi",
		label:    "SerpApi",
		desc:     "Google 搜索 API 老牌服务，字段最全、支持引擎最多；按次消耗额度",
		docURL:   "https://serpapi.com/manage-api-key",
		envVar:   "SERPAPI_KEY",
		needsKey: true,
	}}
}

type serpapiProvider struct{ base }

// Search 对齐上游 serpapi.ts:132-197：
// GET {base}/search.json?engine=google&q=&api_key=&num=&tbs=，
// 响应 {organic_results:[{title,link,snippet}]}。
//
// 鉴权走 **query 参数 api_key**（不是请求头）——这是 SerpApi 的设计
// （serpapi.ts:140），代价是 key 会出现在 URL 里：请求行可能进渠道侧访问日志，
// 出站代理/重定向也会带着它。我们照 TS 实现，但错误消息仍由 requestJSON
// 统一脱敏（key 会从错误文本里抹掉）。
func (p *serpapiProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = serpapiDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	params := url.Values{}
	params.Set("engine", "google")
	params.Set("q", BuildSiteQuery(query, include, exclude))
	params.Set("api_key", ch.APIKey)
	params.Set("num", strconv.Itoa(serpFamilyRequestCount(num, opts.DomainFilter)))
	if tbs, ok := serpFamilyTBS[opts.RecencyFilter]; ok {
		params.Set("tbs", tbs)
	}

	req, err := newJSONRequest(ctx, http.MethodGet, base+"/search.json?"+params.Encode(), nil, nil)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		// error 用 RawMessage：只在它是非空字符串时才算失败（serpapi.ts:123）。
		Error json.RawMessage `json:"error"`
		// 指针切片区分「字段缺席」（上游判无效响应）与「空数组」（合法的没搜到）。
		OrganicResults *[]struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic_results"`
	}
	if err := requestJSON(ctx, p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}
	// 信封级错误：SerpApi 会用 HTTP 200 + error 字段报鉴权/额度问题，
	// 归 invalid-response（可降级）——与上游「抛错」同义，不是静默返回空结果。
	if msg := serpFamilyEnvelopeErrorText(raw.Error); msg != "" {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, msg, ch.APIKey, nil)
	}
	if raw.OrganicResults == nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
			"响应缺少 organic_results 数组（信封形状不符）", ch.APIKey, nil)
	}

	out := Response{}
	for _, item := range *raw.OrganicResults {
		if item.Link == "" || !MatchesDomainFilter(item.Link, include, exclude) {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   sourceTitle(strings.TrimSpace(item.Title), len(out.Results)+1),
			URL:     item.Link,
			Snippet: item.Snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}
