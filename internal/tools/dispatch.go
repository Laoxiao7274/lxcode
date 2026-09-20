// agent.dispatch 工具（M3——主 Agent 的唯一调度通道）：把任务派给名单
// 中的子 Agent。执行体是 agent.Session.runDispatch（runTools 调用位
// 直连——子循环需要工具调用 id 做事件归属，走通用 Exec 的入参形状
// 给不了）；这里的 Exec 是未装配路径的兜底。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// DispatchCall 是一次调度的参数（agent 内核在 runTools 调用位发起）。
type DispatchCall struct {
	DispatchID string // 调用 id（事件归属键——= 主轮里该工具调用的 tc.ID）
	Agent      string // 目标 Agent 的名单 id
	Task       string // 任务描述（应自带验收标准）
	Context    string // 可选背景（主会话的关键约束，不是完整历史）
}

// DispatchResult 是调度执行的结果（回填主会话的工具结果）。
type DispatchResult struct {
	Output  string // 给主 Agent 的结果文本（子 Agent 最终回复或错误说明）
	IsError bool
}

// dispatchDef 返回 agent.dispatch 的注册声明。
// 风险定级：低危、不变更（Mutates=false）——工具本身只发起调度；子
// Agent 的工具执行有各自的确认门与权限门（取严继承），不存在借手
// 提权：子 Agent 看到的权限面 ≤ 主会话授权面。
// 两类制：子 Agent 的白名单不含 agent.dispatch——注册表有它但子语境
// 的白名单过滤天然挡掉（深度恒 1）。
func dispatchDef(r *Registry) *Def {
	return &Def{
		Name:        "agent.dispatch",
		Description: "把任务派给名单中的子 Agent 执行。任务描述必须自包含（目标、约束、验收标准）——子 Agent 看不到当前对话历史；需要背景时放进 context。适合有明确边界且值得独立执行的任务。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"agent": { "type": "string", "description": "目标 Agent 的名单 id（提示词「可委派名单」里列出的）" },
				"task": { "type": "string", "description": "任务描述——目标 + 约束 + 验收标准（子 Agent 据此独立完成并回传结果）" },
				"context": { "type": "string", "description": "可选背景：当前对话的关键约束/已尝试的路径（不是完整历史）" }
			},
			"required": ["agent", "task"]
		}`),
		Risk: RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Agent   string `json:"agent"`
				Task    string `json:"task"`
				Context string `json:"context"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			// 兜底：未装配 agent.Session 的路径（理论不可达——runTools
			// 调用位直连内核；保守给模型自解释错误）。
			return "错误: 调度执行体未装配（agent.Session 未初始化）。", nil
		},
	}
}
