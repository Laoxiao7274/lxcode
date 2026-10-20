// agent_dispatch 工具（M3——主 Agent 的唯一调度通道）：把任务派给名单
// 中的子 Agent。执行体是 agent.Session.runDispatch（runTools 调用位
// 直连——子循环需要工具调用 id 做事件归属，走通用 Exec 的入参形状
// 给不了）；这里的 Exec 是未装配路径的兜底。
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// DispatchCall 是一次调度的参数（agent 内核在 runTools 调用位发起）。
type DispatchCall struct {
	DispatchID string // 调用 id（事件归属键——= 主轮里该工具调用的 tc.ID）
	Agent      string // 目标 Agent 的名单 id
	Task       string // 任务描述（应自带验收标准）
	Context    string // 可选背景（主会话的关键约束，不是完整历史）
	// Session 非空 = **续跑既有子会话**（2026-09-22）：子 Agent 的进度在库里，
	// 附着同一个 id 接着跑；空 = 新开一个子会话。
	Session string
	// Todos 非空 = 主 Agent 拆好的**初始清单**（todo 契约批③——主拆初稿 +
	// 子可修订）：作为子会话的清单初稿装载（子会话执行中可全量替换修订）。
	// 空 = 子 Agent 按 strict 档自拆（与改造前一致）。
	Todos []sessiondata.TodoItem
	// DispatchSeq 是**父会话**那条携带本次 dispatch 的 assistant 消息 seq。
	// 子会话的文件快照按它归属到父会话的这一轮（docs/session-tree.md §C.2.1）：
	// 回滚到这一轮之前时，子会话改的文件要一起还原。
	//
	// 为什么不回库反查 dispatch_id：那个 id 是 tool_call id，**续跑时不变**，
	// 无法区分「第几次派发」——而归属必须按本次派发算（§C.5.1 边界①）。
	DispatchSeq int64
}

// DispatchResult 是调度执行的结果（回填主会话的工具结果）。
type DispatchResult struct {
	Output  string // 给主 Agent 的结果文本（子 Agent 最终回复或错误说明）
	IsError bool
	// SessionID 是这次调度用的子会话 id（回填给主 Agent——它下一轮可以显式
	// 传 session 参数续跑；也是"子过程可回放/可对账"的钥匙）。
	SessionID string
	// Attachments 是这次派发期间子会话共享到对话的附件（share_file 等的产物）：
	// 引用 + 展示元数据随调度结果回流主会话——主时间线渲染附件卡（卡 = 一行
	// 摘要的例外：附件不是子过程，是**成果**），主 Agent 的上下文里也拿得到
	// 路径（Output 文本带 [附件] 行，可转述或再次 share_file）。
	// 恒为引用（相对子会话附件根的路径），base64 绝不经此。
	Attachments []llm.AttachmentRef
	// ExternalCount 是子会话本轮**无法快照的写**（bash / MCP / 自定义工具，
	// 以及被跳过的大文件/二进制）——加到父会话这一轮的 external 计数上。
	// 不这么做的话树面板会显示「这一轮 0 处无法还原」，而子 Agent 用 bash 改了
	// 一堆文件（docs §C.1：**这正是用户最会被骗的一处**）。
	ExternalCount int
}

// DispatchToolName 是调度工具的 id。
//
// **必须匹配 `^[a-zA-Z0-9_-]{1,64}$`**——OpenAI 与 Anthropic 都按这个字符集校验
// 工具名，严格的网关会对带点号的 id 直接 400 拒收**整轮**（2026-09-23 实测：
// 原名 `agent.dispatch` 让主 Agent 每一轮都失败，改成下划线即通过；见 AGENTS.md
// §5 坑 13）。内核判定与提示词都用这个常量，别再写字面量——`tools` 的测试会校验
// 全部内置工具 id 的字符集。
const DispatchToolName = "agent_dispatch"

// dispatchDef 返回 agent_dispatch 的注册声明。
// 风险定级：低危、不变更（Mutates=false）——工具本身只发起调度；子
// Agent 的工具执行有各自的确认门与权限门（取严继承），不存在借手
// 提权：子 Agent 看到的权限面 ≤ 主会话授权面。
// 两类制：子 Agent 的白名单不含 agent_dispatch——注册表有它但子语境
// 的白名单过滤天然挡掉（深度恒 1）。
func dispatchDef(r *Registry) *Def {
	return &Def{
		Name:        DispatchToolName,
		Description: "把任务派给名单中的子 Agent 执行。任务描述必须自包含（目标、约束、验收标准）——子 Agent 看不到当前对话历史；需要背景时放进 context。适合有明确边界且值得独立执行的任务。子 Agent 在**独立会话**里执行（有自己的历史与压缩），返回值里带子会话 id：要接着上次的进度继续，把该 id 填进 session。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"agent": { "type": "string", "description": "目标 Agent 的名单 id（提示词「可委派名单」里列出的）" },
				"task": { "type": "string", "description": "任务描述——目标 + 约束 + 验收标准（子 Agent 据此独立完成并回传结果）" },
				"context": { "type": "string", "description": "可选背景：当前对话的关键约束/已尝试的路径（不是完整历史）" },
				"session": { "type": "string", "description": "可选：续跑既有子会话的 id（上一次 dispatch 结果里带的子会话 id）。填了就接着那个子会话的进度继续，不填则新开一个" },
				"todos": {
					"type": "array",
					"description": "可选：为这次派发预拆的任务清单（3~7 条、动词开头、每条可独立验收）。作为子 Agent 的清单初稿——它执行中可以修订。给了它，用户在派发当下就能看到这次派发要做什么。",
					"items": {
						"type": "object",
						"properties": {
							"content": {"type": "string", "description": "任务描述（一句话，动词开头）"},
							"status": {"type": "string", "enum": ["pending", "active", "done"], "description": "初始状态，通常全部 pending"}
						},
						"required": ["content", "status"]
					}
				}
			},
			"required": ["agent", "task"]
		}`),
		Risk: RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Agent   string                 `json:"agent"`
				Task    string                 `json:"task"`
				Context string                 `json:"context"`
				Session string                 `json:"session"`
				Todos   []sessiondata.TodoItem `json:"todos"`
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
