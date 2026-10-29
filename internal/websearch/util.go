package websearch

import (
	"net/url"
	"strconv"
	"strings"
)

// CollapseSpaces 把连续空白折叠成单个空格并去首尾空白。
// 上游各适配器对 snippet 统一做 `.replace(/\s+/g, " ").trim()`
// （tavily.ts:132 等）——不折叠的话，HTML 里的换行与缩进会原样进上下文，
// 白白吃掉 token 且让结果难读。
func CollapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// HostMatchesDomain 报告 host 是否属于 domain（含子域）。
// 语义对齐上游 hostMatchesDomain：`example.com` 匹配 `example.com` 与
// `a.example.com`，但不匹配 `notexample.com`。
func HostMatchesDomain(host, domain string) bool {
	host = strings.ToLower(host)
	domain = strings.ToLower(strings.TrimPrefix(domain, "www."))
	if host == domain {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}

// MatchesDomainFilter 报告 url 是否通过域名过滤。
// 无过滤时恒 true；URL 解析失败视为不通过（对齐上游：无法判定的不该放行）。
func MatchesDomainFilter(rawURL string, include, exclude []string) bool {
	if len(include) == 0 && len(exclude) == 0 {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	host := u.Hostname()
	if len(include) > 0 {
		ok := false
		for _, d := range include {
			if HostMatchesDomain(host, d) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, d := range exclude {
		if HostMatchesDomain(host, d) {
			return false
		}
	}
	return true
}

// BuildSiteQuery 把域名过滤拼进查询串（Brave / SearXNG 这类没有
// 原生域名参数的渠道用）。
//
// 上游语义（searxng.ts:137-146、brave.ts:75）：多个 include 用 ` OR ` 连接，
// exclude 逐个加 `-site:`。注意这是「尽力而为」——搜索引擎对 OR 的解析
// 各家不同，所以调用方仍需对结果做 MatchesDomainFilter 二次过滤。
func BuildSiteQuery(query string, include, exclude []string) string {
	parts := []string{query}
	switch len(include) {
	case 0:
	case 1:
		parts = append(parts, "site:"+include[0])
	default:
		sites := make([]string, 0, len(include))
		for _, d := range include {
			sites = append(sites, "site:"+d)
		}
		parts = append(parts, strings.Join(sites, " OR "))
	}
	for _, d := range exclude {
		parts = append(parts, "-site:"+d)
	}
	return strings.Join(parts, " ")
}

// SourceAnswer 把结果列表合成为一段「答案」文本。
//
// 纯 SERP 类渠道（Brave/SearXNG/Serper…）自己不产出摘要答案，上游就把
// 结果拼成 `snippet\nSource: title (url)` 的块——这样模型只读 answer 也能
// 拿到带出处的内容，不必再解析 results 数组。这是上游各适配器里
// 逐字重复的同一段逻辑（brave.ts:191-196、searxng.ts:225-227），抽一处。
func SourceAnswer(results []Result) string {
	blocks := make([]string, 0, len(results))
	for _, r := range results {
		if r.Snippet != "" {
			blocks = append(blocks, r.Snippet+"\nSource: "+r.Title+" ("+r.URL+")")
		} else {
			blocks = append(blocks, "Source: "+r.Title+" ("+r.URL+")")
		}
	}
	return strings.Join(blocks, "\n\n")
}

// sourceTitle 返回条目标题，空则回落 "Source N"。
//
// 与 fillResult 的 URL 回落刻意分开：上游两类渠道语义不同——
// tavily/jina/exa 用 "Source N"（tavily.ts:130），brave/searxng 用 URL
// （brave.ts:184）。统一成一种会让另一半的结果标题变得莫名其妙。
func sourceTitle(title string, index int) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	return "Source " + strconv.Itoa(index)
}

// fillResult 归一化一条结果（标题回落 URL、snippet 折叠空白）。
// 上游每个适配器的映射循环里都写着同一段回落逻辑。
func fillResult(r Result) Result {
	if r.Title == "" {
		r.Title = r.URL
	}
	r.Snippet = CollapseSpaces(r.Snippet)
	return r
}
