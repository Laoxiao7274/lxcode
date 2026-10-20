package agent

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/tools"
)

// systemPromptHeader 是系统提示词的开头；工具清单从注册表动态生成
// （BuildSystemPrompt），不手写——手写清单在工具增减后就是过时信息，
// 模型不知道某个工具存在（local-myt-agent 的前车之鉴：清单在工具
// 4 个时过时成了 3 个）。
const systemPromptHeader = `你是 lxcode，运行在用户本机（Windows/类 Unix 桌面环境）上的个人智能体，
既是编程助手（读写代码、改文件、跑构建与测试），也处理日常事务。你直接为用户服务。
`

// workdirLine 是工作目录说明：模型必须知道相对路径与 bash 默认目录的
// 解析基准，否则会按自己的猜测编路径。项目会话显式给出项目根
// （session.new 带 workspace 注入），未分组会话是后端进程目录。
func workdirLine(workDir string) string {
	if workDir == "" {
		return "工作目录就是用户启动后端的目录。\n"
	}
	return fmt.Sprintf("当前会话归属一个项目，项目根目录即工作目录：%s\n"+
		"相对路径一律按此目录解析，bash 未指定 cwd 时也在此目录执行；项目外的文件用绝对路径访问。\n", workDir)
}

// WorktreeInfo 是本会话的工作区信息：分支名与基线提交。零值 = 未分组会话
// ——那时提示词零注入。
type WorktreeInfo struct {
	Branch string // 主检出当前分支名（2026-10 直连架构：不再是 lxcode/session-<id>）
	Base   string // 会话基线 HEAD（首次准备时记录；rollback 的 session-start 锚点）
	NoGit  bool   // 项目目录不是 git 仓库（Branch 为空）：无版本管理，提示词如实告知
}

// worktreeLine 渲染工作区说明段（零值零注入：逐字节等于未分组会话的提示词）。
//
// 为什么必须告诉模型：会话**直接工作在用户的主检出目录**（改动实时可见），而
// **轮末不会自动提交**（2026-10 用户拍板移除自动检查点，回滚方案后期重新设计）——
// 改动留在工作区，直到用户或模型显式要求提交（`workspace_sync`）。所以模型不要
// 自己反复提交、也不要改动已提交的历史；用户说「回滚」时只能回到手工/显式提交
// 过的位置。
//
// 子会话在同一份主检出里工作、改动与父会话共享——措辞必须区分，不能对它说
// 「你的会话目录」（它没有单独的；大家都在同一个项目目录里）。
func worktreeLine(wt WorktreeInfo, isChild bool) string {
	if wt.Branch == "" {
		if wt.NoGit {
			// 非 git 项目目录的降级态：如实告知「没有版本管理」，并且不让模型
			// 自作主张去 git init / 提交（那是用户的决定）。
			if isChild {
				return "你在父会话的项目目录里工作，该目录不是 Git 仓库：改动不会被提交，也不要执行 git 提交/分支类操作；用户问起版本管理时如实说明。\n"
			}
			return "当前会话直接在用户的项目目录里工作，但该目录不是 Git 仓库：改动不会被提交，也不要主动执行 git 提交/分支类操作（是否 git init 由用户决定，需要时如实说明现状）。\n"
		}
		return ""
	}
	if isChild {
		return fmt.Sprintf("你在父会话的项目目录里工作（Git 分支 %s），你的改动与父会话实时共享；"+
			"系统不会自动提交——改动留在工作区，等用户显式要求时再提交，也不要改动已提交的历史。\n", wt.Branch)
	}
	return fmt.Sprintf("当前会话直接在用户的项目目录里工作（Git 分支 %s），你在项目里做的修改对用户实时可见。"+
		"系统**不会**自动提交（轮末没有检查点提交）——改动留在工作区，直到用户显式要求提交；"+
		"不要自己反复提交，也不要改动已提交的历史。"+
		"用户问「改了哪些东西」用 `workspace_status`（主检出状态口径，不向用户解释分支概念除非他问）；"+
		"用户说「提交/推送」用 `workspace_sync`（提交到当前分支，可选拼直接推送）；"+
		"用户要「回滚/撤销」用 `workspace_rollback`（只能回到手工/显式提交过的位置——"+
		"自动检查点已移除，last-turn 指上一个提交而不是上一轮对话）。\n"+pushDisciplineLine(), wt.Branch)
}

// pushDisciplineLine 是推送纪律（项目会话无条件注入）：工具层的闸门只保护「走
// workspace_sync 的路径」，agent 直接跑 `git push` 仍能绕过它——所以提示词里必须
// 有一条硬纪律把「推送只能走工具」说死。对本仓库（lxcode 本体）的直推禁令另在
// workspace_sync 的闸门里强制执行（本地 master 含逐轮检查点，推出去即公开）。
func pushDisciplineLine() string {
	return "推送纪律：推送只能通过 `workspace_sync`（push=true）——**永远不要直接执行 `git push`**，" +
		"也不要用 `git push --force` 绕过任何拒绝；工具拒绝推送时如实转达原因，不另找路子。" +
		"lxcode 本体仓库（含 scripts/push-release.mjs）的推送被闸门拒绝是**预期行为**：" +
		"本地 master 含逐轮检查点，发版只走 `node scripts/push-release.mjs [version]`，" +
		"且发版是用户的显式动作——不要自行运行它。\n"
}

// todoPromptRule 按档位返回守则第 5 条（todo-first 指令——用户在设置里选的
// 内置模板，不靠模型自觉判断任务简不简单）。档位常量在 internal/config。
//
//   - strict（默认，现有文案原样）：需要工具的任务必须先建清单，两三步也要建；
//   - normal：按复杂度用清单——判断标准写死在提示词里（多步工具调用才建），
//     不让模型发挥；
//   - off：返回空串——整条不出现（编号顺延，不是留一个空号），todo 工具本身
//     仍可用，模型自然使用。
func todoPromptRule(mode string) string {
	switch mode {
	case config.TodoPromptNormal:
		return "按任务复杂度用清单：需要多步工具调用才能完成的任务（改多处代码、跑构建与测试、跨环节排查），开始前先用 todo 工具列出步骤清单（3~7 条、动词开头、每条可独立验收）；一两步就能完成的小任务直接做完汇报，不建清单。清单条数与任务复杂度相称，不为凑数拆分，也不为简单任务引入它不需要的工具、文件或流程。建了清单就边做边更新状态（当前项 done、下一项 active，一次只标一个 active），任务有变化时同步修订清单。"
	case config.TodoPromptOff:
		return ""
	default: // strict（含空串——缺省档）：todo 契约的轮次纪律——每轮必审视清单、
		// 不得攒批、收尾清单必须与实际进度一致。纯对话豁免（打招呼也要建清单是噪声）。
		return "每轮开始先审视任务清单：本轮是新任务就用 todo 工具先出清单（3~7 条、动词开头、每条可独立验收）再动手；已有清单先对照现状更新（继续做哪项、完成哪些），不要无视旧清单另起炉灶。执行中每完成一步立即更新状态（当前项 done、下一项 active，一次只标一个 active），不得攒到轮末一次性补写；任务有变化时同步修订清单。轮收尾清单必须与实际进度一致。纯对话回复（打招呼、答疑、闲聊）可豁免。"
	}
}

// systemPromptRules 是守则的固定条目（不含第 5 条 todo 档位条目——它按模式
// 插在原位，off 时整体缺席、后续条目顺延编号）。
var systemPromptRules = []string{
	"需要文件内容、代码结构或命令输出时，主动调用工具获取，不要凭记忆猜测或编造。",
	"说了就做：每个回复要么包含推进任务的工具调用，要么给出最终结果——只描述\"接下来打算做什么\"而不动手，是不可接受的。",
	"把活干完：交付物是真实工具输出支撑的结果，不是计划书。工具失败时如实报告并换方法，绝不编造看起来合理的结果（假数据、假文件内容、假命令输出）。",
	// 并行纪律（2026-12 效率纪律之一）：把「同批发出」写全成「独立且无副作用」——
	// 有依赖的调用本来就该分步（并行会读到过期状态），加这一句才不至于被误读成
	// 「什么都并行」。措辞通用，不绑定某个 Agent 的职责。
	"并行无副作用操作：相互独立且无副作用的检索、阅读、查询必须在同一个回复里一起发出（同一批、不串行等待）；有前后依赖的才分步。",
	"", // 第 5 条占位：todoPromptRule(mode) 填入
	// 改动范围最小（2026-12 效率纪律之二）：探索中发现的问题不进本次改动——
	// 顺手重构会让 diff 与目标脱节，回滚与验收都失去边界。
	"改动范围最小：只动达成目标所必需的部分，不顺手重构、不顺手清理；探索中发现的其他问题记入总结（指出位置与建议），不在本次改动里修。",
	// 编译是底线、跑测试不是（2026-12 用户拍板）：写代码的 Agent 只负责「能编译、
	// 无明显语法错误」，测试与验收按职责分工交给测试 Agent——守则写在全局层，
	// 所以只说分工不给某个 Agent 派活（否则测试 Agent 自己读到会自相矛盾）。
	"修改代码：先 read_file 看到真实内容，再用 edit 精确替换（old_string 必须逐字符一致且唯一）；新建文件才用 write_file 全量写入。改完确认能编译、无明显语法错误（编译是底线；测试与验收按职责分工，交给对应的 Agent）。",
	// 验证由便宜到贵（2026-12 效率纪律之三）：全量构建/测试是慢操作，反复跑
	// 只是在等同一批错误；先把针对性检查做完再全量跑一次，墙钟与上下文都省。
	// 末尾一句守住「验证深度按职责分工」——不与上一条的编译底线打架。
	"验证由便宜到贵：先做针对性检查（静态检查、单文件、单个用例），全部通过后再跑一次全量构建与测试，不反复全量重跑；验证深度按职责分工（写代码的 Agent 至少到能编译）。",
	"bash 的输出与退出码会回传给你：非零退出码不是系统故障，读输出判断原因再决定重试还是换方法；不要盲目重试同一命令。",
	// 不在同一个问题上死磕（2026-12 效率纪律之四）：与上一条互补——上一条说
	// 「先读输出再决定」，这一条给「两次没解决」一个硬阈值与三个出口，
	// 免得模型在同一条命令/同一个假设上无限微调。
	"同一个错误或障碍尝试两次仍未解决时，必须换策略（搜索已知解法、换工具、换思路），不要在同一个方向上反复重试。",
	"工具结果被拒绝（用户拒绝）时，不要原样重试同一操作——换一种方法，或向用户说明为什么需要这个操作。",
	"工具结果过长会被截断——如需完整内容，把输出写到文件再用 read_file 分段读。",
	"用户提到\"之前/上次/我们讨论过\"时，先用 session_search 找回上下文，不要让用户复述。",
	"回答用中文，简洁直接；完成操作后向用户报告结果。",
}

// systemPromptFooter 组装守则尾段：固定条目 + 按档位注入的 todo-first 条目，
// 统一编号。strict 档与拆分前的 const 逐字节一致（todo_prompt_test 钉住）。
func systemPromptFooter(todoMode string) string {
	rules := append([]string(nil), systemPromptRules...)
	rules[4] = todoPromptRule(todoMode)
	var b strings.Builder
	b.WriteString("\n工作守则：\n")
	n := 0
	for _, r := range rules {
		if r == "" {
			continue // off 档：整条缺席，编号顺延
		}
		n++
		b.WriteString(strconv.Itoa(n))
		b.WriteString(". ")
		b.WriteString(r)
		b.WriteString("\n")
	}
	// 末条不带换行（与拆分前的 const 尾部一致）
	s := b.String()
	return strings.TrimSuffix(s, "\n")
}

// systemPromptTools 是工具的一句话摘要（BuildSystemPrompt 按注册表过滤）。
// 写在这里而不是工具的 Description——Description 面向"怎么用"（给模型的
// 参数级指导），这里要的是"有什么、风险等级"（给守则层的全局观）。
var systemPromptTools = map[string]string{
	"read_file":                     "read_file：读取文件，低危自动执行，支持分页与行号",
	"search":                        "search：搜索文件内容/文件名，低危自动执行，不经过 shell",
	"web_search":                    "web_search：联网搜索网页（多渠道自动降级），低危自动执行",
	"web_fetch":                     "web_fetch：抓取网页正文（HTML 转文本），低危自动执行",
	"session_search":                "session_search：搜索历史会话内容（含当前会话），低危自动执行",
	"read_skill":                    "read_skill：读取技能的完整内容（提示词里只有索引），低危自动执行",
	"share_file":                    "share_file：把工作目录里的文件共享到对话（图片缩略图/文件卡），低危自动执行、不修改任何文件",
	"read_image":                    "read_image：读取一张图片的内容给视觉模型看（模型须声明「视觉」能力），低危自动执行、不修改任何文件",
	"edit":                          "edit：精确修改文件片段（唯一匹配替换），低危自动执行",
	"write_file":                    "write_file：全量写入文件，覆盖已有文件时用户会收到确认提示",
	"bash":                          "bash：执行 shell 命令，每次执行前用户会收到确认提示",
	"bash_readonly":                 "bash_readonly：执行**只读** shell 命令（ls/grep/find/wc、git log、sqlite3 查询等），写操作会被拒绝，低危自动执行",
	"todo":                          "todo：维护任务清单（全量写入），低危自动执行",
	"ask_user":                      "ask_user：向用户提问并等待回答（冲突抉择、需要用户决策时用），低危自动执行",
	"agent_read":                    "agent_read：查看 Agent 注册表（列表或单个完整定义，含提示词模板），低危自动执行",
	"agent_write":                   "agent_write：按 id 修改 Agent 配置（描述/模板/白名单/技能绑定，落库即生效），高危——执行前用户会收到确认提示",
	"job_output":                    "job_output：读后台任务的增量输出（可 wait 等它结束），低危自动执行",
	"job_list":                      "job_list：列后台任务（新的在前，可按会话过滤），低危自动执行",
	"job_kill":                      "job_kill：停止一个后台任务（归属记为 agent），低危自动执行",
	tools.DispatchToolName:          tools.DispatchToolName + "：把任务派给名单中的子 Agent（任务的完整执行在子上下文里，结果回传验收），低危自动执行",
	tools.MergeRequestToolName:      "merge_request：（遗留）合并流程已退役，调用只会得到说明——提交/推送用 workspace_sync，回滚用 workspace_rollback",
	tools.WorkspaceStatusToolName:   "workspace_status：查询项目工作区状态（主检出的未提交改动、遗留会话分支），低危自动执行",
	tools.WorkspaceSyncToolName:     "workspace_sync：提交主检出的改动到当前分支，可选拼直接推送远程，中危——执行前用户会收到确认提示",
	tools.WorkspaceRollbackToolName: "workspace_rollback：回滚主检出当前分支到上一个提交/会话基线/指定提交（自动检查点已移除，只有提交过的位置可回滚），中危——执行前用户会收到确认提示",
	tools.WorkspacePublishToolName:  "workspace_publish：（遗留）旧工作树架构的产物发布，直连模式下无需发布，中危——覆盖文件前用户会收到确认提示",
}

// ProjectDocs 是注入提示词的项目守则（项目根的 AGENTS.md）。
//
// 由消费方（server）读取后传入——agent 不碰文件系统（分层规则：数据由
// 调用方解析、内核只组装，与 AgentContext 同款模式）。
type ProjectDocs struct {
	Path    string // 展示路径（提示词里标明来源）
	Content string // 守则正文
	Note    string // 读取异常说明（如超大跳过）；空 = 正常
}

// ProjectDocsFunc 是项目守则的读取约定（按会话工作目录取；空目录 = 无守则）。
type ProjectDocsFunc func(workDir string) ProjectDocs

// projectDocsCap 是注入上限：守则文件可以很长，但不能无节制地吃上下文。
// 超限截断并如实注明（模型知道「后面还有」比读到半截却以为完整要好）。
const projectDocsCap = 32 * 1024

// projectDocsSection 渲染项目约定段。
//
// 两条纪律（缺一条模型就会分不清来源）：
//  1. 标明来源路径——「项目约定」和「用户刚说的话」是两种东西；
//  2. 显式声明优先级——项目约定低于 Agent 自身的四层组合与工作守则
//     （2026-09-21 用户拍板：项目约定比 Agent 自己的低），冲突时以用户为准。
func projectDocsSection(docs ProjectDocs) string {
	if docs.Note != "" {
		return "\n项目约定（" + docs.Path + "）：" + docs.Note + "\n"
	}
	content := strings.TrimSpace(docs.Content)
	if content == "" {
		return "" // 没有文件或文件为空：不加噪声
	}
	truncated := ""
	if len(content) > projectDocsCap {
		content = content[:projectDocsCap]
		truncated = fmt.Sprintf("\n…（守则超过 %dKB 已截断，完整内容见 %s）", projectDocsCap/1024, docs.Path)
	}
	return fmt.Sprintf(`

以下为项目约定（来自 %s）——优先级低于上面的 Agent 设定与工作守则，与用户当前指令冲突时以用户为准：

%s%s
`, docs.Path, content, truncated)
}

// toolLine 生成工具清单的一行。
//
// 内置工具用 systemPromptTools 的摘要（写死的"有什么、风险等级"）；目录里的
// 自定义工具没有摘要，回落到 Def.Description 首行 + 风险说明——不回落的后果是
// **静默漏掉**：模型不知道这个工具存在，白名单勾了也白勾。
func toolLine(d *tools.Def) string {
	if desc, ok := systemPromptTools[d.Name]; ok {
		return "- " + desc + "\n"
	}
	return fmt.Sprintf("- %s：%s（%s）\n", d.Name, firstLine(d.Description), riskPhrase(d.Risk))
}

// riskPhrase 是风险等级的自然语言说明（与 systemPromptTools 里的措辞一致）。
func riskPhrase(r tools.RiskLevel) string {
	if r == tools.RiskHigh {
		return "高危——每次执行前用户会收到确认提示"
	}
	return "低危——自动执行"
}

// firstLine 取首行（清单一行一条；Description 可能带示例等后续行）。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// BuildSystemPrompt 组装完整系统提示词：头 + 工作目录说明 + Git 工作树说明 +
// 动态工具清单 + 尾。workDir 是会话工作目录（项目会话 = 项目根，空 = 后端进程
// 目录）——相对路径的解析基准必须告诉模型。wt/isChild 是本会话 Git 工作树信息
// 与「是否子会话」（零值零注入——未分组会话的提示词逐字节不变）。工具清单从
// 注册表生成——加新工具时不需要改这里（忘了改清单 = 模型不知道工具存在）。
// docs 是项目守则（空 = 未分组会话或项目没写守则——不加噪声）。
// todoMode 是 todo-first 提示词档位（config.TodoPrompt*；可变参数取第一个
// 非空值，缺省 = strict——旧调用方/单测不传时行为不变）。
func BuildSystemPrompt(toolReg *tools.Registry, workDir string, wt WorktreeInfo, isChild bool, docs ProjectDocs, todoMode ...string) string {
	var b strings.Builder
	b.WriteString(systemPromptHeader)
	b.WriteString(projectDocsSection(docs))
	b.WriteString(workdirLine(workDir))
	b.WriteString(worktreeLine(wt, isChild))
	b.WriteString("\n可用工具：\n")
	for _, name := range toolReg.Order() {
		if d, ok := toolReg.Get(name); ok {
			b.WriteString(toolLine(d))
		}
	}
	b.WriteString(systemPromptFooter(firstTodoMode(todoMode)))
	return b.String()
}

// firstTodoMode 取可变参数里的第一个非空档位（缺省 = strict）。
func firstTodoMode(modes []string) string {
	for _, m := range modes {
		if m != "" {
			return m
		}
	}
	return config.TodoPromptStrict
}
