package websearch

import (
	"html"
	"regexp"
	"strings"
)

// dropTags 是「内容不是正文、整块丢掉」的元素。
//
// 不剥的后果：一页的 JS 与 CSS 常常比正文长一个量级，模型拿到的是满屏代码。
// title 也丢——它由 HTMLTitle 单独取，留在正文里会重复一次。
//
// **为什么是一个元素一条正则**：Go 的 regexp 是 RE2，**不支持反向引用**
// （`</\1>` 会 panic: invalid escape sequence）——想用一条正则匹配
// 「同名开闭标签」在 Go 里做不到，只能逐个元素写死。
// 这些都是有闭合标签的元素；void 元素（embed/img/br 等）本来就没内容，
// 交给下面的去标签正则即可。
var dropTags = []string{"script", "style", "noscript", "template", "svg", "title", "iframe", "object"}

var (
	dropRes []*regexp.Regexp

	// 块级边界 → 换行。不换行的话整页挤成一行，标题与正文分不开（模型读不出结构）。
	reHTMLBlock = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|dl|dt|dd|tr|td|th|table|h[1-6]|section|article|header|footer|nav|aside|blockquote|pre|hr|form|figure|figcaption)\b[^>]*>`)

	reHTMLTag   = regexp.MustCompile(`(?s)<[^>]*>`)
	reHTMLTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	// 注释整块丢：常见的长注释（许可证、条件注释）会占掉不少输出。
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
)

func init() {
	for _, tag := range dropTags {
		dropRes = append(dropRes, regexp.MustCompile(`(?is)<`+tag+`\b[^>]*>.*?</`+tag+`\s*>`))
	}
}

// HTMLTitle 取 <title> 的文本（取不到返回空）。
func HTMLTitle(page string) string {
	m := reHTMLTitle.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return CollapseSpaces(html.UnescapeString(m[1]))
}

// HTMLToText 把整页 HTML 转成可读文本。
//
// 与 cleanHTMLText（搜索结果摘要用）刻意分开：那个只去标签——摘要片段里没有
// script/style，而整页转换必须先剥掉它们的内容，且块级元素要换行。两者合并成一个
// 「既能转摘要又能转整页」的函数，只会让摘要路径背上不需要的代价。
func HTMLToText(page string) string {
	s := reHTMLComment.ReplaceAllString(page, "\n")
	for _, re := range dropRes {
		s = re.ReplaceAllString(s, "\n")
	}
	s = reHTMLBlock.ReplaceAllString(s, "\n")
	s = reHTMLTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	// 逐行折叠行内空白；连续空行压成一个（保留段落分隔，但不要满屏空行）
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = CollapseSpaces(l)
		if l == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
