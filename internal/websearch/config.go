package websearch

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/moyunteng/lxcode/internal/atomicfile"
)

// fileVersion 是 search.json 的磁盘格式版本。
// 与 models.json 同款纪律：不匹配整份拒绝（宁可报错也不半懂地读）。
const fileVersion = 1

// Config 是 search.json 的磁盘形状。
//
// 只存「用户配置」——渠道的名称/说明/文档链接/是否需要 key 全在代码里
// （presetProviders）。这样新增渠道零迁移：老配置照样能读，新渠道自动出现
// 在 UI 里（未配置状态）。
type Config struct {
	Version  int                      `json:"version"`
	Primary  string                   `json:"primary,omitempty"`
	Channels map[string]ChannelConfig `json:"channels,omitempty"`
}

// ChannelConfig 是单个渠道的用户配置。
//
// Disabled 用「反向布尔」是刻意的：零值必须是「启用」——渠道出现在配置里
// 就意味着用户配了它，配了却要停用是少数派。若用 Enabled 正向布尔，
// 零值 = 停用，于是「填了 key 却没生效」成为默认行为（这是最难查的一类 bug）。
type ChannelConfig struct {
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	// Options 是渠道私有设置（Bright Data 的 zone、Mistral 的档位等）。
	//
	// 键名由适配器的 OptionSpec 声明，这里只存用户填的值——磁盘不认识语义，
	// 所以给渠道加设置项不需要动磁盘格式（老配置照样能读）。
	Options  map[string]string `json:"options,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`
}

// Enabled 报告该渠道是否启用（Disabled 的反）。
func (c ChannelConfig) Enabled() bool { return !c.Disabled }

// NewConfig 返回空配置（无任何渠道配置，主渠道为空）。
func NewConfig() *Config {
	return &Config{Version: fileVersion, Channels: map[string]ChannelConfig{}}
}

// LoadConfig 读 search.json。文件不存在时返回空配置（首次运行不是错误）；
// 版本不匹配或 JSON 坏则报错——静默降级会让用户以为「配置生效了」。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewConfig(), nil
		}
		return nil, fmt.Errorf("读取搜索配置失败: %w", err)
	}
	// 空文件（用户手建了但没写内容）按空配置处理，不报错。
	if len(strings.TrimSpace(string(data))) == 0 {
		return NewConfig(), nil
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	if cfg.Version != fileVersion {
		return nil, fmt.Errorf("搜索配置版本不支持: 期望 %d，实际 %d（%s）", fileVersion, cfg.Version, path)
	}
	if cfg.Channels == nil {
		cfg.Channels = map[string]ChannelConfig{}
	}
	return &cfg, nil
}

// SaveConfig 原子写 search.json（同目录临时文件 + fsync + rename）。
func SaveConfig(path string, cfg *Config) error {
	cfg.Version = fileVersion
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化搜索配置失败: %w", err)
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建配置目录失败: %w", err)
		}
	}
	if err := atomicfile.Write(path, data); err != nil {
		return fmt.Errorf("写入搜索配置失败: %w", err)
	}
	return nil
}

// channelIDs 返回配置里出现过的渠道 id（排序，便于确定性比较与测试）。
func (c *Config) channelIDs() []string {
	out := make([]string, 0, len(c.Channels))
	for id := range c.Channels {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// snapshot 生成配置的确定性摘要（热加载变更检测用，对齐 models.json 的 snapshot）。
//
// 摘要里**不出现任何明文凭据**：key 只记「有没有」，设置项的值只记哈希——
// 摘要的用途只是「变了没有」，哈希足够回答这个问题，而明文一旦进了摘要，
// 将来某次顺手把它打进日志就是一次泄漏（TestSnapshotOmitsKey 钉住这条）。
func (c *Config) snapshot() string {
	var b strings.Builder
	b.WriteString("primary=")
	b.WriteString(c.Primary)
	for _, id := range c.channelIDs() {
		ch := c.Channels[id]
		// key 不进摘要：摘要会进日志，而 key 不该进日志。
		fmt.Fprintf(&b, "|%s:%t:%t:%s", id, ch.Enabled(), ch.APIKey != "", ch.BaseURL)
		for _, k := range optionKeys(ch.Options) {
			// 键名进摘要（明文，便于看出「哪个设置变了」），值只进哈希
			// （既要检测到值的变化，又不把值本身放进可能被打印的字符串）。
			fmt.Fprintf(&b, ":%s=%s", k, optionDigest(ch.Options[k]))
		}
	}
	return b.String()
}

// optionKeys 返回设置项的键名（排序，保证摘要稳定）。
func optionKeys(opts map[string]string) []string {
	out := make([]string, 0, len(opts))
	for k := range opts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// optionDigest 把设置项的值折成一个短哈希（变更检测用，不泄漏明文）。
func optionDigest(v string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(v))
	return strconv.FormatUint(h.Sum64(), 16)
}

// clone 深拷贝（读侧返回副本，避免调用方改到共享状态）。
//
// Options 必须逐份复制：map 是引用类型，浅拷贝会让「读配置的人」和
// 「改配置的人」共享同一个 map——一次保存就悄悄改掉了别人手里的快照。
func (c *Config) clone() *Config {
	out := &Config{Version: c.Version, Primary: c.Primary, Channels: make(map[string]ChannelConfig, len(c.Channels))}
	for k, v := range c.Channels {
		if v.Options != nil {
			opts := make(map[string]string, len(v.Options))
			for ok, ov := range v.Options {
				opts[ok] = ov
			}
			v.Options = opts
		}
		out.Channels[k] = v
	}
	return out
}

// normalizeOptions 清洗设置项：去空白、丢弃空值（空值等于「没配」，
// 留着会让摘要与「未配置」状态不一致）；全空则返回 nil（磁盘上整键省略）。
func normalizeOptions(opts map[string]string) map[string]string {
	if len(opts) == 0 {
		return nil
	}
	out := make(map[string]string, len(opts))
	for k, v := range opts {
		if t := strings.TrimSpace(v); t != "" {
			out[k] = t
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
