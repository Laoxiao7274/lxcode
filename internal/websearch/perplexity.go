package websearch

import (
	"context"
	"net/http"
	"regexp"
	"strings"
)

// perplexityDefaultBase 是 Perplexity 的默认 API 根（可被 base_url 覆盖）。
// 注意它是 chat/completions 而非 /search——Perplexity 的搜索能力长在
// sonar 模型的对话接口上（上游 perplexity.ts:7 同款）。
const perplexityDefaultBase = "https://api.perplexity.ai"

// perplexityModel 是 Perplexity 的搜索模型：sonar 即「带联网检索的对话模型」，
// 答案正文在 choices[0].message.content，引用在顶层 citations。
const perplexityModel = "sonar"

func newPerplexity() Provider {
	return &perplexityProvider{base: base{
		id:       "perplexity",
		label:    "Perplexity",
		desc:     "自带联网检索的对话模型（sonar），一次调用同时给出答案与引用来源",
		docURL:   "https://perplexity.ai/settings/api",
		envVar:   "PERPLEXITY_API_KEY",
		needsKey: true,
	}}
}

type perplexityProvider struct{ base }

// perplexityDomainPattern 是域名过滤项的字面形态校验（上游 perplexity.ts:96 同款）。
// Perplexity 对 search_domain_filter 的取值挑剔，发进一个明显不是域名的串
// 会让整次请求 400——先在本地筛掉，别把用户的输入错误放大成请求失败。
var perplexityDomainPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-_.]*\.[a-zA-Z]{2,}$`)

// perplexityCitationRef 匹配答案正文里的引用角标 [N]。
var perplexityCitationRef = regexp.MustCompile(`\[(\d{1,3})\]`)

// perplexityMaxCitations 是保留引用的硬上限（对齐上游 MAX_CITATIONS）。
const perplexityMaxCitations = 20

// Search 对齐上游 perplexity.ts:120-215：
// POST {base}/chat/completions，body 带 model/messages/max_tokens/
// return_related_questions + 可选 search_recency_filter/search_domain_filter，
// 响应 {choices[{message:{content}}], citations[]}。
//
// 两处与其它渠道不同，都是上游刻意的设计：
//   - 答案即模型输出（不是「结果摘要」），citations 只是它的出处列表；
//   - 引用条数不是简单取前 numResults 条，而是「按答案里实际引用的最大编号
//     对齐」——否则答案里的 [7] 会在结果列表里找不到对应项，模型拿着角标
//     却查不到来源（perplexity.ts:111-118）。
func (p *perplexityProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base, err := p.resolveBase(ch, perplexityDefaultBase)
	if err != nil {
		return Response{}, err
	}
	num := NormalizeNumResults(opts.NumResults)

	body := map[string]any{
		"model":                    perplexityModel,
		"messages":                 []map[string]string{{"role": "user", "content": query}},
		"max_tokens":               1024,
		"return_related_questions": false,
	}
	if opts.RecencyFilter != "" {
		// Perplexity 的取值与我们同名（day/week/month/year），直接透传。
		body["search_recency_filter"] = opts.RecencyFilter
	}
	// Perplexity 有原生域名参数，所以不拼 site: 查询串、也不做二次过滤
	// （上游同款：把过滤完全交给渠道侧执行）。
	if domains := perplexityDomainFilter(opts.DomainFilter); len(domains) > 0 {
		body["search_domain_filter"] = domains
	}

	req, err := newJSONRequest(ctx, http.MethodPost, base+"/chat/completions", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		// citations 的实际形态是字符串数组；但 Perplexity 在部分版本/网关下
		// 会返回对象数组，所以用 any 收下再逐条判形状（上游同款容忍）。
		Citations []any `json:"citations"`
	}
	if err := requestJSON(ctx, p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}

	answer := ""
	if len(raw.Choices) > 0 {
		answer = raw.Choices[0].Message.Content
	}

	out := Response{Answer: answer}
	keep := perplexityCitationsToKeep(answer, len(raw.Citations), num)
	for i := 0; i < keep; i++ {
		title, url := perplexityCitation(raw.Citations[i], i+1)
		if url == "" {
			continue
		}
		// 引用没有 snippet（答案正文已经承担了摘要职责），标题按上游
		// 用 "Source N" 回落。
		out.Results = append(out.Results, fillResult(Result{
			Title: title,
			URL:   url,
		}))
	}
	return out, nil
}

// perplexityDomainFilter 校验并整理域名过滤项，保留 "-" 排除前缀。
//
// 刻意不复用 DomainFilterParts：后者会剥掉 "-" 前缀并去重，而 Perplexity 的
// search_domain_filter 语义是「带 - 前缀 = 排除」，剥掉前缀会把排除项变成包含项
// （过滤方向反过来，且没有任何报错）。
func perplexityDomainFilter(filter []string) []string {
	out := make([]string, 0, len(filter))
	seen := make(map[string]bool, len(filter))
	for _, raw := range filter {
		d := strings.TrimSpace(raw)
		domain := strings.TrimPrefix(d, "-")
		if !perplexityDomainPattern.MatchString(domain) {
			continue
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// perplexityCitationsToKeep 决定保留前多少条引用（上游 perplexity.ts:112-118）。
//
// 保留「到答案里最大引用编号为止」的前缀：答案正文的 [N] 角标是按 citations
// 数组下标编的，只取前 numResults 条会让 [7] 之类的角标指向不存在的来源。
// 上限 20 与 numResults 的钳制保持一致，避免一次回答拖回几十条引用。
func perplexityCitationsToKeep(answer string, available, numResults int) int {
	highestCited := 0
	for _, m := range perplexityCitationRef.FindAllStringSubmatch(answer, -1) {
		n := 0
		for _, c := range m[1] {
			n = n*10 + int(c-'0')
		}
		if n > highestCited {
			highestCited = n
		}
	}
	keep := numResults
	if highestCited > keep {
		keep = highestCited
	}
	if keep > perplexityMaxCitations {
		keep = perplexityMaxCitations
	}
	if keep > available {
		keep = available
	}
	return keep
}

// perplexityCitation 解出一条引用：字符串形态 = 纯 URL；对象形态 = {title,url}。
// 两种都不匹配时返回空 URL，由调用方跳过（上游同款容错）。
func perplexityCitation(item any, index int) (title, url string) {
	switch v := item.(type) {
	case string:
		return sourceTitle("", index), strings.TrimSpace(v)
	case map[string]any:
		u, _ := v["url"].(string)
		t, _ := v["title"].(string)
		return sourceTitle(t, index), strings.TrimSpace(u)
	default:
		return "", ""
	}
}
