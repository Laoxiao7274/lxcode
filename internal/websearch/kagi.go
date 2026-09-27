package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// kagiDefaultBase 是 Kagi Search API 的默认根。
const kagiDefaultBase = "https://kagi.com/api/v1"

// kagiTimeout 比默认长：Kagi 的搜索端点本身较慢（上游 kagi.ts:14 给 60s）。
const kagiTimeout = 60 * time.Second

func newKagi() Provider {
	return &kagiProvider{base: base{
		id:       "kagi",
		label:    "Kagi",
		desc:     "付费无广告搜索（需订阅），结果质量高、可返回正文摘录",
		docURL:   "https://kagi.com/settings?p=api",
		envVar:   "KAGI_API_KEY",
		needsKey: true,
	}}
}

type kagiProvider struct{ base }

// Search 对齐上游 kagi.ts:157-201：
// POST {base}/search，body {query, limit}，响应信封 {data:{search:[...]}}。
//
// 与其它渠道的两处不同：
//   - 响应里字段名有多种形态（title/name、snippet/description/summary/content/
//     markdown/text、url/href/link），上游是逐个 firstString 回落——真实部署
//     里 Kagi 的响应形态随版本变过，只认一种会把「换个字段名」变成搜索失败；
//   - 信封里的 errors 数组表示渠道侧报错（HTTP 仍是 200），必须当失败处理，
//     否则会被当成「搜到 0 条」（kagi.ts:104-120）。
func (p *kagiProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = kagiDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)
	// Kagi 的搜索接口只有 query/limit 两个字段，没有域名参数——过滤只能
	// 拼进查询串再二次过滤（上游 kagi.ts:166 同款，它也没做域名过滤）。
	searchQuery := BuildSiteQuery(query, include, exclude)

	headers := map[string]string{
		"Authorization": "Bearer " + ch.APIKey,
		"Accept":        "application/json",
	}
	body := map[string]any{"query": searchQuery, "limit": num}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search", headers, body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var probe any
	if err := requestJSON(WithRequestTimeout(ctx, kagiTimeout), p.id, ch.APIKey, req, &probe); err != nil {
		return Response{}, err
	}
	items, err := kagiSearchItems(probe)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	for _, item := range items {
		url := kagiFirstString(item, "url", "href", "link")
		if url == "" || !MatchesDomainFilter(url, include, exclude) {
			continue
		}
		title := kagiFirstString(item, "title", "name")
		snippet := kagiFirstString(item, "snippet", "description", "summary", "content", "markdown", "text")
		out.Results = append(out.Results, fillResult(Result{
			Title:   title,
			URL:     url,
			Snippet: snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// Kagi 不产出独立答案，用结果合成（上游 kagi.ts:194 同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// kagiSearchItems 从响应信封里取出结果条目数组。
//
// 形状容忍（上游 kagi.ts:117-130）：
//   - 信封必须是对象；errors/error 数组非空 → 渠道侧失败；
//   - data 是对象时取 data.search，是数组时直接当条目列表；
//   - search 既可能是条目数组，也可能是单个条目对象（递归展平）。
func kagiSearchItems(probe any) ([]map[string]any, error) {
	envelope, ok := probe.(map[string]any)
	if !ok {
		return nil, errors.New("Kagi 响应结构不符（期望对象信封）")
	}
	if msg := kagiErrorMessages(envelope); msg != "" {
		return nil, fmt.Errorf("Kagi 响应报错: %s", msg)
	}
	// data 是对象时取它的 search 字段，否则把 data 本身当条目列表
	// （上游 kagi.ts:124-128 同款：data 两种形态都见过）。
	data := envelope["data"]
	if obj, ok := data.(map[string]any); ok {
		data = obj["search"]
	}
	return kagiFlattenItems(data), nil
}

// kagiFlattenItems 递归展平条目（数组/单对象都认），非对象项跳过。
func kagiFlattenItems(value any) []map[string]any {
	switch v := value.(type) {
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			out = append(out, kagiFlattenItems(item)...)
		}
		return out
	case map[string]any:
		return []map[string]any{v}
	default:
		return nil
	}
}

// kagiErrorMessages 把信封里的 errors/error 数组拼成一句说明（无错返回空串）。
//
// 取「errors 存在就用 errors，否则用 error」——与上游 `errors ?? error` 的
// 空值合并语义一致：errors 存在但不是数组时不回落到 error（那是另一种形态，
// 硬猜会把正常响应误判成失败）。
func kagiErrorMessages(envelope map[string]any) string {
	raw, ok := envelope["errors"]
	if !ok {
		raw = envelope["error"]
	}
	arr, ok := raw.([]any)
	if !ok {
		return ""
	}
	msgs := make([]string, 0, len(arr))
	for _, entry := range arr {
		if m, ok := entry.(map[string]any); ok {
			if s := kagiFirstString(m, "message", "msg", "code"); s != "" {
				msgs = append(msgs, s)
				continue
			}
		}
		if s := strings.TrimSpace(fmt.Sprintf("%v", entry)); s != "" {
			msgs = append(msgs, s)
		}
	}
	return strings.Join(msgs, "; ")
}

// kagiFirstString 按顺序取第一个非空字符串字段（上游 firstString 同款）。
func kagiFirstString(item map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := item[k].(string); ok {
			if t := strings.TrimSpace(s); t != "" {
				return t
			}
		}
	}
	return ""
}
