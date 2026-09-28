package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// serpbaseDefaultBase 是 SerpBase 的默认 API 根（可被 base_url 覆盖）。
// 端点为 {base}/google/search（上游 serpbase.ts:9 的 SERPBASE_API_URL）。
const serpbaseDefaultBase = "https://api.serpbase.dev"

func newSerpbase() Provider {
	return &serpbaseProvider{base: base{
		id:       "serpbase",
		label:    "SerpBase",
		desc:     "Google SERP API，结果字段命名有多种兼容形态；按次消耗额度",
		docURL:   "https://serpbase.dev",
		envVar:   "SERPBASE_API_KEY",
		needsKey: true,
	}}
}

type serpbaseProvider struct{ base }

// serpbaseStatusText 取 status 字段的文本（只认字符串与数字，对齐上游
// serpbase.ts:144 的 typeof 判定），用于把状态拼进错误消息。
func serpbaseStatusText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

// Search 对齐上游 serpbase.ts:156-208：
// GET {base}/google/search?q=&api_key=&num=&tbs=，
// 响应信封里结果数组可能叫 organic_results / organic / results 三种之一。
//
// 鉴权走 **query 参数 api_key**（serpbase.ts:163 明确注释了这是该端点的设计），
// 所以 key 会出现在 URL 里；错误消息仍由 requestJSON 统一脱敏。
func (p *serpbaseProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, serpbaseDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	params := url.Values{}
	params.Set("q", BuildSiteQuery(query, include, exclude))
	params.Set("api_key", ch.APIKey)
	// SerpBase 不做「有过滤就多取」的补偿（上游 serpbase.ts:164 直接发 numResults），
	// 所以这里也不套 serpFamilyRequestCount。
	params.Set("num", strconv.Itoa(num))
	if tbs, ok := serpFamilyTBS[opts.RecencyFilter]; ok {
		params.Set("tbs", tbs)
	}

	req, err := newJSONRequest(ctx, http.MethodGet, base+"/google/search?"+params.Encode(), nil, nil)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	// 三种结果数组字段名都认（上游 serpbase.ts:147 的 ?? 链）：这是同一个 API
	// 在版本演进中换过字段名，只认一种会让另一版部署静默返回空结果。
	var raw struct {
		Error          json.RawMessage `json:"error"`
		Status         json.RawMessage `json:"status"`
		OrganicResults *[]serpbaseItem `json:"organic_results"`
		Organic        *[]serpbaseItem `json:"organic"`
		Results        *[]serpbaseItem `json:"results"`
	}
	if err := requestJSON(ctx, p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}
	if msg := serpFamilyEnvelopeErrorText(raw.Error); msg != "" {
		// 上游把 status 拼进消息（serpbase.ts:144），照做——有的失败原因
		// 只在 status 里能看出来（如 429 被包在 200 响应里）。
		if s := serpbaseStatusText(raw.Status); s != "" {
			msg += " (status " + s + ")"
		}
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, msg, ch.APIKey, nil)
	}
	items := raw.OrganicResults
	if items == nil {
		items = raw.Organic
	}
	if items == nil {
		items = raw.Results
	}
	if items == nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
			"响应缺少结果数组（organic_results/organic/results 都不存在）", ch.APIKey, nil)
	}

	out := Response{}
	for _, item := range *items {
		// link 与 url、snippet 与 description 两组同义字段，取先出现的非空者。
		// （上游是 typeof 判定后取值，语义差别只在「link 存在但为空串」这种
		// 畸形条目上：那时上游跳过该条，我们退到 url——有合法 url 就收下。）
		link := item.Link
		if link == "" {
			link = item.URL
		}
		if link == "" || !MatchesDomainFilter(link, include, exclude) {
			continue
		}
		snippet := item.Snippet
		if snippet == "" {
			snippet = item.Description
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   sourceTitle(item.Title, len(out.Results)+1),
			URL:     link,
			Snippet: snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// serpbaseItem 是 SerpBase 的结果条目（同义字段并存，见 Search 里的取值说明）。
type serpbaseItem struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Description string `json:"description"`
}
