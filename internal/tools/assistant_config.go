// update_assistant_config 工具（助手自改配置）：助手 Agent 修改自己的名字与
// 性格（写 config/assistant.json）。用户拍板的需求——「用户让你改性格、换个
// 称呼时用；改完向用户确认已生效」。
//
// 注入模式（与 SSHHostLookup / run_task 的执行体同一条纪律）：配置的读写口由
// server 装配层经 SetAssistantConfigFn 注入（Set 函数含「读当前 → 合并 → 写盘
// → 广播」的完整链路），工具层不知道配置路径与事件域。未装配 = 工具回自解释
// 错误（助手域是唯一白名单持有者；主/子 Agent 的白名单不含它）。
//
// 风险定级：低危、不变更（RiskLow / Mutates=false）——只写一个几十字节的配置
// 文件，不碰工作区；改的是下一轮的提示词组装（现读，下一轮生效）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// AssistantConfigToolName 是 update_assistant_config 工具的 id。
//
// 必须匹配 `^[a-zA-Z0-9_-]{1,64}$`（与 run_task 同一条工具名约束）。
const AssistantConfigToolName = "update_assistant_config"

// AssistantConfigSaveFn 是「保存助手配置」的实现约定（server 装配层构造：
// 读当前 → 部分更新 → 写盘 → 广播 configChanged）。当前配置不足四字段时视为
// 清空（调用方传全量）。
type AssistantConfigSaveFn = func(cfg AssistantConfigPatch) error

// AssistantConfigPatch 是工具入参的形状（tools 是叶子包，不 import config）。
// 各指针字段非 nil = 本次要改的字段；nil = 不动。
//
// **刻意不含 ToolWhitelist（工具白名单）、Tools（自定义命令工具绑定）与
// MCPServers**：助手 Approval=auto，它能自改的工具入参形状就是它的权限边界
// ——工具白名单是助手的全部可执行面（含内置写类 bash/edit，auto 档下不经
// 确认直接运行），助手自己给自己改白名单 = 结构性自我提权；绑定/白名单变更
// 只能由用户在配置页走 assistant.config.set（服务端有现读校验）。
type AssistantConfigPatch struct {
	Name     *string   `json:"name,omitempty"`
	Persona  *string   `json:"persona,omitempty"`
	Skills   *[]string `json:"skills,omitempty"`
	Workflow *string   `json:"workflow,omitempty"`
}

// assistantConfigFn 是注册表上的注入槽（与 sessionSearch / mergeStart 同一套
// 锁纪律：装配期写一次、运行期只读，共用 searchMu；槽字段在 Registry 上，
// 见 registry.go）。

func (r *Registry) SetAssistantConfigFn(fn AssistantConfigSaveFn) {
	r.searchMu.Lock()
	r.assistantConfigFn = fn
	r.searchMu.Unlock()
}

func (r *Registry) getAssistantConfigFn() AssistantConfigSaveFn {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.assistantConfigFn
}

// assistantConfigDef 返回 update_assistant_config 的注册声明。
func assistantConfigDef(r *Registry) *Def {
	return &Def{
		Name: AssistantConfigToolName,
		Description: "修改你自己的配置（名字/性格/技能绑定）。用户让你改性格、换个称呼时用；" +
			"改完向用户确认已生效。改动下一轮生效（不需要用户重启或刷新）。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": { "type": "string", "description": "新的显示名（用户怎么称呼你）。不传 = 不改" },
				"persona": { "type": "string", "description": "新的性格/语气设定（一句话描述你该是什么风格，比如「轻松幽默，回答简洁」）。不传 = 不改；传空串 = 清除性格设定" },
				"skills": { "type": "array", "items": { "type": "string" }, "description": "技能模块 id 清单（全量替换）。id 必须真实存在（read_skill 清单里的）" },
				"workflow": { "type": "string", "description": "流程模板 id（全量替换）。不传 = 不改" }
			}
		}`),
		Risk: RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			fn := r.getAssistantConfigFn()
			if fn == nil {
				return "错误: 助手配置写入未装配（本环境不支持自改配置）。", nil
			}
			var p AssistantConfigPatch
			if err := json.Unmarshal(args, &p); err != nil {
				return fmt.Sprintf("错误: 参数解析失败: %v", err), nil
			}
			if p.Name == nil && p.Persona == nil && p.Skills == nil && p.Workflow == nil {
				return "错误: 没有提供任何要修改的字段（name/persona/skills/workflow 至少给一个）。", nil
			}
			if err := fn(p); err != nil {
				return fmt.Sprintf("错误: 保存助手配置失败: %v", err), nil
			}
			var changed []string
			if p.Name != nil {
				changed = append(changed, "名字 → "+*p.Name)
			}
			if p.Persona != nil {
				if *p.Persona == "" {
					changed = append(changed, "性格已清除")
				} else {
					changed = append(changed, "性格已更新")
				}
			}
			if p.Skills != nil {
				changed = append(changed, fmt.Sprintf("技能绑定 %d 项", len(*p.Skills)))
			}
			if p.Workflow != nil {
				changed = append(changed, "工作方式 → "+*p.Workflow)
			}
			return "助手配置已保存（" + joinStrings(changed, "；") + "）。下一轮对话起生效；请向用户确认改动内容。", nil
		},
	}
}

func joinStrings(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
