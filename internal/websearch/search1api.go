package websearch

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// search1apiDefaultBase 是 Search1API 的默认 API 根（可被用户配置的 base_url 覆盖，
// 对齐上游 SEARCH1API_SEARCH_URL 的 origin）。
const search1apiDefaultBase = "https://api.search1api.com"

// search1apiTimeout 对齐上游 SEARCH_TIMEOUT_MS（60s）。
// 它是个聚合型搜索网关（后端再转下游引擎），冷启动明显慢于直连 SERP——
// 用本包默认的 20s 会把「慢」误判成 KindNetwork，白白触发一次降级。
const search1apiTimeout = 60 * time.Second

func newSearch1API() Provider {
	return &search1APIProvider{base: base{
		id:       "search1api",
		label:    "Search1API",
		desc:     "聚合型搜索网关，原生支持站点过滤与时间范围，按积分计费",
		docURL:   "https://dashboard.search1api.com",
		envVar:   "SEARCH1API_KEY",
		needsKey: true,
	}}
}

type search1APIProvider struct{ base }

// Search 对齐上游 search1api.ts:120-234（只移植 Search 一路：Crawl 是抓取能力，
// 与本包的「搜索渠道」职责不同类）。
//
// 请求：POST {base}/search，Authorization: Bearer，body
// {query, max_results, crawl_results, include_sites?, exclude_sites?, time_range?}；
// 响应：{results:[{title, link, snippet, content}]}。
func (p *search1APIProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = search1apiDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{
		"query":       query,
		"max_results": num,
		// crawl_results=0 = 不启用 Deep Search。上游只在 includeContent 为真时
		// 填 numResults；本包不做正文抓取，恒为 0——Deep Search 每抓一页多花
		// 一个积分，默认开着等于替用户花钱。
		"crawl_results": 0,
	}
	if len(include) > 0 {
		body["include_sites"] = include
	}
	if len(exclude) > 0 {
		body["exclude_sites"] = exclude
	}
	if opts.RecencyFilter != "" {
		// Search1API 的 time_range 取值与我们的 recencyFilter 同名，直接透传
		// （上游 buildSearchBody 同款）。
		body["time_range"] = opts.RecencyFilter
	}

	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		// 用指针接收数组：上游对「results 不是数组」直接抛错（结构变更），
		// 但空数组是合法的「没搜到」。用非指针字段这两者都是 nil/空切片，
		// 分不开——把结构变更静默当成「没搜到」是本包最忌讳的一类假象。
		Results *[]struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := requestJSON(WithRequestTimeout(ctx, search1apiTimeout), p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}
	if raw.Results == nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
			"响应缺少 results 数组（渠道结构可能已变更）", ch.APIKey, nil)
	}

	out := Response{}
	for _, item := range *raw.Results {
		link := strings.TrimSpace(item.Link)
		if link == "" {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   item.Title,
			URL:     link,
			Snippet: item.Snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// Search1API 不返回摘要答案，上游用 formatSearchResultsAsAnswer 合成
	// （= 本包的 SourceAnswer）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}
