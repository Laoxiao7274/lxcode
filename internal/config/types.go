// Package config 管理模型注册表：模型条目的增删改查、角色绑定
// （default/vision → 模型）与 JSON 文件持久化（临时文件 + rename 原子写）。
// 注册表是模型配置的唯一事实来源，llm 客户端按其中的条目连接推理端点
// （本地 vLLM/llama.cpp/ollama、云端 OpenAI 兼容与 Anthropic 端点皆可）。
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// wire 格式常量：注册条目用哪种协议适配器调用端点。
// 只支持这两种——主流推理后端（vLLM / llama.cpp / ollama）双格式都提供，
// 云端厂商也几乎都落在 OpenAI 兼容或 Anthropic 两种形态上。
const (
	FormatOpenAI    = "openai"    // OpenAI chat completions（默认）
	FormatAnthropic = "anthropic" // Anthropic Messages /v1/messages
)

// 角色常量：功能域 → 模型的绑定槽位。
const (
	RoleDefault = "default" // 主对话（agent 循环使用）
	RoleVision  = "vision"  // 识图（截图理解，桌面壳接入后使用）
)

// Roles 是全部合法角色，SetRole 只接受其中的值。
var Roles = []string{RoleDefault, RoleVision}

// Capabilities 是模型能力的用户声明：只是路由参考，不是探测缓存——
// 真实能力以 model test 实测为准（AGENTS.md §4.1 本地模型能力约束）。
// Reasoning 声明支持推理强度档位（OpenAI reasoning_effort / Anthropic
// thinking budget）——未声明时 chat.send 的 effort 参数对该模型不生效
// （对不支持的端点传参会直接 400，能力门控是硬需求不是优化）。
// StreamTools 声明该 openai 格式端点的**流式带工具**可用（流式 delta 里
// tool_calls 完整下发）——只有逐端点实测确认过的模型才应置 true；缺省
// false = 维持现状（openai 带工具走非流式回放），schema 向后兼容。
type Capabilities struct {
	Tools       bool `json:"tools"`
	Vision      bool `json:"vision"`
	JSONOut     bool `json:"json_output"`
	Reasoning   bool `json:"reasoning"`
	StreamTools bool `json:"stream_tools,omitempty"`
}

// ProviderSep 是组合键分隔符。注册表主键从「纯 model id」扩展为
// `provider::model`（KeySep），provider 本身是自由文本——不进主键。
const ProviderSep = "::"

// ModelConfig 是注册表中一个模型条目的完整配置（models.json 单条）。
type ModelConfig struct {
	ID string `json:"id"`
	// Provider 是可选的「提供商」标签：同一 id 在不同提供商（不同 base_url /
	// 凭据 / 协议）下可以并存，磁盘主键 = provider::id（见 Key）。
	// 空 = 存量数据（没有这个字段），主键就是纯 id——**存量零迁移**。
	Provider        string       `json:"provider,omitempty"`
	DisplayName     string       `json:"display_name,omitempty"`
	BaseURL         string       `json:"base_url"`
	APIKey          string       `json:"api_key,omitempty"`
	Format          string       `json:"format,omitempty"` // 空 = FormatOpenAI
	Model           string       `json:"model"`
	ContextWindow   int          `json:"context_window,omitempty"`
	MaxOutputTokens int          `json:"max_output_tokens,omitempty"`
	Capabilities    Capabilities `json:"capabilities"`
	Enabled         bool         `json:"enabled"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// EffectiveFormat 返回规范化后的 wire 格式（空值归一为 openai）。
func (m ModelConfig) EffectiveFormat() string {
	if m.Format == "" {
		return FormatOpenAI
	}
	return m.Format
}

// Key 是条目的**注册表主键**：有 provider 时 = `provider::id`，无 provider
// （存量数据）时 = 纯 id。这条规则是全链路唯一的口径——内存 map 的键、
// 落盘的 roles 取值、sessions.model / agents.model 的引用都是它。
func (m ModelConfig) Key() string {
	if m.Provider == "" {
		return m.ID
	}
	return m.Provider + ProviderSep + m.ID
}

// SplitKey 把引用值拆成 (provider, id)：引用可以是组合键（`provider::id`），
// 也可以是纯 id（存量引用 / 无 provider 条目）。解析不出 provider 时返回 ""。
func SplitKey(ref string) (provider, id string) {
	if i := strings.Index(ref, ProviderSep); i > 0 && i+len(ProviderSep) < len(ref) {
		return ref[:i], ref[i+len(ProviderSep):]
	}
	return "", ref
}

// validate 校验条目字段：id/model 非空、base_url 带合法 scheme、format 合法、
// max_output_tokens 小于 context_window——输出上限逼近窗口会导致输入+输出超限
// 报错（判据就是本函数下一行那条检查，前端 validateProviderPatch 与之对齐）。
func (m ModelConfig) validate() error {
	if m.ID == "" {
		return fmt.Errorf("id 不能为空")
	}
	// 主键是 provider::id，所以分隔符出现在任一段里都会让引用解析歧义——
	// 早拒比事后猜用户想指哪个模型好。
	if strings.Contains(m.ID, ProviderSep) {
		return fmt.Errorf("id 不能包含 %q（那是 provider 与 id 的分隔符）", ProviderSep)
	}
	if strings.Contains(m.Provider, ProviderSep) {
		return fmt.Errorf("provider 不能包含 %q", ProviderSep)
	}
	if m.Model == "" {
		return fmt.Errorf("model 不能为空")
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("base_url 必须是带 http(s) scheme 的完整地址: %q", m.BaseURL)
	}
	switch m.Format {
	case "", FormatOpenAI, FormatAnthropic:
	default:
		return fmt.Errorf("format 必须是 %q 或 %q: %q", FormatOpenAI, FormatAnthropic, m.Format)
	}
	if m.ContextWindow < 0 || m.MaxOutputTokens < 0 {
		return fmt.Errorf("context_window / max_output_tokens 不能为负数")
	}
	if m.ContextWindow > 0 && m.MaxOutputTokens > 0 && m.MaxOutputTokens >= m.ContextWindow {
		return fmt.Errorf("max_output_tokens(%d) 必须小于 context_window(%d)——输入+输出会超限",
			m.MaxOutputTokens, m.ContextWindow)
	}
	return nil
}
