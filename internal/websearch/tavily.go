package websearch

import (
	"context"
	"net/http"
	"strings"
)

// tavilyDefaultBase 是 Tavily 的默认 API 根（可被用户配置的 base_url 覆盖，
// 对齐上游 tavilyBaseUrl / TAVILY_BASE_URL）。
const tavilyDefaultBase = "https://api.tavily.com"

func newTavily() Provider {
	return &tavilyProvider{base: base{
		id:       "tavily",
		label:    "Tavily",
		desc:     "为 LLM 设计的搜索 API，结果带摘要答案，接入最省事",
		docURL:   "https://app.tavily.com/",
		envVar:   "TAVILY_API_KEY",
		needsKey: true,
	}}
}

type tavilyProvider struct{ base }

// Search 对齐上游 tavily.ts:160-198：
// POST {base}/search，body 带 search_depth/include_answer/max_results，
// 响应 {answer, results[{title,url,content}]}。
func (p *tavilyProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, tavilyDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{
		"query":          query,
		"search_depth":   "basic",
		"max_results":    num,
		"include_answer": "basic",
	}
	if opts.RecencyFilter != "" {
		// Tavily 的 time_range 取值与我们的 recencyFilter 同名（day/week/month/year）。
		body["time_range"] = opts.RecencyFilter
	}
	if len(include) > 0 {
		body["include_domains"] = include
	}
	if len(exclude) > 0 {
		body["exclude_domains"] = exclude
	}

	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := requestJSON(ctx, p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}

	out := Response{Answer: strings.TrimSpace(raw.Answer)}
	for _, item := range raw.Results {
		if item.URL == "" {
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
	return out, nil
}
