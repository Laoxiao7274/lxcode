package websearch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProvider 是可编程的渠道适配器：按预设的「返回或报错」作答，
// 并记录自己被调用了几次（断言降级链是否真的走了/没走）。
type fakeProvider struct {
	base
	resp  Response
	err   error
	calls int
}

func newFake(id string, needsKey bool) *fakeProvider {
	return &fakeProvider{base: base{id: id, label: id, needsKey: needsKey}}
}

func (f *fakeProvider) Search(context.Context, ChannelConfig, string, Options) (Response, error) {
	f.calls++
	if f.err != nil {
		return Response{}, f.err
	}
	return f.resp, nil
}

// testService 构造注入了假渠道的服务（配置文件落在 t.TempDir）。
func testService(t *testing.T, providers []Provider, cfg *Config) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "search.json")
	if cfg == nil {
		cfg = NewConfig()
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("准备配置失败: %v", err)
	}
	return newServiceWith(path, cfg, providers), path
}

// writeFileForTest 直接写文件（构造坏配置用，绕过 SaveConfig 的校验）。
func writeFileForTest(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestSearchPrimaryFirst(t *testing.T) {
	primary := newFake("primary", true)
	secondary := newFake("secondary", true)
	primary.resp = Response{Results: []Result{{Title: "A", URL: "https://a.com"}}}
	secondary.resp = Response{Results: []Result{{Title: "B", URL: "https://b.com"}}}

	cfg := NewConfig()
	cfg.Primary = "primary"
	cfg.Channels["primary"] = ChannelConfig{APIKey: "k"}
	cfg.Channels["secondary"] = ChannelConfig{APIKey: "k"}

	svc, _ := testService(t, []Provider{secondary, primary}, cfg)
	got, err := svc.Search(context.Background(), "q", Options{})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if got.Provider != "primary" {
		t.Errorf("应由主渠道作答，实际 %q", got.Provider)
	}
	if secondary.calls != 0 {
		t.Errorf("主渠道成功时不该调用备用渠道（实际 %d 次）", secondary.calls)
	}
	if got.Query != "q" {
		t.Errorf("响应应回填查询，实际 %q", got.Query)
	}
}

// 临时故障降级：主渠道 transient 失败 → 用备用渠道作答。
func TestSearchFallbackOnTransient(t *testing.T) {
	primary := newFake("primary", true)
	secondary := newFake("secondary", true)
	primary.err = &ProviderError{Provider: "primary", Kind: KindTransient, Status: 503, Message: "服务不可用"}
	secondary.resp = Response{Results: []Result{{Title: "B", URL: "https://b.com"}}}

	cfg := NewConfig()
	cfg.Primary = "primary"
	cfg.Channels["primary"] = ChannelConfig{APIKey: "k"}
	cfg.Channels["secondary"] = ChannelConfig{APIKey: "k"}

	svc, _ := testService(t, []Provider{primary, secondary}, cfg)
	got, err := svc.Search(context.Background(), "q", Options{})
	if err != nil {
		t.Fatalf("应降级成功，实际报错: %v", err)
	}
	if got.Provider != "secondary" {
		t.Errorf("应由备用渠道作答，实际 %q", got.Provider)
	}
	if primary.calls != 1 {
		t.Errorf("主渠道应被尝试一次，实际 %d", primary.calls)
	}
}

// 凭证错不降级：换渠道不会修好 key，降级只会把配置问题掩盖成搜索成功。
func TestSearchNoFallbackOnCredential(t *testing.T) {
	primary := newFake("primary", true)
	secondary := newFake("secondary", true)
	primary.err = &ProviderError{Provider: "primary", Kind: KindCredential, Status: 401, Message: "key 无效"}
	secondary.resp = Response{Results: []Result{{Title: "B", URL: "https://b.com"}}}

	cfg := NewConfig()
	cfg.Primary = "primary"
	cfg.Channels["primary"] = ChannelConfig{APIKey: "bad"}
	cfg.Channels["secondary"] = ChannelConfig{APIKey: "k"}

	svc, _ := testService(t, []Provider{primary, secondary}, cfg)
	_, err := svc.Search(context.Background(), "q", Options{})
	assertProviderError(t, err, KindCredential, 401)
	if secondary.calls != 0 {
		t.Errorf("凭证错不该降级到备用渠道（实际调用 %d 次）", secondary.calls)
	}
}

// 取消不降级：用户已经不要这次搜索了，换渠道只是白烧配额。
func TestSearchCancelStopsChain(t *testing.T) {
	primary := newFake("primary", true)
	secondary := newFake("secondary", true)
	primary.err = &ProviderError{Provider: "primary", Kind: KindTransient, Message: "x"}

	cfg := NewConfig()
	cfg.Primary = "primary"
	cfg.Channels["primary"] = ChannelConfig{APIKey: "k"}
	cfg.Channels["secondary"] = ChannelConfig{APIKey: "k"}

	svc, _ := testService(t, []Provider{primary, secondary}, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Search(ctx, "q", Options{}); err == nil {
		t.Fatal("取消时应报错")
	}
	if secondary.calls != 0 {
		t.Errorf("取消后不该继续降级（实际调用 %d 次）", secondary.calls)
	}
}

func TestSearchNoChannelsConfigured(t *testing.T) {
	p := newFake("p", true)
	svc, _ := testService(t, []Provider{p}, NewConfig())
	_, err := svc.Search(context.Background(), "q", Options{})
	pe := assertProviderError(t, err, KindConfig, 0)
	if !strings.Contains(pe.Message, "未配置") {
		t.Errorf("错误消息应指引用户去配置渠道: %s", pe.Message)
	}
}

func TestSearchAllFailAggregates(t *testing.T) {
	a := newFake("a", true)
	b := newFake("b", true)
	a.err = &ProviderError{Provider: "a", Kind: KindTransient, Message: "a 挂了"}
	b.err = &ProviderError{Provider: "b", Kind: KindQuota, Message: "b 超限"}

	cfg := NewConfig()
	cfg.Channels["a"] = ChannelConfig{APIKey: "k"}
	cfg.Channels["b"] = ChannelConfig{APIKey: "k"}

	svc, _ := testService(t, []Provider{a, b}, cfg)
	_, err := svc.Search(context.Background(), "q", Options{})
	pe := assertProviderError(t, err, KindTransient, 0)
	if !strings.Contains(pe.Message, "a 挂了") || !strings.Contains(pe.Message, "b 超限") {
		t.Errorf("汇总错误应含每个渠道的失败原因: %s", pe.Message)
	}
}

// 停用的渠道不进降级链。
func TestSearchSkipsDisabled(t *testing.T) {
	a := newFake("a", true)
	cfg := NewConfig()
	cfg.Channels["a"] = ChannelConfig{APIKey: "k", Disabled: true}
	svc, _ := testService(t, []Provider{a}, cfg)
	if _, err := svc.Search(context.Background(), "q", Options{}); err == nil {
		t.Fatal("渠道停用时应报「未配置」")
	}
	if a.calls != 0 {
		t.Errorf("停用的渠道不该被调用（实际 %d 次）", a.calls)
	}
}

// SearchWith 只打一个渠道：用户点「测试」就是想验证这一个，
// 降级会把「这个渠道坏了」测成「搜索正常」。
func TestSearchWithDoesNotFallback(t *testing.T) {
	a := newFake("a", true)
	b := newFake("b", true)
	a.err = &ProviderError{Provider: "a", Kind: KindTransient, Message: "a 挂了"}
	b.resp = Response{Results: []Result{{Title: "B", URL: "https://b.com"}}}

	cfg := NewConfig()
	cfg.Channels["a"] = ChannelConfig{APIKey: "k"}
	cfg.Channels["b"] = ChannelConfig{APIKey: "k"}

	svc, _ := testService(t, []Provider{a, b}, cfg)
	if _, err := svc.SearchWith(context.Background(), "a", "q", Options{}); err == nil {
		t.Fatal("指定渠道失败时应如实报错，不该降级")
	}
	if b.calls != 0 {
		t.Errorf("SearchWith 不该调用其它渠道（实际 %d 次）", b.calls)
	}
}

func TestSearchWithUnconfigured(t *testing.T) {
	a := newFake("a", true)
	svc, _ := testService(t, []Provider{a}, NewConfig())
	_, err := svc.SearchWith(context.Background(), "a", "q", Options{})
	assertProviderError(t, err, KindConfig, 0)
}

func TestSearchWithUnknownChannel(t *testing.T) {
	svc, _ := testService(t, []Provider{newFake("a", true)}, NewConfig())
	if _, err := svc.SearchWith(context.Background(), "不存在", "q", Options{}); err == nil {
		t.Fatal("未知渠道应报错")
	}
}

// 保存渠道：落盘 + 首次配置自动设为主渠道 + 广播通知。
func TestSaveChannelPersistsAndNotifies(t *testing.T) {
	a := newFake("a", true)
	svc, path := testService(t, []Provider{a}, NewConfig())
	notified := 0
	svc.SetNotifier(func() { notified++ })

	if err := svc.SaveChannel("a", ChannelConfig{APIKey: "  secret  "}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if notified != 1 {
		t.Errorf("应广播一次变更，实际 %d", notified)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("重读配置失败: %v", err)
	}
	if got := reloaded.Channels["a"].APIKey; got != "secret" {
		t.Errorf("key 应去空白后落盘，实际 %q", got)
	}
	if reloaded.Primary != "a" {
		t.Errorf("首次配置应自动设为主渠道，实际 %q", reloaded.Primary)
	}
}

func TestSaveChannelRejectsUnknown(t *testing.T) {
	svc, _ := testService(t, []Provider{newFake("a", true)}, NewConfig())
	if err := svc.SaveChannel("不存在", ChannelConfig{APIKey: "k"}); err == nil {
		t.Fatal("未知渠道应拒绝保存")
	}
}

// 自建渠道必须有地址：存下去也是「未就绪」的迷惑状态，存之前拦住。
func TestSaveChannelRequiresBaseURLForSelfHosted(t *testing.T) {
	selfHosted := &fakeProvider{base: base{id: "self", label: "self", needsBaseURL: true}}
	svc, _ := testService(t, []Provider{selfHosted}, NewConfig())
	if err := svc.SaveChannel("self", ChannelConfig{}); err == nil {
		t.Fatal("缺实例地址应拒绝保存")
	}
	if err := svc.SaveChannel("self", ChannelConfig{BaseURL: "ftp://x"}); err == nil {
		t.Fatal("非 http(s) 地址应拒绝保存")
	}
	if err := svc.SaveChannel("self", ChannelConfig{BaseURL: "https://x.example.com/"}); err != nil {
		t.Fatalf("合法地址应保存成功: %v", err)
	}
}

// 删除主渠道后应回落到首个就绪渠道，而不是留下一个指向不存在渠道的 primary。
func TestRemoveChannelRebindsPrimary(t *testing.T) {
	a := newFake("a", true)
	b := newFake("b", true)
	cfg := NewConfig()
	cfg.Primary = "a"
	cfg.Channels["a"] = ChannelConfig{APIKey: "k"}
	cfg.Channels["b"] = ChannelConfig{APIKey: "k"}

	svc, _ := testService(t, []Provider{a, b}, cfg)
	if err := svc.RemoveChannel("a"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if got := svc.Config().Primary; got != "b" {
		t.Errorf("主渠道应回落到 b，实际 %q", got)
	}
}

func TestSetPrimaryRejectsUnknown(t *testing.T) {
	svc, _ := testService(t, []Provider{newFake("a", true)}, NewConfig())
	if err := svc.SetPrimary("不存在"); err == nil {
		t.Fatal("未知渠道应拒绝设为主渠道")
	}
	if err := svc.SetPrimary(""); err != nil {
		t.Errorf("清空主渠道应允许: %v", err)
	}
}

// 环境变量间接引用：$VAR 形态的 key 从环境取（配置不进版本库的标准做法）。
func TestResolveAPIKeyFromEnvIndirection(t *testing.T) {
	t.Setenv("MY_TEST_SEARCH_KEY", "from-env")
	p := newFake("a", true)
	if got := ResolveAPIKey(ChannelConfig{APIKey: "$MY_TEST_SEARCH_KEY"}, p); got != "from-env" {
		t.Errorf("$VAR 间接应取到环境值，实际 %q", got)
	}
	// 约定变量（渠道自带 EnvVar）在没有配置时兜底。
	p2 := &fakeProvider{base: base{id: "b", label: "b", envVar: "MY_CONVENTION_KEY", needsKey: true}}
	t.Setenv("MY_CONVENTION_KEY", "convention")
	if got := ResolveAPIKey(ChannelConfig{}, p2); got != "convention" {
		t.Errorf("约定环境变量应兜底，实际 %q", got)
	}
	// 通用前缀兜底。
	t.Setenv("LXCODE_SEARCH_B", "prefixed")
	if got := ResolveAPIKey(ChannelConfig{}, newFake("b", true)); got != "prefixed" {
		t.Errorf("通用前缀环境变量应兜底，实际 %q", got)
	}
}

// 环境变量名打错时不该直接判定「未配置」——约定变量还有机会救回来。
func TestResolveAPIKeyFallsThroughOnBadIndirection(t *testing.T) {
	p := &fakeProvider{base: base{id: "c", label: "c", envVar: "GOOD_KEY", needsKey: true}}
	t.Setenv("GOOD_KEY", "good")
	if got := ResolveAPIKey(ChannelConfig{APIKey: "$TYPO_KEY"}, p); got != "good" {
		t.Errorf("间接引用取不到值时应回落到约定变量，实际 %q", got)
	}
}

// 配置版本不匹配整份拒绝（宁可报错也不半懂地读）。
func TestLoadConfigRejectsVersionMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.json")
	if err := writeFileForTest(path, `{"version": 99, "channels": {}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("版本不匹配应报错")
	}
}

// 文件不存在 = 空配置（首次运行不是错误）。
func TestLoadConfigMissingFileIsEmpty(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("缺文件不该报错: %v", err)
	}
	if cfg.Primary != "" || len(cfg.Channels) != 0 {
		t.Errorf("缺文件应得到空配置，实际 %+v", cfg)
	}
}

// 坏 JSON 报错（静默降级会让用户以为配置生效了）。
func TestLoadConfigRejectsBrokenJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.json")
	if err := writeFileForTest(path, `{not json`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
}

func TestReloadDetectsChange(t *testing.T) {
	a := newFake("a", true)
	svc, path := testService(t, []Provider{a}, NewConfig())

	changed, err := svc.Reload()
	if err != nil {
		t.Fatalf("重读失败: %v", err)
	}
	if changed {
		t.Error("未改动时不该报变更")
	}

	cfg := NewConfig()
	cfg.Channels["a"] = ChannelConfig{APIKey: "new"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	changed, err = svc.Reload()
	if err != nil {
		t.Fatalf("重读失败: %v", err)
	}
	if !changed {
		t.Error("改动后应报变更")
	}
}

// 摘要不含 key：摘要会进日志，key 不该进日志。
func TestSnapshotOmitsKey(t *testing.T) {
	cfg := NewConfig()
	cfg.Channels["a"] = ChannelConfig{APIKey: "super-secret"}
	if s := cfg.snapshot(); strings.Contains(s, "super-secret") {
		t.Errorf("摘要不该包含 key: %s", s)
	}
}

func TestReadyReportsConfiguredChannel(t *testing.T) {
	a := newFake("a", true)
	svc, _ := testService(t, []Provider{a}, NewConfig())
	if svc.Ready() {
		t.Error("未配置时 Ready 应为 false")
	}
	if err := svc.SaveChannel("a", ChannelConfig{APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if !svc.Ready() {
		t.Error("配置后 Ready 应为 true")
	}
}

func TestChannelsViewReflectsConfig(t *testing.T) {
	a := newFake("a", true)
	cfg := NewConfig()
	cfg.Primary = "a"
	cfg.Channels["a"] = ChannelConfig{APIKey: "k"}
	svc, _ := testService(t, []Provider{a}, cfg)
	chs := svc.Channels()
	if len(chs) != 1 {
		t.Fatalf("渠道数 = %d，期望 1", len(chs))
	}
	if !chs[0].Configured || !chs[0].Primary || !chs[0].Enabled {
		t.Errorf("渠道视图状态不对: %+v", chs[0])
	}
}

// 取消类错误在链里应原样上抛（不是被包成「全部渠道失败」）。
func TestSearchCancelErrorSurfaces(t *testing.T) {
	a := newFake("a", true)
	a.err = &ProviderError{Provider: "a", Kind: KindAborted, Err: context.Canceled}
	cfg := NewConfig()
	cfg.Channels["a"] = ChannelConfig{APIKey: "k"}
	svc, _ := testService(t, []Provider{a}, cfg)
	_, err := svc.Search(context.Background(), "q", Options{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("取消错误应可穿透 errors.Is，实际 %v", err)
	}
}

// ---------- 零配置渠道的显式启用语义（opt-in） ----------

// ---------- 刻意默认就绪的渠道（defaultReady） ----------

// defaultReady 渠道**不需要任何配置**就就绪，并在没有主渠道时自然成为首选——
// 这是「开箱即用的默认渠道」的语义（Exa 的免配置通道）。
//
// 与 optIn 的区别是刻意的：optIn 要用户显式启用（防意外抓外部页面），
// defaultReady 是产品决策（用户拍板 Exa 开箱可用），两个标记互斥。
func TestDefaultReadyChannelReadyWithoutConfig(t *testing.T) {
	def := &fakeProvider{base: base{id: "def", label: "def", defaultReady: true}}
	def.resp = Response{Results: []Result{{Title: "A", URL: "https://a.com"}}}
	paid := newFake("paid", true)

	svc, _ := testService(t, []Provider{def, paid}, NewConfig())

	if !svc.Ready() {
		t.Fatal("有 defaultReady 渠道时 Ready 应为 true（不配置也能搜）")
	}
	chs := svc.Channels()
	for _, ch := range chs {
		if ch.ID == "def" {
			if !ch.Configured {
				t.Error("defaultReady 渠道未配置时也该就绪")
			}
			if !ch.DefaultReady {
				t.Error("渠道视图应标明 defaultReady（前端据此提示开箱可用）")
			}
		}
	}
	// 没配主渠道 → 预设顺序里第一个就绪的就是它。
	got, err := svc.Search(context.Background(), "q", Options{})
	if err != nil {
		t.Fatalf("defaultReady 渠道应能作答: %v", err)
	}
	if got.Provider != "def" {
		t.Errorf("应由 defaultReady 渠道作答，实际 %q", got.Provider)
	}
	if paid.calls != 0 {
		t.Errorf("默认渠道成功时不该调用需要 key 的渠道（实际 %d 次）", paid.calls)
	}

	// 用户仍可停用（产品决策不等于不可关闭）。
	if err := svc.SaveChannel("def", ChannelConfig{Disabled: true}); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	if svc.Ready() {
		t.Error("停用 defaultReady 渠道后（且无其它就绪渠道）Ready 应为 false")
	}
}

// Exa 是内置渠道里唯一的 defaultReady——「不配置也能搜」的默认渠道。
func TestExaIsTheDefaultReadyChannel(t *testing.T) {
	var found []string
	for _, p := range presetProviders() {
		if p.DefaultReady() {
			found = append(found, p.ID())
		}
	}
	if len(found) != 1 || found[0] != "exa" {
		t.Errorf("内置 defaultReady 渠道 = %v，期望只有 exa", found)
	}
	// 真装配路径：空配置下 Exa 必须排在降级链首位（前面几个都需要凭据/地址）。
	svc, _ := testService(t, presetProviders(), NewConfig())
	order := svc.chainOrder(svc.cfg.clone())
	if len(order) == 0 || order[0].ID() != "exa" {
		ids := make([]string, 0, len(order))
		for _, p := range order {
			ids = append(ids, p.ID())
		}
		t.Errorf("空配置下降级链首位应是 exa，实际 %v", ids)
	}
}

// Stored 报告「配置文件里是否真的存在该条目」——前端据此决定要不要显示
// 「清除」按钮。不能用 Configured 代替：defaultReady 渠道不配也是就绪的，
// 也不能用「Options 有没有值」代替：后端给的是**已解析**取值（含默认值）。
func TestChannelViewStoredReflectsDiskEntry(t *testing.T) {
	def := &fakeProvider{base: base{
		id: "def", label: "def", defaultReady: true,
		options: []OptionSpec{{Key: "url", Label: "端点", Default: "https://default.example.com"}},
	}}
	svc, _ := testService(t, []Provider{def}, NewConfig())

	// 未配置：就绪（defaultReady）但 stored=false（磁盘上没有条目）。
	chs := svc.Channels()
	if len(chs) != 1 {
		t.Fatalf("渠道数 = %d", len(chs))
	}
	if !chs[0].Configured {
		t.Error("defaultReady 渠道未配置时也应就绪")
	}
	if chs[0].Stored {
		t.Error("磁盘上没有条目时 stored 应为 false（否则空卡片上会出现「清除」按钮）")
	}
	// 关键：已解析取值里有**默认值**，它不该被当成「用户配过」。
	if chs[0].Options["url"] != "https://default.example.com" {
		t.Errorf("应回带解析后的默认值，实际 %q", chs[0].Options["url"])
	}

	// 用户保存过（哪怕只填了一个非默认值）→ stored=true。
	if err := svc.SaveChannel("def", ChannelConfig{}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	chs = svc.Channels()
	if !chs[0].Stored {
		t.Error("保存过之后 stored 应为 true（这时「清除」才该出现）")
	}
}

// optIn 渠道（DuckDuckGo 这类零配置渠道）在用户显式启用前不算就绪——
// 「技术上能跑」不等于「用户想用它」。
func TestOptInChannelNotReadyUntilEnabled(t *testing.T) {
	zero := &fakeProvider{base: base{id: "zero", label: "zero", optIn: true}}
	paid := newFake("paid", true)
	svc, _ := testService(t, []Provider{zero, paid}, NewConfig())

	// 未配置：零配置渠道不该让 Ready 变 true，也不该入链。
	if svc.Ready() {
		t.Error("只有 opt-in 渠道可用时 Ready 应为 false")
	}
	chs := svc.Channels()
	if len(chs) != 2 {
		t.Fatalf("渠道数 = %d", len(chs))
	}
	for _, ch := range chs {
		if ch.ID == "zero" && ch.Configured {
			t.Error("未显式启用的 opt-in 渠道不该报告就绪")
		}
	}
	if !chs[0].OptIn {
		t.Error("渠道视图应标明 opt-in（前端据此提示需手动开启）")
	}
	if _, err := svc.Search(context.Background(), "q", Options{}); err == nil {
		t.Error("没有任何就绪渠道时应报错")
	}

	// 显式启用后（写一条配置）才就绪。
	if err := svc.SaveChannel("zero", ChannelConfig{}); err != nil {
		t.Fatalf("启用零配置渠道不该要求 key/地址: %v", err)
	}
	if !svc.Ready() {
		t.Error("显式启用后 Ready 应为 true")
	}
	if _, err := svc.Search(context.Background(), "q", Options{}); err != nil {
		t.Errorf("显式启用后应能搜到: %v", err)
	}
}

// 停用（Disabled）优先于 opt-in 判定：显式启用过但已停用的渠道仍不就绪。
func TestOptInChannelDisabledStaysNotReady(t *testing.T) {
	zero := &fakeProvider{base: base{id: "zero", label: "zero", optIn: true}}
	cfg := NewConfig()
	cfg.Channels["zero"] = ChannelConfig{Disabled: true}
	svc, _ := testService(t, []Provider{zero}, cfg)
	if svc.Ready() {
		t.Error("已停用的 opt-in 渠道不该就绪")
	}
	// 单独测试该渠道时报「需先手动启用」而非含糊的「未配置」。
	_, err := svc.SearchWith(context.Background(), "zero", "q", Options{})
	pe := assertProviderError(t, err, KindConfig, 0)
	if !strings.Contains(pe.Message, "停用") && !strings.Contains(pe.Message, "启用") {
		t.Errorf("错误消息应说清原因: %s", pe.Message)
	}
}

// 非 opt-in 渠道不受显式启用要求影响（配了 key 就入链）——回归保护：
// 别把 opt-in 判定误加到所有渠道上。
func TestNonOptInChannelReadyWithKeyOnly(t *testing.T) {
	paid := newFake("paid", true)
	cfg := NewConfig()
	cfg.Channels["paid"] = ChannelConfig{APIKey: "k"}
	svc, _ := testService(t, []Provider{paid}, cfg)
	if !svc.Ready() {
		t.Error("配了 key 的普通渠道应就绪（无需额外显式启用）")
	}
}
