package websearch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// xcrawlDefaultBase 是 XCrawl SERP API 的默认根（可被 base_url 覆盖）。
const xcrawlDefaultBase = "https://run.xcrawl.com"

// xcrawlTimeout 比包默认长：SERP 抓取通常几秒，但上游刻意留了 60s 余量
// 再判失败（xcrawl.ts:12-14 的注释）。
const xcrawlTimeout = 60 * time.Second

func newXcrawl() Provider {
	return &xcrawlProvider{base: base{
		id:       "xcrawl",
		label:    "XCrawl",
		desc:     "Google SERP 抓取 API（engine=google_search），返回自然结果列表",
		docURL:   "https://dash.xcrawl.com/",
		envVar:   "XCRAWL_API_KEY",
		needsKey: true,
	}}
}

type xcrawlProvider struct{ base }

// Search 对齐上游 xcrawl.ts:159-236：
// POST {base}/v1/serp，body {engine:"google_search", q}，
// 响应 {search_metadata:{status}, organic_results:[{title,link,snippet}]}。
//
// 请求体只有 engine 与 q 两项：XCrawl 的 SERP 接口没有域名/时间参数
// （上游注释明说「has no server-side domain filter」，xcrawl.ts:97-98），
// 域名过滤只能在结果上做——所以这里不把 site: 拼进查询串。
func (p *xcrawlProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = xcrawlDefaultBase
	}
	endpoint := base + "/v1/serp"
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{"engine": "google_search", "q": query}
	req, err := newJSONRequest(ctx, http.MethodPost, endpoint, bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var env map[string]any
	if err := requestJSON(WithRequestTimeout(ctx, xcrawlTimeout), p.id, ch.APIKey, req, &env); err != nil {
		return Response{}, err
	}
	results, err := xcrawlParseResults(env, endpoint)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	for _, r := range results {
		if !MatchesDomainFilter(r.URL, include, exclude) {
			continue
		}
		out.Results = append(out.Results, fillResult(r))
		if len(out.Results) >= num {
			break
		}
	}
	// 纯 SERP 渠道：XCrawl 不产出摘要答案，用结果合成（上游 xcrawl.ts:234 同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// xcrawlParseResults 校验并映射 organic_results。
//
// 上游对每一条都是硬校验（xcrawl.ts:119-157）：link 必须是非空字符串、
// title/snippet 只能是字符串或 null。照搬的理由与其它渠道一致——
// 「结构变了」要降级换渠道，不能静默变成「没搜到」。
func xcrawlParseResults(env map[string]any, endpoint string) ([]Result, error) {
	meta, ok := env["search_metadata"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("响应缺少 search_metadata 对象")
	}
	// status 缺席时放行（上游 status !== undefined 才判），
	// 但出现且不是 completed 就是任务没跑完——结果不完整，不能当成功。
	if status, present := meta["status"]; present && status != "completed" {
		return nil, fmt.Errorf("search_metadata.status = %v，期望 completed", status)
	}
	raw, ok := env["organic_results"].([]any)
	if !ok {
		return nil, fmt.Errorf("响应缺少 organic_results 数组")
	}

	results := make([]Result, 0, len(raw))
	for i, it := range raw {
		entry, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("organic_results[%d] 不是对象", i)
		}
		link, ok := entry["link"].(string)
		if !ok || strings.TrimSpace(link) == "" {
			return nil, fmt.Errorf("organic_results[%d].link 不是非空字符串", i)
		}
		if err := xcrawlCheckOptionalString(entry, "title", i); err != nil {
			return nil, err
		}
		if err := xcrawlCheckOptionalString(entry, "snippet", i); err != nil {
			return nil, err
		}

		resolved := xcrawlAbsolutizeLink(strings.TrimSpace(link), endpoint)
		// 标题回落的是 URL（不是 "Source N"）：上游用 resolved 兜底
		// （xcrawl.ts:150），本包 fillResult 的空标题回落恰好同语义。
		title := resolved
		if s, ok := entry["title"].(string); ok && strings.TrimSpace(s) != "" {
			title = s
		}
		snippet := ""
		if s, ok := entry["snippet"].(string); ok {
			snippet = s
		}
		results = append(results, Result{Title: title, URL: resolved, Snippet: snippet})
	}
	return results, nil
}

// xcrawlCheckOptionalString 校验可选字符串字段：允许缺席或 null，出现就必须是字符串。
func xcrawlCheckOptionalString(entry map[string]any, key string, index int) error {
	v, present := entry[key]
	if !present || v == nil {
		return nil
	}
	if _, isStr := v.(string); !isStr {
		return fmt.Errorf("organic_results[%d].%s 不是字符串", index, key)
	}
	return nil
}

// xcrawlAbsolutizeLink 把相对链接解析成绝对地址。
//
// XCrawl 的 Google SERP 结果偶尔回的是相对 API 源的跳转壳（如 "/goto?url=..."），
// 直接当 URL 用会让调用方拿到一个不可访问的相对路径——上游为此专门做了
// 绝对化（xcrawl.ts:88-95），这里以**实际生效的端点**为基（base_url 可覆盖，
// 不能写死上游常量，否则自建/代理地址下会解析到别人的域名）。
func xcrawlAbsolutizeLink(link, endpoint string) string {
	lower := strings.ToLower(link)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return link
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return link
	}
	ref, err := url.Parse(link)
	if err != nil {
		return link
	}
	return base.ResolveReference(ref).String()
}
