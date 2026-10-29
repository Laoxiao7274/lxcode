package websearch

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// tinyFishDefaultBase 是 TinyFish Search 的默认根。
// 上游的端点是裸主机名（`https://api.search.tinyfish.ai`），查询串直接挂在它
// 后面——没有路径段，所以根常量就是完整端点。
const tinyFishDefaultBase = "https://api.search.tinyfish.ai"

// tinyFishTimeout 对齐上游 SEARCH_TIMEOUT_MS（60s）。
// 翻页时每页各自计时（上游对每页单独起 AbortSignal），不是总预算。
const tinyFishTimeout = 60 * time.Second

// tinyFishPageSize 是渠道的单页条数（上游按「本页是否满 10 条」决定要不要翻页）。
const tinyFishPageSize = 10

// tinyFishRecencyMinutes 把 recencyFilter 映射成 recency_minutes（上游 recencyMinutes）。
var tinyFishRecencyMinutes = map[string]int{
	"day":   1440,
	"week":  10080,
	"month": 43200,
	"year":  525600,
}

func newTinyFish() Provider {
	return &tinyFishProvider{base: base{
		id:       "tinyfish",
		label:    "TinyFish",
		desc:     "免信用点的搜索 API（按分钟限流），原生站点过滤与分钟级时间范围",
		docURL:   "https://agent.tinyfish.ai/api-keys",
		envVar:   "TINYFISH_API_KEY",
		needsKey: true,
	}}
}

type tinyFishProvider struct{ base }

// Search 对齐上游 tinyfish.ts:121-336（只移植 Search 一路：Fetch 是抓取能力，
// 与本包的「搜索渠道」职责不同类）。
//
// 请求：GET {base}?query=&include_domains=&exclude_domains=&recency_minutes=&page=，
// 鉴权头 X-API-Key（注意不是 Authorization）；
// 响应：{results:[{title, url, snippet}]}。
//
// 单页只有 10 条，所以条数需求 > 10 时要再翻一页（上游同款）；两页的结果
// 按 URL 去重后再截到需求条数——翻页会带回重复项，不去重会白占上下文。
func (p *tinyFishProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, tinyFishDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	pages := 1
	if num > tinyFishPageSize {
		pages = 2
	}

	var combined []Result
	for page := 0; page < pages; page++ {
		params := url.Values{}
		params.Set("query", query)
		if len(include) > 0 {
			params.Set("include_domains", strings.Join(include, ","))
		}
		if len(exclude) > 0 {
			params.Set("exclude_domains", strings.Join(exclude, ","))
		}
		if mins, ok := tinyFishRecencyMinutes[opts.RecencyFilter]; ok {
			params.Set("recency_minutes", strconv.Itoa(mins))
		}
		// 首页不带 page 参数（上游只在 page > 0 时设置）。
		if page > 0 {
			params.Set("page", strconv.Itoa(page))
		}

		headers := map[string]string{"X-API-Key": ch.APIKey}
		req, err := newJSONRequest(ctx, http.MethodGet, base+"?"+params.Encode(), headers, nil)
		if err != nil {
			return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
		}

		var raw struct {
			Results *[]struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			} `json:"results"`
		}
		if err := requestJSON(WithRequestTimeout(ctx, tinyFishTimeout), p.id, ch.APIKey, req, &raw); err != nil {
			return Response{}, err
		}
		if raw.Results == nil {
			return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
				"响应缺少 results 数组（渠道结构可能已变更）", ch.APIKey, nil)
		}
		for _, item := range *raw.Results {
			url := strings.TrimSpace(item.URL)
			if url == "" {
				continue
			}
			combined = append(combined, fillResult(Result{
				Title:   item.Title,
				URL:     url,
				Snippet: item.Snippet,
			}))
		}
		// 本页不满一页的量 = 没有下一页，提前收工（省一次配额）。
		if len(*raw.Results) < tinyFishPageSize {
			break
		}
	}

	out := Response{Results: tinyFishDedupe(combined, num)}
	// 该渠道不返回独立的答案字段，用结果合成（上游 formatSearchResultsAsAnswer）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// tinyFishDedupe 按 URL 去重并截到 limit（上游 deduplicateResults）。
func tinyFishDedupe(results []Result, limit int) []Result {
	seen := make(map[string]bool, len(results))
	out := make([]Result, 0, len(results))
	for _, r := range results {
		if seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out
}
