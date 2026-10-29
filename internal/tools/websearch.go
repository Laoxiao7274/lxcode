package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/websearch"
)

// WebSearchFn 是网页搜索的实现约定：server 装配时把 websearch.Service 接进来。
//
// 与 SessionSearchFn 同款注入——工具本身不持搜索实现（渠道配置由
// server 装配并热加载，注册表是进程级单例，把 Service 挂进注册表会让
// 「配置热加载」与「注册表生命周期」两件事纠缠在一起）。
type WebSearchFn func(ctx context.Context, query string, opts websearch.Options) (websearch.Response, error)

// SetWebSearch 注入网页搜索实现（server 装配时调用）。
func (r *Registry) SetWebSearch(fn WebSearchFn) {
	r.searchMu.Lock()
	r.webSearch = fn
	r.searchMu.Unlock()
}

func (r *Registry) getWebSearch() WebSearchFn {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.webSearch
}

// WebSearchToolName 是网页搜索工具名。
//
// 单独导出的理由与 DispatchToolName 相同：内核判定、提示词表、
// 测试三处引用同一个字面量，改名时不会漏改某一处。
const WebSearchToolName = "web_search"

// webSearchDef 构造网页搜索工具。
//
// 低危 + 不变更外部世界（Mutates=false）：搜索是只读操作，strict
// 只读模式下也必须可用——把它标成变更类会让「只读模式」连查资料都做不到。
func webSearchDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "搜索查询词。用自然语言问题即可，搜索引擎会自行处理；不要塞进 URL 或引号"},
			"num_results": {"type": "integer", "description": "期望结果条数，默认 5，上限 20"},
			"recency": {"type": "string", "description": "只要最近这段时间的结果：day / week / month / year（默认不限）"},
			"domains": {"type": "array", "items": {"type": "string"}, "description": "限定域名（如 example.com）；前缀 - 表示排除（如 -pinterest.com）"}
		},
		"required": ["query"]
	}`)
	return &Def{
		Name: WebSearchToolName,
		Description: "联网搜索网页。返回结果标题、链接与摘要（部分渠道还带一段综合答案）。" +
			"适合查最新信息、文档、报错、API 用法等你不知道或可能过时的事实——" +
			"不要用它搜本仓库的代码（那用 search）。" +
			"多个渠道按主渠道优先自动降级，失败会如实报告是哪个渠道出的错。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Query      string   `json:"query"`
				NumResults int      `json:"num_results"`
				Recency    string   `json:"recency"`
				Domains    []string `json:"domains"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			query := strings.TrimSpace(a.Query)
			if query == "" {
				return "", fmt.Errorf("query 不能为空")
			}
			if !websearch.ValidRecency(a.Recency) {
				return "", fmt.Errorf("recency 只能是 day/week/month/year 之一: %q", a.Recency)
			}
			fn := r.getWebSearch()
			if fn == nil {
				return "", fmt.Errorf("网页搜索未装配（后端未初始化搜索渠道）")
			}
			resp, err := fn(ctx, query, websearch.Options{
				NumResults:    a.NumResults,
				RecencyFilter: a.Recency,
				DomainFilter:  a.Domains,
			})
			if err != nil {
				// 错误回填模型而不是中断整轮：渠道失败是常态（额度/网络），
				// 模型据此可以改写查询或换关键词再试。
				return "", err
			}
			return formatWebSearchResult(resp), nil
		},
	}
}

// formatWebSearchResult 把搜索结果渲染成给模型的文本。
//
// 两件事必须显式告知，否则模型会误判：
//  1. **哪个渠道作答**——降级链可能换了渠道，出处影响可信度判断；
//  2. **结果为空**——「搜到 0 条」和「搜索失败」是两种不同的结论，
//     模型对前者该换关键词、对后者该报告失败。
func formatWebSearchResult(resp websearch.Response) string {
	var b strings.Builder
	fmt.Fprintf(&b, "搜索渠道: %s\n", resp.Provider)
	if resp.Query != "" {
		fmt.Fprintf(&b, "查询: %s\n", resp.Query)
	}
	if len(resp.Results) == 0 {
		b.WriteString("\n没有搜到结果。可以换更具体或更通用的关键词再试，不要反复用同一组关键词。\n")
		return b.String()
	}
	if answer := strings.TrimSpace(resp.Answer); answer != "" {
		b.WriteString("\n综合答案:\n")
		b.WriteString(answer)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n结果（%d 条）:\n", len(resp.Results))
	for i, r := range resp.Results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
	}
	return b.String()
}
