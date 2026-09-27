package websearch

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// searchInfinityDefaultBase 是 Searchinfinity（BytePlus，豆包搜索海外版）的默认根。
// 注意端点带路径前缀 /search_api/web_search，所以根只到 host（对齐上游
// SEARCHINFINITY_SEARCH_URL 的 origin）。
const searchInfinityDefaultBase = "https://torchlight.byteintlapi.com"

// searchInfinityTimeout 对齐上游 SEARCH_TIMEOUT_MS（30s）。
// 渠道文档写明 API Key 请求服务端 30 秒超时——客户端给同一档，
// 让我们先于服务端超时收尾，拿到的是可诊断的本地错误而不是空响应。
const searchInfinityTimeout = 30 * time.Second

// searchInfinityMaxDomains 是上游对 include / block 域名各自的硬上限
// （searchinfinity.ts:130 `target.length < 5`）：超出部分静默丢弃，不报错。
// 渠道侧的契约就是 5 个，多发只会让请求被拒。
const searchInfinityMaxDomains = 5

// searchInfinityTimeRange 把 recencyFilter 映射成渠道的 TimeRange 枚举
// （上游 mapRecencyFilter）。
var searchInfinityTimeRange = map[string]string{
	"day":   "OneDay",
	"week":  "OneWeek",
	"month": "OneMonth",
	"year":  "OneYear",
}

func newSearchInfinity() Provider {
	return &searchInfinityProvider{base: base{
		id:       "searchinfinity",
		label:    "Searchinfinity",
		desc:     "BytePlus 网页搜索（豆包搜索海外版），结果自带模型摘要，站点过滤上限 5 个",
		docURL:   "https://console.byteplus.com/search-infinity/api-key",
		envVar:   "SEARCHINFINITY_API_KEY",
		needsKey: true,
	}}
}

type searchInfinityProvider struct{ base }

// Search 对齐上游 searchinfinity.ts:115-244。
//
// 请求：POST {base}/search_api/web_search，Authorization: Bearer，body
// {Query, Count, Filter?{Sites, BlockHosts}, TimeRange?}（域名用 "|" 连接）；
// 响应：{ResponseMetadata{Error?}, Result{WebResults:[{Title, Url, Snippet, Summary}]}}。
//
// 该渠道用 HTTP 200 + 业务错误码表达失败，所以除 requestJSON 的状态码分类外
// 还要单独认一遍 ResponseMetadata.Error（见 searchInfinityBusinessStatus）。
func (p *searchInfinityProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = searchInfinityDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{
		"Query": query,
		"Count": num,
	}
	filter := map[string]any{}
	if len(include) > 0 {
		filter["Sites"] = strings.Join(searchInfinityCapDomains(include), "|")
	}
	if len(exclude) > 0 {
		filter["BlockHosts"] = strings.Join(searchInfinityCapDomains(exclude), "|")
	}
	if len(filter) > 0 {
		body["Filter"] = filter
	}
	if tr, ok := searchInfinityTimeRange[opts.RecencyFilter]; ok {
		body["TimeRange"] = tr
	}

	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search_api/web_search", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		ResponseMetadata struct {
			Error *struct {
				// 指针是为了区分「没这个字段」与「值为 0」——上游按
				// typeof === "number" 决定错误文案里带不带数字码。
				CodeN   *float64 `json:"CodeN"`
				Code    string   `json:"Code"`
				Message string   `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Result *struct {
			WebResults *[]struct {
				Title   string `json:"Title"`
				URL     string `json:"Url"`
				Snippet string `json:"Snippet"`
				Summary string `json:"Summary"`
			} `json:"WebResults"`
		} `json:"Result"`
	}
	if err := requestJSON(WithRequestTimeout(ctx, searchInfinityTimeout), p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}

	if be := raw.ResponseMetadata.Error; be != nil && (be.Code != "" || be.Message != "") {
		return Response{}, searchInfinityBusinessError(p.id, ch.APIKey, be.CodeN, be.Code, be.Message)
	}
	if raw.Result == nil || raw.Result.WebResults == nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
			"响应缺少 Result.WebResults 数组（渠道结构可能已变更）", ch.APIKey, nil)
	}

	out := Response{}
	for _, item := range *raw.Result.WebResults {
		url := strings.TrimSpace(item.URL)
		if url == "" {
			continue
		}
		// 优先用模型生成的摘要（Summary），它比原始 Snippet 更适合直接进上下文；
		// 没有则回落 Snippet（上游 `summary || snippet`）。
		snippet := CollapseSpaces(item.Summary)
		if snippet == "" {
			snippet = CollapseSpaces(item.Snippet)
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   item.Title,
			URL:     url,
			Snippet: snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// 该渠道不返回独立的答案字段，用结果合成（上游 formatSearchResultsAsAnswer）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// searchInfinityBusinessError 把业务错误码翻成本包的分类错误。
//
// 关键点：状态码先经 searchInfinityBusinessStatus 映射成 HTTP 语义，再交给
// ClassifyStatus——降级判据只有那一张表，这里不另立一套 kind 判定。
// 映射不出状态时归 KindUnknown（对应上游的 "unknown"，不触发降级）：
// 看不懂的错误码不该让降级链把它当成「换个渠道就好」。
func searchInfinityBusinessError(provider, key string, codeN *float64, code, message string) *ProviderError {
	if message == "" {
		message = "unknown error"
	}
	if code == "" {
		code = "unknown"
	}
	status := searchInfinityBusinessStatus(codeN, code, message)
	kind := KindUnknown
	if status > 0 {
		kind = ClassifyStatus(status)
	}
	label := code
	if codeN != nil {
		label = strconv.Itoa(int(*codeN)) + " " + code
	}
	return NewProviderError(provider, kind, status,
		"渠道业务错误: "+message+"（code "+label+"）", key, nil)
}

// searchInfinityBusinessStatus 把渠道业务码映射成最接近的 HTTP 语义
// （上游 businessErrorStatus 的逐条照搬）。返回 0 = 无法判定。
func searchInfinityBusinessStatus(codeN *float64, code, message string) int {
	n := 0.0
	if codeN != nil {
		n = *codeN
	}
	switch {
	case n == 700901 || code == "invalid_api_key":
		return 401
	case n == 700429 || code == "700429":
		return 429
	case n == 10400 || code == "10400":
		return 400
	case n == 10500 || code == "10500":
		return 500
	case n == 10403 || code == "10403":
		// 10403 既可能是「无权限」也可能是「额度耗尽」，只有文案能区分
		// （上游 /quota|exhaust/i 同款）。
		lower := strings.ToLower(message)
		if strings.Contains(lower, "quota") || strings.Contains(lower, "exhaust") {
			return 429
		}
		return 403
	}
	return 0
}

// searchInfinityCapDomains 截到渠道上限（保序，多余丢弃）。
func searchInfinityCapDomains(domains []string) []string {
	if len(domains) <= searchInfinityMaxDomains {
		return domains
	}
	return domains[:searchInfinityMaxDomains]
}
