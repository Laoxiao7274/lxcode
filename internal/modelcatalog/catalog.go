// Package modelcatalog 提供「可选模型目录」与「端点模型发现」两件事。
//
// 目录（Catalog）是厂商 → 模型的元数据：上下文窗口、输出上限、能力位、弃用
// 状态。数据取自 models.dev 公开目录（与 pi-ai 的生成目录同源）。它是**建议
// 来源**，不是事实源——模型注册表（internal/config）仍是唯一事实源，用户对
// 目录里列出的东西可改可删、也可以完全不用它。
//
// 发现（Discover）向一个端点要它**实际**提供的模型清单（/v1/models），服务于
// 自建端点（vLLM / llama.cpp / ollama / 公司网关）——它们不在目录里。
//
// 两件事都在后端做：CSP 的 connect-src 不放行外域，且对外使用 API key 的位置
// 只能是后端。只依赖标准库。
//
// 刷新语义照 pi-ai 的 Models.refresh：**先用手上的缓存，过期才联网**；联网失败
// 保留旧目录并标记陈旧（stale），而不是让整个设置面板打不开。
package modelcatalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
)

// SourceURL 是目录的公开数据源（models.dev——pi-ai 的生成目录同源）。
const SourceURL = "https://models.dev/api.json"

// Model 是目录里的一个模型条目。字段与 config.ModelConfig 的能力位同名，
// 便于前端勾选时直接映射（少一层翻译就少一处漂移）。
type Model struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Context   int    `json:"context_window,omitempty"`
	MaxOutput int    `json:"max_output_tokens,omitempty"`
	Tools     bool   `json:"tools,omitempty"`
	Vision    bool   `json:"vision,omitempty"`
	JSONOut   bool   `json:"json_output,omitempty"`
	Reasoning bool   `json:"reasoning,omitempty"`
	// Status 只在非正常状态时出现（deprecated / beta）——实测 8394 个条目里
	// 只有 332 个有值，所以「空 = 正常」比到处显示一个 stable 标签更干净。
	Status string `json:"status,omitempty"`
	// Released 是发布日期（YYYY-MM-DD），用于把新模型排在前面。
	Released string `json:"released,omitempty"`
}

// Provider 是目录里的一个厂商（= 一套端点 + 一种 wire 格式）。
type Provider struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Doc    string   `json:"doc,omitempty"`
	API    string   `json:"api,omitempty"` // 端点地址：注册表 base_url 的建议值
	Env    []string `json:"env,omitempty"` // key 的环境变量名（表单提示用；本包不读它）
	Format string   `json:"format"`        // config.FormatOpenAI / FormatAnthropic
	// ModelCount 是列表载荷里的模型数。Models 为 nil 时用它——列表方法绝不
	// 内联 6600+ 个模型明细（那是 model.catalog.models 的事）。
	ModelCount int     `json:"model_count"`
	Models     []Model `json:"models,omitempty"`
}

// Catalog 是一份目录快照（也是磁盘缓存的形状）。
type Catalog struct {
	FetchedAt time.Time  `json:"fetched_at"`
	Providers []Provider `json:"providers"`
}

// ProviderList 是厂商清单载荷（model.catalog.list 的结果）。
type ProviderList struct {
	FetchedAt time.Time `json:"fetched_at"`
	// Stale 表示「手上这份已过期且这次没刷新成功」——UI 据此如实提示，
	// 而不是把旧目录当新鲜的展示（本项目对「不编数字」的一贯要求）。
	Stale     bool       `json:"stale,omitempty"`
	Providers []Provider `json:"providers"`
}

// ModelList 是某厂商的模型清单载荷（model.catalog.models 的结果）。
type ModelList struct {
	FetchedAt time.Time `json:"fetched_at"`
	Stale     bool      `json:"stale,omitempty"`
	Provider  string    `json:"provider"`
	Models    []Model   `json:"models"`
}

// Provider 按 id 找厂商（目录装入后不再改动，可安全并发读）。
func (c Catalog) Provider(id string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// providerViews 把目录里的厂商剥成列表载荷（去掉模型明细、只留计数）。
func (c Catalog) providerViews() []Provider {
	out := make([]Provider, 0, len(c.Providers))
	for _, p := range c.Providers {
		p.ModelCount = len(p.Models)
		p.Models = nil
		out = append(out, p)
	}
	return out
}

// formatOf 把 models.dev 的 npm 包名映射成 lxcode 的两种 wire 格式。
//
// 只认有把握的四个包名：lxcode 只会这两种协议，列进目录的必须真能连上——
// 把 Google / Bedrock / Azure 这些走原生协议的厂商混进来，用户点进去只会失败。
// OpenRouter 按 openai 计：它的 API 是 OpenAI 兼容的（pi-ai 同时挂了两种适配器，
// 我们取兼容性更宽的那种）。
func formatOf(npm string) (string, bool) {
	switch npm {
	case "@ai-sdk/openai-compatible", "@ai-sdk/openai", "@openrouter/ai-sdk-provider":
		return config.FormatOpenAI, true
	case "@ai-sdk/anthropic":
		return config.FormatAnthropic, true
	}
	return "", false
}

// rawCatalog 是 models.dev 的响应形状（只声明用到的字段——目录字段会演化，
// 多声明的字段只会变成需要维护的噪音）。
type rawCatalog map[string]rawProvider

type rawProvider struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Doc    string              `json:"doc"`
	API    string              `json:"api"`
	Env    []string            `json:"env"`
	Npm    string              `json:"npm"`
	Models map[string]rawModel `json:"models"`
}

type rawModel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolCall  bool   `json:"tool_call"`
	Vision    bool   `json:"attachment"` // models.dev 用 attachment 表示可收图片
	JSONOut   bool   `json:"structured_output"`
	Reasoning bool   `json:"reasoning"`
	Status    string `json:"status"`
	Released  string `json:"release_date"`
	Limit     struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

// parseCatalog 把 models.dev 的原始 JSON 映射成本包的目录形状。
//
// 过滤规则（两条，都是为了「列出来的就能用」）：
//   - 格式必须能映射到 lxcode 的两种 wire 格式；
//   - 必须有端点地址（没有 base_url 的厂商没法进注册表）；
//   - 一个模型都没有的厂商不进目录（点进去空空如也是噪音）。
//
// 排序固定（厂商按名称、模型按发布日期倒序再按 id）：目录会被缓存、被测试断言、
// 也会在 UI 里反复出现——顺序不定的列表每次刷新都在跳。
func parseCatalog(raw []byte, now time.Time) (*Catalog, error) {
	var rc rawCatalog
	if err := json.Unmarshal(raw, &rc); err != nil {
		return nil, fmt.Errorf("目录 JSON 解析失败: %w", err)
	}
	cat := &Catalog{FetchedAt: now, Providers: make([]Provider, 0, len(rc))}
	for _, rp := range rc {
		format, ok := formatOf(rp.Npm)
		if !ok || rp.API == "" {
			continue
		}
		p := Provider{
			ID:     rp.ID,
			Name:   rp.Name,
			Doc:    rp.Doc,
			API:    rp.API,
			Env:    rp.Env,
			Format: format,
		}
		if p.ID == "" {
			continue
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		for key, rm := range rp.Models {
			id := rm.ID
			if id == "" {
				// 键就是 id（models.dev 的键与 id 恒等，这里只是不信任外部数据）。
				id = key
			}
			if id == "" {
				continue
			}
			p.Models = append(p.Models, Model{
				ID:        id,
				Name:      rm.Name,
				Context:   rm.Limit.Context,
				MaxOutput: rm.Limit.Output,
				Tools:     rm.ToolCall,
				Vision:    rm.Vision,
				JSONOut:   rm.JSONOut,
				Reasoning: rm.Reasoning,
				Status:    rm.Status,
				Released:  rm.Released,
			})
		}
		if len(p.Models) == 0 {
			continue
		}
		sortModels(p.Models)
		cat.Providers = append(cat.Providers, p)
	}
	sort.Slice(cat.Providers, func(i, j int) bool {
		a, b := strings.ToLower(cat.Providers[i].Name), strings.ToLower(cat.Providers[j].Name)
		if a != b {
			return a < b
		}
		return cat.Providers[i].ID < cat.Providers[j].ID
	})
	return cat, nil
}

// sortModels 新模型排前面（发布日期倒序），同日期按 id——没有发布日期的排最后
// （未知不该冒充最新）。
func sortModels(models []Model) {
	sort.Slice(models, func(i, j int) bool {
		a, b := models[i].Released, models[j].Released
		if a != b {
			if a == "" {
				return false
			}
			if b == "" {
				return true
			}
			return a > b
		}
		return models[i].ID < models[j].ID
	})
}
