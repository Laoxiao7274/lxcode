package websearch

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// serpdiveDefaultBase 是 SERPdive 的默认 API 根（可被 base_url 覆盖）。
// 端点为 {base}/search（上游 serpdive.ts:10 的 SERPDIVE_API_URL）。
const serpdiveDefaultBase = "https://api.serpdive.com/v1"

// serpdiveTimeout 比包默认（RequestTimeout = 20s）长：krill 档返回的是**网页抽取
// 正文**，慢在渠道侧抓取而不在我们这头（上游给 60s，serpdive.ts:12）。
// 与 exa 带 contents 的搜索同一类，所以照 exa 的先例单独放宽。
const serpdiveTimeout = 60 * time.Second

// serpdiveModel 是固定的检索深度档位。
//
// 上游有 krill/mako/moby 三档（serpdive.ts:21，mako/moby 按次计费），默认 krill
// 是刻意的——"装上一个扩展不该开始替用户花钱"。我们**恒用 krill**：ChannelConfig
// 只有 api_key/base_url/disabled 三个字段，没有承载渠道私有选项的位置，
// 而适配器里读 os.Getenv 会让 25 个渠道各自决定行为（credential.go 的纪律）。
// 后果是 mako/moby 的「API 侧合成答案」在本期不可用，答案一律由结果合成。
const serpdiveModel = "krill"

// serpdiveMaxResults 是请求条数上限。
// 上游把 max_results 压到 10（serpdive.ts:225：它是「上限」不是「下限」，
// 要更多也拿不到更多），照搬。
const serpdiveMaxResults = 10

// serpdiveRecencyHints 是 recencyFilter → 查询串提示语的映射。
//
// SERPdive 没有任何时间范围参数（serpdive.ts:139-149），上游把提示语追加进
// 问题文本，由引擎自己理解——这是「排序偏置」而非过滤，窗口外的结果仍会返回。
var serpdiveRecencyHints = map[string]string{
	"day":   "past 24 hours",
	"week":  "past week",
	"month": "past month",
	"year":  "past year",
}

func newSerpdive() Provider {
	return &serpdiveProvider{base: base{
		id:       "serpdive",
		label:    "SERPdive",
		desc:     "检索 API，默认免费 krill 档返回网页抽取内容（无合成答案，答案由结果合成）",
		docURL:   "https://serpdive.com/dashboard/keys",
		envVar:   "SERPDIVE_API_KEY",
		needsKey: true,
	}}
}

type serpdiveProvider struct{ base }

// Search 对齐上游 serpdive.ts:215-278：
// POST {base}/search，头 Authorization: Bearer，body {query,model,max_results}，
// 响应 {answer,results:[{url,title,content}]}。
func (p *serpdiveProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = serpdiveDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	// 时间范围走查询串提示（上游 applyRecencyHint），不是 API 参数。
	searchQuery := BuildSiteQuery(query, include, exclude)
	if hint, ok := serpdiveRecencyHints[opts.RecencyFilter]; ok {
		searchQuery += " " + hint
	}

	maxResults := num
	if maxResults > serpdiveMaxResults {
		maxResults = serpdiveMaxResults
	}

	body := map[string]any{
		"query":       searchQuery,
		"model":       serpdiveModel,
		"max_results": maxResults,
	}
	// krill 档没有答案合成能力，上游对它连 answer 都不发（serpdive.ts:228）——
	// 发了也被静默忽略，不如不发，省得读代码的人以为答案会回来。

	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		Answer  string `json:"answer"`
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := requestJSON(WithRequestTimeout(ctx, serpdiveTimeout), p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}

	out := Response{}
	for _, item := range raw.Results {
		if item.URL == "" || !MatchesDomainFilter(item.URL, include, exclude) {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   sourceTitle(item.Title, len(out.Results)+1),
			URL:     item.URL,
			Snippet: item.Content,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// 上游 buildAnswer（serpdive.ts:197-205）：有 API 摘要就用，没有就用结果
	// 合成——后者与 SourceAnswer 逐字同款（krill 档永远走这条路）。
	out.Answer = SourceAnswer(out.Results)
	if a := strings.TrimSpace(raw.Answer); a != "" {
		out.Answer = a
	}
	return out, nil
}
