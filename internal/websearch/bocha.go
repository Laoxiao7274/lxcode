package websearch

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// bochaDefaultBase 是博查 Web Search API 的默认根（可被用户配置的 base_url 覆盖）。
const bochaDefaultBase = "https://api.bochaai.com"

// bochaTimeout 比包默认长：summary=true 时博查要现抓网页摘要，
// 上游给到 60s（bocha.ts:11 的 SEARCH_TIMEOUT_MS）。
const bochaTimeout = 60 * time.Second

// bochaFreshness 是 recencyFilter → 博查 freshness 枚举的映射（bocha.ts:67-75）。
// 上游对空值/未知值发 noLimit 而不是省略字段，这里照搬：省略时博查的默认行为
// 没有文档承诺，显式发 noLimit 才是确定语义。
var bochaFreshness = map[string]string{
	"day":   "oneDay",
	"week":  "oneWeek",
	"month": "oneMonth",
	"year":  "oneYear",
}

func newBocha() Provider {
	return &bochaProvider{base: base{
		id:       "bocha",
		label:    "博查 Bocha",
		desc:     "中文网页搜索 API（国内可直连），条目自带摘要，适合中文资料检索",
		docURL:   "https://open.bochaai.com/",
		envVar:   "BOCHA_API_KEY",
		needsKey: true,
	}}
}

type bochaProvider struct{ base }

// Search 对齐上游 bocha.ts:152-202：
// POST {base}/v1/web-search，body {query,count,freshness,summary}，
// 响应 {code,msg,data:{webPages:{value:[{name,url,summary,...}]}}}。
//
// 域名过滤只有「本地二次过滤」一条路：博查没有域名参数，上游也没有把 site:
// 拼进查询串（bocha.ts:200 只在结果上 filter）。这里照上游，不额外改写查询——
// 给一个不保证支持 Google 语法的中文引擎塞 site: 只会换来更差的结果集。
func (p *bochaProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, bochaDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	freshness := "noLimit"
	if f, ok := bochaFreshness[opts.RecencyFilter]; ok {
		freshness = f
	}
	body := map[string]any{
		"query":     query,
		"count":     num,
		"freshness": freshness,
		// summary=true 才回摘要字段——snippet 的唯一来源，关了等于每次只拿到标题。
		"summary": true,
	}

	req, err := newJSONRequest(ctx, http.MethodPost, base+"/v1/web-search", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	// 信封先解成 map 再逐层校验：博查的失败是「200 + code≠200」，
	// 用定长 struct 接会把这个错误信封静默解成空结果。
	var env map[string]any
	if err := requestJSON(WithRequestTimeout(ctx, bochaTimeout), p.id, ch.APIKey, req, &env); err != nil {
		return Response{}, err
	}
	items, err := bochaWebPages(env)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	for _, item := range items {
		u := bochaFirstString(item["url"], item["link"], item["href"])
		if u == "" || !MatchesDomainFilter(u, include, exclude) {
			continue
		}
		title := bochaFirstString(item["title"], item["name"])
		if title == "" {
			title = u
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   title,
			URL:     u,
			Snippet: bochaFirstString(item["summary"], item["snippet"], item["description"], item["content"]),
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// 纯 SERP 渠道：博查回的 summary 是网页摘要、不是引擎答案，
	// 所以答案用结果合成（上游 bocha.ts:201 同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// bochaWebPages 从响应信封里取出 data.webPages.value 数组。
//
// 结构不符时报错而不是回空结果：「渠道改了响应结构」与「确实没搜到」必须区分——
// 前者要降级换渠道，后者是正常结果（上游 parseSearchResponse 同样硬校验，
// bocha.ts:121-146）。这是本包各适配器一致的纪律（见 DuckDuckGo 的解析测试）。
func bochaWebPages(env map[string]any) ([]map[string]any, error) {
	if code, present := env["code"]; present {
		n, ok := bochaEnvelopeCode(code)
		if !ok || n != 200 {
			msg := "unknown error"
			if s, ok := env["msg"].(string); ok && strings.TrimSpace(s) != "" {
				msg = strings.TrimSpace(s)
			}
			return nil, fmt.Errorf("响应信封报错 code=%v: %s", code, msg)
		}
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("响应缺少 data 对象")
	}
	pages, ok := data["webPages"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("响应缺少 data.webPages 对象")
	}
	raw, ok := pages["value"].([]any)
	if !ok {
		return nil, fmt.Errorf("响应缺少 data.webPages.value 数组")
	}
	// 非对象条目跳过而不是整次失败：单条脏数据不该丢掉一整页结果
	// （上游同款 continue，bocha.ts:137-138）。
	items := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items, nil
}

// bochaEnvelopeCode 把信封 code 归一成数字：JSON 数字与数字字符串都认
// （上游用 Number(envelope.code)，字符串 "200" 同样通过）。
func bochaEnvelopeCode(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// bochaFirstString 返回第一个非空字符串。博查同一语义的字段名有多个变体
// （title/name、summary/snippet/description/content），按优先级取第一个有值的
// ——上游 firstString 同款（bocha.ts:114-119）。
func bochaFirstString(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok {
			if t := strings.TrimSpace(s); t != "" {
				return t
			}
		}
	}
	return ""
}
