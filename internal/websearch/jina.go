package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// jinaDefaultBase 是 Jina Reader 搜索端点（查询作为路径段，不是 query 参数）。
const jinaDefaultBase = "https://s.jina.ai/"

func newJina() Provider {
	return &jinaProvider{base: base{
		id:       "jina",
		label:    "Jina Reader",
		desc:     "搜索 + 正文抓取一体（s.jina.ai），返回可直接读的内容",
		docURL:   "https://jina.ai/api-dashboard",
		envVar:   "JINA_API_KEY",
		needsKey: true,
	}}
}

type jinaProvider struct{ base }

// Search 对齐上游 jina-search.ts:210-273。
//
// 与其它渠道的两处不同：
//   - 查询串是**路径段**（https://s.jina.ai/<encoded>），不是 q 参数；
//   - 响应可能是信封 {code,data[]} 也可能是裸数组，两种都要认
//     （上游 parseItems 同款；信封 code≠200 视为渠道侧失败）。
func (p *jinaProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = jinaDefaultBase
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	// 查询构造（上游 jina-search.ts:105-114）：排除项与时间范围拼进查询串，
	// 包含项走 site 参数——Jina 的 site 参数只支持包含。
	constrained := strings.TrimSpace(query)
	for _, d := range exclude {
		constrained += " -site:" + d
	}
	if opts.RecencyFilter != "" {
		constrained += " published in the past " + opts.RecencyFilter
	}

	params := url.Values{}
	params.Set("count", strconv.Itoa(num))
	for _, d := range include {
		params.Add("site", d)
	}
	target := base + url.PathEscape(constrained) + "?" + params.Encode()

	headers := map[string]string{
		"Authorization":   "Bearer " + ch.APIKey,
		"Accept":          "application/json",
		"X-Respond-With":  "no-content", // 只要结果元数据，正文按需再抓
		"X-Retain-Images": "none",
	}
	req, err := newJSONRequest(ctx, http.MethodGet, target, headers, nil)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	// 信封 {code,data[]} 与裸数组两种形态都要认，所以先解成 any 再判定形状。
	var probe any
	if err := requestJSON(ctx, p.id, ch.APIKey, req, &probe); err != nil {
		return Response{}, err
	}
	items, err := parseJinaItems(probe)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	seen := map[string]bool{}
	for _, item := range items {
		u := strings.TrimSpace(item.URL)
		if u == "" || seen[u] || !MatchesDomainFilter(u, include, exclude) {
			continue
		}
		seen[u] = true
		out.Results = append(out.Results, fillResult(Result{
			Title:   item.Title,
			URL:     u,
			Snippet: item.Description,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// jinaSearchItem 是 Jina 结果条目。
type jinaSearchItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// parseJinaItems 兼容信封与裸数组两种响应形态。
func parseJinaItems(probe any) ([]jinaSearchItem, error) {
	switch v := probe.(type) {
	case []any:
		return decodeJinaItems(v), nil
	case map[string]any:
		if code, ok := v["code"].(float64); ok && code != 200 {
			return nil, fmt.Errorf("Jina 响应信封报错 code=%d", int(code))
		}
		arr, ok := v["data"].([]any)
		if !ok {
			return nil, errors.New("Jina 响应结构不符（既非数组也无 data 字段）")
		}
		return decodeJinaItems(arr), nil
	default:
		return nil, errors.New("Jina 响应结构不符（既非数组也无 data 字段）")
	}
}

// decodeJinaItems 把 []any 逐条解成 jinaSearchItem。
// 单条结构异常只跳过该条——过滤逻辑本来就会丢掉没有 url 的条目，
// 为一条坏数据让整次搜索失败是拿小概率换大损失。
func decodeJinaItems(arr []any) []jinaSearchItem {
	out := make([]jinaSearchItem, 0, len(arr))
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		var item jinaSearchItem
		if s, ok := m["title"].(string); ok {
			item.Title = s
		}
		if s, ok := m["url"].(string); ok {
			item.URL = s
		}
		if s, ok := m["description"].(string); ok {
			item.Description = s
		}
		out = append(out, item)
	}
	return out
}
