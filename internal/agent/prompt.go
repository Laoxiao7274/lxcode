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
	"read_file":      "read_file：读取文件，低危自动执行，支持分页与行号",
	"search":         "search：搜索文件内容/文件名，低危自动执行，不经过 shell",
	"session_search": "session_search：搜索历史会话内容（含当前会话），低危自动执行",
	"read_skill":     "read_skill：读取技能的完整内容（提示词里只有索引），低危自动执行",
	"edit":           "edit：精确修改文件片段（唯一匹配替换），低危自动执行",
	"write_file":     "write_file：全量写入文件，覆盖已有文件时用户会收到确认提示",
	"bash":           "bash：执行 shell 命令，每次执行前用户会收到确认提示",
	"todo":           "todo：维护任务清单（全量写入），低危自动执行",
}

// BuildSystemPrompt 组装完整系统提示词：头 + 工作目录说明 + 动态工具清单 + 尾。
// workDir 是会话工作目录（项目会话 = 项目根，空 = 后端进程目录）——相对路径
// 的解析基准必须告诉模型。工具清单从注册表生成——加新工具时不需要改这里
// （忘了改清单 = 模型不知道工具存在）。
func BuildSystemPrompt(toolReg *tools.Registry, workDir string) string {
	var b strings.Builder
	b.WriteString(systemPromptHeader)
	b.WriteString(workdirLine(workDir))
	b.WriteString("\n可用工具：\n")
	for _, name := range toolReg.Order() {
		if desc, ok := systemPromptTools[name]; ok {
			b.WriteString("- " + desc + "\n")
		}
	}
	b.WriteString(systemPromptFooter)
	return b.String()
}
