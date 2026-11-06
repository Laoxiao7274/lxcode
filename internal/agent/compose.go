// Agent 上下文组装（M2——路线图 docs/backend-roadmap.md）：四层组合
// （协议 → 流程模块 → 技能模块 → 自定义段）+ 动态注入（委派名单），
// 叠加既有的工作目录说明与动态工具清单。协议层与前端 shared/agent-protocol.ts
// 的默认文本一致（可定制——AgentDef.Protocol 非空时整段替换）。
package agent

import (
	"fmt"
	"sort"
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
		"2. 通过 agent_dispatch 下发——任务描述必须自带验收标准",
		"3. 验收子任务结果（不合格的带着理由重派或自己说明）",
		"4. 汇总答复用户；没有合适人选时说明缺口，不硬派",
		"",
		"选人纪律（名单里各 Agent 的描述就是它的职责边界，按描述选人）：",
		"- 同一份职责只派给一个 Agent：写/改代码 → 写代码的那个；纯只读调研/排查 → 只读的那个（实现 Agent 的白名单与职责都不含调研）；跑构建测试 → 验证的那个；改 Agent 配置 → 配置维护的那个；提交/推送/回滚 → 仓库管理 Agent",
		"- 任务跨多个职责就拆成多个 dispatch（互不依赖的可并行下发），不要让一个 Agent 越界完成另一类的活",
		"- 派出去的任务若需要别类能力（如调研后要改代码），先收结论再按结论另派，不要求同一个 Agent 兼顾",
		"",
		"流水线纪律（代码改动默认走三段式）：",
		"- 调研（researcher）→ 实现（coder）→ 验证（tester），派发之间传递**调研结论原文**而不是转述摘要——转述丢的细节往往是排查的关键",
		"- 三段式可按任务裁剪：一行文案类小改动不需要完整三段；但**写代码的任务不许跳过 tester 验证**——自己的代码自己测会放水",
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
		"- 收到职责范围外的请求时不动手，在结论里注明「这属于 XX Agent 的职责，建议另行派发」（如写代码的收到纯调研、跑全量测试套件的要求），不越界硬干",
	}, "\n")
}

// assistantProtocol 是助手 Agent 的内置协议（个人助理——小事自己干、
// 项目任务经 run_task 派发进项目）。
//
// 与 main 的分工：主 Agent 是「调度中枢」（一切任务都派发、不亲自执行）；
// 助手是「助理优先」——日常问答/检索/写作类直接自己干，只有明确指向某个
// 项目的任务才经 run_task 派进项目里。
//
// 2026-12 用户拍板「助手默认工具完全可配」：白名单可勾入内置写类工具
//（bash/edit/write_file），最后一条边界句按 allowTools 条件化——有写类工具
// 时句子不再说「不能改项目里的文件」（假边界会让模型自己混着用或谎报能力），
// 改为「项目内的代码改动仍优先经 run_task 派给项目里的 Agent」；其余段落
//（run_task 纪律 / 感知当前活动 / MCP 管理）与写类无关，两态同文。
// 「你的工作目录不是任何项目」句**恒保留**（写类工具也不改变助手的域归属）。
func assistantProtocol(allow map[string]bool) string {
	writeTool := allow["bash"] || allow["edit"] || allow["write_file"]
	boundary := "- 工具受白名单约束（只读面 + run_task）：不能改项目里的文件——改动必须经 run_task 派进项目"
	if writeTool {
		boundary = "- 你有写类工具（bash/edit/write_file），日常/临时文件可直接改；项目内的代码改动仍优先经 run_task 派给项目里的 Agent"
	}
	return strings.Join([]string{
		"你是用户的个人智能体助手。",
		"",
		"分工原则：",
		"- 日常事务（问答、检索、写作、日程、文件整理类）自己直接干，不派发",
		"- 任务明确指向某个项目（改代码、跑构建测试、按项目约定干活）时，用 run_task 派给项目里的 Agent",
		"",
		"run_task 纪律：",
		"- project_id 必须是用户明确指到的项目；用户没指到就不猜——先问清是哪个项目",
		"- 用户提到某个项目或问「我有哪些项目」时，先看上方可用项目清单或用 project_list 查——你的工作目录不是任何项目，别拿工作目录的文件回答项目问题",
		"- agent 参数按职责选：调研/排查/查资料→researcher；写代码/改代码→coder；跑测试→tester；仓库操作→repo-manager；改 Agent 配置→lxagent。拿不准职责时宁可默认也不要指定——默认执行者会按名单顺序选。别让调研任务落到 coder 上",
		"- 派发后等结论回来转述给用户，不替子 Agent 编造结果",
		"",
		"感知用户当前活动：",
		"- 用户问「我在做什么/最近在忙什么/项目近况」→ 先看当前状态与可用项目注入段（此刻运行中的会话就在当前状态段里），要完整清单用 project_list",
		"- 用户问「我们聊过什么/上次怎么解决的」→ 用 session_search 搜会话历史，不靠猜",
		"- 当前状态段里的会话属于项目模式（主域工作区），不是你与用户的对话——不要把其中的会话标题说成『我们的对话』；用户在助手模式与你聊天，与项目模式的会话是两回事",
		"- 不要拿 git log 猜用户的活动——注入段与查询工具才是事实来源",
		"",
		"MCP 管理（mcp_manage）：你有 mcp_manage 工具，可以查询/安装/修改/删除用户的 MCP 服务器。",
		"- 用户说「装个 xxx 的 MCP」「看看我有哪些 MCP 服务器」这类需求时用它，不派发",
		"- add 会真实在本机启动命令/连接端点——安装前先向用户复述 command/url 并确认",
		"- 新装的服务器默认自动绑定到你的 MCP 列表，装完即可用；删除后绑定项一并失效",
		"",
		"- 不确定就问，不编造",
		boundary,
	}, "\n")
}

// ProtocolRole 是协议形态的维度枚举（main/assistant/sub 三态）。DefaultProtocol
// 按维度选择，不再按 isMain bool——委派能力与主 Agent 身份解耦后，协议形态
// 是独立的维度（切面 B）。
type ProtocolRole string

const (
	ProtocolRoleMain      ProtocolRole = "main"
	ProtocolRoleSub       ProtocolRole = "sub"
	ProtocolRoleAssistant ProtocolRole = "assistant"
)

// DefaultProtocol 返回 Agent 的内置协议（main = 调度 / assistant = 助理 /
// sub = 执行）。AgentDef.Protocol 非空时整段替换（「宪法修正案」——保存时
// 与默认相同则存空）。assistant 角色的边界句按 allowTools 条件化（白名单
// 勾入写类工具时换成写类态句——见 assistantProtocol 注释）；allowTools 为 nil
// = 默认态（只读面，与「宪法修正案」的保存判定/前端镜像一致）。
func DefaultProtocol(role ProtocolRole, allowTools ...[]string) string {
	switch role {
	case ProtocolRoleMain:
		return mainProtocol()
	case ProtocolRoleAssistant:
		if len(allowTools) > 0 {
			return assistantProtocol(toolSet(allowTools[0]))
		}
		return assistantProtocol(nil)
	default:
		return subProtocol()
	}
}

// assistantOnboardingSection 是助手语境「首次初始化」的注入段（persona 为空时
// 追加在自定义段位置）：提示词驱动——助手在第一条回复时自然地问称呼与性格
// 偏好，收集后调用 update_assistant_config 保存。用户不想设定就不强求。
const assistantOnboardingSection = `用户的助手配置还没有性格设定。在对话开始时（你的第一条回复里）自然地向用户询问称呼偏好与性格/语气偏好（比如正式/轻松/简洁/详细），收集后调用 update_assistant_config 保存 persona 与 name。只问一次：若用户已回答过或明确表示不想设定，就不再重复问，也不要强求。`

// ComposeSystemPrompt 组装完整系统提示词：协议层（定制 ?? 内置默认）
// + 流程模块（单选注入）+ 技能索引（名称 + 一句话——渐进披露，模型
// 需要时经 read_skill 取全文，没注入的它当没有）+ 自定义段 + 动态注入
// （主 Agent 的有效委派名单；助手语境的可用项目全景）+ 项目约定（项目根
// AGENTS.md）+ 工作目录说明 + Git 工作树说明（wt/isChild）+ 工具清单（按
// 白名单过滤）+ 工作守则。ac 为 nil = 无 Agent 语境（兼容旧路径：全局默认
// 提示词）。panorama 是助手语境的可用项目快照（主域传 nil——调用方按语境
// 决定，见 Session.projectPanoramaFor）。
//
// 项目约定的位置即优先级（2026-09-21 用户拍板「项目约定比 Agent 自己的低」）：
// 排在 Agent 四层组合之后、环境与工具清单之前，并在段内显式声明。
//
// activity 是助手语境的「当前状态」快照（主域传零值——调用方按语境决定，见
// Session.activityFor）。导出的 ComposeSystemPrompt 不含它（零值=不注入）——
// 生产路径 turn.go/compaction.go 经 Session.activityFor 现取（每轮快照），
// 同包调用 composeSystemPrompt。
func ComposeSystemPrompt(toolReg *tools.Registry, workDir string, ac *sessiondata.AgentContext, allowTools []string, wt WorktreeInfo, isChild bool, docs ProjectDocs, panorama []sessiondata.ProjectStats, todoMode ...string) string {
	return composeSystemPrompt(toolReg, workDir, ac, allowTools, wt, isChild, docs, panorama, ActivitySnapshot{}, todoMode...)
}

func composeSystemPrompt(toolReg *tools.Registry, workDir string, ac *sessiondata.AgentContext, allowTools []string, wt WorktreeInfo, isChild bool, docs ProjectDocs, panorama []sessiondata.ProjectStats, activity ActivitySnapshot, todoMode ...string) string {
	if ac == nil {
		return BuildSystemPrompt(toolReg, workDir, wt, isChild, docs, todoMode...)
	}
	var b strings.Builder

	// ① 协议层（可定制）
	protocol := ac.Def.Protocol
	if strings.TrimSpace(protocol) == "" {
		// 协议形态按维度选择（main/assistant/sub 三态——assistant 按域
		// 标记判定，sub 兜底兼容旧两类制路径）
		role := ProtocolRoleSub
		switch {
		case ac.Def.IsMain:
			role = ProtocolRoleMain
		case ac.Def.Space == sessiondata.SpaceAssistant:
			role = ProtocolRoleAssistant
		}
		protocol = DefaultProtocol(role, allowTools)
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

	// ③b 助手语境的首次初始化指令（性格为空时的主动询问——提示词驱动，
	// 不是硬代码弹窗）。Persona 为空 = 用户还没设定助手的性格/称呼：在
	// 对话开始时自然地问一次，收集后经 update_assistant_config 保存。指令
	// 每轮都在（直到 persona 非空——现读配置），「只问一次」由助手自觉
	//（软约束，简单方案）：提示词里写明「若用户已回答过就不再重复问」。
	if ac.Def.Space == sessiondata.SpaceAssistant && strings.TrimSpace(ac.Def.Prompt) == "" {
		b.WriteString("\n" + assistantOnboardingSection + "\n")
	}

	// ④ 动态注入：可委派名单按 Delegates 维度判定（不按 IsMain——委派
	// 能力与主 Agent 身份解耦，前置 assistant Agent 定义；子 Agent 的职责
	// 描述 = 委派方的选人信号）。id 必须在场——agent_dispatch 的入参是
	// id 不是名字（真实模型实测：只给名字会把名字当 id 传）。
	if len(ac.Def.Delegates) > 0 && len(ac.Delegates) > 0 {
		b.WriteString("\n可委派名单（agent_dispatch 的 agent 参数填下面的 id）：\n")
		for i := range ac.Delegates {
			d := ac.Delegates[i]
			status := ""
			if !d.Enabled {
				status = "（已停用——不可分派）"
			}
			b.WriteString(fmt.Sprintf("- %s（%s）：%s%s\n", d.ID, d.Name, d.Desc, status))
		}
	}

	// ④b 助手语境的可用项目全景（run_task 的 project_id 唯一可读来源——
	// id 是不可读的时间戳随机串，模型没有任何其他途径获取）。按 Space 二次
	// 判定（即使调用方误传数据，主域会话也不注入）；每轮快照：长会话里注册
	// 新项目下一轮就可见，实时详查走 project_list 工具。同位注入「当前状态」
	//（最近会话 + 此刻运行中——快照经 SetCurrentActivity 挂载，见
	// activitySection）。
	if ac.Def.Space == sessiondata.SpaceAssistant {
		b.WriteString(projectPanoramaSection(panorama))
		b.WriteString(activitySection(activity))
	}

	// ⑤ 项目约定（项目根 AGENTS.md）——低于上面四层，高于环境与工具清单
	b.WriteString(projectDocsSection(docs))

	// 环境说明（工作目录 + Git 工作树）+ 工具清单（白名单过滤）+ 守则
	b.WriteString("\n" + workdirLine(workDir))
	b.WriteString(worktreeLine(wt, isChild))
	b.WriteString("\n可用工具：\n")
	allowed := toolSet(allowTools)
	for _, name := range toolReg.Order() {
		if allowed != nil && !allowed[name] {
			continue
		}
		if d, ok := toolReg.Get(name); ok {
			b.WriteString(toolLine(d))
		}
	}
	// 白名单里勾了但注册表里没有的工具：如实告知，别让模型以为它有
	//（自定义工具没配 command、MCP 服务器没启用都会落到这里）
	if missing := missingTools(toolReg, allowTools); len(missing) > 0 {
		b.WriteString("注意：白名单里的 " + strings.Join(missing, "、") +
			" 当前不可用（自定义工具需先配置 command，MCP 工具需先启用服务器）——不要调用它们。\n")
	}
	b.WriteString(systemPromptFooter(firstTodoMode(todoMode)))
	return b.String()
}

// missingTools 返回白名单里未注册的工具名（稳定顺序）。
func missingTools(toolReg *tools.Registry, allow []string) []string {
	if len(allow) == 0 {
		return nil
	}
	var out []string
	for _, name := range allow {
		if _, ok := toolReg.Get(name); !ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
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
