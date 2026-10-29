package websearch

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

func newSearxng() Provider {
	return &searxngProvider{base: base{
		id:           "searxng",
		label:        "SearXNG",
		desc:         "自建元搜索：无需 key、聚合多引擎；实例须开启 JSON 输出",
		docURL:       "https://docs.searxng.org/admin/settings/settings.html",
		envVar:       "SEARXNG_BASE_URL",
		needsBaseURL: true,
	}}
}

type searxngProvider struct{ base }

// Configured 覆写：SearXNG 不需要 key，只看实例地址（地址也可来自
// SEARXNG_BASE_URL 环境变量——effectiveChannel 已把环境值解析进 BaseURL）。
func (p *searxngProvider) Configured(ch ChannelConfig) bool {
	return ch.Enabled() && ch.BaseURL != ""
}

// Search 对齐上游 searxng.ts:177-228：
// GET {base}/search?q=&format=json&time_range=，响应 {results[], answers[]}。
//
// 注意实例侧前提：SearXNG 默认关闭 JSON 输出，需在 settings.yml 的
// search.formats 里加 json，否则这里会拿到 HTML（表现为 invalid-response）。
// 这是最常见的接入失败原因，所以错误信息里要点明。
func (p *searxngProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.BaseURL == "" {
		return Response{}, NewProviderError(p.id, KindConfig, 0,
			"未配置实例地址（如 https://searx.example.com）", "", nil)
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)
	searchQuery := BuildSiteQuery(query, include, exclude)

	params := url.Values{}
	params.Set("q", searchQuery)
	params.Set("format", "json")
	if ValidRecency(opts.RecencyFilter) && opts.RecencyFilter != "" {
		params.Set("time_range", opts.RecencyFilter)
	}

	req, err := newJSONRequest(ctx, http.MethodGet, ch.BaseURL+"/search?"+params.Encode(), nil, nil)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), "", err)
	}

	var raw struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
		Answers []string `json:"answers"`
	}
	if err := requestJSON(ctx, p.id, "", req, &raw); err != nil {
		return Response{}, err
	}

	out := Response{}
	for _, item := range raw.Results {
		if item.URL == "" || !MatchesDomainFilter(item.URL, include, exclude) {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Content,
		}))
		if len(out.Results) >= num {
			break
		}
	}

	// 答案 = 引擎直出答案（answers）+ 结果合成块（上游 searxng.ts:224-227）。
	var parts []string
	for _, a := range raw.Answers {
		if t := strings.TrimSpace(a); t != "" {
			parts = append(parts, t)
		}
	}
	if s := SourceAnswer(out.Results); s != "" {
		parts = append(parts, s)
	}
	out.Answer = strings.Join(parts, "\n\n")
	return out, nil
}
