// Agent 定义的字段级更新与共享校验。
//
// 为什么放在 sessiondata 而不是 store 或 tools：AgentFieldUpdate 是「对一条
// AgentDef 做字段级修改」的请求形状，ValidateAgentFieldUpdate 是这份请求的
// 全部硬校验——它必须被**所有**改 Agent 配置的写路径共用（现在只有 agent_write
// 工具一条，将来若加别的入口，规则漂移的代价是两套入口各放行一批对方拒绝的
// 数据）。sessiondata 是两侧共同的数据层，纯函数无依赖，store 与 tools 都
// 已经 import 它。
package sessiondata

import (
	"fmt"
	"strings"
)

// AgentFieldUpdate 是对一条 Agent 定义的字段级更新（agent_write 的参数形状）。
//
// 指针字段区分「没提供」与「提供但为空」：Desc 可以清空（*string 指到空串），
// Prompt 不允许空（校验拦下），Tools/Skills 用 nil = 不改、非 nil = 全量替换
// 表达同一件事（切片没有「提供了空」与「没提供」之外的第三态歧义——空切片
// 就是显式清空，校验按语义处理）。
type AgentFieldUpdate struct {
	// Desc 职责描述（主 Agent 的选人信号）。nil = 不改。
	Desc *string
	// Prompt 系统提示词模板。nil = 不改；非 nil 必须非空白（模板为空 = 该
	// Agent 失去身份——四层组合里它那层变成空串，错误在写入前拦下）。
	Prompt *string
	// Tools 工具白名单。nil = 不改；非 nil = 全量替换，至少保留一项且每个
	// 名字必须在工具目录里真实存在（写不存在的名字 = 白名单静默失效）。
	Tools []string
	// Skills 技能绑定。nil = 不改；非 nil = 全量替换（可以为空——不绑技能
	// 是合法配置），每个 id 必须在模块目录里存在。
	Skills []string
}

// ValidateAgentFieldUpdate 校验一次字段级更新（toolExists / skillExists 是
// 目录查询的注入口——调用方决定「存在」以什么为准：store 查 tools/modules 表，
// 测试给桩）。返回 nil = 通过。
//
// 硬校验清单（每条都有测试钉住）：
//  1. 主 Agent 不可改——它的工具白名单是结构性的（只有调度通道），提示词走
//     每轮现组装；改主 Agent 等于改调度中枢本身，不属于配置维护的语义范围；
//  2. Prompt 非空白（模板非空）；
//  3. Tools 非 nil 时不得为空（清空白名单 = 该 Agent 失去全部工具，等同失能；
//     要「没有工具的 Agent」应该删掉重建或直接禁用，不走字段级写）；
//  4. Tools 里每个名字必须在目录里存在（未知名字逐个列出）；
//  5. Skills 里每个 id 必须在模块目录里存在。
//
// id 不可改是**形状保证**而非校验：AgentFieldUpdate 根本没有 id 字段，目标是
// 更新的寻址键（UpdateAgentFields(id, u)），不存在「改 id」这条路径。
func ValidateAgentFieldUpdate(def AgentDef, u AgentFieldUpdate, toolExists, skillExists func(string) bool) error {
	if def.IsMain {
		return fmt.Errorf("主 Agent 不可经配置维护修改（它的工具白名单是结构性的、提示词每轮现组装）——如需调整委派名单，用 Agent 编辑器")
	}
	if u.Prompt != nil && strings.TrimSpace(*u.Prompt) == "" {
		return fmt.Errorf("提示词模板不能为空（空模板 = 该 Agent 失去身份）")
	}
	if u.Tools != nil {
		if len(u.Tools) == 0 {
			return fmt.Errorf("工具白名单不能清空（清空 = 该 Agent 失去全部工具）——要停用请禁用该 Agent")
		}
		var unknown []string
		for _, name := range u.Tools {
			if toolExists != nil && !toolExists(name) {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			return fmt.Errorf("工具白名单含不存在的工具: %s（只能写工具目录里真实存在的名字）", strings.Join(unknown, "、"))
		}
	}
	if u.Skills != nil && skillExists != nil {
		var unknown []string
		for _, id := range u.Skills {
			if !skillExists(id) {
				unknown = append(unknown, id)
			}
		}
		if len(unknown) > 0 {
			return fmt.Errorf("技能绑定含不存在的模块: %s（只能绑定模块目录里已有的条目）", strings.Join(unknown, "、"))
		}
	}
	return nil
}
