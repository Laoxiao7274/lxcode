package websearch

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Service 是搜索服务：持渠道配置、提供搜索（主渠道优先 + 降级链）
// 与配置读写。与 config.Registry 同构（原子写 + 周期热加载 + 变更广播）。
type Service struct {
	mu   sync.RWMutex
	path string
	cfg  *Config
	// providers 是渠道适配器列表（顺序即降级优先级）。
	// 生产环境恒为 presetProviders()；可注入是为了测试能控住降级链的
	// 成员与顺序——否则测降级就得去配真渠道的 key 打真网络。
	providers []Provider
	// notify 是变更回调（server 挂载后广播 search.changed）。
	// 在锁外调用——回调里会遍历客户端连接，持锁会拖住搜索。
	notify func()
}

// LoadService 从 search.json 加载服务。文件不存在 = 空配置（首次运行）。
func LoadService(path string) (*Service, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	return &Service{path: path, cfg: cfg, providers: presetProviders()}, nil
}

// newServiceWith 构造可注入渠道列表的服务（测试用）。
func newServiceWith(path string, cfg *Config, providers []Provider) *Service {
	if cfg == nil {
		cfg = NewConfig()
	}
	return &Service{path: path, cfg: cfg, providers: providers}
}

// providersLocked 返回渠道列表（生产恒非空）。
func (s *Service) providersLocked() []Provider {
	if len(s.providers) == 0 {
		return presetProviders()
	}
	return s.providers
}

// lookupProvider 在本服务的渠道列表里按 id 找适配器。
//
// 刻意不用包级的 ProviderByID：服务的渠道列表才是「有哪些渠道」的事实源，
// 两处各查一套会出现「能搜索但存不进配置」这类自相矛盾的状态。
func (s *Service) lookupProvider(id string) (Provider, bool) {
	for _, p := range s.providersLocked() {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// availableIDs 返回本服务可用渠道 id（错误消息里给用户看）。
func (s *Service) availableIDs() string {
	ps := s.providersLocked()
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID())
	}
	return strings.Join(ids, ", ")
}

// Path 返回配置文件路径。
func (s *Service) Path() string { return s.path }

// SetNotifier 挂载变更回调（server 用来广播 search.changed）。
func (s *Service) SetNotifier(fn func()) {
	s.mu.Lock()
	s.notify = fn
	s.mu.Unlock()
}

// Config 返回配置副本（调用方改不到内部状态）。
func (s *Service) Config() *Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.clone()
}

// Reload 从磁盘重读。返回是否有变更（供热加载决定是否广播）。
//
// 失败保留旧状态（对齐 models.json 热加载：一次手滑的编辑不该让搜索整体失效）。
func (s *Service) Reload() (bool, error) {
	cfg, err := LoadConfig(s.path)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	changed := cfg.snapshot() != s.cfg.snapshot()
	s.cfg = cfg
	s.mu.Unlock()
	return changed, nil
}

// Channels 返回全部内置渠道的视图（元数据 + 用户配置），按预设顺序。
func (s *Service) Channels() []Channel {
	s.mu.RLock()
	cfg := s.cfg.clone()
	providers := s.providersLocked()
	s.mu.RUnlock()
	return channelsOf(cfg, providers)
}

// channelsOf 由配置构造渠道视图列表。
func channelsOf(cfg *Config, providers []Provider) []Channel {
	out := make([]Channel, 0, len(providers))
	for _, p := range providers {
		raw, present := cfg.Channels[p.ID()]
		ch := effectiveChannel(p, raw)
		out = append(out, ChannelView(p, ch, present, cfg.Primary == p.ID()))
	}
	return out
}

// Ready 报告是否存在至少一个就绪渠道（工具据此给出「未配置」的明确指引）。
func (s *Service) Ready() bool {
	s.mu.RLock()
	cfg := s.cfg.clone()
	providers := s.providersLocked()
	s.mu.RUnlock()
	for _, p := range providers {
		if readyIn(cfg, p) {
			return true
		}
	}
	return false
}

// readyIn 判定渠道在当前配置下是否就绪（配置值够用 + opt-in 须显式启用过）。
func readyIn(cfg *Config, p Provider) bool {
	raw, present := cfg.Channels[p.ID()]
	return channelReady(p, effectiveChannel(p, raw), present)
}

// SaveChannel 保存（upsert）一个渠道的配置并落盘。
// 未知 id 报错——渠道适配器是代码，不是数据：没有适配器的「自定义渠道」
// 无从构造请求（自定义 HTTP 渠道需要另一套字段映射设计，不在本期）。
func (s *Service) SaveChannel(id string, ch ChannelConfig) error {
	p, ok := s.lookupProvider(id)
	if !ok {
		return fmt.Errorf("未知搜索渠道 %q（可用: %s）", id, s.availableIDs())
	}
	// 自建渠道必须有地址，否则存下去也是「未就绪」的迷惑状态——存之前拦住。
	if p.NeedsBaseURL() && ResolveBaseURL(ch) == "" {
		return fmt.Errorf("渠道 %s 需要填写实例地址", p.Label())
	}
	if ch.BaseURL != "" && !strings.HasPrefix(ch.BaseURL, "http://") && !strings.HasPrefix(ch.BaseURL, "https://") {
		return fmt.Errorf("实例地址必须是 http(s) 开头的完整地址: %q", ch.BaseURL)
	}
	s.mu.Lock()
	s.cfg.Channels[id] = ChannelConfig{
		APIKey:   strings.TrimSpace(ch.APIKey),
		BaseURL:  ResolveBaseURL(ch),
		Options:  normalizeOptions(ch.Options),
		Disabled: ch.Disabled,
	}
	// 首次配置主渠道：用户配好第一个渠道却没有任何渠道可用是纯损失，
	// 这里顺手补上（用户之后可以改）。
	if s.cfg.Primary == "" && !ch.Disabled {
		s.cfg.Primary = id
	}
	cfg := s.cfg.clone()
	notify := s.notify
	s.mu.Unlock()
	if err := SaveConfig(s.path, cfg); err != nil {
		return err
	}
	if notify != nil {
		notify()
	}
	return nil
}

// RemoveChannel 删除渠道配置（回到未配置状态）。
// 主渠道被删则回落首个就绪渠道，没有就绪渠道则清空。
func (s *Service) RemoveChannel(id string) error {
	if _, ok := s.lookupProvider(id); !ok {
		return fmt.Errorf("未知搜索渠道 %q", id)
	}
	s.mu.Lock()
	delete(s.cfg.Channels, id)
	if s.cfg.Primary == id {
		s.cfg.Primary = s.firstReadyLocked(s.cfg)
	}
	cfg := s.cfg.clone()
	notify := s.notify
	s.mu.Unlock()
	if err := SaveConfig(s.path, cfg); err != nil {
		return err
	}
	if notify != nil {
		notify()
	}
	return nil
}

// SetPrimary 设置主渠道（空串 = 清空，回落预设顺序的第一个就绪渠道）。
func (s *Service) SetPrimary(id string) error {
	if id != "" {
		if _, ok := s.lookupProvider(id); !ok {
			return fmt.Errorf("未知搜索渠道 %q", id)
		}
	}
	s.mu.Lock()
	s.cfg.Primary = id
	cfg := s.cfg.clone()
	notify := s.notify
	s.mu.Unlock()
	if err := SaveConfig(s.path, cfg); err != nil {
		return err
	}
	if notify != nil {
		notify()
	}
	return nil
}

// firstReadyLocked 返回预设顺序里第一个就绪渠道的 id（无则空串）。调用方须持锁。
func (s *Service) firstReadyLocked(cfg *Config) string {
	for _, p := range s.providersLocked() {
		if readyIn(cfg, p) {
			return p.ID()
		}
	}
	return ""
}

// Search 按「主渠道优先 → 预设顺序」依次尝试，按错误类型决定是否继续降级。
//
// 降级判定只有 ShouldFallback 一个依据：凭证错/参数错立刻返回，
// 因为它们换渠道也不会好，反而会把「配置错了」掩盖成「搜到了」。
func (s *Service) Search(ctx context.Context, query string, opts Options) (Response, error) {
	s.mu.RLock()
	cfg := s.cfg.clone()
	s.mu.RUnlock()

	order := s.chainOrder(cfg)
	if len(order) == 0 {
		return Response{}, NewProviderError("", KindConfig, 0,
			"未配置任何搜索渠道（请在设置 → 网页搜索里配置至少一个渠道）", "", nil)
	}

	var attempts []string
	for _, p := range order {
		ch := effectiveChannel(p, cfg.Channels[p.ID()])
		resp, err := p.Search(ctx, ch, query, opts)
		if err == nil {
			if resp.Provider == "" {
				resp.Provider = p.ID()
			}
			if resp.Query == "" {
				resp.Query = query
			}
			return resp, nil
		}
		// 调用方取消：直接上抛，不要再试下一个（用户已经不要这次搜索了）。
		if ctx.Err() != nil {
			return Response{}, err
		}
		attempts = append(attempts, err.Error())
		if !ShouldFallback(err) {
			// 凭证/参数类错误：继续降级会把配置问题藏起来。
			return Response{}, err
		}
	}
	return Response{}, NewProviderError("", KindTransient, 0,
		fmt.Sprintf("全部 %d 个渠道均失败：%s", len(attempts), strings.Join(attempts, "；")), "", nil)
}

// SearchWith 只用一个指定渠道搜索（不降级）——设置面板的「测试」按钮用，
// 用户点测试就是想验证这一个渠道，降级会把「这个渠道坏了」测成「搜索正常」。
func (s *Service) SearchWith(ctx context.Context, id, query string, opts Options) (Response, error) {
	p, ok := s.lookupProvider(id)
	if !ok {
		return Response{}, fmt.Errorf("未知搜索渠道 %q", id)
	}
	s.mu.RLock()
	cfg := s.cfg.clone()
	s.mu.RUnlock()
	raw, present := cfg.Channels[id]
	ch := effectiveChannel(p, raw)
	if !channelReady(p, ch, present) {
		if p.OptIn() && !present {
			return Response{}, NewProviderError(id, KindConfig, 0,
				fmt.Sprintf("渠道 %s 需要先在设置里手动启用（它无需凭据，但不会默认开启）", p.Label()), "", nil)
		}
		return Response{}, NewProviderError(id, KindConfig, 0, "渠道未配置或已停用", ch.APIKey, nil)
	}
	resp, err := p.Search(ctx, ch, query, opts)
	if err != nil {
		return Response{}, err
	}
	if resp.Provider == "" {
		resp.Provider = id
	}
	if resp.Query == "" {
		resp.Query = query
	}
	return resp, nil
}

// chainOrder 返回降级链顺序：主渠道（就绪时）在前，其余就绪渠道按预设顺序。
//
// 与上游的一处**有意差异**（勿照搬上游的 explicit-only 清单）：
// pi-web-access 把 14 个渠道标为 explicit-only（serper/serpapi/valyu 等），
// 永不参与它的 `all` 扇出——因为 `all` 会**每次搜索并发打所有渠道**，
// 付费渠道被静默扇出就是额度雪崩。lxcode 没有扇出：这里是顺序降级，
// 只在主渠道**失败**时多打一个请求，且渠道入链的前提是用户配置过它
// （配置即显式意图，不存在「没要求却用了」）。所以不引入该标记。
// 若将来加入并行扇出，必须先补上这个标记——那是它真正要防的东西。
func (s *Service) chainOrder(cfg *Config) []Provider {
	providers := s.providersLocked()
	var primary Provider
	rest := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if !readyIn(cfg, p) {
			continue
		}
		if cfg.Primary != "" && p.ID() == cfg.Primary {
			primary = p
			continue
		}
		rest = append(rest, p)
	}
	if primary != nil {
		return append([]Provider{primary}, rest...)
	}
	// 主渠道未配置（或没设）：预设顺序即优先级——免 key/自建在前，
	// 用户没显式指定时先用不花钱的。presetProviders 的顺序天然保序，
	// 上面遍历时已按序追加，无需再排。
	return rest
}
