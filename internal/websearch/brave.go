package websearch

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// braveDefaultBase 是 Brave Search API 的默认根（可被 base_url 覆盖）。
const braveDefaultBase = "https://api.search.brave.com/res/v1"

// braveFreshness 是 recencyFilter → Brave freshness 参数的映射
// （上游 brave.ts:146-151）。
var braveFreshness = map[string]string{
	"day":   "pd",
	"week":  "pw",
	"month": "pm",
	"year":  "py",
}

func newBrave() Provider {
	return &braveProvider{base: base{
		id:       "brave",
		label:    "Brave Search",
		desc:     "独立索引的搜索 API，隐私友好、免费额度较大",
		docURL:   "https://brave.com/search/api/",
		envVar:   "BRAVE_API_KEY",
		needsKey: true,
	}}
}

type braveProvider struct{ base }

// Search 对齐上游 brave.ts:121-198：
// GET {base}/web/search?q=&count=&freshness=，头 X-Subscription-Token。
// Brave 没有域名参数，过滤拼进查询串后仍需二次过滤（上游同款）。
func (p *braveProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = braveDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)
	searchQuery := BuildSiteQuery(query, include, exclude)

	// 有域名过滤时多取一些再过滤：过滤会砍掉不少结果，
	// 只取 num 条会导致过滤后不足（上游 brave.ts:142 同款处理）。
	count := num
	if len(include) > 0 || len(exclude) > 0 {
		count = 20
	}

	params := url.Values{}
	params.Set("q", searchQuery)
	params.Set("count", strconv.Itoa(count))
	if f, ok := braveFreshness[opts.RecencyFilter]; ok {
		params.Set("freshness", f)
	}

	headers := map[string]string{
		"X-Subscription-Token": ch.APIKey,
		"Accept":               "application/json",
	}
	req, err := newJSONRequest(ctx, http.MethodGet, base+"/web/search?"+params.Encode(), headers, nil)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := requestJSON(ctx, p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}

	out := Response{}
	for _, item := range raw.Web.Results {
		if item.URL == "" || !MatchesDomainFilter(item.URL, include, exclude) {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Description,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// 纯 SERP 渠道没有自带答案，用结果合成（上游同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}
