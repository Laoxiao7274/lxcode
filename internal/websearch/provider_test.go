package websearch

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// idOK 是渠道 id 的合法字符集。id 会出现在协议载荷、配置文件键与前端
// 路由里；带点号/斜杠的 id 在别的层（工具名、URL 路径）会炸，所以这里钉死。
var idOK = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// 注册表契约：每个渠道的元数据必须齐备且自洽。
// 漏登记 = 渠道在 UI 与工具里都不可见（静默失效），所以这条必须有人把关。
func TestPresetProvidersContract(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range presetProviders() {
		id := p.ID()
		if !idOK.MatchString(id) {
			t.Errorf("渠道 id %q 不匹配 %s", id, idOK)
		}
		if seen[id] {
			t.Errorf("渠道 id 重复: %s", id)
		}
		seen[id] = true
		if p.Label() == "" {
			t.Errorf("%s 缺 Label（设置面板显示为空）", id)
		}
		if p.Desc() == "" {
			t.Errorf("%s 缺 Desc（用户不知道该不该配它）", id)
		}
		// 需要 key 的渠道必须给出获取入口，否则用户配不了。
		// 判据用 AcceptsKey 而不是 NeedsKey：Exa 这类「key 可选」的渠道
		// 同样要有入口，否则用户没法走直连路径。
		if p.AcceptsKey() && p.DocURL() == "" {
			t.Errorf("%s 接受 API key 却没给 DocURL", id)
		}
		// 接受 key 的渠道应有约定环境变量名（配置留空时的兜底来源）。
		if p.AcceptsKey() && p.EnvVar() == "" {
			t.Errorf("%s 接受 API key 却没给 EnvVar", id)
		}
		// 零配置渠道必须标 optIn（需用户显式启用）**或** defaultReady
		//（刻意默认就绪）——否则它会「默认就绪」并悄悄参与降级。
		//
		// 两个标记的区别就是「意外 vs 刻意」：optIn 是默认要求，defaultReady
		// 是用户拍板的例外（Exa 的免配置通道）。放宽成「什么都不用标」等于
		// 把这条守卫废掉——将来再加一个零配置渠道就没人拦了。
		if !p.NeedsKey() && !p.NeedsBaseURL() && !p.OptIn() && !p.DefaultReady() {
			t.Errorf("%s 既不需要 key 也不需要地址，应标 optIn（需用户启用）或 defaultReady（刻意默认就绪）", id)
		}
		// 两个标记互斥：既「必须显式启用」又「刻意默认就绪」是自相矛盾。
		if p.OptIn() && p.DefaultReady() {
			t.Errorf("%s 同时标了 optIn 与 defaultReady（语义矛盾）", id)
		}
		// 必须落在某个已声明的分类里（否则设置面板落到「其他」）。
		if _, ok := channelCategories[id]; !ok {
			t.Errorf("%s 未登记分类（会落到「其他」组）", id)
		}
	}
	if len(seen) < 26 {
		t.Errorf("内置渠道数 = %d，少于已移植的 26 个（漏登记会让渠道在 UI 里消失）", len(seen))
	}
}

// 分类表不得含未注册的 id（改名后忘删会留下幽灵分组项）。
func TestChannelCategoriesMatchRegistry(t *testing.T) {
	known := map[string]bool{}
	for _, p := range presetProviders() {
		known[p.ID()] = true
	}
	for id := range channelCategories {
		if !known[id] {
			t.Errorf("分类表里的 %q 不在注册表中（改名后漏删？）", id)
		}
	}
	// 每个分类都必须有渠道，否则设置面板出现空分组。
	used := map[string]bool{}
	for _, c := range channelCategories {
		used[c] = true
	}
	for _, c := range categoryOrder {
		if c.ID == defaultCategory {
			continue // 兜底组允许为空
		}
		if !used[c.ID] {
			t.Errorf("分类 %q（%s）没有任何渠道", c.ID, c.Label)
		}
		if c.Label == "" {
			t.Errorf("分类 %q 缺显示名", c.ID)
		}
	}
}

// 降级链的优先级是注册顺序——免 key / 自建必须排在前（用户没指定主渠道时
// 先用不花钱的，别一上来就烧付费额度）。
func TestPresetOrderPutsFreeFirst(t *testing.T) {
	ids := ProviderIDs()
	if len(ids) == 0 {
		t.Fatal("注册表为空")
	}
	if ids[0] != "searxng" {
		t.Errorf("首位应是自建免 key 的 searxng，实际 %q", ids[0])
	}
	// 前三个必须是 free 分类（开箱可用优先）。
	for i, id := range ids[:3] {
		if categoryOf(id) != "free" {
			t.Errorf("第 %d 位 %q 属于 %q，前三位应全是 free 分类", i+1, id, categoryOf(id))
		}
	}
}

// ProviderIDs 与 ProviderByID 必须一致（两处口径漂移会让 UI 列出点不开的渠道）。
func TestProviderIDsAndLookupAgree(t *testing.T) {
	ids := ProviderIDs()
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	for _, id := range ids {
		p, ok := ProviderByID(id)
		if !ok {
			t.Errorf("ProviderIDs 里的 %q 查不到适配器", id)
			continue
		}
		if p.ID() != id {
			t.Errorf("索引键 %q 与适配器 ID %q 不一致", id, p.ID())
		}
	}
	// 无重复。
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			t.Errorf("ProviderIDs 有重复: %s", sorted[i])
		}
	}
}

// 分类显示名必须可查（前端直接用后端给的名字渲染分组标题）。
func TestCategoryLabel(t *testing.T) {
	if got := CategoryLabel("free"); got != "开箱可用" {
		t.Errorf("CategoryLabel(free) = %q", got)
	}
	if got := CategoryLabel("不存在"); got != "其他" {
		t.Errorf("未知分类应回落「其他」，实际 %q", got)
	}
	if got := categoryOf("不存在"); got != defaultCategory {
		t.Errorf("未登记渠道应回落 %q，实际 %q", defaultCategory, got)
	}
	if !strings.Contains(CategoryLabel("serp"), "SERP") {
		t.Errorf("SERP 分类名应含 SERP: %q", CategoryLabel("serp"))
	}
}

// optionKeyOK 是设置项键名的合法字符集。
// 键名会进 search.json 与协议载荷，也是前端 form 字段名——带点号/空格的键
// 在 HTML 属性与 CSS 选择器里都会出问题，所以约束比渠道 id 更保守。
var optionKeyOK = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// envVarOK 是环境变量名的合法字符集（POSIX 惯例）。
var envVarOK = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// 设置项契约：声明必须自洽，否则设置面板会渲染出一个填了也没用的输入框。
//
// 这一层最容易出的错是「声明了但适配器没读」——UI 上看着能配、配了不生效，
// 用户只会以为渠道坏了。所以除了形状，这里还要求每个声明过的键都被适配器读到
// （由各适配器自己的用例钉住实际取值，见 adapters_options_test.go）。
func TestOptionSpecsContract(t *testing.T) {
	for _, p := range presetProviders() {
		seen := map[string]bool{}
		for _, spec := range p.Options() {
			where := p.ID() + "." + spec.Key
			if !optionKeyOK.MatchString(spec.Key) {
				t.Errorf("%s 键名不匹配 %s", where, optionKeyOK)
			}
			if seen[spec.Key] {
				t.Errorf("%s 重复声明", where)
			}
			seen[spec.Key] = true
			if spec.Label == "" {
				t.Errorf("%s 缺 Label（输入项没有标题）", where)
			}
			// 环境变量回退是这次改造的兼容承诺：老用户此前只能靠环境变量配它。
			if spec.EnvVar == "" {
				t.Errorf("%s 缺 EnvVar（老用户的环境变量配置会失效）", where)
			} else if !envVarOK.MatchString(spec.EnvVar) {
				t.Errorf("%s 的 EnvVar %q 不匹配 %s", where, spec.EnvVar, envVarOK)
			}
			// 有限枚举必须把默认值也列进去，否则「默认值」在下拉里选不出来。
			if len(spec.Choices) > 0 {
				if spec.Default == "" {
					t.Errorf("%s 有 Choices 却没给 Default", where)
				}
				found := false
				for _, c := range spec.Choices {
					if c == spec.Default {
						found = true
					}
				}
				if !found {
					t.Errorf("%s 的 Default %q 不在 Choices 里", where, spec.Default)
				}
			}
		}
	}
}

// 有设置项的渠道必须是「需要凭据」或「需要地址」的——纯零配置渠道（optIn）
// 不该藏着一个必填项：那会让它既要点启用又要填表，与「零配置」的定位矛盾。
func TestOptionChannelsAreNotZeroConfig(t *testing.T) {
	for _, p := range presetProviders() {
		if len(p.Options()) == 0 {
			continue
		}
		if !p.NeedsKey() && !p.NeedsBaseURL() && p.OptIn() {
			t.Errorf("%s 是零配置渠道却声明了设置项（定位矛盾）", p.ID())
		}
	}
}

// 声明了必填项却没有 Default 的渠道，其 Configured 必须真的因缺项而不就绪
// ——「必填」若在就绪判定里没生效，就是一个只有 UI 在乎的装饰。
func TestRequiredOptionGatesReadiness(t *testing.T) {
	checked := 0
	for _, p := range presetProviders() {
		var required *OptionSpec
		for i, spec := range p.Options() {
			if spec.Required && spec.Default == "" {
				required = &p.Options()[i]
			}
		}
		if required == nil {
			continue
		}
		checked++
		// 清掉该项的环境变量回退，否则开发机上的真实取值会掩盖判定。
		t.Setenv(required.EnvVar, "")
		ch := ChannelConfig{APIKey: "placeholder-key", BaseURL: "https://example.com"}
		if p.Configured(ch) {
			t.Errorf("%s 缺必填项 %s 时不该就绪", p.ID(), required.Key)
		}
		ch.Options = map[string]string{required.Key: "value"}
		if !p.Configured(ch) {
			t.Errorf("%s 填了必填项 %s 后应就绪", p.ID(), required.Key)
		}
	}
	if checked == 0 {
		t.Error("没有任何「必填且无默认值」的设置项，这条测试成了空转——brightdata 的 zone 应该在其中")
	}
}
