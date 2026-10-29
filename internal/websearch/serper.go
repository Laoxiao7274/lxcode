package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// serperDefaultBase 是 Serper.dev 的默认 API 根（可被用户配置的 base_url 覆盖）。
// 端点为 {base}/search（上游 serper.ts:10 的 SERPER_SEARCH_URL）。
const serperDefaultBase = "https://google.serper.dev"

// —— 本批次（SERP 系五家：serper/serpapi/serpbase/serply/serpdive）共享的私有助手 ——
//
// 五家都是 Google SERP 转售，请求与响应形状高度同构，上游各自的 .ts 里也逐字
// 重复了同一段映射逻辑。共享部分集中在这里定义一份（刻意不放进 util.go：
// 这些是 SERP 系特有的，不是全包通用语义）。

// serpFamilyTBS 是 recencyFilter → Google tbs 时间过滤参数的映射。
//
// serper / serpapi / serpbase / serply 四家的 RECENCY_TBS 逐字相同，所以只留
// 一份——四份拷贝将来必然改三漏一。serpdive 没有原生时间参数，走查询串提示
// （见 serpdive.go 的 serpdiveRecencyHints）。
var serpFamilyTBS = map[string]string{
	"day":   "qdr:d",
	"week":  "qdr:w",
	"month": "qdr:m",
	"year":  "qdr:y",
}

// serpFamilyRequestCount 计算实际请求条数。
//
// 有域名过滤时要多取几条（上游 serper.ts:135 / serpapi.ts:136 / serply.ts:131
// 同款）：过滤是本地二次判定，site: 子句的命中率各家不同，只取 num 条常常
// 过滤后不足。判定用**原始** domainFilter 长度而不是解析后的 include/exclude
// ——与上游逐字一致（上游看的就是 options.domainFilter?.length）。
func serpFamilyRequestCount(num int, domainFilter []string) int {
	if len(domainFilter) == 0 {
		return num
	}
	if num+5 > MaxNumResults {
		return MaxNumResults
	}
	return num + 5
}

// serpFamilyEnvelopeErrorText 解出信封里的 error 字段文本。
//
// 用 json.RawMessage 而不是 string 是刻意的：上游的判定是 typeof === "string"
// （serpapi.ts:123、serpbase.ts:143），即「error 不是字符串就当它不存在，
// 继续看结果数组」。声明成 string 的话，某家某天把 error 改成对象形态会让
// 整个响应解码失败——把一个字段的形态变化放大成整轮搜索失败。
// 返回已 TrimSpace 的非空字符串；字段缺席或不是字符串时返回空串。
func serpFamilyEnvelopeErrorText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func newSerper() Provider {
	return &serperProvider{base: base{
		id:       "serper",
		label:    "Serper.dev",
		desc:     "Google SERP 转售，结构化结果快而全；按次计费，适合要 Google 结果的场景",
		docURL:   "https://serper.dev",
		envVar:   "SERPER_API_KEY",
		needsKey: true,
	}}
}

type serperProvider struct{ base }

// Search 对齐上游 serper.ts:131-188：
// POST {base}/search，头 X-API-KEY，body {q,num,tbs?}，
// 响应 {organic:[{title,link,snippet}]}。
//
// 域名过滤：Serper 没有原生域名参数，site: 子句拼进 q 之后**仍要**本地
// 二次过滤（上游 serper.ts:179 同款）——搜索引擎对 site:/OR 的解析不可依赖。
func (p *serperProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, serperDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{
		"q":   BuildSiteQuery(query, include, exclude),
		"num": serpFamilyRequestCount(num, opts.DomainFilter),
	}
	if tbs, ok := serpFamilyTBS[opts.RecencyFilter]; ok {
		body["tbs"] = tbs
	}

	headers := map[string]string{
		"X-API-KEY": ch.APIKey,
		"Accept":    "application/json",
	}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search", headers, body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	// 指针切片用来区分「organic 字段缺席」与「organic 是空数组」：前者上游判为
	// 无效响应（serper.ts:123），后者是合法的「没搜到」。
	var raw struct {
		Organic *[]struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := requestJSON(ctx, p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}
	if raw.Organic == nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
			"响应缺少 organic 数组（信封形状不符）", ch.APIKey, nil)
	}

	out := Response{}
	for _, item := range *raw.Organic {
		if item.Link == "" || !MatchesDomainFilter(item.Link, include, exclude) {
			continue
		}
		// 标题先 TrimSpace 再交给 sourceTitle：上游是 `entry.title.trim() ? ... : Source N`
		// （serper.ts:181），空白标题也算「没有标题」。
		out.Results = append(out.Results, fillResult(Result{
			Title:   sourceTitle(strings.TrimSpace(item.Title), len(out.Results)+1),
			URL:     item.Link,
			Snippet: item.Snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// 纯 SERP 渠道不产出摘要答案，用结果合成（上游 formatSearchResultsAsAnswer）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}
