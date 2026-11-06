// run_task 工具（助手模式 P0 能力桥）：助手 Agent 把**项目任务**派进项目里
// 执行——它自己的白名单是只读面 + 本工具，不能改项目文件；改动经 run_task
// 派给项目里的执行 Agent（在项目的独立子会话里跑，结论回传）。
//
// 与 agent_dispatch 的分工：agent_dispatch 是**主 Agent 的调度通道**（子语境
// 白名单不含它）；run_task 是**助手的桥**（普通 Agent 白名单不含它）——两个
// 白名单互不重叠，谁也不能越界。
package tools

import (
	"context"
	"encoding/json"
)

// RunTaskToolName 是 run_task 工具的 id。
//
// 必须匹配 `^[a-zA-Z0-9_-]{1,64}$`（与 DispatchToolName 同一条工具名约束——
// OpenAI 与 Anthropic 都按这个字符集校验，越界的名字严格端点 400 拒收整轮）。
const RunTaskToolName = "run_task"

// runTaskDef 返回 run_task 的注册声明。
//
// 风险定级：低危、不变更（Mutates=false）——工具本身只发起派发；被派到的
// 子会话有自己的白名单与确认门，助手的权限面不借本工具放大。
// 主会话/子会话的白名单天然不含它（只有助手 Agent 的白名单装配 run_task），
// 注册表里有它但没被勾选 = 语境白名单过滤挡掉（与 agent_dispatch 同一套机制）。
func runTaskDef() *Def {
	return &Def{
		Name: RunTaskToolName,
		Description: "把一个项目任务派进指定项目执行（在项目的独立子会话里跑，结论回传）。" +
			"project_id 必须是用户明确指到的项目，没指到就先问——不要猜。" +
			"agent 不填时按任务性质由后端选默认执行者。返回值带子会话 id：这次没做完时可填进 session 续跑。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"project_id": { "type": "string", "description": "项目 id（用户明确指到的那个项目；不确定先问用户，不猜）" },
				"task": { "type": "string", "description": "任务描述——目标 + 约束 + 验收标准（项目里的 Agent 据此独立完成并回传结论，它看不到当前对话）" },
				"agent": { "type": "string", "description": "可选：目标 Agent 的名单 id（不填 = 按任务性质选默认执行者）" },
				"session": { "type": "string", "description": "可选：续跑既有子会话的 id（上一次 run_task 结果里带的子会话 id，且只限于本项目派出的）。填了就接着那个子会话的进度继续" }
			},
			"required": ["project_id", "task"]
		}`),
		Risk: RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				ProjectID string `json:"project_id"`
				Task      string `json:"task"`
			}
			_ = json.Unmarshal(args, &p)
			// 兜底：未装配 agent.Session 的路径（理论不可达——与 agent_dispatch
			// 同款，内核在 runTools 调用位直连，不经过这里）。保守给模型自解释错误。
			return "错误: run_task 执行体未装配（agent.Session 未初始化）。", nil
		},
	}
}

// RunTaskCall 是 run_task 调用位的参数形状（内核在 runTools 调用位展开解析——
// 与 DispatchCall 同一条纪律：执行体需要事件归属与校验上下文，json.RawMessage
// 给不了）。由 agent 包定义派发参数（tools 是叶子包，不见会话/项目 id）。
type RunTaskCall struct {
	ProjectID string `json:"project_id"`
	Task      string `json:"task"`
	Agent     string `json:"agent"`   // 空 = 按任务性质选默认执行者
	Session   string `json:"session"` // 空 = 新开子会话；非空 = 续跑（只限本项目派出的子会话）
}
