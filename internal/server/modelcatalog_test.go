package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/modelcatalog"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// newCatalogService 造一个「有新鲜缓存」的目录服务：走的是生产同款加载路径
// （LoadService 读缓存文件），所以整条服务端链路被测到，且不会联网。
func newCatalogService(t *testing.T) *modelcatalog.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model-catalog.json")
	cat := modelcatalog.Catalog{
		FetchedAt: time.Now(),
		Providers: []modelcatalog.Provider{
			{ID: "deepseek", Name: "DeepSeek", API: "https://api.deepseek.com",
				Env: []string{"DEEPSEEK_API_KEY"}, Format: "openai",
				Models: []modelcatalog.Model{
					{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", Context: 1000000,
						MaxOutput: 384000, Tools: true, Reasoning: true},
					{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Context: 1000000,
						MaxOutput: 393216, Tools: true, Reasoning: true, Vision: true},
				}},
			{ID: "anthropic", Name: "Anthropic", API: "https://api.anthropic.com",
				Format: "anthropic", Models: []modelcatalog.Model{{ID: "claude-x", Context: 200000}}},
		},
	}
	data, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return modelcatalog.LoadService(path)
}

// 未装配时方法要回「未装配」，而不是让前端拿到空清单（那会被当成「目录里没有
// 厂商」——两种失败必须分得开）。
func TestModelCatalogMethodsRequireService(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	for _, m := range []string{protocol.MethodModelCatalogList, protocol.MethodModelDiscover} {
		resp := client.call(m, map[string]any{})
		if resp == nil || resp.Error == nil {
			t.Fatalf("%s 未装配时必须报错: %+v", m, resp)
		}
		if resp.Error.Code != protocol.CodeInternal {
			t.Errorf("%s 错误码 = %d，期望 CodeInternal", m, resp.Error.Code)
		}
	}
}

func TestModelCatalogListAndModels(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	srv.AttachModelCatalog(newCatalogService(t))

	resp := client.call(protocol.MethodModelCatalogList, map[string]any{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("目录列表失败: %+v", resp)
	}
	var list modelcatalog.ProviderList
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Providers) != 2 {
		t.Fatalf("厂商数 = %d，期望 2", len(list.Providers))
	}
	// 列表载荷不带模型明细（6600+ 个模型不能塞进一次响应）。
	if list.Providers[0].Models != nil {
		t.Error("列表载荷里带了模型明细")
	}
	if list.Providers[0].ModelCount == 0 {
		t.Error("列表载荷缺模型计数")
	}
	if list.Stale {
		t.Error("新鲜缓存不该标 stale")
	}

	resp = client.call(protocol.MethodModelCatalogModels, protocol.ModelCatalogModelsParams{Provider: "deepseek"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("模型列表失败: %+v", resp)
	}
	var models modelcatalog.ModelList
	b, _ = json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &models); err != nil {
		t.Fatal(err)
	}
	if models.Provider != "deepseek" || len(models.Models) != 2 {
		t.Fatalf("模型清单不对: %+v", models)
	}
	if models.Models[0].Context != 1000000 {
		t.Errorf("上下文窗口未上线: %+v", models.Models[0])
	}

	// 未知厂商必须报错（不能静默返回空清单）。
	resp = client.call(protocol.MethodModelCatalogModels, protocol.ModelCatalogModelsParams{Provider: "nope"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("未知厂商必须报错: %+v", resp)
	}
}

// 按注册表条目 id 探测：base_url / key / format 都取自注册表——前端不必把 key
// 再送一遍，也就没有「前端手里的 key 与注册表不一致」这种分叉。
func TestModelDiscoverResolvesRegistryEntry(t *testing.T) {
	var gotPath, gotAuth string
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"m-b"},{"id":"m-a"}]}`))
	}))
	defer srv2.Close()

	srv, client, reg := newTestServer(t, nil)
	srv.AttachModelCatalog(newCatalogService(t))
	if err := reg.Add(config.ModelConfig{
		ID: "probe-me", BaseURL: srv2.URL + "/v1", APIKey: "sk-registry",
		Model: "probe-me", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	resp := client.call(protocol.MethodModelDiscover, protocol.ModelDiscoverParams{ID: "probe-me"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("探测失败: %+v", resp)
	}
	if gotPath != "/v1/models" {
		t.Errorf("探测路径 = %q，期望 /v1/models", gotPath)
	}
	if gotAuth != "Bearer sk-registry" {
		t.Errorf("鉴权头 = %q（应取自注册表条目）", gotAuth)
	}
	var res modelcatalog.DiscoverResult
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Models) != 2 || res.Models[0].ID != "m-a" {
		t.Fatalf("探测结果不对: %+v", res)
	}
	if res.Endpoint != srv2.URL+"/v1/models" {
		t.Errorf("回显端点 = %q", res.Endpoint)
	}
}

// 未注册的新端点：直接给 base_url + key（自定义提供商表单的路径）。
func TestModelDiscoverByBaseURL(t *testing.T) {
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"local-1"}]}`))
	}))
	defer srv2.Close()

	srv, client, _ := newTestServer(t, nil)
	srv.AttachModelCatalog(newCatalogService(t))

	resp := client.call(protocol.MethodModelDiscover, protocol.ModelDiscoverParams{
		BaseURL: srv2.URL, APIKey: "k", Format: "openai",
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("探测失败: %+v", resp)
	}
	var res modelcatalog.DiscoverResult
	b, _ := json.Marshal(resp.Result)
	_ = json.Unmarshal(b, &res)
	if len(res.Models) != 1 || res.Models[0].ID != "local-1" {
		t.Fatalf("探测结果不对: %+v", res)
	}
}

func TestModelDiscoverRejectsUnknownIDAndBadURL(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	srv.AttachModelCatalog(newCatalogService(t))

	resp := client.call(protocol.MethodModelDiscover, protocol.ModelDiscoverParams{ID: "不存在"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("未知条目必须报错: %+v", resp)
	}
	resp = client.call(protocol.MethodModelDiscover, protocol.ModelDiscoverParams{BaseURL: "不是地址"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("坏地址必须报错: %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "http") {
		t.Errorf("错误应说清要 http(s) 地址: %q", resp.Error.Message)
	}
}
