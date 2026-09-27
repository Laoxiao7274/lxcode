package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// exaDefaultBase 是 Exa 的默认 API 根（直连路径，需要 key）。
const exaDefaultBase = "https://api.exa.ai"

// exaMCPDefaultURL 是 Exa 的**免配置 MCP 端点**：不需要 key、不需要注册，
// 有免费额度（上游 exa.ts:9 同款）。注意它与直连 API 是**不同主机**，
// 所以不能用 ch.BaseURL 推导——单列一个设置项（带默认值）。
const exaMCPDefaultURL = "https://mcp.exa.ai/mcp"

// Exa 免配置通道的两个工具（上游 exa.ts:11-12）：
//   - basic 只认 query/numResults（过滤条件要拼进查询文本）；
//   - advanced 认真参数（includeDomains/startPublishedDate/...），返回原始搜索 JSON。
const (
	exaMCPBasicTool    = "web_search_exa"
	exaMCPAdvancedTool = "web_search_advanced_exa"
)

// exaMCPURLOption 是免配置通道的端点（有默认值，通常不必改）。
//
// 做成声明式设置项而不是硬编码常量，有两个理由：
//  1. 透明——这是「没填 key 时查询实际发往哪里」，用户有权看见与改写；
//  2. 可测——适配器测试要把请求指向 httptest 假服务器，而 effectiveChannel
//     只透传**声明过**的键，不声明就没有注入点（包级可变变量是更差的做法）。
var exaMCPURLOption = OptionSpec{
	Key:     "mcp_url",
	Label:   "免配置 MCP 端点",
	Hint:    "留空时用官方端点；只有不填 API Key 时才会走这里（Exa 免费额度，无需注册）。",
	EnvVar:  "EXA_MCP_URL",
	Default: exaMCPDefaultURL,
}

// exaTimeout 比默认长：Exa 带 contents 的搜索本身较慢（上游 exa.ts:102 给 60s）。
const exaTimeout = 60 * time.Second

func newExa() Provider {
	return &exaProvider{base: base{
		id:     "exa",
		label:  "Exa",
		desc:   "面向 AI 的语义搜索，支持按发布时间过滤与正文摘要；不填 API Key 也能用（走免配置通道，有免费额度）",
		docURL: "https://dashboard.exa.ai/api-keys",
		envVar: "EXA_API_KEY",
		// key 不是就绪前提：留空时走免配置 MCP 通道（上游同款「无 key 自动
		// 走 mcp.exa.ai」）。但它**仍然接受** key——有 key 走直连 API，
		// 所以 acceptsKey 必须为真，否则设置面板会把输入框藏掉。
		needsKey:   false,
		acceptsKey: true,
		// 刻意默认就绪：用户不配任何东西，web_search 也开箱可用（用户拍板）。
		// 这个标记是 provider_test 那条「零配置渠道必须 optIn」守卫的显式例外。
		defaultReady: true,
		options:      []OptionSpec{exaMCPURLOption},
	}}
}

type exaProvider struct{ base }

// exaRecencyDays 把 recencyFilter 映射成「往前推的天数」。
// Exa 没有时间范围枚举，只接受 startPublishedDate 绝对时间（上游同款）。
var exaRecencyDays = map[string]int{"day": 1, "week": 7, "month": 30, "year": 365}

// Search 对齐上游 exa.ts：有 key 走直连 API（exa.ts:454-525），
// 没 key 走免配置 MCP 通道（exa.ts:204-291 的 callExaMcp）。
//
// 两条路径的差别不只是地址：直连返回结构化 JSON（results[].highlights），
// 免配置通道返回的是**一段文本**（basic 工具给 Title/URL/Highlights 块，
// advanced 工具给原始搜索 JSON 字符串），所以解析也分两套。
func (p *exaProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return p.searchMCP(ctx, ch, query, opts)
	}
	return p.searchDirect(ctx, ch, query, opts)
}

// searchDirect 是原来的直连 API 路径（需要 key）。
func (p *exaProvider) searchDirect(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	base := ch.BaseURL
	if base == "" {
		base = exaDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{
		"query":      query,
		"type":       "auto",
		"numResults": num,
		// highlights 是 Exa 的摘要形态；答案由 highlights 合成。
		"contents": map[string]any{"highlights": true},
	}
	if len(include) > 0 {
		body["includeDomains"] = include
	}
	if len(exclude) > 0 {
		body["excludeDomains"] = exclude
	}
	if days, ok := exaRecencyDays[opts.RecencyFilter]; ok {
		body["startPublishedDate"] = time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	}

	headers := map[string]string{
		"x-api-key": ch.APIKey,
	}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/search", headers, body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var raw struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Text       string   `json:"text"`
			Highlights []string `json:"highlights"`
		} `json:"results"`
	}
	if err := requestJSON(WithRequestTimeout(ctx, exaTimeout), p.id, ch.APIKey, req, &raw); err != nil {
		return Response{}, err
	}

	out := Response{}
	var answerParts []string
	for i, item := range raw.Results {
		if item.URL == "" {
			continue
		}
		title := sourceTitle(item.Title, i+1)
		out.Results = append(out.Results, fillResult(Result{
			Title: title,
			URL:   item.URL,
			// Exa 的 snippet 语义是 highlights（原文片段），没有独立的
			// 一句话摘要字段——上游把 snippet 留空、把内容放进 answer。
			Snippet: "",
		}))
		if content := exaAnswerText(item.Highlights, item.Text); content != "" {
			answerParts = append(answerParts, content+"\nSource: "+title+" ("+item.URL+")")
		}
		if len(out.Results) >= num {
			break
		}
	}
	out.Answer = strings.Join(answerParts, "\n\n")
	return out, nil
}

// ---- 免配置 MCP 通道 ----

// exaMCPURL 解析免配置端点（设置项 → 默认值；effectiveChannel 已做过
// 环境变量回退，这里只看最终值）。
func exaMCPURL(ch ChannelConfig) string {
	if v := strings.TrimSpace(ch.Options[exaMCPURLOption.Key]); v != "" {
		return v
	}
	return exaMCPDefaultURL
}

// searchMCP 走 Exa 的免配置通道。
//
// 工具选择对齐上游 exa.ts:400-428：有过滤条件（域名/时间）时用 advanced
// 工具——它认真参数；basic 工具只认 query/numResults，过滤条件只能拼进
// 查询文本（`site:example.com` / `past week`），效果差一档。advanced 不可用
// （部分部署没暴露它）则退到 basic 并把条件拼进查询。
func (p *exaProvider) searchMCP(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)
	basicQuery := exaMCPQuery(query, opts)
	basicArgs := map[string]any{"query": basicQuery, "numResults": num}

	filtered := opts.RecencyFilter != "" || len(include) > 0 || len(exclude) > 0
	if !filtered {
		return p.exaMCPResult(ctx, ch, exaMCPBasicTool, basicArgs, num)
	}

	args := map[string]any{
		"query":      query,
		"type":       "auto",
		"numResults": num,
		// 免配置通道要正文才有答案可合成（直连路径靠 highlights）。
		"enableHighlights":  true,
		"textMaxCharacters": 3000,
	}
	if len(include) > 0 {
		args["includeDomains"] = include
	}
	if len(exclude) > 0 {
		args["excludeDomains"] = exclude
	}
	if days, ok := exaRecencyDays[opts.RecencyFilter]; ok {
		args["startPublishedDate"] = time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	}

	resp, err := p.exaMCPResult(ctx, ch, exaMCPAdvancedTool, args, num)
	if err == nil {
		return resp, nil
	}
	// 用户取消不算「这个工具不可用」，直接上抛（否则会再发一次请求）。
	if KindOf(err) == KindAborted {
		return Response{}, err
	}
	// advanced 不可用（部署没暴露它）→ 退到 basic，过滤条件拼进查询文本。
	// 这是**降级到同渠道的另一条路**，不是换渠道，所以不违反「配置错误不降级」。
	return p.exaMCPResult(ctx, ch, exaMCPBasicTool, basicArgs, num)
}

// exaMCPResult 调一次免配置工具并把文本结果解析成 Response。
func (p *exaProvider) exaMCPResult(ctx context.Context, ch ChannelConfig, tool string, args map[string]any, num int) (Response, error) {
	text, err := p.exaMCPCall(ctx, ch, tool, args)
	if err != nil {
		return Response{}, err
	}
	return exaParseMCPText(p.id, text, num, ch.APIKey)
}

// exaMCPCall 发起一次免配置通道的 tools/call。
//
// 该端点是无状态 JSON-RPC over HTTP：**不需要 initialize 握手**（实测裸
// tools/list 也返回 200），响应可能是纯 JSON 或 SSE（`data:` 行）。
func (p *exaProvider) exaMCPCall(ctx context.Context, ch ChannelConfig, tool string, args map[string]any) (string, error) {
	url := exaMCPURL(ch) + "?tools=" + tool
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      tool,
			"arguments": args,
		},
	}
	headers := map[string]string{
		"Accept":       "application/json, text/event-stream",
		"x-exa-source": "lxcode-websearch",
	}
	req, err := newJSONRequest(ctx, http.MethodPost, url, headers, body)
	if err != nil {
		return "", NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}
	raw, err := requestText(WithRequestTimeout(ctx, exaTimeout), p.id, ch.APIKey, req, maxResponseBytes)
	if err != nil {
		// 429 = 免费额度用尽：这是**可降级**的（KindQuota），但必须把
		// 「加 key 可解除限制」告诉用户——否则只会看到一句「额度不足」。
		var pe *ProviderError
		if errors.As(err, &pe) && pe.Status == http.StatusTooManyRequests {
			return "", NewProviderError(p.id, pe.Kind, pe.Status,
				"Exa 免配置额度已用尽（429）。到 "+p.docURL+" 申请 API Key 后填入即可解除限制。", ch.APIKey, nil)
		}
		return "", err
	}
	return exaMCPContent(p.id, raw, ch.APIKey)
}

// exaRPCPayload 是免配置通道的 JSON-RPC 响应载荷（result 与 error 二选一）。
type exaRPCPayload struct {
	Result *struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// exaMCPContent 从 JSON-RPC 响应里取出文本内容。
//
// 两种响应形态都要认（上游 exa.ts:238-267 同款）：
// SSE（`event: message` + `data: {...}` 行）与纯 JSON。
func exaMCPContent(provider, raw, key string) (string, error) {
	var payload exaRPCPayload
	found := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") && !strings.HasPrefix(line, "{") {
			continue
		}
		payloadText := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if !strings.HasPrefix(payloadText, "{") {
			continue
		}
		var candidate exaRPCPayload
		if err := json.Unmarshal([]byte(payloadText), &candidate); err != nil {
			continue
		}
		if candidate.Result == nil && candidate.Error == nil {
			continue
		}
		payload = candidate
		found = true
		break
	}
	if !found {
		return "", NewProviderError(provider, KindInvalidResponse, 0,
			"免配置通道返回了无法解析的响应（既不是 JSON 也不是 SSE）", key, nil)
	}
	if payload.Error != nil {
		return "", NewProviderError(provider, KindInvalidResponse, 0,
			fmt.Sprintf("免配置通道报错 %d: %s", payload.Error.Code, payload.Error.Message), key, nil)
	}
	if payload.Result == nil {
		return "", NewProviderError(provider, KindInvalidResponse, 0, "免配置通道返回空结果", key, nil)
	}
	for _, item := range payload.Result.Content {
		if item.Type != "text" {
			continue
		}
		if payload.Result.IsError {
			return "", NewProviderError(provider, KindInvalidResponse, 0,
				"免配置通道执行失败: "+CollapseSpaces(item.Text), key, nil)
		}
		// 文本为空是**成功**（搜到 0 条），不是错误——报错会让降级链换渠道
		// 重试，把「Exa 没搜到」误报成「Exa 坏了」（§3.2 的空结果语义）。
		return item.Text, nil
	}
	if payload.Result.IsError {
		return "", NewProviderError(provider, KindInvalidResponse, 0, "免配置通道执行失败", key, nil)
	}
	// 一个 text 项都没有才是形态错误（content 缺失或全是非文本类型）。
	return "", NewProviderError(provider, KindInvalidResponse, 0, "免配置通道响应里没有文本内容", key, nil)
}

// exaParseMCPText 把免配置通道的文本结果解析成 Response。
//
// 两种形态（上游 exa.ts:381-397 同款）：advanced 工具返回**原始搜索 JSON**，
// basic 工具返回**格式化文本块**（Title/URL/Published/Author/Highlights）。
// 先试 JSON 再试文本块——JSON 解析失败不代表结果坏，可能本来就是文本块。
func exaParseMCPText(provider, text string, num int, key string) (Response, error) {
	if results := exaParseJSONResults(text); len(results) > 0 {
		return exaBuildResponse(results, num), nil
	}
	if blocks := exaParseTextBlocks(text); len(blocks) > 0 {
		return exaBuildResponse(blocks, num), nil
	}
	// 解析不出条目不是错误：可能确实没搜到（空结果是**成功**，与降级无关）。
	// 但把原文当答案留着，免得模型只看到一句「没有结果」而无从判断。
	return Response{Answer: CollapseSpaces(text)}, nil
}

// exaMCPItem 是解析后的一个结果条目（两种形态归一到这里）。
type exaMCPItem struct {
	Title   string
	URL     string
	Content string
}

// exaParseJSONResults 解析 advanced 工具的原始搜索 JSON。
func exaParseJSONResults(text string) []exaMCPItem {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil
	}
	var raw struct {
		Results []struct {
			Title      string   `json:"title"`
			URL        string   `json:"url"`
			Text       string   `json:"text"`
			Highlights []string `json:"highlights"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil
	}
	out := make([]exaMCPItem, 0, len(raw.Results))
	for _, item := range raw.Results {
		if strings.TrimSpace(item.URL) == "" {
			continue
		}
		out = append(out, exaMCPItem{
			Title:   item.Title,
			URL:     item.URL,
			Content: exaAnswerText(item.Highlights, item.Text),
		})
	}
	return out
}

// exaParseTextBlocks 解析 basic 工具的格式化文本块（上游 exa.ts:293-311）。
//
// 形态（实测）：
//
//	Title: ...
//	URL: ...
//	Published: N/A
//	Author: ...
//	Highlights:
//	<正文>
//	---
func exaParseTextBlocks(text string) []exaMCPItem {
	parts := strings.Split(text, "\n---")
	out := make([]exaMCPItem, 0, len(parts))
	for _, block := range parts {
		block = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(block), "---"))
		if block == "" {
			continue
		}
		item := exaMCPItem{
			Title: exaBlockField(block, "Title:"),
			URL:   exaBlockField(block, "URL:"),
		}
		// 正文优先 Highlights（免配置通道的语义），其次 Text（上游同款）。
		if idx := strings.Index(block, "\nHighlights:"); idx >= 0 {
			item.Content = CollapseSpaces(block[idx+len("\nHighlights:"):])
		} else if idx := strings.Index(block, "\nText: "); idx >= 0 {
			item.Content = CollapseSpaces(block[idx+len("\nText: "):])
		}
		if item.URL == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

// exaBlockField 取文本块里某个字段的值（"Title: xxx" 的 xxx）。
func exaBlockField(block, field string) string {
	for _, line := range strings.Split(block, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), field); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// exaBuildResponse 把归一后的条目拼成 Response。
// 与直连路径同款约定：snippet 留空、内容进 answer（Exa 没有独立摘要字段）。
func exaBuildResponse(items []exaMCPItem, num int) Response {
	out := Response{}
	var answerParts []string
	for i, item := range items {
		title := sourceTitle(item.Title, i+1)
		out.Results = append(out.Results, fillResult(Result{Title: title, URL: item.URL}))
		if item.Content != "" {
			answerParts = append(answerParts, item.Content+"\nSource: "+title+" ("+item.URL+")")
		}
		if len(out.Results) >= num {
			break
		}
	}
	out.Answer = strings.Join(answerParts, "\n\n")
	return out
}

// exaMCPQuery 把过滤条件拼进查询文本——basic 工具只认 query/numResults
// （上游 exa.ts:338-355 同款：`site:` 前缀与 "past week" 这类自然语言）。
func exaMCPQuery(query string, opts Options) string {
	parts := []string{query}
	include, exclude := DomainFilterParts(opts.DomainFilter)
	for _, d := range include {
		parts = append(parts, "site:"+d)
	}
	for _, d := range exclude {
		parts = append(parts, "-site:"+d)
	}
	switch opts.RecencyFilter {
	case "day":
		parts = append(parts, "past 24 hours")
	case "week":
		parts = append(parts, "past week")
	case "month":
		now := time.Now()
		parts = append(parts, now.Month().String()+" "+fmt.Sprint(now.Year()))
	case "year":
		parts = append(parts, fmt.Sprint(time.Now().Year()))
	}
	return strings.Join(parts, " ")
}

// exaAnswerText 取条目用于合成答案的正文：优先 highlights（上游语义），
// 没有则退回 text 的前 1000 字符（exa.ts:156-158）。
func exaAnswerText(highlights []string, text string) string {
	var hs []string
	for _, h := range highlights {
		if t := strings.TrimSpace(h); t != "" {
			hs = append(hs, t)
		}
	}
	if len(hs) > 0 {
		return CollapseSpaces(strings.Join(hs, " "))
	}
	t := strings.TrimSpace(text)
	if len(t) > 1000 {
		// 按 rune 截断：按字节切会把中文切成半个字（乱码进上下文）。
		r := []rune(t)
		if len(r) > 1000 {
			t = string(r[:1000])
		}
	}
	return CollapseSpaces(t)
}
