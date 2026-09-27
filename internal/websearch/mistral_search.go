package websearch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// mistralSearchDefaultBase 是 Mistral API 的默认根（可被 base_url 覆盖）。
const mistralSearchDefaultBase = "https://api.mistral.ai"

// 上游把搜索模型与搜索档位放在 web-search.json（mistral-search.ts:17-21）；
// 本包把它们做成声明式设置项（设置面板渲染，磁盘存 ChannelConfig.Options）。
// EnvVar 是回退——这两项此前只能靠环境变量配。
var (
	mistralSearchModelOption = OptionSpec{
		Key:         "model",
		Label:       "会话模型",
		Placeholder: mistralSearchDefaultModel,
		Hint:        "跑检索的 Mistral 会话模型；留空即用默认值",
		EnvVar:      "MISTRAL_SEARCH_MODEL",
		Default:     mistralSearchDefaultModel,
	}
	mistralSearchToolOption = OptionSpec{
		Key:         "tool",
		Label:       "搜索档位",
		Placeholder: mistralSearchDefaultTool,
		Hint:        "web_search_premium 检索更广但更贵，按次计费前请确认额度",
		EnvVar:      "MISTRAL_SEARCH_TOOL",
		Default:     mistralSearchDefaultTool,
		Choices:     []string{mistralSearchDefaultTool, mistralSearchPremiumTool},
	}
)

const (
	mistralSearchDefaultModel = "mistral-small-latest"
	mistralSearchDefaultTool  = "web_search"
	mistralSearchPremiumTool  = "web_search_premium"
)

// mistralSearchTimeout 比包默认长：会话接口要等模型跑完一轮检索+生成，上游给 60s。
const mistralSearchTimeout = 60 * time.Second

// mistralSearchRecencyLabels 是 recencyFilter → 提示词措辞的映射
// （mistral-search.ts:108-113）。Mistral 的会话接口没有时间参数，
// 时间范围只能写成自然语言约束。
var mistralSearchRecencyLabels = map[string]string{
	"day":   "past 24 hours",
	"week":  "past week",
	"month": "past month",
	"year":  "past year",
}

func newMistralSearch() Provider {
	return &mistralSearchProvider{base: base{
		id:       "mistral-search",
		label:    "Mistral 搜索",
		desc:     "Mistral 会话接口 + 内置 web_search 工具，返回带出处的模型答案",
		docURL:   "https://console.mistral.ai/api-keys",
		envVar:   "MISTRAL_API_KEY",
		needsKey: true,
		options:  []OptionSpec{mistralSearchModelOption, mistralSearchToolOption},
	}}
}

type mistralSearchProvider struct{ base }

// Search 对齐上游 mistral-search.ts:213-267：
// POST {base}/v1/conversations，body {inputs,stream:false,model,tools:[{type}]}，
// 响应 {outputs:[{type:"message.output",content:[{type:"text"|"tool_reference"}]}]}。
//
// 与其它渠道最大的不同：它返回的是**模型答案**（answer 来自模型正文），
// 结果条目是答案里的引用，所以 answer 不做 SourceAnswer 合成。
func (p *mistralSearchProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	tool, err := mistralSearchTool(ch)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}
	base := ch.BaseURL
	if base == "" {
		base = mistralSearchDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)

	body := map[string]any{
		"inputs": []map[string]any{{
			"role":    "user",
			"content": mistralSearchPrompt(query, opts, num, include, exclude),
		}},
		// stream=false：本包只消费完整响应，SSE 不在移植范围内。
		"stream": false,
		"model":  mistralSearchModel(ch),
		"tools":  []map[string]any{{"type": tool}},
	}
	req, err := newJSONRequest(ctx, http.MethodPost, base+"/v1/conversations", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var env map[string]any
	if err := requestJSON(WithRequestTimeout(ctx, mistralSearchTimeout), p.id, ch.APIKey, req, &env); err != nil {
		return Response{}, err
	}
	out, err := mistralSearchParse(env, num, include, exclude)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}
	return out, nil
}

// mistralSearchPrompt 把搜索选项写成自然语言约束（上游 mistral-search.ts:105-124）。
//
// 这里刻意不用 BuildSiteQuery：Mistral 的会话接口没有域名/时间参数，
// 只能靠提示词表达，而 site: 语法对模型不是硬约束——上游选的是明确的祈使句。
// 本地仍会做 MatchesDomainFilter 二次过滤兜底。
func mistralSearchPrompt(query string, opts Options, num int, include, exclude []string) string {
	var lines []string
	if opts.RecencyFilter != "" {
		label := opts.RecencyFilter
		if l, ok := mistralSearchRecencyLabels[opts.RecencyFilter]; ok {
			label = l
		}
		lines = append(lines, "Prefer sources from the "+label+".")
	}
	// 只在调用方显式要了条数时才写进提示词（上游同样看原始 numResults > 0，
	// 而不是归一化后的值）。
	if opts.NumResults > 0 {
		lines = append(lines, fmt.Sprintf("Prefer up to %d distinct sources.", num))
	}
	if len(include) > 0 {
		lines = append(lines, "Only use sources from: "+strings.Join(mistralSearchCap(include, 100), ", ")+".")
	}
	if len(exclude) > 0 {
		lines = append(lines, "Do not use sources from: "+strings.Join(mistralSearchCap(exclude, 100), ", ")+".")
	}
	if len(lines) == 0 {
		return query
	}
	return strings.Join(lines, " ") + "\n\n" + query
}

// mistralSearchCap 截断域名列表：一个超长列表会把提示词撑爆，
// 而上游也只取前 100 条（mistral-search.ts:121-122）。
func mistralSearchCap(list []string, max int) []string {
	if len(list) > max {
		return list[:max]
	}
	return list
}

// mistralSearchParse 从会话响应里取答案文本与引用来源。
//
// 结构不符时报错而不是回空结果：「模型没答出来」与「响应结构变了」必须区分，
// 后者要降级换渠道（上游 mistral-search.ts:176 同款硬校验）。
func mistralSearchParse(env map[string]any, num int, include, exclude []string) (Response, error) {
	outputs, ok := env["outputs"].([]any)
	if !ok {
		return Response{}, fmt.Errorf("响应缺少 outputs 数组")
	}

	var answerParts []string
	var results []Result
	seen := map[string]bool{}
	for _, o := range outputs {
		entry, ok := o.(map[string]any)
		if !ok {
			continue
		}
		// 只认 message.output：其余 output 类型是工具调用过程，不是给用户看的内容。
		if entry["type"] != "message.output" {
			continue
		}
		content := entry["content"]
		if s, ok := content.(string); ok {
			if t := strings.TrimSpace(s); t != "" {
				answerParts = append(answerParts, t)
			}
			continue
		}
		chunks, ok := content.([]any)
		if !ok {
			continue
		}
		for _, c := range chunks {
			part, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if part["type"] == "text" {
				if s, ok := part["text"].(string); ok {
					if t := strings.TrimSpace(s); t != "" {
						answerParts = append(answerParts, t)
					}
				}
				continue
			}
			if part["type"] != "tool_reference" || len(results) >= num {
				continue
			}
			rawURL, ok := part["url"].(string)
			if !ok {
				continue
			}
			u := mistralSearchNormalizeURL(rawURL)
			// 去重与域名过滤都在这里做：模型可能把同一个来源引两次，
			// 也可能引用被明确排除的域名。
			if u == "" || seen[u] || !MatchesDomainFilter(u, include, exclude) {
				continue
			}
			seen[u] = true
			title := u
			if s, ok := part["title"].(string); ok && strings.TrimSpace(s) != "" {
				title = strings.TrimSpace(s)
			}
			snippet := ""
			if s, ok := part["description"].(string); ok {
				snippet = s
			}
			results = append(results, fillResult(Result{Title: title, URL: u, Snippet: snippet}))
		}
	}

	answer := strings.TrimSpace(strings.Join(answerParts, "\n"))
	if answer == "" && len(results) == 0 {
		return Response{}, fmt.Errorf("响应既没有答案也没有引用来源")
	}
	return Response{Answer: answer, Results: results}, nil
}

// mistralSearchNormalizeURL 只接受绝对的 http(s) 地址。
// 模型回引用的 url 字段什么形态都有（协议相对、纯域名、data:），
// 非 http(s) 的必须丢掉——它们进不了搜索结果（上游 normalizeResultUrl 同款）。
func mistralSearchNormalizeURL(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	if parsed.Host == "" {
		return ""
	}
	return u
}

// mistralSearchModel 返回搜索用的会话模型（默认 mistral-small-latest）。
// ch 是已解析配置，所以「配置 → 环境变量 → 默认」在这里已经收敛成一个值。
func mistralSearchModel(ch ChannelConfig) string {
	if v := strings.TrimSpace(ch.Options[mistralSearchModelOption.Key]); v != "" {
		return v
	}
	return mistralSearchDefaultModel
}

// mistralSearchTool 解析搜索档位。非法值报错而不是回落默认：
// web_search_premium 是更贵的档位，静默换档等于悄悄改计费
// （上游 mistral-search.ts:60-66 同样是硬校验）。
//
// 面板给的是下拉（Choices），所以非法值只可能来自手写配置或环境变量——
// 那两种情况恰恰最需要报错而不是猜。
func mistralSearchTool(ch ChannelConfig) (string, error) {
	v := strings.TrimSpace(ch.Options[mistralSearchToolOption.Key])
	if v == "" {
		return mistralSearchDefaultTool, nil
	}
	if v != mistralSearchDefaultTool && v != mistralSearchPremiumTool {
		return "", fmt.Errorf("%s 只能是 %s 或 %s（实际 %q）",
			mistralSearchToolOption.Label, mistralSearchDefaultTool, mistralSearchPremiumTool, v)
	}
	return v, nil
}
