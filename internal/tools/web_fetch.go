package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/websearch"
)

// WebFetchToolName 是网页正文抓取工具名。
//
// 单独导出与 WebSearchToolName 同理：内核判定、提示词表、种子目录三处
// 引用同一个字面量，改名时不会漏改某一处。
const WebFetchToolName = "web_fetch"

// webFetchDef 构造网页正文抓取工具。
//
// 为什么需要它：web_search 只回标题与摘要（渠道不返回全文），模型想读
// 一页文档的正文时没有工具——只能让用户贴，或者凭摘要猜。这是「查资料」
// 这条日常路径上最缺的一环。
//
// 低危 + Mutates=false：抓取是只读操作，strict 只读模式下也必须可用
// （与 web_search 同款判定——把它标成变更类会让只读模式连读文档都做不到）。
func webFetchDef() *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {"type": "string", "description": "要抓取的 http/https 地址（先用 web_search 找到它）"},
			"max_chars": {"type": "integer", "description": "正文返回上限（字符），默认 20000，上限 80000"}
		},
		"required": ["url"]
	}`)
	return &Def{
		Name: WebFetchToolName,
		Description: "抓取一个网页并返回正文文本（HTML 已去标签、脚本与样式）。" +
			"web_search 只给标题与摘要，要看全文时用它——先用 web_search 找到地址，再抓正文。" +
			"只支持 http/https，禁止访问本机与内网地址（环回/私有网段/云元数据端点）。" +
			"正文超上限会被截断（可调 max_chars）；纯 JS 渲染的页面可能抓不到内容。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				URL      string `json:"url"`
				MaxChars int    `json:"max_chars"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if strings.TrimSpace(a.URL) == "" {
				return "", fmt.Errorf("url 不能为空")
			}
			res, err := websearch.Fetch(ctx, a.URL, websearch.FetchOptions{MaxChars: a.MaxChars})
			if err != nil {
				// 错误回填模型而不是中断整轮（与 web_search 同款纪律）：
				// 404/超时/内网拒绝都是「这一页读不到」，模型据此可以换地址再试。
				return "", err
			}
			return formatFetchResult(res), nil
		},
	}
}

// formatFetchResult 把抓取结果渲染成给模型的文本。
//
// 三件事必须显式告知，否则模型会误判：
//  1. **最终地址**——重定向可能换了站点，出处影响可信度判断；
//  2. **正文为空**——「抓到了但没正文」与「抓取失败」是两种结论，
//     前者该换来源（纯 JS 渲染的页面），后者该重试或报告失败；
//  3. **被截断**——不注明的话模型会以为这就是全文，据此下结论。
func formatFetchResult(r websearch.FetchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "地址: %s\n", r.URL)
	if r.Title != "" {
		fmt.Fprintf(&b, "标题: %s\n", r.Title)
	}
	if r.ContentType != "" {
		fmt.Fprintf(&b, "类型: %s\n", r.ContentType)
	}
	body := strings.TrimSpace(r.Text)
	if body == "" {
		b.WriteString("\n（抓到了页面，但没有提取出正文——可能是纯 JS 渲染的页面，" +
			"或内容都在脚本里。换一个来源，或改用 web_search 的摘要。）\n")
		return b.String()
	}
	b.WriteString("\n")
	b.WriteString(body)
	if r.Truncated {
		fmt.Fprintf(&b, "\n\n…（正文超过上限已截断；需要更多内容请提高 max_chars，上限 %d 字符）",
			websearch.FetchMaxCharsLimit)
	}
	return b.String()
}
