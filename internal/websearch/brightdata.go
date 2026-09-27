package websearch

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// brightdataDefaultBase 是 Bright Data 的 API 根（可被 base_url 覆盖）。
const brightdataDefaultBase = "https://api.brightdata.com"

// brightdataZoneOption 是 SERP zone 的设置项声明。
//
// 上游把 zone 放在 web-search.json 的 brightdataSerpZone（brightdata.ts:150）；
// 本包把它做成声明式设置项：设置面板据此渲染输入项，磁盘存值（ChannelConfig.Options）。
// EnvVar 是回退——此前这个渠道只能靠环境变量配，去掉回退会让老用户升级后失效。
var brightdataZoneOption = OptionSpec{
	Key:         "zone",
	Label:       "SERP zone",
	Placeholder: "my_serp_zone",
	Hint:        "Bright Data 控制台里的 serp 类型 zone 名（只允许字母/数字/-/_；unblocker 类型不返回 SERP JSON）",
	EnvVar:      "BRIGHTDATA_SERP_ZONE",
	Required:    true,
}

// brightdataTimeout 比包默认长：这是一次真实 Google SERP 抓取，上游给 60s。
const brightdataTimeout = 60 * time.Second

// brightdataRecencyTBS 是 recencyFilter → Google tbs 时间过滤（brightdata.ts:25-30）。
// 与查询词里的时间提示不同，tbs 是引擎侧过滤——窗口外的结果根本不会返回。
var brightdataRecencyTBS = map[string]string{
	"day":   "qdr:d",
	"week":  "qdr:w",
	"month": "qdr:m",
	"year":  "qdr:y",
}

func newBrightdata() Provider {
	return &brightdataProvider{base: base{
		id:       "brightdata",
		label:    "Bright Data",
		desc:     "经 SERP zone 代理真实 Google 结果页（按次计费），需 zone 名 + API key",
		docURL:   "https://brightdata.com/cp/setting/users",
		envVar:   "BRIGHTDATA_API_KEY",
		needsKey: true,
		options:  []OptionSpec{brightdataZoneOption},
	}}
}

type brightdataProvider struct{ base }

// Configured 覆写：Bright Data 要 key 与 SERP zone 两样齐备才算就绪。
// 上游 isBrightDataAvailable 同款（brightdata.ts:435-446）——半配好的渠道
// 不该进降级链，否则每次搜索都白失败一次，用户还以为是搜索本身有问题。
//
// 比 base 的通用判定更严的一点：zone 还要过字符集校验（一个手滑的值会被
// Bright Data 当成通用 400 报回来，读起来像凭据问题，实际只是名字写错了）。
func (p *brightdataProvider) Configured(ch ChannelConfig) bool {
	return ch.Enabled() && ch.APIKey != "" && brightdataZone(ch) != ""
}

// Search 对齐上游 brightdata.ts:448-537：
// POST {base}/request，body {url: Google SERP 地址, zone, format:"raw",
// data_format:"parsed_light"}，响应 {organic:[{link,title,description}]}。
//
// 它是**代理式**接口：真正的搜索发生在 body.url 里的 Google 上，
// Bright Data 只负责取回并把 SERP 解析成 JSON。
func (p *brightdataProvider) Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error) {
	// key 先于 zone 检查。上游刻意反过来（brightdata.ts:449-451：zone 检查会
	// 触发 !command 凭据解析器、且不能打到计费端点），本包这两项都只是纯本地读、
	// 没有任何副作用，所以按「用户最可能缺哪个先报哪个」的顺序，让缺 key
	// 永远得到凭据错（本包对外的统一契约）。
	if ch.APIKey == "" {
		return Response{}, NewProviderError(p.id, KindCredential, 0,
			"未配置 API key（获取地址: "+p.docURL+"）", "", nil)
	}
	zone := brightdataZone(ch)
	if zone == "" {
		return Response{}, NewProviderError(p.id, KindConfig, 0,
			"未配置 SERP zone：请在设置 → 网页搜索里填写该渠道的「SERP zone」（也可用环境变量 "+brightdataZoneOption.EnvVar+"）；zone 名只允许字母/数字/-/_，且必须是 Bright Data 的 serp 类型 zone（unblocker 类型不返回 SERP JSON）", ch.APIKey, nil)
	}
	base := ch.BaseURL
	if base == "" {
		base = brightdataDefaultBase
	}
	num := NormalizeNumResults(opts.NumResults)
	include, exclude := DomainFilterParts(opts.DomainFilter)
	// 域名过滤以 site: 表达（上游 brightdata.ts:209-220 同款）：Bright Data 背后
	// 就是 Google，用引擎自己的语法能让引擎去找对的页面，而不是本地丢掉错的页面。
	searchQuery := BuildSiteQuery(query, include, exclude)

	body := map[string]any{
		"url":  brightdataSerpURL(searchQuery, num, opts.RecencyFilter),
		"zone": zone,
		// format=raw 取回被代理的响应体原文，data_format 选 Bright Data 自己的
		// 解析形态：parsed_light 就是 SERP 结构（无页面正文）。
		"format":      "raw",
		"data_format": "parsed_light",
	}

	req, err := newJSONRequest(ctx, http.MethodPost, base+"/request", bearer(ch.APIKey), body)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindConfig, 0, err.Error(), ch.APIKey, err)
	}

	var env map[string]any
	if err := requestJSON(WithRequestTimeout(ctx, brightdataTimeout), p.id, ch.APIKey, req, &env); err != nil {
		return Response{}, err
	}
	organic, err := brightdataOrganic(env)
	if err != nil {
		return Response{}, NewProviderError(p.id, KindInvalidResponse, 200, err.Error(), ch.APIKey, err)
	}

	out := Response{}
	for _, item := range organic {
		u := ""
		if s, ok := item["link"].(string); ok {
			u = strings.TrimSpace(s)
		}
		if u == "" || !MatchesDomainFilter(u, include, exclude) {
			continue
		}
		title := ""
		if s, ok := item["title"].(string); ok {
			title = strings.TrimSpace(s)
		}
		snippet := ""
		if s, ok := item["description"].(string); ok {
			snippet = s
		}
		out.Results = append(out.Results, fillResult(Result{
			// 空标题回落 "Source N"（上游用已接受条数 +1，brightdata.ts:413），
			// 与 xcrawl/firecrawl 的「回落 URL」是两种语义，不要统一。
			Title:   sourceTitle(title, len(out.Results)+1),
			URL:     u,
			Snippet: snippet,
		}))
		if len(out.Results) >= num {
			break
		}
	}
	// 纯 SERP 渠道：答案由结果合成（上游 brightdata.ts:536 同款）。
	out.Answer = SourceAnswer(out.Results)
	return out, nil
}

// brightdataSerpURL 构造要交给 Bright Data 代理的 Google 搜索地址。
func brightdataSerpURL(query string, num int, recency string) string {
	params := url.Values{}
	params.Set("q", query)
	// 一次 SERP 请求按次计费、与 num 无关，所以多要 5 条是免费的余量——
	// 留给本地二次过滤后仍有足够结果（上游 brightdata.ts:224-226）。
	extra := num + 5
	if extra > 20 {
		extra = 20
	}
	params.Set("num", strconv.Itoa(extra))
	if tbs, ok := brightdataRecencyTBS[recency]; ok {
		params.Set("tbs", tbs)
	}
	// brd_json=1 才让 Bright Data 回 JSON 而不是 Google 的 HTML——
	// 少了它 data_format=parsed_light 无从下手（上游 brightdata.ts:230-231）。
	params.Set("brd_json", "1")
	return "https://www.google.com/search?" + params.Encode()
}

// brightdataOrganic 校验并取出 organic 数组。
//
// 上游对信封是硬校验（brightdata.ts:374-399）：format=raw 时 HTTP 层描述的是
// 代理跳转而不是结果，所以 Bright Data 会把自己的错误也塞进 200 响应体；
// 「已计费却拿不到结果」绝不能表现成「网上没有答案」。
func brightdataOrganic(env map[string]any) ([]map[string]any, error) {
	if msg := brightdataEnvelopeError(env); msg != "" {
		return nil, fmt.Errorf("Bright Data 返回的是错误信封而不是 SERP: %s", msg)
	}
	raw, present := env["organic"]
	if !present || raw == nil {
		return nil, fmt.Errorf("响应没有 organic 数组（zone 类型不是 serp，或请求少了 brd_json=1）")
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("organic 不是数组")
	}
	out := make([]map[string]any, 0, len(arr))
	for i, it := range arr {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("organic[%d] 不是对象", i)
		}
		out = append(out, m)
	}
	return out, nil
}

// brightdataEnvelopeError 提取 200 错误信封里的说明（error/errors/code/error_code）。
//
// 上游还会把上游文本里的 "rate limit"/"quota" 等词改写成中性措辞
// （brightdata.ts:290-336）：那是为了绕开上游「按消息文本猜错误类型」的分类器。
// 本包按 HTTP 状态码分类（ClassifyStatus）、不做字符串匹配，所以不需要这层改写。
func brightdataEnvelopeError(env map[string]any) string {
	var parts []string
	if v, present := env["error"]; present && v != nil {
		if s, ok := v.(string); ok {
			if t := strings.TrimSpace(s); t != "" {
				parts = append(parts, t)
			}
		} else {
			parts = append(parts, fmt.Sprint(v))
		}
	}
	if arr, ok := env["errors"].([]any); ok && len(arr) > 0 {
		parts = append(parts, fmt.Sprint(arr))
	} else if s, ok := env["errors"].(string); ok && strings.TrimSpace(s) != "" {
		parts = append(parts, strings.TrimSpace(s))
	}
	for _, key := range []string{"code", "error_code"} {
		switch t := env[key].(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				parts = append(parts, key+" "+s)
			}
		case float64:
			parts = append(parts, key+" "+strconv.FormatFloat(t, 'f', -1, 64))
		}
	}
	return strings.Join(parts, ", ")
}

// brightdataZone 读并校验 SERP zone，无效或缺失都返回空串。
//
// 刻意是「不抛错的纯函数」：Configured 会在调用方的错误处理之外被求值，
// 一个手滑的设置值不该让整条降级链一起失败（上游 brightdata.ts:122-135 同款理由）。
//
// ch 是 effectiveChannel 解析过的，所以这里读到的已经含「配置 → 环境变量」的结果。
func brightdataZone(ch ChannelConfig) string {
	v := strings.TrimSpace(ch.Options[brightdataZoneOption.Key])
	if !brightdataZoneValid(v) {
		return ""
	}
	return v
}

// brightdataZoneValid 校验 zone 名字符集（上游 ZONE_PATTERN）。
// 不校验的话，一个手滑的值会被 Bright Data 当成通用 400 报回来，
// 读起来像凭据问题，实际只是名字写错了。
func brightdataZoneValid(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
