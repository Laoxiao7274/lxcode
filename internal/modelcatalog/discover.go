package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/moyunteng/lxcode/internal/config"
)

// anthropicVersion 与 internal/llm 用同一个版本号（Anthropic 要求显式声明）。
const anthropicVersion = "2023-06-01"

// DiscoverInput 是一次端点探测的输入。
type DiscoverInput struct {
	BaseURL string
	APIKey  string
	Format  string // 空 = openai
}

// Discovered 是探测到的一个模型。元数据字段由目录按 id 回填（best-effort）：
// 目录里查不到就保持缺省 = 未知——探测的主价值是「这个端点有什么」，元数据
// 只是让勾选添加后的注册表条目不用再手填，绝不为它编数。
type Discovered struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// 以下回填自目录快照（models.dev）。缺省 = 目录里没有 / 目录服务未装配。
	Context   int  `json:"context_window,omitempty"`
	MaxOutput int  `json:"max_output_tokens,omitempty"`
	Tools     bool `json:"tools,omitempty"`
	Vision    bool `json:"vision,omitempty"`
	JSONOut   bool `json:"json_output,omitempty"`
	Reasoning bool `json:"reasoning,omitempty"`
}

// DiscoverResult 是探测结果载荷（model.discover 的结果）。
type DiscoverResult struct {
	// Endpoint 回显实际请求的地址：用户填的 base_url 会被归一化（裸地址/带
	// /v1/完整路径三种写法都吃），回显出来他才能核对「我填的地址被怎么用了」。
	Endpoint string       `json:"endpoint"`
	Format   string       `json:"format"`
	Models   []Discovered `json:"models"`
}

// rawDiscovered 兼容 OpenAI/Anthropic 的 {"data":[…]}：OpenAI 给 name、Anthropic
// 给 display_name，两个都认，都没有就只显示 id。
type rawDiscovered struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// modelsResponse 兼容两种清单形状：{"data":[…]}（OpenAI / Anthropic / vLLM /
// ollama 的 OpenAI 兼容层）与 {"models":[…]}（部分自建网关）。
type modelsResponse struct {
	Data   []rawDiscovered `json:"data"`
	Models []rawDiscovered `json:"models"`
}

// Discover 向一个端点要它**实际**提供的模型清单。
//
// 刻意不做内网地址拦截（与 web_fetch 的 SSRF 守卫相反）：探测的目标就是用户
// 自己的端点——vLLM 常跑在 127.0.0.1、公司网关在内网段。这条路径与注册表让
// 用户手填 base_url 是同一类信任边界（用户显式发起的本机动作）。
func (s *Service) Discover(ctx context.Context, in DiscoverInput) (DiscoverResult, error) {
	base := strings.TrimSpace(in.BaseURL)
	if err := validateBaseURL(base); err != nil {
		return DiscoverResult{}, err
	}
	format := in.Format
	if format == "" {
		format = config.FormatOpenAI
	}
	if format != config.FormatOpenAI && format != config.FormatAnthropic {
		return DiscoverResult{}, fmt.Errorf("format 必须是 %q 或 %q: %q", config.FormatOpenAI, config.FormatAnthropic, format)
	}
	endpoint := modelsURL(base)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("构造探测请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	applyAuth(req, format, in.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("连接端点失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes))
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("读取端点响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return DiscoverResult{}, statusError(resp.StatusCode, body)
	}

	var parsed modelsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return DiscoverResult{}, fmt.Errorf("端点返回的不是模型清单（期望 JSON 的 data/models 数组）: %w", err)
	}
	entries := parsed.Data
	if len(entries) == 0 {
		entries = parsed.Models
	}
	out := make([]Discovered, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		id := strings.TrimSpace(e.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(e.Name)
		if name == "" {
			name = strings.TrimSpace(e.DisplayName)
		}
		out = append(out, Discovered{ID: id, Name: name})
	}
	// 端点给什么顺序都有（有的按 created 倒序、有的字典序），排一遍让 UI 稳定。
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	s.enrichFromCatalog(out)
	return DiscoverResult{Endpoint: endpoint, Format: format, Models: out}, nil
}

// enrichFromCatalog 用**内存中的**目录快照按模型 id 给探测结果补元数据。
//
// 两条刻意的设计：
//   - 只读内存快照（s.catalog），不触发目录刷新——探测是用户正在等的一个交互，
//     为锦上添花的元数据多等一次 15s 的目录拉取不值；快照不在内存就跳过
//     （服务启动时已尽力装缓存，实际命中率很高）。
//   - 同一模型 id 可能出现在多个厂商（gpt-4o 在 openai 与 azure 都有）：取目录
//     序第一个（厂商已按名称/ID 排序，结果稳定），不猜哪个更对。
func (s *Service) enrichFromCatalog(models []Discovered) {
	s.mu.RLock()
	cat := s.catalog
	s.mu.RUnlock()
	if cat == nil {
		return
	}
	index := make(map[string]Model)
	for _, p := range cat.Providers {
		for _, m := range p.Models {
			if _, ok := index[m.ID]; !ok {
				index[m.ID] = m
			}
		}
	}
	for i := range models {
		cm, ok := index[models[i].ID]
		if !ok {
			continue
		}
		models[i].Context = cm.Context
		models[i].Tools = cm.Tools
		models[i].Vision = cm.Vision
		models[i].JSONOut = cm.JSONOut
		models[i].Reasoning = cm.Reasoning
		// 上限不小于窗口时不填（与前端 catalogMetadata 同一守卫）：config.validate
		// 硬拒这种组合（输入+输出会超限），照抄会让模型整条加不进去。留空 = 未知。
		if cm.MaxOutput > 0 && (cm.Context == 0 || cm.MaxOutput < cm.Context) {
			models[i].MaxOutput = cm.MaxOutput
		}
	}
}

// applyAuth 按格式挂鉴权头。无 key 不挂（自建端点常不鉴权，挂了反而被拒）。
func applyAuth(req *http.Request, format, key string) {
	if key == "" {
		return
	}
	if format == config.FormatAnthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", anthropicVersion)
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
}

// validateBaseURL 只校验形状（scheme + host）——地址可达性由请求本身回答。
func validateBaseURL(base string) error {
	if base == "" {
		return fmt.Errorf("端点地址不能为空")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("端点地址必须是带 http(s) scheme 的完整地址: %q", base)
	}
	return nil
}

// modelsURL 把端点地址归一化到它的模型清单路径。
//
// 与 llm.chatCompletionsURL 同款三形态（裸地址 / 带 /v1 / 完整路径）：用户从
// 注册表复制过来的地址常是完整路径（…/v1/chat/completions），直接拼 /v1/models
// 会得到 …/v1/chat/completions/v1/models 这种必然 404 的地址。
func modelsURL(base string) string {
	u := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(u, "/models") {
		return u
	}
	for _, suffix := range []string{"/chat/completions", "/completions", "/messages"} {
		if strings.HasSuffix(u, suffix) {
			u = strings.TrimSuffix(u, suffix)
			break
		}
	}
	u = strings.TrimRight(u, "/")
	if strings.HasSuffix(u, "/v1") {
		return u + "/models"
	}
	return u + "/v1/models"
}

// statusError 把非 200 变成能自解释的错误：用户该看到「检查 API Key」，
// 而不是一个光秃秃的状态码。
func statusError(status int, body []byte) error {
	hint := ""
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		hint = "（检查 API Key）"
	case http.StatusNotFound:
		hint = "（该端点没有 /v1/models——可能不是 OpenAI 兼容端点）"
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "…"
	}
	if snippet == "" {
		return fmt.Errorf("端点返回 HTTP %d%s", status, hint)
	}
	return fmt.Errorf("端点返回 HTTP %d%s: %s", status, hint, snippet)
}
