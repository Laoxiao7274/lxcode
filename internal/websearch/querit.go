package websearch

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// queritDefaultBase 是 Querit 的默认 API 根。
const queritDefaultBase = "https://api.querit.ai"

// queritTimeout 对齐上游 SEARCH_TIMEOUT_MS（60s）。
const queritTimeout = 60 * time.Second

// queritTimeRange 把 recencyFilter 映射成渠道 filters.timeRange.date 的取值
// （上游 mapRecencyFilter）。
var queritTimeRange = map[string]string{
	"day":   "d1",
	"week":  "w1",
	"month": "m1",
	"year":  "y1",
}

func newQuerit() Provider {
	return &queritProvider{base: base{
		id:       "querit",
		label:    "Querit",
		desc:     "面向 AI 应用的搜索 API，站点过滤与时间范围都有原生参数",
		docURL:   "https://www.querit.ai/en/dashboard/api-keys",
		envVar:   "QUERIT_API_KEY",
		needsKey: true,
	}}
}

type queritProvider struct{ base }

// Search 对齐上游 querit.ts:129-361（只移植 Search 一路：Contents 是抓取能力，
// 与本包的「搜索渠道」职责不同类）。
//
// 请求：POST {base}/v1/search，Authorization: Bearer + Accept: application/json，body
// {query, count, filters?{sites?{include,exclude}, timeRange?{date}}}；
// 响应：{error_code, error_msg, results:{result:[{url,title,snippet}]}}。
//
// 与 Searchinfinity 同类：HTTP 200 也可能带业务错误码，需单独认 error_code。
func (p *queritProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, queritDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{
		"query": query,
		"count": num,
	}
	filters := map[string]any{}
	sites := map[string]any{}
	if len(include) > 0 {
		sites["include"] = include
	}
	if len(exclude) > 0 {
		sites["exclude"] = exclude
	}
	if len(sites) > 0 {
		filters["sites"] = sites
	}
	if d, ok := queritTimeRange[opts.RecencyFilter]; ok {
		filters["timeRange"] = map[string]any{"date": d}
	}
	if len(filters) > 0 {
		body["filters"] = filters
	}

	headers := map[string]string{
		"Authorization": "Bearer " + ch.APIKey,
		"Accept":        "application/json",
	}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/v1/search", headers, body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		// error_code 可能是数字（200）也可能是字符串（"200"），上游用 Number()
		// 统一成数字再判 —— 这里用 any + queritErrorCode 复刻同一语义。
		ErrorCode any    `json:"error_code"`
		ErrorMsg  string `json:"error_msg"`
		Results   *struct {
			Result *[]struct {
				URL     string `json:"url"`
				Title   string `json:"title"`
				Snippet string `json:"snippet"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := requestJSON(WithRequestTimeout(ctx, queritTimeout), p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}

	// 成功判据是「error_code 数值上等于 200」：缺字段或非法值都算渠道侧失败
	// （上游 assertApiSuccess 同款）。
	code, ok := queritErrorCode(raw.ErrorCode)
	if !ok || code != 200 {
		status := 0
		if ok && code >= 100 && code <= 599 {
			// 码值本身就是 HTTP 状态码时按它分类，好让降级判据（ClassifyStatus）
			// 继续有效；其它码值归 KindUnknown——看不懂的错不该触发降级。
			status = code
		}
		kind := KindUnknown
		if status > 0 {
			kind = ClassifyStatus(status)
		}
		rendered := "unknown"
		if ok {
			rendered = strconv.Itoa(code)
		}
		msg := "渠道业务错误: " + rendered
		if trimmed := strings.TrimSpace(raw.ErrorMsg); trimmed != "" {
			msg += "（" + trimmed + "）"
		}
		return Response{}, NewProviderError(p.id, kind, status, msg, ch.APIKey, nil)
	}
	if raw.Results == nil || raw.Results.Result == nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
			"响应缺少 results.result 数组（渠道结构可能已变更）", ch.APIKey, nil)
	}

	out := Response{}
	for _, item := range *raw.Results.Result {
		url := strings.TrimSpace(item.URL)
		if url == "" {
			continue
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   item.Title,
			URL:     url,
			Snippet: item.Snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// 该渠道不返回独立的答案字段，用结果合成（上游 formatSearchResultsAsAnswer）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// queritErrorCode 复刻上游 `Number(data.error_code)`：数字与数字字符串都接受，
// 其余（缺字段 / 非数字串 / null）一律判为不可用。
func queritErrorCode(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
