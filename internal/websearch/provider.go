package websearch

import (
	"context"
	"sort"
	"strings"
)

// Provider 是一个搜索渠道的适配器。
//
// 一渠道一文件（对齐上游组织方式：pi-web-access 的 brave.ts/tavily.ts/...）。
// 适配器只负责「请求长什么样、响应怎么映射」——错误分类、超时、脱敏、
// 降级判定全部由本包统一做，新增渠道不必重复实现这些横切逻辑。
type Provider interface {
	// ID 渠道 id（稳定标识，落配置与协议载荷；禁含 "/"）。
	ID() string
	// Label 显示名。
	Label() string
	// Desc 一句话说明（设置面板展示）。
	Desc() string
	// DocURL 用户获取 key 的入口（空 = 无需 key）。
	DocURL() string
	// EnvVar 约定环境变量名（配置留空时从这里取 key；空 = 无约定）。
	EnvVar() string
	// NeedsKey 是否需要 API key 才就绪；false 时用 BaseURL（自建实例）。
	//
	// 与 AcceptsKey 的区别（Exa 是唯一分叉的渠道）：Exa 没有 key 也能用
	//（走免配置 MCP 通道），所以 NeedsKey=false；但它**仍然接受** key，
	// 有 key 就走直连 API——面板必须继续给出 key 输入框。
	NeedsKey() bool
	// AcceptsKey 报告面板是否应当提供 API Key 输入框。
	//
	// 多数渠道与 NeedsKey 同值（默认派生）；Exa 这类「key 可选、有则更优」
	// 的渠道二者分叉：NeedsKey=false（缺 key 也就绪）而 AcceptsKey=true
	//（不显示输入框的话用户永远进不了直连路径）。
	AcceptsKey() bool
	// NeedsBaseURL 是否需要自填实例地址（SearXNG 等自建渠道）。
	NeedsBaseURL() bool
	// OptIn 报告该渠道是否必须由用户**显式启用**才生效。
	//
	// 零配置渠道（不需要 key 也不需要地址，如 DuckDuckGo 抓取）技术上永远
	// 「可用」，但这不等于用户想用它：它靠抓公开页面，稳定性与合规性都不是
	// 用户默许的事。上游同款语义（pi-web-access 的 ALL_SEARCH_PROVIDERS 注释：
	// 「all 绝不能未经用户要求就扇出到 opt-in 或付费渠道」）。
	OptIn() bool
	// DefaultReady 报告该渠道是**刻意**默认就绪的零配置渠道（如 Exa 的
	// 免配置 MCP 通道）：用户不填任何东西，搜索请求就会发往它。
	//
	// 为什么要有这个显式标记：OptIn 的守卫（provider_test 钉住）是「零配置
	// 渠道必须 optIn」——防止某个渠道**意外**默认就绪并悄悄参与降级。Exa 是
	// 刻意的例外（用户拍板「开箱即用」），所以把例外也写成声明而不是放宽
	// 守卫：这样「意外就绪」仍然会被测试拦下，而「刻意就绪」有据可查。
	DefaultReady() bool
	// Options 声明该渠道的私有设置项（无则返回 nil）。
	//
	// 声明式是刻意的：设置面板按声明渲染输入项，所以「渠道多了一个设置」
	// 不需要改 UI 也不需要改磁盘格式——与「代码持元数据、磁盘持用户配置」
	// 同一条纪律。
	Options() []OptionSpec
	// Configured 报告该渠道在当前配置下是否就绪（凭据/端点齐备）。
	// 内嵌 base 给出通用判定；特殊渠道（SearXNG 免 key 可用）自行覆盖。
	//
	// 注意：Configured 只看「配置值够不够用」，不看「用户是否显式启用过」——
	// 后者由 Service 层的 readyIn 合并判定（配置项是否存在只有 Service 知道）。
	Configured(ch ChannelConfig) bool
	// Search 执行一次搜索。ch 是该渠道的用户配置（key/baseURL 已解析）。
	Search(ctx context.Context, ch ChannelConfig, query string, opts Options) (Response, error)
}

// OptionSpec 描述渠道的一个私有设置项。
//
// 为什么需要这一层：渠道除了「通用凭据」（api_key / base_url）之外还有各自
// 私有的设置——Bright Data 的 SERP zone、Mistral 的搜索模型与档位、Firecrawl
// 的 API 版本。这类设置既不能塞进通用凭据（语义与校验各不相同：zone 有字符集
// 约束、档位是有限枚举、版本影响请求路径），也不该只认环境变量——那样设置
// 面板就配不出一个能用的渠道（Bright Data 此前正是这种状态）。
//
// 于是：**声明放适配器，取值放磁盘，渲染放面板**。
type OptionSpec struct {
	// Key 是配置键（存进 ChannelConfig.Options 的键名）。
	Key string `json:"key"`
	// Label 是输入项标题。
	Label string `json:"label"`
	// Placeholder 是输入框占位提示（通常是取值示例）。
	Placeholder string `json:"placeholder,omitempty"`
	// Hint 是输入项下方的一句说明（取值约束与注意事项）。
	Hint string `json:"hint,omitempty"`
	// EnvVar 是回退环境变量：配置留空时读它。
	//
	// 保留回退是为了不弄丢老用户的配置——他们此前只能靠环境变量配这些项，
	// 加了 UI 就要求他们重新填一遍是纯粹的倒退。
	EnvVar string `json:"env_var,omitempty"`
	// Required 表示缺了它渠道就不就绪（Bright Data 的 zone 就是这种：
	// 没有 zone 连请求都构造不出来）。
	Required bool `json:"required,omitempty"`
	// Default 是「配置与环境变量都为空」时的取值（如 Mistral 的默认模型）。
	Default string `json:"default,omitempty"`
	// Choices 限定可选值（空 = 自由文本）。
	//
	// 有它就该有 UI 下拉：档位/版本这类有限枚举让用户手打，打错了要么被
	// 硬校验拦下（白填一次），要么静默改变计费（更糟）。
	Choices []string `json:"choices,omitempty"`
}

// base 承载渠道的静态元数据——全部适配器内嵌它，只写差异字段，
// 避免每个文件重复 7 个 getter（样板会让新增渠道的成本凭空翻倍）。
type base struct {
	id           string
	label        string
	desc         string
	docURL       string
	envVar       string
	needsKey     bool
	needsBaseURL bool
	// acceptsKey：面板是否给出 key 输入框（见 Provider.AcceptsKey）。
	// 不设时派生自 needsKey——只有「key 可选」的渠道（Exa）需要显式置位。
	acceptsKey bool
	// optIn：零配置渠道必须由用户显式启用（见 Provider.OptIn）。
	optIn bool
	// defaultReady：刻意默认就绪的零配置渠道（见 Provider.DefaultReady）。
	defaultReady bool
	// options：渠道私有设置项的声明（见 Provider.Options）。
	options []OptionSpec
}

func (b base) ID() string         { return b.id }
func (b base) Label() string      { return b.label }
func (b base) Desc() string       { return b.desc }
func (b base) DocURL() string     { return b.docURL }
func (b base) EnvVar() string     { return b.envVar }
func (b base) NeedsKey() bool     { return b.needsKey }
func (b base) NeedsBaseURL() bool { return b.needsBaseURL }
func (b base) OptIn() bool        { return b.optIn }
func (b base) DefaultReady() bool { return b.defaultReady }

// AcceptsKey 默认与 NeedsKey 同值——25 个渠道的行为与加这个字段之前完全一致，
// 只有显式置 acceptsKey 的渠道（Exa）才会「不需要 key 但接受 key」。
func (b base) AcceptsKey() bool { return b.needsKey || b.acceptsKey }

func (b base) Options() []OptionSpec { return b.options }

// resolveBase 统一「必填凭据校验 + base_url 兜底」这段开头——20 个适配器逐字
// 重复它。为什么要提出来：漏了凭据校验的渠道会在缺 key 时照发请求，后端回
// 401/403，分类从 credential 掉成 invalid-request，而**这一档不降级**（见
// FallbackKinds）——用户看到的是「搜索失败」，而不是「去配 key」。
//
// defaultBase 是渠道私有的默认根（各文件一个 const），所以由调用方传入：
// 「默认根各不相同」正是这段没能整体内联进 base 的唯一原因。
func (b base) resolveBase(ch ChannelConfig, defaultBase string) (string, error) {
	if ch.APIKey == "" {
		return "", NewProviderError(b.id, KindCredential, 0,
			"未配置 API key（获取地址: "+b.docURL+"）", "", nil)
	}
	if ch.BaseURL != "" {
		return ch.BaseURL, nil
	}
	return defaultBase, nil
}

// presetProviders 返回全部内置渠道适配器。顺序即降级链的优先级：
// 免 key / 自建在前（开箱可用、不花钱），付费 API 在后。
//
// 登记点唯一：漏登记 = 渠道在 UI 与工具里都不可见（有测试钉住数量与顺序）。
// 上游共 32 个渠道，未移植的三类：需要 MCP 客户端的（parallel-mcp/baizhi）、
// 需要浏览器 Cookie 或 ADC 的（gemini-web/gemini-adc）、复用宿主模型凭据的
// （openai/gemini/kimi/xai——等模型注册表凭据复用落地）。
func presetProviders() []Provider {
	return []Provider{
		// 自建 / 零配置：开箱可用（不花钱）
		newSearxng(),
		newDuckDuckGo(),
		newJina(),
		// 通用搜索 API（LLM 友好）
		newTavily(),
		newExa(),
		newBrave(),
		newPerplexity(),
		newKagi(),
		newValyu(),
		newAnysearch(),
		newSearch1API(),
		newQuerit(),
		newParallel(),
		newTinyFish(),
		newSearchInfinity(),
		newOllama(),
		newMistralSearch(),
		newFirecrawl(),
		// 中文/国内
		newBocha(),
		// SERP 代理（Google 结果的结构化封装）
		newSerper(),
		newSerpapi(),
		newSerpbase(),
		newSerply(),
		newSerpdive(),
		newBrightdata(),
		newXcrawl(),
	}
}

// 渠道分类：设置面板按它分组展示。
//
// 放在这里而不是各适配器的 base 里——分类是**跨渠道的编排判断**
// （「哪些算开箱可用」「哪些是 SERP 代理」），一次看全才能保持一致；
// 分散到 26 个文件里，同类渠道必然被归进不同的组。
// 未登记的 id 回落 defaultCategory（新增渠道忘了登记也能正常显示）。
const defaultCategory = "other"

var channelCategories = map[string]string{
	// 自建或零配置即可用
	"searxng":    "free",
	"duckduckgo": "free",
	"jina":       "free",
	// 通用搜索 API
	"tavily":         "general",
	"exa":            "general",
	"brave":          "general",
	"perplexity":     "general",
	"kagi":           "general",
	"valyu":          "general",
	"anysearch":      "general",
	"search1api":     "general",
	"querit":         "general",
	"parallel":       "general",
	"tinyfish":       "general",
	"searchinfinity": "general",
	"ollama":         "general",
	"mistral-search": "general",
	"firecrawl":      "general",
	// 中文 / 国内可直连
	"bocha": "cn",
	// Google SERP 的结构化代理
	"serper":     "serp",
	"serpapi":    "serp",
	"serpbase":   "serp",
	"serply":     "serp",
	"serpdive":   "serp",
	"brightdata": "serp",
	"xcrawl":     "serp",
}

// categoryOrder 是分组的展示顺序与标题（前端按此渲染分节）。
var categoryOrder = []struct {
	ID    string
	Label string
	Hint  string
}{
	{"free", "开箱可用", "无需付费凭据（自建实例或公开端点）"},
	{"general", "通用搜索 API", "面向 LLM 的结构化搜索服务"},
	{"cn", "中文搜索", "国内可直连的中文检索"},
	{"serp", "SERP 代理", "Google 搜索结果的结构化封装"},
	{"other", "其他", "未归类的渠道"},
}

// CategoryLabel 返回分类的显示名（未知分类回落「其他」）。
func CategoryLabel(id string) string {
	for _, c := range categoryOrder {
		if c.ID == id {
			return c.Label
		}
	}
	return "其他"
}

// registry 是 id → 适配器的索引（包级构建一次，只读）。
var registry = func() map[string]Provider {
	m := make(map[string]Provider)
	for _, p := range presetProviders() {
		m[p.ID()] = p
	}
	return m
}()

// ProviderByID 按 id 取适配器。
func ProviderByID(id string) (Provider, bool) {
	p, ok := registry[id]
	return p, ok
}

// ProviderIDs 返回全部内置渠道 id（稳定顺序）。
func ProviderIDs() []string {
	out := make([]string, 0, len(registry))
	for _, p := range presetProviders() {
		out = append(out, p.ID())
	}
	return out
}

// Channel 是「代码元数据 + 用户配置」合并后的渠道视图（协议与前端用）。
type Channel struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Desc     string `json:"desc"`
	DocURL   string `json:"doc_url,omitempty"`
	EnvVar   string `json:"env_var,omitempty"`
	NeedsKey bool   `json:"needs_key"`
	NeedsURL bool   `json:"needs_url"`
	// AcceptsKey 标明面板是否应给出 API Key 输入框。多数渠道与 NeedsKey
	// 同值；Exa 是「缺 key 也就绪、但有 key 走直连」——前端若只看 NeedsKey
	// 就会把它的输入框藏掉，用户永远进不了直连路径。
	AcceptsKey bool `json:"accepts_key"`
	// OptIn 标明该渠道必须由用户显式启用（零配置渠道）——前端据此提示
	// 「需要手动开启」，而不是显示成开箱即用。
	OptIn bool `json:"opt_in,omitempty"`
	// DefaultReady 标明该渠道是刻意默认就绪的零配置渠道（见 Provider.DefaultReady）
	// ——前端据此提示「开箱可用，无需配置」，与 OptIn 的提示互斥。
	DefaultReady bool `json:"default_ready,omitempty"`
	// Category 是设置面板的分组键（free/general/cn/serp/other）。
	Category string `json:"category"`
	// CategoryLabel 是分组显示名（前端直接用，不必内置一份分类表）。
	CategoryLabel string `json:"category_label"`
	// APIKey 是用户配置的 key（可能来自配置字面量、$ENV 间接或约定环境变量）。
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	// OptionSpecs 是该渠道私有设置项的声明（前端据此渲染输入项）。
	OptionSpecs []OptionSpec `json:"option_specs,omitempty"`
	// Options 是这些设置项的当前取值（已解析：配置 → 环境变量 → 默认值）。
	//
	// 注意：这里**可能包含默认值**（用户没填也有值），所以不能用「有没有值」
	// 判断「用户配过没有」——那是 Stored 的职责。
	Options map[string]string `json:"options,omitempty"`
	// Stored 报告配置文件里**是否真的存在**该渠道的条目（用户配过东西）。
	//
	// 前端据此决定要不要显示「清除」按钮：零配置渠道（Exa 的免配置通道）
	// 不填任何东西也就绪，若拿 Configured 当判据，按钮会出现在一张空卡片上，
	// 点了什么都不会发生。
	Stored  bool `json:"stored"`
	Enabled bool `json:"enabled"`
	Primary bool `json:"primary"`
	// Configured 报告该渠道是否就绪（凭据/端点齐备）。
	Configured bool `json:"configured"`
}

// ChannelView 把适配器元数据与用户配置合并成渠道视图。
//
// present 报告配置文件里是否存在该渠道的条目——opt-in 渠道（零配置）只有
// 显式启用过才算就绪，而「存在条目」这件事只有 Service 知道。
func ChannelView(p Provider, ch ChannelConfig, present, primary bool) Channel {
	cat := categoryOf(p.ID())
	return Channel{
		ID:            p.ID(),
		Label:         p.Label(),
		Desc:          p.Desc(),
		DocURL:        p.DocURL(),
		EnvVar:        p.EnvVar(),
		NeedsKey:      p.NeedsKey(),
		NeedsURL:      p.NeedsBaseURL(),
		AcceptsKey:    p.AcceptsKey(),
		OptIn:         p.OptIn(),
		DefaultReady:  p.DefaultReady(),
		Category:      cat,
		CategoryLabel: CategoryLabel(cat),
		APIKey:        ch.APIKey,
		BaseURL:       ch.BaseURL,
		OptionSpecs:   p.Options(),
		Options:       ch.Options,
		Stored:        present,
		Enabled:       ch.Enabled(),
		Primary:       primary,
		Configured:    channelReady(p, ch, present),
	}
}

// categoryOf 取渠道分类（未登记回落 other）。
func categoryOf(id string) string {
	if c, ok := channelCategories[id]; ok {
		return c
	}
	return defaultCategory
}

// channelReady 判定渠道是否就绪：配置值够用 + （opt-in 渠道须显式启用过）。
//
// 单点判定：ChannelView / Ready / availableIDs / firstReadyLocked / chainOrder /
// SearchWith 六处共用它。复制判定的后果是「列表显示就绪、搜索却跳过它」
// 这类只在某个入口出现的诡异不一致。
func channelReady(p Provider, ch ChannelConfig, present bool) bool {
	if p.OptIn() && !present {
		return false
	}
	return p.Configured(ch)
}

// Configured 报告渠道的**配置值**是否够用：需要 key 的看 key，需要端点的看
// base_url，都不需要的（DuckDuckGo 等）恒为够用；声明了必填设置项的还要看它。
//
// 「够用」不等于「就绪」——零配置渠道还要用户显式启用过（见 channelReady）。
//
// ch 一定是 effectiveChannel 解析过的（见 readyIn / channelsOf）：设置项的
// 环境变量回退在那一层就完成了，所以这里只看最终值，不必也不该再碰 os.Getenv。
func (b base) Configured(ch ChannelConfig) bool {
	if !ch.Enabled() {
		return false
	}
	if b.needsKey && ch.APIKey == "" {
		return false
	}
	if b.needsBaseURL && ch.BaseURL == "" {
		return false
	}
	for _, spec := range b.options {
		if spec.Required && strings.TrimSpace(ch.Options[spec.Key]) == "" {
			return false
		}
	}
	return true
}

// sortChannels 按 id 排序（稳定输出，便于测试与 UI 展示一致）。
func sortChannels(chs []Channel) {
	sort.Slice(chs, func(i, j int) bool { return chs[i].ID < chs[j].ID })
}
