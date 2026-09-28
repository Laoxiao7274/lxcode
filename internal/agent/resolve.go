// Agent/模型解析与审批档位：请求级 > Agent 默认 > confirm；派发时取严。
// 这一组函数是纯判定，不碰会话状态，所以单列一个文件。

package agent

import (
	"fmt"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// agentToolsOf 取 Agent 的工具白名单（nil = 无白名单语义——不过滤）。
func agentToolsOf(ac *sessiondata.AgentContext) []string {
	if ac == nil {
		return nil
	}
	return ac.Def.Tools
}

// llmToolsFiltered 按白名单过滤 wire 声明（nil = 全量——旧语境）。
func llmToolsFiltered(toolReg *tools.Registry, allow []string) []llm.Tool {
	all := toolReg.LLMTools()
	if allow == nil {
		return all
	}
	allowed := toolSet(allow)
	out := make([]llm.Tool, 0, len(allow))
	for _, t := range all {
		if allowed[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// SendOpt 是 Send 的请求级选项（变参——现有调用点零改动）。
type SendOpt func(*sendConfig)

type sendConfig struct {
	effort   string // 推理强度（空 = 模型默认）
	approval string // 权限模式（空 = confirm）
	agentID  string // Agent 名单 id（空 = 主语境——无 resolver 时旧语义）
}

// WithEffort 指定本轮推理强度（模型须声明 reasoning 能力才真正生效）。
func WithEffort(e string) SendOpt { return func(c *sendConfig) { c.effort = e } }

// WithApproval 指定本轮工具执行的权限模式（空/未指定 = confirm）。
func WithApproval(a string) SendOpt { return func(c *sendConfig) { c.approval = a } }

// WithAgent 指定本轮的执行 Agent（名单 id；空 = 旧语境——全局默认
// 提示词与 default 角色模型，兼容不接名单的调用方/单测）。
func WithAgent(id string) SendOpt { return func(c *sendConfig) { c.agentID = id } }

// resolveAgent 解析本轮 Agent 载荷。agentID 空 + 无 resolver = nil
// （旧语境）；agentID 空 + 有 resolver = 主 Agent（调度中枢——M3 后
// agent_dispatch 已注册，主语境完整生效：不带 agent 的消息默认走
// 主 Agent，它只派活不亲自执行）；agentID 非空但名单没有 = 哨兵
// 错误。停用的 Agent 不可选用。
func (s *Session) resolveAgent(agentID string) (*sessiondata.AgentContext, error) {
	if s.agents == nil {
		return nil, nil // 无名单语境（旧调用方/单测——全部旧语义）
	}
	if agentID == "" {
		agentID = "main" // 默认主 Agent（名单语境下空 = 主）
	}
	ac, ok := s.agents.Resolve(agentID)
	if !ok {
		// 容错：模型可能把名字当 id 传（提示词已列 id，但弱模型仍会
		// 拿名字填参数）——按名字再解析一次。
		ac, ok = s.agents.ResolveByName(agentID)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s（提示词「可委派名单」里括号前的就是 id）", ErrAgentNotFound, agentID)
	}
	if !ac.Def.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrAgentDisabled, ac.Def.Name)
	}
	return ac, nil
}

// modelFor 按 Agent 绑定取模型（绑定优先，回落 default 角色）；
// 无 Agent 语境走 default 角色（旧语义）。
func (s *Session) modelFor(ac *sessiondata.AgentContext) (config.ModelConfig, error) {
	if ac != nil && ac.Def.Model != "" {
		m, ok := s.reg.Get(ac.Def.Model)
		if !ok {
			return config.ModelConfig{}, fmt.Errorf("Agent 绑定的模型 %s 不存在（编辑该 Agent 换绑或先在设置里添加模型）", ac.Def.Model)
		}
		if !m.Enabled {
			return config.ModelConfig{}, fmt.Errorf("%w: %s（Agent 绑定）", ErrModelDisabled, m.ID)
		}
		return m, nil
	}
	m, err := s.reg.ModelForRole(config.RoleDefault)
	if err != nil {
		return config.ModelConfig{}, fmt.Errorf("%w: %w", ErrNoDefaultModel, err)
	}
	if !m.Enabled {
		return config.ModelConfig{}, fmt.Errorf("%w: %s", ErrModelDisabled, m.ID)
	}
	return m, nil
}

// effectiveApproval 权限取严：请求级 > Agent 默认 > confirm。
// 注意它只解决"这一轮用哪一档"：请求级是用户的显式选择（UI 里选了 auto 就
// 是这一轮不要确认），所以它优先，不在这里取严——取严发生在派发子 Agent 那
// 一层（见 stricterApproval 与 runDispatch）。
func effectiveApproval(requested, agentDefault string) string {
	if requested != "" {
		return requested
	}
	if agentDefault != "" {
		return agentDefault
	}
	return "confirm"
}

// stricterApproval 取两档权限中更严的一档（auto < confirm < strict）——派发子
// Agent 时用父轮的授权面与子 Agent 自己的默认取严：子执行面不大于请求方。
//
// 为什么要单独做这一步：effectiveApproval 是"请求级优先"，而派发时请求级就是
// 父轮的审批（runDispatch 透传），于是子 Agent 自己更严的默认（如调研 Agent 的
// strict、测试 Agent 的 confirm）会被父轮的 auto 放大——代码注释写着"取严"而实现
// 不是，是自相矛盾的。
//
// 空值 = 未声明该档：按"继承请求方"处理（返回另一档），与 effectiveApproval
// 的空值语义一致。
func stricterApproval(requested, agentDefault string) string {
	if agentDefault == "" {
		return requested
	}
	if requested == "" {
		return agentDefault
	}
	if approvalRank(agentDefault) > approvalRank(requested) {
		return agentDefault
	}
	return requested
}

// approvalRank 是权限档位的严格度（越大越严）。未知值按 confirm 同档——与运行期
// 行为一致：只有精确等于 strict 才走"只读拒绝"，其余值都走确认门。
func approvalRank(a string) int {
	switch tools.Approval(a) {
	case tools.ApprovalAuto:
		return 0
	case tools.ApprovalStrict:
		return 2
	default:
		return 1
	}
}

// agentDefaultOf 取 Agent 的权限默认（nil 安全）。
func agentDefaultOf(ac *sessiondata.AgentContext) string {
	if ac == nil {
		return ""
	}
	return ac.Def.Approval
}
