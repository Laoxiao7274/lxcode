package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// serplyDefaultBase 是 Serply 的默认 API 根（可被 base_url 覆盖）。
// 端点为 {base}/search（上游 serply.ts:10 的 SERPLY_SEARCH_URL）。
const serplyDefaultBase = "https://api.serply.io/v1"

func newSerply() Provider {
	return &serplyProvider{base: base{
		id:       "serply",
		label:    "Serply",
		desc:     "Google 搜索 API，key 走 X-Api-Key 头；按次消耗额度",
		docURL:   "https://serply.io",
		envVar:   "SERPLY_API_KEY",
		needsKey: true,
	}}
}

type serplyProvider struct{ base }

// Search 对齐上游 serply.ts:127-196：
// GET {base}/search?q=&num=&tbs=，头 X-Api-Key，
// 响应 {results:[{title,link,description}]}。
func (p *serplyProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, serplyDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	params := url.Values{}
	params.Set("q", BuildSiteQuery(query, include, exclude))
	params.Set("num", strconv.Itoa(serpFamilyRequestCount(num, opts.DomainFilter)))
	if tbs, ok := serpFamilyTBS[opts.RecencyFilter]; ok {
		params.Set("tbs", tbs)
	}

	// 头名按上游原样（X-Api-Key）——HTTP 头名不区分大小写，写法只作可读性用。
	headers := map[string]string{
		"X-Api-Key": ch.APIKey,
		"Accept":    "application/json",
	}
	req, err := newJSONRequest(ctx, http.MethodGet, base+"/search?"+params.Encode(), headers, nil)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		// detail 是 Serply 的错误字段（serply.ts:118），同样只在是非空字符串时算失败。
		Detail json.RawMessage `json:"detail"`
		// 指针切片区分「results 缺席」（上游判无效响应）与「空数组」。
		Results *[]struct {
			Title       string `json:"title"`
			Link        string `json:"link"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := requestJSON(ctx, p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}
	if msg := serpFamilyEnvelopeErrorText(raw.Detail); msg != "" {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, msg, ch.APIKey, nil)
	}
	if raw.Results == nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
			"响应缺少 results 数组（信封形状不符）", ch.APIKey, nil)
	}

	out := Response{}
	for _, item := range *raw.Results {
		link := strings.TrimSpace(item.Link)
		if link == "" {
			continue
		}
		// 只收 http(s)：上游对每条链接做 new URL() + 协议判定（serply.ts:181-187），
		// 挡掉 javascript:/data: 这类会被前端当链接渲染的伪协议。
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		if !MatchesDomainFilter(link, include, exclude) {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			Title: sourceTitle(strings.TrimSpace(item.Title), len(out.Results)+1),
			// 保留渠道给的原始链接文本，不用 url.Parse 再序列化一遍：
			// 上游的 resultUrl.href 会把裸域补成 https://a.com/，这种改写是
			// 装饰性的，而重新序列化还可能动到本来就正确的百分号编码。
			URL:     link,
			Snippet: item.Description,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}
