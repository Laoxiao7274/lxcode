package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/moyunteng/lxcode/internal/atomicfile"
)

const (
	// DefaultTTL 是目录的保鲜期。目录回答的是「有哪些模型可选」，一天一刷足够
	// （数据源按天更新），用户也可以用 refresh 强制刷。
	DefaultTTL = 24 * time.Hour
	// fetchTimeout 是单次拉取的上限，**不重试**：照 pi-ai 的 refresh 语义，失败
	// 就用手上的旧目录——重试只会让设置面板多等一轮。
	fetchTimeout = 15 * time.Second
	// maxCatalogBytes 是响应体积上限（实测 5 MB；给足余量但拒绝无界读取）。
	maxCatalogBytes = 32 << 20
	userAgent       = "lxcode-modelcatalog/1"
)

// Service 是目录服务：持一份目录快照 + 磁盘缓存，按 TTL 懒刷新。
type Service struct {
	mu      sync.RWMutex
	catalog *Catalog

	// fetchMu 串行化刷新（持它做网络请求）。只读路径不经过它——一次 15s 的
	// 拉取不该把已经拿到的目录挡住。
	fetchMu sync.Mutex

	cachePath string
	sourceURL string
	ttl       time.Duration
	client    *http.Client
	now       func() time.Time
	// fetchFn 可注入：测试用它替掉网络（绝不打真网）。
	fetchFn func(context.Context) (*Catalog, error)
}

// LoadService 构造目录服务并尽力读回磁盘缓存。
//
// cachePath 为空 = 不用缓存（纯内存，测试用）。
func LoadService(cachePath string) *Service {
	s := &Service{
		cachePath: cachePath,
		sourceURL: SourceURL,
		ttl:       DefaultTTL,
		client:    &http.Client{Timeout: fetchTimeout},
		now:       time.Now,
	}
	s.fetchFn = s.fetchRemote
	s.loadCache()
	return s
}

// Providers 返回厂商清单（不含模型明细）。
func (s *Service) Providers(ctx context.Context, force bool) (ProviderList, error) {
	cat, stale, err := s.snapshot(ctx, force)
	if err != nil {
		return ProviderList{}, err
	}
	return ProviderList{FetchedAt: cat.FetchedAt, Stale: stale, Providers: cat.providerViews()}, nil
}

// Models 返回某厂商的模型清单。
//
// 厂商不在目录里就是错误（不隐式再拉一次：调用方的 id 来自它刚拿到的列表，
// 对不上说明状态不一致，静默重试只会把问题掩盖成一次多余的请求）。
func (s *Service) Models(ctx context.Context, providerID string, force bool) (ModelList, error) {
	cat, stale, err := s.snapshot(ctx, force)
	if err != nil {
		return ModelList{}, err
	}
	p, ok := cat.Provider(providerID)
	if !ok {
		return ModelList{}, fmt.Errorf("模型目录里没有厂商 %q", providerID)
	}
	return ModelList{FetchedAt: cat.FetchedAt, Stale: stale, Provider: p.ID, Models: p.Models}, nil
}

// snapshot 返回当前目录快照（值拷贝；装入后不再改动，切片可安全共享只读）。
//
// 两阶段（照 pi-ai 的 Models.refresh）：手上有的先用，过期才联网；联网失败
// 保留旧目录并标 stale——陈旧清单比打不开的面板有用得多。
func (s *Service) snapshot(ctx context.Context, force bool) (Catalog, bool, error) {
	s.mu.RLock()
	cat := s.catalog
	s.mu.RUnlock()

	if cat != nil && !force && s.now().Sub(cat.FetchedAt) < s.ttl {
		return *cat, false, nil
	}

	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()
	// 双检：等锁期间可能已经有人刷过了（force 除外——它就是要重拉）。
	s.mu.RLock()
	cat = s.catalog
	s.mu.RUnlock()
	if cat != nil && !force && s.now().Sub(cat.FetchedAt) < s.ttl {
		return *cat, false, nil
	}

	fresh, err := s.fetchFn(ctx)
	if err != nil {
		if cat != nil {
			return *cat, true, nil
		}
		return Catalog{}, false, err
	}
	s.mu.Lock()
	s.catalog = fresh
	s.mu.Unlock()
	s.saveCache(fresh)
	return *fresh, false, nil
}

// fetchRemote 拉取并解析目录（单次请求，不重试）。
func (s *Service) fetchRemote(ctx context.Context) (*Catalog, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造模型目录请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取模型目录失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取模型目录失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
	if err != nil {
		return nil, fmt.Errorf("读取模型目录失败: %w", err)
	}
	return parseCatalog(body, s.now())
}

// loadCache 尽力而为地读回上次的目录。
//
// 读不到/坏了都当「没有」——缓存是加速手段不是事实源，为它打断启动不值得。
// 但**文件在却解析失败**要记一行日志：那是磁盘问题的信号，静默会让人查不到。
func (s *Service) loadCache() {
	if s.cachePath == "" {
		return
	}
	data, err := os.ReadFile(s.cachePath)
	if err != nil {
		return // 首次运行没有缓存文件是正常态
	}
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		log.Printf("模型目录缓存解析失败（将重新拉取）: %v", err)
		return
	}
	if len(cat.Providers) == 0 {
		return
	}
	s.mu.Lock()
	s.catalog = &cat
	s.mu.Unlock()
}

// saveCache 落盘目录（失败只记日志：内存里这份已经可用，缓存只为下次启动省一次拉取）。
func (s *Service) saveCache(cat *Catalog) {
	if s.cachePath == "" {
		return
	}
	data, err := json.Marshal(cat)
	if err != nil {
		log.Printf("模型目录缓存序列化失败: %v", err)
		return
	}
	if err := atomicfile.Write(s.cachePath, data); err != nil {
		log.Printf("模型目录缓存写入失败: %v", err)
	}
}
