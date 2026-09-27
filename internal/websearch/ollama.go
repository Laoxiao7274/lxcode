package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ollamaDefaultBase 是 Ollama 云搜索 API 的默认根。
const ollamaDefaultBase = "https://ollama.com/api"

// ollamaTimeout 比默认长：Ollama 的搜索端点要现抓网页，上游给 60s
// （ollama.ts:13）。
const ollamaTimeout = 60 * time.Second

// ollamaMaxResults 是 Ollama 侧接受的条数上限。上游 normalizeCount
// （ollama.ts:89-92）把上限钉在 10——比本包的 MaxNumResults(20) 更紧，
// 超发会被渠道拒或静默截断，所以在本地先钳住。
const ollamaMaxResults = 10

func newOllama() Provider {
	return &ollamaProvider{base: base{
		id:       "ollama",
		label:    "Ollama",
		desc:     "Ollama 云搜索 API：返回抓取后的正文内容，适合需要全文片段的场景",
		docURL:   "https://ollama.com/settings/keys",
		envVar:   "OLLAMA_API_KEY",
		needsKey: true,
	}}
}

type ollamaProvider struct{ base }

// Search 对齐上游 ollama.ts:130-178：
// POST {base}/web_search，body {query, max_results}，
// 响应 {results:[{title,url,content}]}。
//
// 与其它渠道的一处不同：上游对每条结果做严格类型校验，任一条 title/url/content
// 不是字符串、或 url 为空 → 整次响应判为 invalid-response（可降级）。
// 这里照搬——Ollama 的这个端点在出问题时会返回结构诡异的 JSON，
// 静默跳过坏条目会把「渠道坏了」显示成「搜到几条」。
func (p *ollamaProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = ollamaDefaultBase
	}
	num := ollamaClampNumResults(opts.NumResults)
	// Ollama 的 web_search 只接受 query/max_results，没有域名与时间范围参数
	// （上游同款）：域名过滤拼进查询串并二次过滤；recencyFilter 无处表达，
	// 刻意忽略——上游也忽略它，不编一个渠道不认的字段。
	include, exclude := DomainFilterParts(opts.DomainFilter)
	searchQuery := BuildSiteQuery(query, include, exclude)

	body := map[string]any{"query": searchQuery, "max_results": num}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/web_search", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var probe any
	if err := requestJSON(WithRequestTimeout(ctx, ollamaTimeout), p.id, ch.APIKey, req, &probe); err != nil {
		return Response{}, err
	}
	items, err := ollamaSearchItems(probe)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	for i, item := range items {
		if !MatchesDomainFilter(item.URL, include, exclude) {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			// content 是抓取后的正文片段，直接当 snippet（上游 ollama.ts:169）。
			Title:   sourceTitle(item.Title, i+1),
			URL:     item.URL,
			Snippet: item.Content,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// Ollama 不产出独立答案，用结果合成（上游 ollama.ts:170 同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// ollamaResult 是一条 Ollama 搜索结果。
type ollamaResult struct {
	Title   string
	URL     string
	Content string
}

// ollamaSearchItems 解出结果条目并做严格类型校验（上游 parseSearchResponse 同款）。
// 校验不通过时返回 error，由调用方包成 KindInvalidResponse。
func ollamaSearchItems(probe any) ([]ollamaResult, error) {
	envelope, ok := probe.(map[string]any)
	if !ok {
		return nil, errors.New("期望对象信封")
	}
	raw, ok := envelope["results"].([]any)
	if !ok {
		return nil, errors.New("期望 results 数组")
	}
	out := make([]ollamaResult, 0, len(raw))
	for i, entry := range raw {
		item, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("期望 results[%d] 是对象", i)
		}
		title, ok := item["title"].(string)
		if !ok {
			return nil, fmt.Errorf("期望 results[%d].title 是字符串", i)
		}
		url, ok := item["url"].(string)
		if !ok || url == "" {
			return nil, fmt.Errorf("期望 results[%d].url 是非空字符串", i)
		}
		content, ok := item["content"].(string)
		if !ok {
			return nil, fmt.Errorf("期望 results[%d].content 是字符串", i)
		}
		out = append(out, ollamaResult{Title: title, URL: url, Content: content})
	}
	return out, nil
}

// ollamaClampNumResults 在通用归一化的基础上再钳到 Ollama 的上限 10。
// 私有函数（不改 util.go 的共享助手）：这个上限只属于 Ollama。
func ollamaClampNumResults(n int) int {
	num := NormalizeNumResults(n)
	if num > ollamaMaxResults {
		return ollamaMaxResults
	}
	return num
}
