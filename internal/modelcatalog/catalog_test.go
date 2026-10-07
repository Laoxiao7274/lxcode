package modelcatalog

import (
	"testing"
	"time"
)

// 目录解析的契约：能映射的格式、有端点、有模型才进目录；字段映射不许漂移。
func TestFormatOfMapsOnlySupportedPackages(t *testing.T) {
	cases := []struct {
		npm    string
		format string
		ok     bool
	}{
		{"@ai-sdk/openai-compatible", "openai", true},
		{"@ai-sdk/openai", "openai", true},
		{"@openrouter/ai-sdk-provider", "openai", true},
		{"@ai-sdk/anthropic", "anthropic", true},
		// 走原生协议的厂商必须被拒：列进目录等于给用户一个点进去必然失败的入口。
		{"@ai-sdk/google", "", false},
		{"@ai-sdk/azure", "", false},
		{"@aws-sdk/client-bedrock-runtime", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := formatOf(c.npm)
		if ok != c.ok || got != c.format {
			t.Errorf("formatOf(%q) = (%q, %v)，期望 (%q, %v)", c.npm, got, ok, c.format, c.ok)
		}
	}
}

const fixture = `{
  "deepseek": {
    "id": "deepseek", "name": "DeepSeek", "doc": "https://api-docs.deepseek.com",
    "api": "https://api.deepseek.com", "env": ["DEEPSEEK_API_KEY"],
    "npm": "@ai-sdk/openai-compatible",
    "models": {
      "deepseek-v4-pro": {"id": "deepseek-v4-pro", "name": "DeepSeek V4 Pro", "reasoning": true,
        "tool_call": true, "structured_output": true, "attachment": false,
        "release_date": "2026-08-01", "limit": {"context": 1000000, "output": 384000}},
      "deepseek-v4-flash": {"id": "deepseek-v4-flash", "name": "DeepSeek V4 Flash", "reasoning": true,
        "tool_call": true, "structured_output": false, "attachment": true, "status": "deprecated",
        "release_date": "2026-09-10", "limit": {"context": 1000000, "output": 393216}},
      "deepseek-old": {"id": "deepseek-old", "name": "Old", "release_date": ""}
    }
  },
  "anthropic": {
    "id": "anthropic", "name": "Anthropic", "doc": "https://docs.anthropic.com",
    "api": "https://api.anthropic.com", "env": ["ANTHROPIC_API_KEY"],
    "npm": "@ai-sdk/anthropic",
    "models": {"claude-x": {"id": "claude-x", "name": "Claude X", "tool_call": true,
      "release_date": "2026-07-01", "limit": {"context": 200000, "output": 64000}}}
  },
  "google": {
    "id": "google", "name": "Google", "api": "https://generativelanguage.googleapis.com",
    "npm": "@ai-sdk/google", "models": {"gemini": {"id": "gemini"}}
  },
  "no-endpoint": {
    "id": "no-endpoint", "name": "No Endpoint", "api": "",
    "npm": "@ai-sdk/openai-compatible", "models": {"m": {"id": "m"}}
  },
  "empty": {
    "id": "empty", "name": "Empty", "api": "https://empty.example.com",
    "npm": "@ai-sdk/openai-compatible", "models": {}
  }
}`

func TestParseCatalogFiltersAndMaps(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cat, err := parseCatalog([]byte(fixture), now)
	if err != nil {
		t.Fatalf("parseCatalog: %v", err)
	}
	if !cat.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v，期望 %v", cat.FetchedAt, now)
	}
	// 只有 deepseek 与 anthropic 能映射格式且有端点；google（原生协议）、
	// no-endpoint（无地址）、empty（无模型）都不进目录。
	if len(cat.Providers) != 2 {
		ids := make([]string, 0, len(cat.Providers))
		for _, p := range cat.Providers {
			ids = append(ids, p.ID)
		}
		t.Fatalf("厂商数 = %d（%v），期望 2", len(cat.Providers), ids)
	}
	// 厂商按名称排序：Anthropic 在 DeepSeek 前。
	if cat.Providers[0].ID != "anthropic" || cat.Providers[1].ID != "deepseek" {
		t.Fatalf("厂商顺序 = %s, %s，期望 anthropic, deepseek", cat.Providers[0].ID, cat.Providers[1].ID)
	}
	ds, ok := cat.Provider("deepseek")
	if !ok {
		t.Fatal("找不到 deepseek")
	}
	if ds.Format != "openai" || ds.API != "https://api.deepseek.com" {
		t.Errorf("deepseek 映射错了: format=%q api=%q", ds.Format, ds.API)
	}
	if len(ds.Env) != 1 || ds.Env[0] != "DEEPSEEK_API_KEY" {
		t.Errorf("env 未透传: %v", ds.Env)
	}
	// 模型按发布日期倒序：09-10 的 flash 在 08-01 的 pro 之前，无日期的排最后。
	wantOrder := []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-old"}
	for i, want := range wantOrder {
		if ds.Models[i].ID != want {
			t.Fatalf("模型顺序 = %v，期望 %v", idsOf(ds.Models), wantOrder)
		}
	}
	flash := ds.Models[0]
	if flash.Context != 1000000 || flash.MaxOutput != 393216 {
		t.Errorf("上下文/输出未映射: %d / %d", flash.Context, flash.MaxOutput)
	}
	if !flash.Reasoning || !flash.Tools || !flash.Vision || flash.JSONOut {
		t.Errorf("能力位映射错了: %+v", flash)
	}
	if flash.Status != "deprecated" {
		t.Errorf("status 未透传: %q", flash.Status)
	}
	pro := ds.Models[1]
	if !pro.JSONOut || pro.Vision {
		t.Errorf("pro 的能力位映射错了: %+v", pro)
	}
	if pro.Status != "" {
		t.Errorf("无状态应为空串，得到 %q", pro.Status)
	}
}

func TestParseCatalogRejectsBadJSON(t *testing.T) {
	if _, err := parseCatalog([]byte("{not json"), time.Now()); err == nil {
		t.Fatal("坏 JSON 必须报错")
	}
}

func TestParseCatalogUsesKeyWhenIDMissing(t *testing.T) {
	raw := `{"p":{"id":"p","name":"P","api":"https://p.example.com","npm":"@ai-sdk/openai",
		"models":{"m-1":{"name":"M1"}}}}`
	cat, err := parseCatalog([]byte(raw), time.Now())
	if err != nil {
		t.Fatalf("parseCatalog: %v", err)
	}
	if len(cat.Providers) != 1 || len(cat.Providers[0].Models) != 1 {
		t.Fatalf("厂商/模型数不对: %+v", cat.Providers)
	}
	if cat.Providers[0].Models[0].ID != "m-1" {
		t.Errorf("id 缺失时应回落到 map 键，得到 %q", cat.Providers[0].Models[0].ID)
	}
}

// 列表载荷绝不能内联模型明细（6600+ 个模型塞进一次响应）。
func TestProviderViewsDropModelsKeepCount(t *testing.T) {
	cat, err := parseCatalog([]byte(fixture), time.Now())
	if err != nil {
		t.Fatalf("parseCatalog: %v", err)
	}
	views := cat.providerViews()
	for _, v := range views {
		if v.Models != nil {
			t.Errorf("列表载荷里带了模型明细: %s", v.ID)
		}
	}
	if views[0].ModelCount != 1 || views[1].ModelCount != 3 {
		t.Errorf("模型计数错了: %d, %d", views[0].ModelCount, views[1].ModelCount)
	}
	// 剥离只作用于副本：原目录不受影响（否则 model.catalog.models 会拿到空）。
	if len(cat.Providers[1].Models) != 3 {
		t.Errorf("providerViews 改动了原目录: %d", len(cat.Providers[1].Models))
	}
}

func idsOf(models []Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}
