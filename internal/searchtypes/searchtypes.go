// Package searchtypes 定义网页搜索渠道的**共享 wire 形状**（Channel / OptionSpec /
// Result）：websearch 的业务实现与 protocol 的协议载荷共用，两边用同一个类型。
//
// 为什么住在这里（分层纪律）：类型原先定义在 internal/websearch（业务实现包），
// protocol 为取 wire 形状反向 import 它——协议层被客户端与服务端共享，被实现包
// 拖进依赖是分层倒置（与 chatmsg/sessiondata 同一条理由：共享形状住在叶包，
// 业务包与协议包各自 import 它，方向都是叶包 ← 上层）。
package searchtypes

// Result 是一条搜索结果（对齐上游 SearchResult）。
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
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
