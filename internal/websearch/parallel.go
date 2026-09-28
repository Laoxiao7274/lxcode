package websearch

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// parallelDefaultBase 是 Parallel 的默认 API 根。
const parallelDefaultBase = "https://api.parallel.ai"

// parallelTimeout 对齐上游 SEARCH_TIMEOUT_MS（60s）。
const parallelTimeout = 60 * time.Second

// parallelSnippetChars 是单条 snippet 的字符上限（上游 mapSearchResults 里
// `.slice(0, 200)`）：Parallel 的 excerpt 是整段正文，不截断会让第一条就把
// 上下文预算吃掉大半。
const parallelSnippetChars = 200

// parallelRecencyDays 把 recencyFilter 映射成「往前推的天数」，再转成
// source_policy.after_date 绝对日期（上游 recencyToAfterDate）。
var parallelRecencyDays = map[string]int{"day": 1, "week": 7, "month": 30, "year": 365}

func newParallel() Provider {
	return &parallelProvider{base: base{
		id:       "parallel",
		label:    "Parallel",
		desc:     "面向 AI 研究场景的搜索 API，返回带正文摘录的结果",
		docURL:   "https://platform.parallel.ai",
		envVar:   "PARALLEL_API_KEY",
		needsKey: true,
	}}
}

type parallelProvider struct{ base }

// Search 对齐上游 parallel.ts:170-314（只移植 Search 一路：Extract 是抓取能力，
// 与本包的「搜索渠道」职责不同类）。
//
// 请求：POST {base}/v1/search，鉴权头 x-api-key，body
// {objective, search_queries:[query], advanced_settings{max_results, source_policy?}}；
// 响应：{results:[{url, title, excerpts:[]}]}。
//
// 该渠道的正文形态是 excerpts 数组而不是单条 snippet，所以答案与 snippet
// 用的是两种取法（上游 buildAnswerFromExcerpts vs mapSearchResults）：
// snippet 只取第一条摘录并截断，答案把所有摘录连起来——两者的信息量刻意不同。
func (p *parallelProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, parallelDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	advanced := map[string]any{"max_results": num}
	policy := map[string]any{}
	if len(include) > 0 {
		policy["include_domains"] = include
	}
	if len(exclude) > 0 {
		policy["exclude_domains"] = exclude
	}
	if days, ok := parallelRecencyDays[opts.RecencyFilter]; ok {
		// 只接受绝对日期，没有 day/week 这类枚举（上游同款）。
		policy["after_date"] = time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	}
	if len(policy) > 0 {
		advanced["source_policy"] = policy
	}
	body := map[string]any{
		"objective": query,
		// objective 之外还要显式给一条检索式：渠道把前者当「要解决什么」，
		// 后者才是真正发给索引的查询（上游同款）。
		"search_queries":    []string{query},
		"advanced_settings": advanced,
	}

	headers := map[string]string{"x-api-key": ch.APIKey}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/v1/search", headers, body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		Results *[]struct {
			URL      string   `json:"url"`
			Title    string   `json:"title"`
			Excerpts []string `json:"excerpts"`
		} `json:"results"`
	}
	if err := requestJSON(WithRequestTimeout(ctx, parallelTimeout), p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}

	out := Response{}
	var answerParts []string
	if raw.Results != nil {
		for i, item := range *raw.Results {
			url := strings.TrimSpace(item.URL)
			if url == "" {
				continue
			}
			excerpts := parallelExcerpts(item.Excerpts)
			// 标题回落 "Source N"（上游同款，不是 URL 回落）。
			title := sourceTitle(item.Title, i+1)
			snippet := ""
			if len(excerpts) > 0 {
				snippet = parallelTruncate(CollapseSpaces(excerpts[0]), parallelSnippetChars)
			}
			out.Results = append(out.Results, fillResult(Result{
				Title:   title,
				URL:     url,
				Snippet: snippet,
			}))
			if len(excerpts) > 0 {
				answerParts = append(answerParts, strings.Join(excerpts, " ")+"\nSource: "+title+" ("+url+")")
			}
			if len(out.Results) >= num {
				break
			}
		}
	}
	out.Answer = strings.Join(answerParts, "\n\n")
	return out, nil
}

// parallelExcerpts 丢掉空摘录（上游 normalizeExcerpts：非空字符串才算数）。
func parallelExcerpts(raw []string) []string {
	var out []string
	for _, e := range raw {
		if strings.TrimSpace(e) != "" {
			out = append(out, e)
		}
	}
	return out
}

// parallelTruncate 按 rune 截断。按字节切会把中文切成半个字，
// 半个字进上下文就是乱码。
func parallelTruncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
