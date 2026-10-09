package agent

import (
	"fmt"
	"strings"

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

// WorktreeInfo 是本会话 Git 工作树的分支与基线提交。零值 = 未分组会话
// （没有独立 worktree）——那时提示词零注入。
type WorktreeInfo struct {
	Branch string // lxcode/session-<id>
	Base   string // 创建时的基线提交
}

// worktreeLine 渲染 Git 工作树说明段（零值零注入：逐字节等于未分组会话的提示词）。
//
// 为什么必须告诉模型：每轮对话结束时系统会**自动**把改动提交为一个检查点提交，
// 所以模型不要自己反复提交、也不要改动已提交的历史；而用户说「回滚到上一次提交」
// 指的是上一轮结束时的状态。
//
// 子会话在父会话的工作树里工作、改动与父会话共享——措辞必须区分，不能对它说
// 「你有独立分支」（它没有；那是父会话的分支）。
func worktreeLine(wt WorktreeInfo, isChild bool) string {
	if wt.Branch == "" {
		return ""
	}
	if isChild {
		return fmt.Sprintf("你在父会话的 Git 工作树里工作（分支 %s），你的改动与父会话共享；"+
			"每轮对话结束时系统会自动把改动提交为一个检查点提交——不要自己反复提交，也不要改动已提交的历史。\n", wt.Branch)
	}
	return fmt.Sprintf("本会话在独立 Git 工作树上工作（分支 %s）；"+
		"每轮对话结束时系统会自动把改动提交为一个检查点提交——所以不要自己反复提交，也不要改动已提交的历史；"+
		"用户说「回滚到上一次提交」指的是上一轮结束时的状态；"+
		"一轮对话要结束（给出最终答复）时，如果这一轮有实质改动，调用 `merge_request` 起一个合并进程把改动交给合并 Agent 汇总。\n"+
		"用户问「改了哪些东西」用 `workspace_status`，按项目口径汇报，不向用户解释分支概念（除非他问）；"+
		"用户说「提交/推送」用 `workspace_sync`；"+
		"用户要「回滚/撤销」用 `workspace_rollback`（默认回滚到上一轮结束的状态）；"+
		"已合并进集成分支的内容要在集成分支上 revert，不要 reset 会话分支来期望它消失。\n", wt.Branch)
}

const systemPromptFooter = `
工作守则：
1. 需要文件内容、代码结构或命令输出时，主动调用工具获取，不要凭记忆猜测或编造。
2. 说了就做：每个回复要么包含推进任务的工具调用，要么给出最终结果——只描述"接下来打算做什么"而不动手，是不可接受的。
3. 把活干完：交付物是真实工具输出支撑的结果，不是计划书。工具失败时如实报告并换方法，绝不编造看起来合理的结果（假数据、假文件内容、假命令输出）。
4. 相互独立的读取/搜索调用，在同一个回复里一起发出（不要一次只调一个）。
5. 多步骤工作（≥3 步）先用 todo 建清单，每完成一步就更新状态（当前项 done、下一项 active）——既防漏步骤，也让用户看到进度。
6. 修改代码：先 read_file 看到真实内容，再用 edit 精确替换（old_string 必须逐字符一致且唯一）；新建文件才用 write_file 全量写入。改完能验证就验证（跑测试/编译）。
7. bash 的输出与退出码会回传给你：非零退出码不是系统故障，读输出判断原因再决定重试还是换方法；不要盲目重试同一命令。
8. 工具结果被拒绝（用户拒绝）时，不要原样重试同一操作——换一种方法，或向用户说明为什么需要这个操作。
9. 工具结果过长会被截断——如需完整内容，把输出写到文件再用 read_file 分段读。
10. 用户提到"之前/上次/我们讨论过"时，先用 session_search 找回上下文，不要让用户复述。
11. 回答用中文，简洁直接；完成操作后向用户报告结果。`

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
	"edit":                          "edit：精确修改文件片段（唯一匹配替换），低危自动执行",
	"write_file":                    "write_file：全量写入文件，覆盖已有文件时用户会收到确认提示",
	"bash":                          "bash：执行 shell 命令，每次执行前用户会收到确认提示",
	"todo":                          "todo：维护任务清单（全量写入），低危自动执行",
	"ask_user":                      "ask_user：向用户提问并等待回答（冲突抉择、需要用户决策时用），低危自动执行",
	"job_output":                    "job_output：读后台任务的增量输出（可 wait 等它结束），低危自动执行",
	"job_list":                      "job_list：列后台任务（新的在前，可按会话过滤），低危自动执行",
	"job_kill":                      "job_kill：停止一个后台任务（归属记为 agent），低危自动执行",
	tools.DispatchToolName:          tools.DispatchToolName + "：把任务派给名单中的子 Agent（任务的完整执行在子上下文里，结果回传验收），低危自动执行",
	tools.MergeRequestToolName:      "merge_request：起一个合并进程，把本会话的改动交给合并 Agent 汇总到集成分支，低危自动执行",
	tools.WorkspaceStatusToolName:   "workspace_status：查询项目工作区状态（主检出改动、各会话分支、集成分支），低危自动执行",
	tools.WorkspaceSyncToolName:     "workspace_sync：提交本会话改动并起合并进程（可选拼推送），中危——执行前用户会收到确认提示",
	tools.WorkspaceRollbackToolName: "workspace_rollback：回滚本会话分支到上一轮/会话起点/指定提交，中危——执行前用户会收到确认提示",
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
func BuildSystemPrompt(toolReg *tools.Registry, workDir string, wt WorktreeInfo, isChild bool, docs ProjectDocs) string {
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
	b.WriteString(systemPromptFooter)
	return b.String()
}
