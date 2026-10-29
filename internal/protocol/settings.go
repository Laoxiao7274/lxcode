package protocol

import (
	"github.com/moyunteng/lxcode/internal/websearch"
)

// ---- 网页搜索渠道 ----

// SearchChannelsResult 是渠道快照（也是 search.changed 事件的载荷，
// 与 ModelListResult 同款：一次拉全，客户端不需要增量合并）。
//
// Channels 含**全部内置渠道**（含未配置的）——前端要能列出可配置的渠道，
// 只回已配置的话用户永远看不到「还能配什么」。
type SearchChannelsResult struct {
	Channels []websearch.Channel `json:"channels"`
	// Primary 是主渠道 id（空 = 未指定，按预设顺序降级）。
	Primary string `json:"primary,omitempty"`
	// Ready 报告是否存在至少一个就绪渠道——前端据此决定要不要提示去配置。
	Ready bool `json:"ready"`
}

// SearchChannelSaveParams 保存（upsert）一个渠道的配置。
//
// APIKey 为空表示**清空**该渠道的 key（用户主动删 key 的场景）；
// 想「只改 base_url 不动 key」请先把现有 key 回填（前端本来就拿着快照）。
//
// Options 是渠道私有设置（键名见该渠道的 option_specs）；整体覆盖而非增量合并
// ——与 APIKey/BaseURL 同款语义：提交的载荷就是该渠道配置的完整新状态。
type SearchChannelSaveParams struct {
	ID      string            `json:"id"`
	APIKey  string            `json:"api_key"`
	BaseURL string            `json:"base_url"`
	Options map[string]string `json:"options,omitempty"`
	Enabled bool              `json:"enabled"`
}

// SearchChannelRefParams 按 id 定位一个渠道（删除用）。
type SearchChannelRefParams struct {
	ID string `json:"id"`
}

// SearchPrimarySetParams 设主渠道（空 id = 清空，回落预设顺序第一个就绪渠道）。
type SearchPrimarySetParams struct {
	ID string `json:"id"`
}

// SearchTestParams 测试单个渠道（不降级）。
type SearchTestParams struct {
	ID string `json:"id"`
	// Query 测试查询词；空 = 用一个默认词（用户点测试时通常不想先想关键词）。
	Query string `json:"query,omitempty"`
}

// SearchTestResult 是 search.test 的结果：把渠道真实返回的摘要回给前端，
// 让用户当场看到「这个 key 到底能不能用」。
type SearchTestResult struct {
	Provider string             `json:"provider"`
	Answer   string             `json:"answer,omitempty"`
	Results  []websearch.Result `json:"results"`
	// ElapsedMS 是耗时（用户判断渠道快慢的依据）。
	ElapsedMS int `json:"elapsed_ms"`
}

// TodoPromptResult 是 settings.todoPrompt.get 的结果，也是
// settings.todoPrompt.changed 事件的载荷（一份形状——与 search.changed 同款）。
type TodoPromptResult struct {
	// TodoPrompt 是当前档位（strict/normal/off；读侧已归一，恒非空）。
	TodoPrompt string `json:"todoPrompt"`
}

// TodoPromptSetParams 是 settings.todoPrompt.set 的参数。
type TodoPromptSetParams struct {
	TodoPrompt string `json:"todoPrompt"`
}

// DefaultApprovalResult 是 settings.defaultApproval.get 的结果，也是
// settings.defaultApproval.changed 事件的载荷（一份形状——与 todoPrompt 同款）。
type DefaultApprovalResult struct {
	// Approval 是当前默认档（auto/confirm/strict；读侧已归一，恒非空）。
	Approval string `json:"approval"`
}

// DefaultApprovalSetParams 是 settings.defaultApproval.set 的参数。
type DefaultApprovalSetParams struct {
	Approval string `json:"approval"`
}
