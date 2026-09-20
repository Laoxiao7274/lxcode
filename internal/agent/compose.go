// Agent 上下文组装（M2——路线图 docs/backend-roadmap.md）：四层组合
// （协议 → 流程模块 → 技能模块 → 自定义段）+ 动态注入（委派名单），
// 叠加既有的工作目录说明与动态工具清单。协议层与前端 shared/agent-protocol.ts
// 的默认文本一致（可定制——AgentDef.Protocol 非空时整段替换）。
package agent

import (
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// mainProtocol 是主 Agent 的内置调度协议（决策与分派——不亲自执行）。
func mainProtocol() string {
	return strings.Join([]string{
		"你是主 Agent（调度中枢）。你不亲自执行任务。",
		"",
		"收到用户请求后：",
		"1. 判断意图，从「可委派名单」选最合适的 Agent",
		"2. 通过 agent.dispatch 下发——任务描述必须自带验收标准",
		"3. 验收子任务结果（不合格的带着理由重派或自己说明）",
		"4. 汇总答复用户；没有合适人选时说明缺口，不硬派",
	}, "\n")
}

// subProtocol 是子 Agent 的内置执行协议（纯执行者，不可委派）。
func subProtocol() string {
	return strings.Join([]string{
		"你是执行 Agent（纯执行者，不可委派）。",
		"",
		"- 按收到的任务执行，受工具白名单约束（工具清单随白名单生成）",
		"- 结果如实回传：做了什么、输出是什么、有什么问题",
		"- 不确定就问，不编造",
	}, "\n")
}

// DefaultProtocol 返回 Agent 的内置协议（主 = 调度 / 子 = 执行）。
// AgentDef.Protocol 非空时整段替换（「宪法修正案」——保存时与默认
// 相同则存空）。
func DefaultProtocol(isMain bool) string {
	if isMain {
		return mainProtocol()
	}
	return subProtocol()
}

// ComposeSystemPrompt 组装完整系统提示词：协议层（定制 ?? 内置默认）
// + 流程模块（单选注入）+ 技能索引（名称 + 一句话——渐进披露，模型
// 需要时经 read_skill 取全文，没注入的它当没有）+ 自定义段 + 动态注入
// （主 Agent 的有效委派名单）+ 工作目录说明 + 工具清单（按白名单过滤）
// + 工作守则。ac 为 nil = 无 Agent 语境（兼容旧路径：全局默认提示词）。
func ComposeSystemPrompt(toolReg *tools.Registry, workDir string, ac *sessiondata.AgentContext, allowTools []string) string {
	if ac == nil {
		return BuildSystemPrompt(toolReg, workDir)
	}
	var b strings.Builder

	// ① 协议层（可定制）
	protocol := ac.Def.Protocol
	if strings.TrimSpace(protocol) == "" {
		protocol = DefaultProtocol(ac.Def.IsMain)
	}
	b.WriteString(protocol)
	b.WriteString("\n")

	// ② 模块层：流程单选（工作方式是原子单元——正文直接注入）+ 技能索引
	//（渐进披露：只注入名称与摘要，正文经 read_skill 按需获取——上下文
	// 不被没在用的技能撑爆）。
	if ac.Workflow != nil {
		b.WriteString("\n" + strings.TrimSpace(ac.Workflow.Body) + "\n")
	}
	if len(ac.Skills) > 0 {
		b.WriteString("\n可用技能（按需用 read_skill 取完整内容）：\n")
		for i := range ac.Skills {
			b.WriteString(fmt.Sprintf("- %s：%s\n", ac.Skills[i].ID, ac.Skills[i].Desc))
		}
	}

	// ③ 自定义段
	if p := strings.TrimSpace(ac.Def.Prompt); p != "" {
		b.WriteString("\n" + p + "\n")
	}

	// ④ 动态注入：主 Agent 的有效委派名单（子 Agent 的职责描述 =
	// 主 Agent 的选人信号）
	if ac.Def.IsMain && len(ac.Delegates) > 0 {
		b.WriteString("\n可委派名单（agent.dispatch 只可调用以下 Agent）：\n")
		for i := range ac.Delegates {
			d := ac.Delegates[i]
			status := ""
			if !d.Enabled {
				status = "（已停用——不可分派）"
			}
			b.WriteString(fmt.Sprintf("- %s：%s%s\n", d.Name, d.Desc, status))
		}
	}

	// 环境说明 + 工具清单（白名单过滤）+ 守则
	b.WriteString("\n" + workdirLine(workDir))
	b.WriteString("\n可用工具：\n")
	allowed := toolSet(allowTools)
	for _, name := range toolReg.Order() {
		if allowed != nil && !allowed[name] {
			continue
		}
		if desc, ok := systemPromptTools[name]; ok {
			b.WriteString("- " + desc + "\n")
		}
	}
	b.WriteString(systemPromptFooter)
	return b.String()
}

// toolSet 把白名单转为集合（nil = 不过滤——兼容无 Agent 语境）。
func toolSet(allow []string) map[string]bool {
	if allow == nil {
		return nil
	}
	out := make(map[string]bool, len(allow))
	for _, n := range allow {
		out[n] = true
	}
	return out
}
