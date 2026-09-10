// Package config 管理模型注册表：模型条目的增删改查、角色绑定
// （default/vision → 模型）与 JSON 文件持久化（临时文件 + rename 原子写）。
// 注册表是模型配置的唯一事实来源，llm 客户端按其中的条目连接推理端点
// （本地 vLLM/llama.cpp/ollama、云端 OpenAI 兼容与 Anthropic 端点皆可）。
package config

import (
	"fmt"
	"net/url"
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
type Capabilities struct {
	Tools   bool `json:"tools"`
	Vision  bool `json:"vision"`
	JSONOut bool `json:"json_output"`
}

// ModelConfig 是注册表中一个模型条目的完整配置（models.json 单条）。
type ModelConfig struct {
	ID              string       `json:"id"`
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

// validate 校验条目字段：id/model 非空、base_url 带合法 scheme、format 合法、
// max_output_tokens 小于 context_window——云端版教训：输出上限逼近窗口会导致
// 输入+输出超限报错（docs/01-model-management.md §4.2）。
func (m ModelConfig) validate() error {
	if m.ID == "" {
		return fmt.Errorf("id 不能为空")
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
