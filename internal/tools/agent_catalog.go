// agent_read / agent_write：Agent 配置的读与字段级写（lxagent 的手脚）。
//
// 职责边界：Agent 定义（提示词模板/工具白名单/技能绑定）的编辑原本只有前端
// Agent 编辑器一条路（agent.update RPC）；lxagent 是「用对话改 Agent 配置」
// 的内置 Agent，这两个工具就是它的手脚。实现经注入（AgentCatalogOps）：
// 注册表只持回调，不 import store——与 workspace 三件套的 SetWorkspaceOps
// 同款模式（tools 是叶子包，server 装配时接线）。
//
// 风险定级：agent_read 低危只读（Mutates=false，strict 档可用）；
// agent_write **高危**（Mutates=true + Confirm 文案）——改 Agent 定义影响
// 之后所有派发（白名单加写工具 = 目标 Agent 获得改动文件的能力），必须过
// 确认门。硬校验在 sessiondata.ValidateAgentFieldUpdate（共享层，防两套
// 写路径规则漂移），这里不做第二份判定。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// AgentReadToolName / AgentWriteToolName 是工具 id。
// **必须匹配 `^[a-zA-Z0-9_-]{1,64}$`**（与 agent_dispatch 同一条网关约束）。
const (
	AgentReadToolName  = "agent_read"
	AgentWriteToolName = "agent_write"
)

// AgentCatalogOps 是两个工具的 store 侧实现（装配期注入一次，运行期只读）。
type AgentCatalogOps struct {
	// List 返回全部 Agent 定义（含 prompt 全文——裁剪给模型看是工具侧的事）。
	List func() ([]sessiondata.AgentDef, error)
	// Update 执行字段级更新（校验+落库），返回给模型的结果文本（说清改了什么、
	// 何时生效）。校验拒绝时返回 error（Execute 转自解释错误回填模型）。
	Update func(id string, u sessiondata.AgentFieldUpdate) (string, error)
}

// SetAgentCatalogOps 注入 Agent 配置读写实现（server 装配时调用）。
// 与 mergeStart / workspace 共用 searchMu：同一批「装配期写一次、运行期只读」
// 的注入点。
func (r *Registry) SetAgentCatalogOps(ops AgentCatalogOps) {
	r.searchMu.Lock()
	r.agentCatalog = ops
	r.searchMu.Unlock()
}

func (r *Registry) getAgentCatalogOps() AgentCatalogOps {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.agentCatalog
}

// ---- agent_read ----

type agentReadParams struct {
	ID string `json:"id"`
}

func agentReadDef(r *Registry) *Def {
	return &Def{
		Name: AgentReadToolName,
		Description: "查看 Agent 注册表：不带 id 列出全部 Agent（id/名称/职责描述/工具白名单/技能绑定），" +
			"带 id 读单个 Agent 的完整定义（含系统提示词模板全文）。修改 Agent 配置前先读现状。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "要读的 Agent id（可选；缺省 = 列出全部）"}
  }
}`),
		Risk:    RiskLow,
		Mutates: false,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			ops := r.getAgentCatalogOps()
			if ops.List == nil {
				return "", fmt.Errorf("Agent 注册表未装配（会话存储未启用）——无配置可读")
			}
			var p agentReadParams
			if len(args) > 0 {
				if err := json.Unmarshal(args, &p); err != nil {
					return "", fmt.Errorf("参数解析失败: %v", err)
				}
			}
			list, err := ops.List()
			if err != nil {
				return "", fmt.Errorf("读 Agent 注册表失败: %v", err)
			}
			if id := strings.TrimSpace(p.ID); id != "" {
				for _, a := range list {
					if a.ID == id {
						return formatAgentFull(a), nil
					}
				}
				return "", fmt.Errorf("Agent %s 不存在（可用: %s）", id, agentIDs(list))
			}
			return formatAgentList(list), nil
		},
	}
}

// formatAgentList 是列表视图：一行一个 Agent，字段只到「选人/读配置」需要的
// 粒度（id/名称/描述/白名单/技能/状态）——prompt 全文只在单读里给（列表里带
// 全部模板会把上下文撑爆）。
func formatAgentList(list []sessiondata.AgentDef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个 Agent：\n", len(list))
	for _, a := range list {
		b.WriteString("\n")
		fmt.Fprintf(&b, "## %s（id: %s）\n", a.Name, a.ID)
		if a.IsMain {
			b.WriteString("角色：主 Agent（调度中枢，不可被委派）\n")
		}
		if a.Desc != "" {
			fmt.Fprintf(&b, "职责: %s\n", a.Desc)
		}
		fmt.Fprintf(&b, "工具白名单: %s\n", orNone(a.Tools))
		fmt.Fprintf(&b, "技能绑定: %s\n", orNone(a.Skills))
		fmt.Fprintf(&b, "状态: %s，权限档 %s\n", enabledText(a.Enabled), approvalText(a.Approval))
	}
	b.WriteString("\n读单个 Agent 的完整定义（含提示词模板全文）用 agent_read 带 id。")
	return b.String()
}

// formatAgentFull 是单读视图：含模板全文与委派名单。
func formatAgentFull(a sessiondata.AgentDef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s（id: %s）\n", a.Name, a.ID)
	if a.IsMain {
		b.WriteString("角色：主 Agent（调度中枢）\n")
	}
	fmt.Fprintf(&b, "职责描述: %s\n", orNone([]string{a.Desc}))
	fmt.Fprintf(&b, "权限档: %s，状态: %s\n", approvalText(a.Approval), enabledText(a.Enabled))
	if a.Model != "" {
		fmt.Fprintf(&b, "绑定模型: %s\n", a.Model)
	}
	if a.Workflow != "" {
		fmt.Fprintf(&b, "工作流模块: %s\n", a.Workflow)
	}
	fmt.Fprintf(&b, "工具白名单: %s\n", orNone(a.Tools))
	fmt.Fprintf(&b, "技能绑定: %s\n", orNone(a.Skills))
	fmt.Fprintf(&b, "委派名单: %s\n", orNone(a.Delegates))
	fmt.Fprintf(&b, "\n## 系统提示词模板\n\n%s\n", orNone([]string{a.Prompt}))
	b.WriteString("\n修改字段用 agent_write（高危，过确认门）：可改 描述/模板/工具白名单/技能绑定；id 不可改。")
	return b.String()
}

// ---- agent_write ----

type agentWriteParams struct {
	ID     string   `json:"id"`
	Desc   *string  `json:"desc"`
	Prompt *string  `json:"prompt"`
	Tools  []string `json:"tools"`
	Skills []string `json:"skills"`
}

func (p agentWriteParams) toUpdate() sessiondata.AgentFieldUpdate {
	return sessiondata.AgentFieldUpdate{Desc: p.Desc, Prompt: p.Prompt, Tools: p.Tools, Skills: p.Skills}
}

// changedFields 列出本次要动的字段（确认门文案与执行校验共用）。
func (p agentWriteParams) changedFields() []string {
	var out []string
	if p.Desc != nil {
		out = append(out, "职责描述")
	}
	if p.Prompt != nil {
		out = append(out, "系统提示词模板")
	}
	if p.Tools != nil {
		out = append(out, "工具白名单")
	}
	if p.Skills != nil {
		out = append(out, "技能绑定")
	}
	return out
}

func agentWriteDef(r *Registry) *Def {
	return &Def{
		Name: AgentWriteToolName,
		Description: "按 id 修改一个 Agent 的配置（高危，须用户确认）：可改 职责描述/系统提示词模板/工具白名单/技能绑定，" +
			"未提供的字段保持不变；id 不可改。硬校验：白名单不能清空、只能写工具目录里真实存在的工具名、模板不能为空、主 Agent 不可改。" +
			"改动落库即生效——该 Agent 下次被派发就用新配置。改之前先 agent_read 摸现状、向用户确认意图。",
		Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "目标 Agent 的 id（不可改）"},
    "desc": {"type": "string", "description": "新的职责描述（可选；主 Agent 按它选人，改完要说清能力变化）"},
    "prompt": {"type": "string", "description": "新的系统提示词模板全文（可选；全量替换，不能为空）"},
    "tools": {"type": "array", "description": "新的工具白名单（可选；全量替换，不能清空，工具名必须真实存在）"},
    "skills": {"type": "array", "description": "新的技能绑定（可选；全量替换，可为空）"}
  },
  "required": ["id"]
}`),
		Risk:    RiskHigh,
		Mutates: true,
		Confirm: confirmAgentWrite,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			ops := r.getAgentCatalogOps()
			if ops.Update == nil {
				return "", fmt.Errorf("Agent 注册表未装配（会话存储未启用）——配置不可写")
			}
			var p agentWriteParams
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("参数解析失败: %v", err)
			}
			if strings.TrimSpace(p.ID) == "" {
				return "", fmt.Errorf("缺少 id 参数（要修改的 Agent id）")
			}
			if len(p.changedFields()) == 0 {
				return "", fmt.Errorf("没有提供任何要改的字段（desc/prompt/tools/skills 至少给一个）——agent_write 是高危操作，不做空转")
			}
			return ops.Update(p.ID, p.toUpdate())
		},
	}
}

// confirmAgentWrite 是确认门文案：列明改哪个 Agent、动哪些字段、何时生效。
// （白名单具体加了什么工具由模型在对话里向用户说明——这里给结构性的后果提示。）
func confirmAgentWrite(_ context.Context, args json.RawMessage) string {
	var p agentWriteParams
	if err := json.Unmarshal(args, &p); err != nil {
		return fmt.Sprintf("修改 Agent 配置（参数解析失败: %v）——请核对参数后重试。", err)
	}
	fields := p.changedFields()
	if len(fields) == 0 {
		return fmt.Sprintf("即将更新 Agent %s，但没有任何要改的字段——这次调用不会产生变化。", p.ID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "即将修改 Agent「%s」的配置：%s。\n\n", p.ID, strings.Join(fields, "、"))
	b.WriteString("该 Agent 下次被派发就按新配置运行；工具白名单的增删会直接改变它能做什么（加写工具 = 它将能改动文件）。确认执行？")
	return b.String()
}

// ---- 共用小件 ----

func agentIDs(list []sessiondata.AgentDef) string {
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.ID)
	}
	return strings.Join(names, "、")
}

func orNone(list []string) string {
	if len(list) == 0 {
		return "（无）"
	}
	return strings.Join(list, ", ")
}

func enabledText(e bool) string {
	if e {
		return "启用"
	}
	return "停用"
}

func approvalText(a string) string {
	switch a {
	case "auto":
		return "auto（高危自动执行）"
	case "strict":
		return "strict（只读严格）"
	default:
		return "confirm（高危需确认）"
	}
}
