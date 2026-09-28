package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// anysearchDefaultBase 是 AnySearch 的默认 API 根。
const anysearchDefaultBase = "https://api.anysearch.com/v1"

// anysearchTimeout 是 AnySearch 的上游超时（anysearch.ts:12 给 30s，
// 比本包默认的 20s 略宽）。
const anysearchTimeout = 30 * time.Second

func newAnysearch() Provider {
	return &anysearchProvider{base: base{
		id:       "anysearch",
		label:    "AnySearch",
		desc:     "聚合型搜索 API，返回带 snippet 的结果列表，接入简单",
		docURL:   "https://anysearch.com",
		envVar:   "ANYSEARCH_API_KEY",
		needsKey: true,
	}}
}

type anysearchProvider struct{ base }

// Search 对齐上游 anysearch.ts:117-183：
// POST {base}/search，body {query, max_results}，
// 响应 {code:0, data:{results:[{title,url,snippet,content}], metadata:{}}}。
//
// 三处照搬上游的判定：
//   - code 必须是 0，否则整次响应判为失败（信封自带业务错误码）；
//   - data 与 data.metadata 必须是对象、data.results 必须是数组——
//     上游对 metadata 也做校验（anysearch.ts:89-91），这里同款：
//     它的存在是「响应来自真端点」的旁证；
//   - 每条结果的 title/url/snippet 必须是字符串且 url 非空，否则整体失败。
//
// 上游的 isAnySearchAvailable() 恒返回 true、请求头也是「有 key 才带」，
// 但同一文件里的 getApiKey 仍会解析凭据——没有 key 时拿不到有效响应。
// 我们按 needsKey=true 处理（缺 key 早报 KindCredential，比发出请求再吃 401 清楚）。
func (p *anysearchProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, anysearchDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	// AnySearch 只有 query/max_results 两个字段：域名过滤拼查询串 + 二次过滤；
	// recencyFilter 无处表达，忽略（上游同款）。
	include, exclude := DomainFilterParts(opts.DomainFilter)
	searchQuery := BuildSiteQuery(query, include, exclude)

	body := map[string]any{"query": searchQuery, "max_results": num}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var probe any
	if err := requestJSON(WithRequestTimeout(ctx, anysearchTimeout), p.id, ch.APIKey, req, &probe); err != nil {
		return Response{}, err
	}
	items, err := anysearchResults(probe)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	for _, item := range items {
		if !MatchesDomainFilter(item.URL, include, exclude) {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// AnySearch 不产出独立答案，用结果合成（上游 anysearch.ts:175 同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// anysearchResult 是一条 AnySearch 结果（content 字段上游不映射进 snippet）。
type anysearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// anysearchResults 解出并严格校验响应（上游 parseResponse 同款）。
func anysearchResults(probe any) ([]anysearchResult, error) {
	envelope, ok := probe.(map[string]any)
	if !ok {
		return nil, errors.New("期望对象信封")
	}
	if code, ok := envelope["code"].(float64); !ok || code != 0 {
		return nil, errors.New("期望 code 0")
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		return nil, errors.New("期望 data 对象")
	}
	raw, ok := data["results"].([]any)
	if !ok {
		return nil, errors.New("期望 data.results 数组")
	}
	if _, ok := data["metadata"].(map[string]any); !ok {
		return nil, errors.New("期望 data.metadata 对象")
	}

	out := make([]anysearchResult, 0, len(raw))
	for i, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("期望 data.results[%d] 是对象", i)
		}
		title, ok := item["title"].(string)
		if !ok {
			return nil, fmt.Errorf("期望 data.results[%d].title 是字符串", i)
		}
		url, ok := item["url"].(string)
		if !ok {
			return nil, fmt.Errorf("期望 data.results[%d].url 是字符串", i)
		}
		if url == "" {
			return nil, fmt.Errorf("期望 data.results[%d].url 非空", i)
		}
		snippet, ok := item["snippet"].(string)
		if !ok {
			return nil, fmt.Errorf("期望 data.results[%d].snippet 是字符串", i)
		}
		// content 允许缺席或为 null（上游只校验「不是字符串时才报错」）。
		if v, exists := item["content"]; exists && v != nil {
			if _, ok := v.(string); !ok {
				return nil, fmt.Errorf("期望 data.results[%d].content 是字符串", i)
			}
		}
		out = append(out, anysearchResult{Title: title, URL: url, Snippet: snippet})
	}
	return out, nil
}
