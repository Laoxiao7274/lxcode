package websearch

import (
	"os"
	"strings"
)

// EnvPrefix 是约定环境变量的前缀（渠道自定义 EnvVar 为空时的兜底）。
const EnvPrefix = "LXCODE_SEARCH_"

// ResolveAPIKey 解析渠道的 API key。顺序（对齐上游 credential-source.ts 的语义）：
//
//  1. 配置里的字面量；
//  2. 配置里以 "$" 开头 → 当作环境变量名去取（"$TAVILY_API_KEY"）——
//     这是「配置不进版本库」的标准做法，key 只存在环境里；
//  3. 渠道约定的环境变量（Provider.EnvVar，如 TAVILY_API_KEY）；
//  4. 通用前缀环境变量（LXCODE_SEARCH_<ID 大写>）。
//
// 全都没有则返回空串——调用方据此判定「未配置」，不要在这里报错
// （未配置是常态：25 个渠道用户通常只配一两个）。
func ResolveAPIKey(ch ChannelConfig, p Provider) string {
	raw := strings.TrimSpace(ch.APIKey)
	if raw != "" {
		if strings.HasPrefix(raw, "$") {
			if v := strings.TrimSpace(os.Getenv(strings.TrimPrefix(raw, "$"))); v != "" {
				return v
			}
			// 间接引用取不到值时继续往下找约定变量，而不是就此判定未配置：
			// 用户写了 "$TAVILY_API_KEY" 但环境变量名打错时，
			// 约定变量还有机会救回来。
		} else {
			return raw
		}
	}
	if ev := p.EnvVar(); ev != "" {
		if v := strings.TrimSpace(os.Getenv(ev)); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(os.Getenv(EnvPrefix + strings.ToUpper(strings.ReplaceAll(p.ID(), "-", "_")))); v != "" {
		return v
	}
	return ""
}

// ResolveBaseURL 解析渠道的实例地址（自建渠道用）。
// 配置优先；空则取渠道默认（Provider 未暴露默认时为空）。
func ResolveBaseURL(ch ChannelConfig) string {
	return strings.TrimRight(strings.TrimSpace(ch.BaseURL), "/")
}

// ResolveOption 解析一个渠道私有设置项的取值，顺序：
//
//  1. 配置里的值（用户在设置面板填的，或手写进 search.json 的）；
//  2. 该项声明的环境变量（spec.EnvVar）；
//  3. 该项的默认值（spec.Default）。
//
// 环境变量这一层不是历史包袱而是兼容承诺：这些设置项在加 UI 之前只能靠环境
// 变量配，去掉回退等于让老用户的 Bright Data 渠道在某次升级后突然失效。
func ResolveOption(ch ChannelConfig, spec OptionSpec) string {
	if v := strings.TrimSpace(ch.Options[spec.Key]); v != "" {
		return v
	}
	if spec.EnvVar != "" {
		if v := strings.TrimSpace(os.Getenv(spec.EnvVar)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(spec.Default)
}

// effectiveChannel 把磁盘配置 + 环境变量解析成适配器可直接用的渠道配置。
// 适配器拿到的一定是「已解析」的值——适配器里不该再碰 os.Getenv
// （否则 25 个文件各自决定读哪个变量，行为不可预测也无法测试）。
func effectiveChannel(p Provider, raw ChannelConfig) ChannelConfig {
	out := ChannelConfig{
		APIKey:   ResolveAPIKey(raw, p),
		BaseURL:  ResolveBaseURL(raw),
		Disabled: raw.Disabled,
	}
	specs := p.Options()
	if len(specs) == 0 {
		return out
	}
	// 只解析适配器**声明过**的键：磁盘上多出来的键不往适配器传，
	// 免得手写配置里一个拼错的键被当成有效设置悄悄生效（用户以为配上了）。
	out.Options = make(map[string]string, len(specs))
	for _, spec := range specs {
		if v := ResolveOption(raw, spec); v != "" {
			out.Options[spec.Key] = v
		}
	}
	return out
}
