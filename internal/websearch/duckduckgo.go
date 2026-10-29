package websearch

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// duckDuckGoHTML 是 DuckDuckGo 的无 JS 端点（返回可直接解析的 HTML）。
const duckDuckGoHTML = "https://html.duckduckgo.com/html/"

// duckDuckGoUA 用浏览器形态的 UA：无 JS 端点对默认 UA 更容易返回
// 反爬页（表现为 200 + 0 条可解析结果，见下面的 invalid-response）。
const duckDuckGoUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

var (
	// 结果容器起点：class="result" 或 class="result <其它>"。
	// 刻意要求 result 后紧跟空白或引号——这样 `result__title` /
	// `result__body` 这类嵌套元素（双下划线）不会被当成块边界。
	reDDGBlock = regexp.MustCompile(`(?i)class="result[\s"]`)

	// 结果标题锚点（属性顺序不定，所以先抓整段属性再取 href）。
	reDDGAnchor  = regexp.MustCompile(`(?is)<a\s+([^>]*result__a[^>]*)>(.*?)</a>`)
	reDDGSnippet = regexp.MustCompile(`(?is)<a\s+[^>]*result__snippet[^>]*>(.*?)</a>`)
	reDDGHref    = regexp.MustCompile(`(?i)href="([^"]*)"`)
	reDDGTags    = regexp.MustCompile(`(?s)<[^>]*>`)
)

func newDuckDuckGo() Provider {
	return &duckDuckGoProvider{base: base{
		id:     "duckduckgo",
		label:  "DuckDuckGo",
		desc:   "免 key 抓取公开搜索页（稳定性低于正式 API，需手动启用）",
		docURL: "https://duckduckgo.com/",
		// 零配置渠道必须由用户显式启用：它靠抓公开页面，稳定性与合规性
		// 都不是「默认就该开」的事（对齐上游 explicit-only 语义）。
		optIn: true,
	}}
}

type duckDuckGoProvider struct{ base }

// Search 对齐上游 duckduckgo.ts:51-98。
//
// 上游用 linkedom 解析 DOM；这里不引入 HTML 解析库（为一个渠道新增依赖
// 不划算），改用「容器分块 + 锚点提取」的正则方案。两者都依赖 DDG 的
// class 命名，脆弱性同级——class 一改两边都得改。
//
// 关键信号：**解析出 0 条结果 = 反爬页/结构变更**，报 invalid-response
// 触发降级（上游同款 duckduckgo.ts:90-92）——不这么判的话，
// 「被墙」会被当成「没搜到」静默返回空结果。
func (p *duckDuckGoProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	// 允许覆盖端点（镜像/代理），默认官方 HTML 端点。
	base := ch.BaseURL
	if base == "" {
		base = duckDuckGoHTML
	}
	u := base + "?q=" + url.QueryEscape(query)
	req, err := newJSONRequest(ctx, http.MethodGet, u, map[string]string{
		"Accept":     "text/html",
		"User-Agent": duckDuckGoUA,
	}, nil)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), "", err)
	}

	body, err := requestText(ctx, p.id, "", req, maxResponseBytes)
	if err != nil {
		return Response{}, err
	}

	blocks := splitDDGBlocks(body)
	parseable := 0
	out := Response{}
	for _, block := range blocks {
		if strings.Contains(block.head, "result--ad") {
			continue
		}
		m := reDDGAnchor.FindStringSubmatch(block.body)
		if m == nil {
			continue
		}
		href := reDDGHref.FindStringSubmatch(m[1])
		if href == nil {
			continue
		}
		resultURL := decodeDDGURL(href[1])
		title := cleanHTMLText(m[2])
		if title == "" || resultURL == "" {
			continue
		}
		parseable++
		if !MatchesDomainFilter(resultURL, include, exclude) {
			continue
		}
		snippet := ""
		if sm := reDDGSnippet.FindStringSubmatch(block.body); sm != nil {
			snippet = cleanHTMLText(sm[1])
		}
		out.Results = append(out.Results, fillResult(Result{Title: title, URL: resultURL, Snippet: snippet}))
		if len(out.Results) >= num {
			break
		}
	}
	if parseable == 0 {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200,
			"未解析出任何结果（可能是反爬页或页面结构变更）", "", nil)
	}
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// ddgBlock 是一个结果容器：head 是容器标签的属性文本（判广告用），
// body 是该容器到下一个容器之间的 HTML（取锚点用）。
type ddgBlock struct {
	head string
	body string
}

// splitDDGBlocks 按结果容器把 HTML 切成块。
// 块边界取 `class="result"` 的出现位置；每块的范围是「本次出现处 →
// 下次出现处」，因此嵌套元素（result__title 等）自然落在所属块内。
func splitDDGBlocks(page string) []ddgBlock {
	idx := reDDGBlock.FindAllStringIndex(page, -1)
	if len(idx) == 0 {
		return nil
	}
	blocks := make([]ddgBlock, 0, len(idx))
	for i, loc := range idx {
		end := len(page)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		// 容器标签的属性文本：从匹配处到本标签的 '>'（判 result--ad 用）。
		head := page[loc[0]:end]
		if gt := strings.IndexByte(head, '>'); gt >= 0 {
			head = head[:gt]
		}
		blocks = append(blocks, ddgBlock{head: head, body: page[loc[0]:end]})
	}
	return blocks
}

// decodeDDGURL 解出 DDG 跳转链接里的真实目标地址。
// DDG 的结果链接形如 /l/?uddg=<encoded>&rut=...；没有 uddg 时按原样用。
func decodeDDGURL(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	dest := u.Query().Get("uddg")
	if dest == "" {
		dest = href
	}
	abs, err := url.Parse(dest)
	if err != nil || (abs.Scheme != "http" && abs.Scheme != "https") || abs.Host == "" {
		return ""
	}
	return abs.String()
}

// cleanHTMLText 去标签 + 解实体 + 折叠空白（等价上游 textContent.trim()）。
func cleanHTMLText(s string) string {
	return CollapseSpaces(html.UnescapeString(reDDGTags.ReplaceAllString(s, " ")))
}
