package modelcatalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testService(fetch func(context.Context) (*Catalog, error)) *Service {
	return &Service{
		ttl:     time.Hour,
		client:  &http.Client{Timeout: 5 * time.Second},
		now:     time.Now,
		fetchFn: fetch,
	}
}

func sampleCatalog(fetchedAt time.Time) *Catalog {
	return &Catalog{FetchedAt: fetchedAt, Providers: []Provider{{
		ID: "p", Name: "P", API: "https://p.example.com", Format: "openai",
		Models: []Model{{ID: "m", Context: 1000}},
	}}}
}

// 保鲜期内绝不联网——目录每次打开设置面板都会被问，联网要按 TTL 而不是按访问次数。
func TestSnapshotServesCacheWithinTTL(t *testing.T) {
	calls := 0
	s := testService(func(context.Context) (*Catalog, error) {
		calls++
		return nil, errors.New("保鲜期内不该联网")
	})
	s.catalog = sampleCatalog(s.now().Add(-time.Minute))

	cat, stale, err := s.snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if calls != 0 {
		t.Errorf("保鲜期内联网了 %d 次", calls)
	}
	if stale {
		t.Error("保鲜期内的目录不该标 stale")
	}
	if len(cat.Providers) != 1 {
		t.Errorf("目录内容不对: %+v", cat)
	}
}

func TestSnapshotRefreshesWhenExpired(t *testing.T) {
	fresh := sampleCatalog(time.Now())
	calls := 0
	s := testService(func(context.Context) (*Catalog, error) { calls++; return fresh, nil })
	s.catalog = sampleCatalog(s.now().Add(-2 * time.Hour))

	_, stale, err := s.snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if calls != 1 {
		t.Errorf("过期后应联网一次，实际 %d 次", calls)
	}
	if stale {
		t.Error("刷新成功后不该标 stale")
	}
}

func TestSnapshotForceRefreshesEvenWhenFresh(t *testing.T) {
	calls := 0
	s := testService(func(context.Context) (*Catalog, error) { calls++; return sampleCatalog(time.Now()), nil })
	s.catalog = sampleCatalog(s.now().Add(-time.Minute))

	if _, _, err := s.snapshot(context.Background(), true); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if calls != 1 {
		t.Errorf("force 应联网一次，实际 %d 次", calls)
	}
}

// 刷新失败时保留旧目录并标 stale：陈旧清单比打不开的面板有用得多。
func TestSnapshotKeepsStaleCatalogWhenFetchFails(t *testing.T) {
	old := sampleCatalog(time.Now().Add(-72 * time.Hour))
	s := testService(func(context.Context) (*Catalog, error) { return nil, errors.New("断网") })
	s.catalog = old

	cat, stale, err := s.snapshot(context.Background(), false)
	if err != nil {
		t.Fatalf("有旧目录时不该报错: %v", err)
	}
	if !stale {
		t.Error("刷新失败必须标 stale（UI 要如实提示）")
	}
	if len(cat.Providers) != 1 || cat.Providers[0].ID != "p" {
		t.Errorf("应返回旧目录，得到 %+v", cat)
	}
}

func TestSnapshotErrorsWhenNothingCached(t *testing.T) {
	s := testService(func(context.Context) (*Catalog, error) { return nil, errors.New("断网") })
	if _, _, err := s.snapshot(context.Background(), false); err == nil {
		t.Fatal("既没缓存又拉不到，必须报错")
	}
}

func TestModelsUnknownProviderErrors(t *testing.T) {
	s := testService(func(context.Context) (*Catalog, error) { return sampleCatalog(time.Now()), nil })
	if _, err := s.Models(context.Background(), "不存在", false); err == nil {
		t.Fatal("未知厂商必须报错")
	}
	if _, err := s.Models(context.Background(), "p", false); err != nil {
		t.Fatalf("已知厂商应成功: %v", err)
	}
}

func TestProvidersPayloadDropsModels(t *testing.T) {
	s := testService(func(context.Context) (*Catalog, error) { return sampleCatalog(time.Now()), nil })
	list, err := s.Providers(context.Background(), false)
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(list.Providers) != 1 || list.Providers[0].ModelCount != 1 || list.Providers[0].Models != nil {
		t.Fatalf("列表载荷形状不对: %+v", list.Providers)
	}
	models, err := s.Models(context.Background(), "p", false)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models.Models) != 1 || models.Provider != "p" {
		t.Fatalf("模型载荷形状不对: %+v", models)
	}
}

// 缓存往返：第二个实例直接吃盘上的目录，不再联网。
func TestCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-catalog.json")
	first := LoadService(path)
	first.fetchFn = func(context.Context) (*Catalog, error) { return sampleCatalog(time.Now()), nil }
	if _, err := first.Providers(context.Background(), false); err != nil {
		t.Fatalf("首次拉取: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("缓存未落盘: %v", err)
	}

	second := LoadService(path)
	second.fetchFn = func(context.Context) (*Catalog, error) {
		t.Fatal("有新鲜缓存时不该联网")
		return nil, nil
	}
	list, err := second.Providers(context.Background(), false)
	if err != nil {
		t.Fatalf("读缓存: %v", err)
	}
	if len(list.Providers) != 1 || list.Providers[0].ID != "p" {
		t.Fatalf("缓存内容不对: %+v", list.Providers)
	}
}

// 坏缓存当「没有」：不能让它把服务卡死，也不能静默当成有效目录。
func TestLoadCacheIgnoresCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-catalog.json")
	if err := os.WriteFile(path, []byte("{坏掉的 JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := LoadService(path)
	if s.catalog != nil {
		t.Fatal("坏缓存不该被当成有效目录")
	}
	s.fetchFn = func(context.Context) (*Catalog, error) { return sampleCatalog(time.Now()), nil }
	if _, err := s.Providers(context.Background(), false); err != nil {
		t.Fatalf("坏缓存后应能重新拉取: %v", err)
	}
}

func TestLoadCacheIgnoresEmptyCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-catalog.json")
	if err := os.WriteFile(path, []byte(`{"fetched_at":"2026-10-06T00:00:00Z","providers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := LoadService(path); s.catalog != nil {
		t.Fatal("空目录不该被当成有效缓存（会让面板永远空着）")
	}
}

func TestFetchRemoteParsesAndRejectsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("缺 User-Agent")
		}
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	s := testService(nil)
	s.sourceURL = srv.URL
	s.fetchFn = s.fetchRemote
	cat, err := s.fetchRemote(context.Background())
	if err != nil {
		t.Fatalf("fetchRemote: %v", err)
	}
	if len(cat.Providers) != 2 {
		t.Errorf("解析出的厂商数 = %d，期望 2", len(cat.Providers))
	}

	s.sourceURL = srv.URL + "/bad"
	if _, err := s.fetchRemote(context.Background()); err == nil {
		t.Fatal("非 200 必须报错")
	}
}

// 探测结果按模型 id 从**内存**目录快照回填元数据；目录查不到的保持未知（不编数）。
func TestDiscoverEnrichesFromCatalogSnapshot(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3-flash"},{"id":"custom-local"}]}`))
	}))
	defer endpoint.Close()

	s := testService(nil)
	s.catalog = &Catalog{FetchedAt: time.Now(), Providers: []Provider{{
		ID: "zhipu", Name: "Zhipu", API: "https://open.zhipu.com", Format: "openai",
		Models: []Model{
			{ID: "glm-5.3-flash", Name: "GLM-5.3 Flash", Context: 128000, MaxOutput: 8192, Tools: true, Vision: true, Reasoning: true},
		},
	}}}

	res, err := s.Discover(context.Background(), DiscoverInput{BaseURL: endpoint.URL, Format: "openai"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	byID := map[string]Discovered{}
	for _, m := range res.Models {
		byID[m.ID] = m
	}
	got, ok := byID["glm-5.3-flash"]
	if !ok {
		t.Fatal("探测结果缺 glm-5.3-flash")
	}
	if got.Context != 128000 || got.MaxOutput != 8192 || !got.Tools || !got.Vision || !got.Reasoning {
		t.Fatalf("目录元数据没回填: %+v", got)
	}
	if m := byID["custom-local"]; m.Context != 0 || m.Tools || m.Reasoning {
		t.Fatalf("目录里没有的模型不该被编出元数据: %+v", m)
	}
}

// 目录里「上限≥窗口」的坏行（数据源有 ~14%）：输出必须留空——照抄会被
// config.validate 硬拒（输入+输出超限），模型整条加不进去；留空 = 未知。
func TestDiscoverSkipsOutputThatWouldFailValidate(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"bad-limits"}]}`))
	}))
	defer endpoint.Close()

	s := testService(nil)
	s.catalog = &Catalog{FetchedAt: time.Now(), Providers: []Provider{{
		ID: "p", Name: "P", API: "https://p.example.com", Format: "openai",
		Models: []Model{{ID: "bad-limits", Context: 8000, MaxOutput: 8000, Tools: true}},
	}}}

	res, err := s.Discover(context.Background(), DiscoverInput{BaseURL: endpoint.URL, Format: "openai"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Models) != 1 {
		t.Fatalf("模型数 = %d，期望 1", len(res.Models))
	}
	got := res.Models[0]
	if got.Context != 8000 || got.MaxOutput != 0 {
		t.Fatalf("上限≥窗口时输出必须留空: %+v", got)
	}
	if !got.Tools {
		t.Fatalf("能力位应正常回填: %+v", got)
	}
}

// 目录不在内存（未装配/没缓存）：探测照常工作，只是没元数据——绝不报错，
// 也绝不为元数据触发一次目录网络拉取（探测是用户正在等的交互）。
func TestDiscoverWorksWithoutCatalogSnapshot(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer endpoint.Close()

	s := testService(func(context.Context) (*Catalog, error) {
		t.Error("目录不在内存时不该联网刷新")
		return nil, errors.New("不该联网")
	})

	res, err := s.Discover(context.Background(), DiscoverInput{BaseURL: endpoint.URL, Format: "openai"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Models) != 1 || res.Models[0].ID != "m" {
		t.Fatalf("探测结果不对: %+v", res.Models)
	}
	if res.Models[0].Context != 0 || res.Models[0].Tools {
		t.Fatalf("没有目录时不该有元数据: %+v", res.Models[0])
	}
}
