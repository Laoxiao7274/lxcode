package websearch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 渠道私有设置项（OptionSpec）的解析与持久化。
//
// 这一层的存在理由：Bright Data 的 SERP zone、Mistral 的模型与档位、Firecrawl 的
// API 版本此前只能靠环境变量配——设置面板配不出一个能用的 Bright Data 渠道。
// 改成设置项之后必须同时满足三件事，缺一条都会退回「配了不生效」：
//  1. 面板填的值能存进 search.json 并原样解析回适配器；
//  2. 环境变量仍是回退（老用户的配置不能因为加了 UI 就失效）；
//  3. 默认值在两者都空时兜底（Mistral 的模型/档位、Firecrawl 的版本都靠它）。

// ---------------------------------------------------------- 解析优先级

// 解析顺序必须是「配置 → 环境变量 → 默认值」。
func TestResolveOptionPrecedence(t *testing.T) {
	spec := OptionSpec{Key: "zone", Label: "Z", EnvVar: "LXCODE_TEST_OPT_ZONE", Default: "from-default"}
	t.Setenv(spec.EnvVar, "from-env")

	// ① 配置里有值 → 配置赢（用户显式填的最优先）。
	if got := ResolveOption(ChannelConfig{Options: map[string]string{"zone": "from-config"}}, spec); got != "from-config" {
		t.Errorf("配置值应优先，实际 %q", got)
	}
	// ② 配置空 → 环境变量。
	if got := ResolveOption(ChannelConfig{}, spec); got != "from-env" {
		t.Errorf("配置空时应取环境变量，实际 %q", got)
	}
	// ③ 配置只有空白 → 也算空（用户清空输入框 = 没配）。
	if got := ResolveOption(ChannelConfig{Options: map[string]string{"zone": "   "}}, spec); got != "from-env" {
		t.Errorf("空白配置应视为未配置，实际 %q", got)
	}
	// ④ 环境变量也空 → 默认值。
	t.Setenv(spec.EnvVar, "")
	if got := ResolveOption(ChannelConfig{}, spec); got != "from-default" {
		t.Errorf("两者都空时应取默认值，实际 %q", got)
	}
}

// 没有 EnvVar 与 Default 的设置项：解析不出值就是空串（调用方据此判「未配置」）。
func TestResolveOptionWithoutFallback(t *testing.T) {
	spec := OptionSpec{Key: "zone", Label: "Z"}
	if got := ResolveOption(ChannelConfig{}, spec); got != "" {
		t.Errorf("无回退来源时应为空串，实际 %q", got)
	}
}

// effectiveChannel 只解析适配器**声明过**的键。
//
// 磁盘上多出来的键（手写配置里拼错的键名）不该往适配器传：传了就是
// 「用户以为配上了、实际没生效」——这正是设置项这一层要消灭的状态。
func TestEffectiveChannelDropsUndeclaredOptions(t *testing.T) {
	p := newBrightdata()
	ch := effectiveChannel(p, ChannelConfig{
		APIKey: "k",
		Options: map[string]string{
			"zone":  "my_zone",
			"zonne": "typo-should-be-dropped",
		},
	})
	if ch.Options["zone"] != "my_zone" {
		t.Errorf("声明过的键应被解析，实际 %v", ch.Options)
	}
	if _, present := ch.Options["zonne"]; present {
		t.Errorf("未声明的键不该传下去: %v", ch.Options)
	}
}

// 没有设置项的渠道不该凭空得到 Options（避免下游误以为它有设置）。
func TestEffectiveChannelNoOptions(t *testing.T) {
	p := newTavily()
	ch := effectiveChannel(p, ChannelConfig{APIKey: "k", Options: map[string]string{"zone": "x"}})
	if len(ch.Options) != 0 {
		t.Errorf("无设置项的渠道不该有 Options: %v", ch.Options)
	}
}

// ------------------------------------------------------- 三个渠道的实际取值

// Bright Data：面板填的 zone 必须真的进请求体。
func TestBrightdataZoneFromConfig(t *testing.T) {
	t.Setenv(brightdataZoneOption.EnvVar, "from-env-zone")
	srv, cap := captureServer(t, 200, `{"organic":[]}`)
	p := newBrightdata()
	ch := effectiveChannel(p, ChannelConfig{
		APIKey:  "bd-key",
		BaseURL: srv.URL,
		Options: map[string]string{"zone": "from-config-zone"},
	})
	if _, err := p.Search(t.Context(), ch, "q", Options{}); err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if got := decodeBody(t, cap.Body)["zone"]; got != "from-config-zone" {
		t.Errorf("zone = %v，期望配置值（配置应盖过环境变量）", got)
	}
}

// Mistral：面板填的模型与档位必须真的进请求体。
func TestMistralSearchOptionsFromConfig(t *testing.T) {
	pinMistralSearchEnv(t)
	srv, cap := captureServer(t, 200, `{"outputs":[{"type":"message.output","content":"答案"}]}`)
	p := newMistralSearch()
	ch := effectiveChannel(p, ChannelConfig{
		APIKey:  "m-key",
		BaseURL: srv.URL,
		Options: map[string]string{"model": "mistral-large-latest", "tool": mistralSearchPremiumTool},
	})
	if _, err := p.Search(t.Context(), ch, "q", Options{}); err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	body := decodeBody(t, cap.Body)
	if body["model"] != "mistral-large-latest" {
		t.Errorf("model = %v，期望配置值", body["model"])
	}
	tools, _ := body["tools"].([]any)
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != mistralSearchPremiumTool {
		t.Errorf("档位 = %v，期望配置值", tool["type"])
	}
}

// Firecrawl：面板填的版本必须真的进请求路径。
func TestFirecrawlVersionFromConfig(t *testing.T) {
	pinFirecrawlEnv(t)
	srv, cap := captureServer(t, 200, `{"success":true,"data":{"web":[{"url":"https://a.com/1","title":"A"}]}}`)
	p := newFirecrawl()
	ch := effectiveChannel(p, ChannelConfig{
		BaseURL: srv.URL,
		Options: map[string]string{"api_version": "v1"},
	})
	if _, err := p.Search(t.Context(), ch, "q", Options{}); err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if cap.Path != "/v1/search" {
		t.Errorf("路径 = %s，期望 /v1/search（面板选的版本没生效）", cap.Path)
	}
}

// 未配置时各渠道的默认值必须真的生效（面板上「留空即用默认」的承诺）。
func TestOptionDefaultsApply(t *testing.T) {
	pinMistralSearchEnv(t)
	pinFirecrawlEnv(t)

	mistral := effectiveChannel(newMistralSearch(), ChannelConfig{APIKey: "k"})
	if mistral.Options["model"] != mistralSearchDefaultModel {
		t.Errorf("mistral model 默认值没生效: %v", mistral.Options)
	}
	if mistral.Options["tool"] != mistralSearchDefaultTool {
		t.Errorf("mistral tool 默认值没生效: %v", mistral.Options)
	}

	fc := effectiveChannel(newFirecrawl(), ChannelConfig{BaseURL: "https://fc.example.com"})
	if fc.Options["api_version"] != firecrawlDefaultAPIVersion {
		t.Errorf("firecrawl 版本默认值没生效: %v", fc.Options)
	}
}

// brightdata 的 zone 没有默认值：没填就是空（渠道不就绪），不能编一个值出来。
func TestBrightdataZoneHasNoDefault(t *testing.T) {
	t.Setenv(brightdataZoneOption.EnvVar, "")
	p := newBrightdata()
	ch := effectiveChannel(p, ChannelConfig{APIKey: "k"})
	if ch.Options["zone"] != "" {
		t.Errorf("zone 不该有默认值，实际 %q", ch.Options["zone"])
	}
	if p.Configured(ch) {
		t.Error("缺 zone 时不该就绪")
	}
}

// --------------------------------------------------- 落盘与摘要

// 设置项必须原样落盘并能读回（面板保存 → 重启后端仍生效）。
func TestOptionsPersistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.json")
	cfg := NewConfig()
	cfg.Channels["brightdata"] = ChannelConfig{
		APIKey:  "bd-key",
		Options: map[string]string{"zone": "my_serp_zone"},
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	back, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got := back.Channels["brightdata"].Options["zone"]; got != "my_serp_zone" {
		t.Errorf("zone 没读回来: %q（%+v）", got, back.Channels["brightdata"])
	}
	// 磁盘形状必须是 options 对象（不是被拍平成 zone 字段）。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("磁盘 JSON 坏了: %v", err)
	}
	chans, _ := probe["channels"].(map[string]any)
	bd, _ := chans["brightdata"].(map[string]any)
	if _, ok := bd["options"].(map[string]any); !ok {
		t.Errorf("磁盘上应是 options 对象: %s", raw)
	}
}

// 摘要必须能察觉设置项**值**的变化（否则手改配置后热加载不广播），
// 但不得把值本身写进摘要（摘要可能被打印，值可能是凭据）。
func TestSnapshotDetectsOptionChangeWithoutLeaking(t *testing.T) {
	mk := func(v string) *Config {
		c := NewConfig()
		c.Channels["x"] = ChannelConfig{Options: map[string]string{"zone": "zone-" + v}}
		return c
	}
	if mk("a").snapshot() == mk("b").snapshot() {
		t.Error("设置项值变了，摘要却没变（热加载不会广播）")
	}
	if strings.Contains(mk("super-secret").snapshot(), "super-secret") {
		t.Errorf("摘要不该包含设置项明文: %s", mk("super-secret").snapshot())
	}
}

// clone 必须深拷贝 Options：否则读侧拿到的副本与内部状态共享同一个 map，
// 一次「只读」调用里的写入就会改到服务端状态。
func TestConfigCloneDeepCopiesOptions(t *testing.T) {
	cfg := NewConfig()
	cfg.Channels["x"] = ChannelConfig{Options: map[string]string{"zone": "orig"}}
	cp := cfg.clone()
	cp.Channels["x"].Options["zone"] = "mutated"
	if got := cfg.Channels["x"].Options["zone"]; got != "orig" {
		t.Errorf("改副本污染了原配置: %q", got)
	}
}

// 空白值等于没配：落盘时就该丢掉，否则摘要会显示「配了」而实际解析为空。
func TestNormalizeOptionsDropsBlank(t *testing.T) {
	if got := normalizeOptions(map[string]string{"a": "  ", "b": " v "}); len(got) != 1 || got["b"] != "v" {
		t.Errorf("normalizeOptions = %v，期望只留 b=v", got)
	}
	if got := normalizeOptions(map[string]string{"a": " "}); got != nil {
		t.Errorf("全空应返回 nil（磁盘上整键省略），实际 %v", got)
	}
	if got := normalizeOptions(nil); got != nil {
		t.Errorf("nil 应返回 nil，实际 %v", got)
	}
}

// ChannelView 必须把声明与取值一起给前端——面板靠它渲染输入项。
func TestChannelViewCarriesOptionSpecs(t *testing.T) {
	p := newBrightdata()
	ch := effectiveChannel(p, ChannelConfig{APIKey: "k", Options: map[string]string{"zone": "z1"}})
	view := ChannelView(p, ch, true, false)
	if len(view.OptionSpecs) != 1 || view.OptionSpecs[0].Key != "zone" {
		t.Fatalf("OptionSpecs 没带给前端: %+v", view.OptionSpecs)
	}
	if view.Options["zone"] != "z1" {
		t.Errorf("Options 没带给前端: %v", view.Options)
	}
	if !view.OptionSpecs[0].Required {
		t.Error("zone 应标为必填（面板要据此提示）")
	}
}
