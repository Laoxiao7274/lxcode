package websearch

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// valyuDefaultBase 是 Valyu 的默认 API 根。
const valyuDefaultBase = "https://api.valyu.ai/v1"

// valyuTimeout 是上游给 Valyu 的超时（valyu.ts:13 给 60s）。
const valyuTimeout = 60 * time.Second

// Valyu 的正文/摘要截断上限（上游 valyu.ts:14-15）。
// snippet 比 content 更短：snippet 要进结果列表反复出现，content 是单条正文。
const (
	valyuMaxSnippetChars = 2500
	valyuMaxContentChars = 4000
)

func newValyu() Provider {
	return &valyuProvider{base: base{
		id:       "valyu",
		label:    "Valyu",
		desc:     "面向 AI 的搜索 API，支持域名包含/排除与起始日期过滤",
		docURL:   "https://platform.valyu.ai",
		envVar:   "VALYU_API_KEY",
		needsKey: true,
	}}
}

type valyuProvider struct{ base }

// valyuRecencyDays 把 recencyFilter 映射成「往前推的天数」。
// Valyu 只接受 start_date 绝对日期（上游 valyu.ts:89-93 同款）。
var valyuRecencyDays = map[string]int{"day": 1, "week": 7, "month": 30, "year": 365}

// valyuDomainPattern 校验归一化后的域名（对齐上游 domain-filter-normalization.ts:13）。
var valyuDomainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$`)

// Search 对齐上游 valyu.ts:119-176：
// POST {base}/search，头 x-api-key，body {query, max_num_results,
// included_sources/excluded_sources, start_date}，
// 响应 {success:true, results:[{title,url,description,content}]}。
//
// 与其它渠道的两处不同：
//   - 有原生域名参数，所以不拼 site: 查询串、也不二次过滤（交给渠道侧执行）；
//   - 结果条目的四个字段都可能缺席或类型不对，上游逐个 text() 归一化
//     （valyu.ts:95-97：折叠空白 + 截断），坏条目跳过而不是整体失败。
func (p *valyuProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = valyuDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := valyuDomainFilter(opts.DomainFilter)

	body := map[string]any{"query": query, "max_num_results": num}
	if len(include) > 0 {
		body["included_sources"] = include
	}
	if len(exclude) > 0 {
		body["excluded_sources"] = exclude
	}
	if days, ok := valyuRecencyDays[opts.RecencyFilter]; ok {
		// Valyu 要的是 YYYY-MM-DD 日期（不是 RFC3339 时间戳）。
		body["start_date"] = time.Now().AddDate(0, 0, -days).UTC().Format("2006-01-02")
	}

	headers := map[string]string{
		"x-api-key": ch.APIKey,
		"Accept":    "application/json",
	}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search", headers, body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var probe any
	if err := requestJSON(WithRequestTimeout(ctx, valyuTimeout), p.id, ch.APIKey, req, &probe); err != nil {
		return Response{}, err
	}
	entries, err := valyuResults(probe)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	for _, entry := range entries {
		url := valyuText(entry["url"], 0)
		if url == "" {
			continue
		}
		title := valyuText(entry["title"], 0)
		content := valyuText(entry["content"], valyuMaxContentChars)
		description := valyuText(entry["description"], valyuMaxSnippetChars)
		snippet := content
		if snippet == "" {
			snippet = description
		}
		out.Results = append(out.Results, fillResult(Result{
			Title:   sourceTitle(title, len(out.Results)+1),
			URL:     url,
			Snippet: valyuTruncate(snippet, valyuMaxSnippetChars),
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// Valyu 不产出独立答案，用结果合成（上游 valyu.ts:175 同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// valyuResults 解出结果条目（只校验信封，条目的坏字段由映射阶段跳过）。
func valyuResults(probe any) ([]map[string]any, error) {
	envelope, ok := probe.(map[string]any)
	if !ok {
		return nil, errors.New("期望对象信封")
	}
	if success, ok := envelope["success"].(bool); !ok || !success {
		return nil, errors.New("期望 success 为 true")
	}
	raw, ok := envelope["results"].([]any)
	if !ok {
		return nil, errors.New("期望 results 数组")
	}
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if item, ok := entry.(map[string]any); ok {
			out = append(out, item)
		}
	}
	return out, nil
}

// valyuText 归一化一个文本字段：非字符串返回空串，字符串折叠空白并截断
// （limit ≤ 0 表示不截断）。对齐上游 text()（valyu.ts:95-97）。
func valyuText(value any, limit int) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	s = CollapseSpaces(s)
	return valyuTruncate(s, limit)
}

// valyuTruncate 按 rune 截断（limit ≤ 0 表示不截断）。
// 按字节切会把中文切成半个字（乱码进上下文），与 exaAnswerText 同款处理。
func valyuTruncate(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

// valyuDomainFilter 归一化域名过滤项并保留 "-" 排除语义。
//
// 刻意不复用 DomainFilterParts：Valyu 的 included_sources/excluded_sources 是
// 两个独立参数，剥掉 "-" 前缀会把排除项变成包含项。归一化规则对齐上游
// normalizeDomain（domain-filter-normalization.ts:1-14）：小写、去掉协议与
// 路径、去掉首尾点，非法域名丢弃（发进去只会让渠道 400）。
func valyuDomainFilter(filter []string) (include, exclude []string) {
	seen := make(map[string]bool, len(filter))
	for _, raw := range filter {
		d := strings.ToLower(strings.TrimSpace(raw))
		excludeIt := strings.HasPrefix(d, "-")
		domain := valyuNormalizeDomain(strings.TrimPrefix(d, "-"))
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		if excludeIt {
			exclude = append(exclude, domain)
		} else {
			include = append(include, domain)
		}
	}
	return include, exclude
}

// valyuNormalizeDomain 从用户输入里抽出域名并校验。
// 接受 "https://a.com/x"、"a.com"、"a.com:8080" 三种形态。
func valyuNormalizeDomain(input string) string {
	s := strings.TrimSpace(strings.ToLower(input))
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	// 去掉路径与端口：host 段 = 第一个 "/" 之前、"?"/"#" 之前。
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(s, ".")
	if !valyuDomainPattern.MatchString(s) {
		return ""
	}
	return s
}
