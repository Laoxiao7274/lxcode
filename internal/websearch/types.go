// Package websearch 实现网页搜索渠道：渠道注册表（一渠道一适配器）、
// 主渠道优先的降级链、凭据解析与 config/search.json 持久化。
//
// 设计参考 pi-web-access（MIT，https://github.com/nicobailon/pi-web-access）：
// 统一契约 SearchResult/SearchResponse/SearchOptions 出自其 perplexity.ts:17-34，
// 错误类型驱动的降级语义出自其 gemini-search.ts:54-71。移植保留其两条硬纪律：
//
//  1. 降级只看错误类型（ProviderError.Kind），不做字符串匹配——与 agent 包
//     「哨兵错误供上层结构化判断」同一条纪律；
//  2. 凭证错/参数错不触发降级（见 FallbackKinds）——否则「配置错了」会被
//     下一个渠道的偶然成功掩盖成「搜索正常」，用户永远查不出问题在哪。
package websearch

import "strings"

// Result 是一条搜索结果（对齐上游 SearchResult）。
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Response 是一次搜索的结果集（对齐上游 SearchResponse）。
// Answer 是渠道自带的摘要答案：有的渠道返回（Tavily/Perplexity 等），
// 有的不返回（纯 SERP 类）——空串表示该渠道没有答案，不是错误。
type Response struct {
	Provider string   `json:"provider"`
	Query    string   `json:"query,omitempty"`
	Answer   string   `json:"answer,omitempty"`
	Results  []Result `json:"results"`
}

// Options 是一次搜索的选项（对齐上游 SearchOptions）。
type Options struct {
	// NumResults 期望条数；0 取 DefaultNumResults，超 MaxNumResults 截断。
	NumResults int
	// RecencyFilter 时间范围：day/week/month/year（空 = 不限）。
	RecencyFilter string
	// DomainFilter 限定域名；前缀 "-" 表示排除。
	DomainFilter []string
}

// 默认与上限对齐上游（numResults 默认 5、上限 20）。
const (
	DefaultNumResults = 5
	MaxNumResults     = 20
)

// NormalizeNumResults 归一化条数：0 → 默认，超上限截断。
func NormalizeNumResults(n int) int {
	if n <= 0 {
		return DefaultNumResults
	}
	if n > MaxNumResults {
		return MaxNumResults
	}
	return n
}

// ValidRecency 报告 recencyFilter 是否合法（空串合法 = 不限）。
func ValidRecency(s string) bool {
	switch s {
	case "", "day", "week", "month", "year":
		return true
	}
	return false
}

// DomainFilterParts 把域名过滤拆成 include / exclude 两组。
// 上游语义（tavily.ts:100-113 同款）：前缀 "-" 表示排除、空项丢弃、
// 去掉 www. 前缀、各自去重保序。返回的两个切片都非 nil 才算有过滤。
func DomainFilterParts(filter []string) (include, exclude []string) {
	seen := make(map[string]bool, len(filter))
	for _, raw := range filter {
		d := strings.ToLower(strings.TrimSpace(raw))
		if d == "" {
			continue
		}
		excludeIt := strings.HasPrefix(d, "-")
		d = strings.TrimPrefix(strings.TrimPrefix(d, "-"), "www.")
		if d == "" {
			continue
		}
		// 同域名同时出现在 include 与 exclude 时按先出现者为准：
		// 两处都发会让渠道自己决定谁赢，结果不可预期。
		if seen[d] {
			continue
		}
		seen[d] = true
		if excludeIt {
			exclude = append(exclude, d)
		} else {
			include = append(include, d)
		}
	}
	return include, exclude
}
