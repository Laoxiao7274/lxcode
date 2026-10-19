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
	// model 是本轮显式指定的模型 id（空 = 用会话级选择）。协议侧 chat.send 不带
	// 这个参数（模型选择走 chat.model 的会话级状态）——保留它给内部调用方
	// （派发时显式继承父会话的模型），优先级最高。
	model string
	// todoPrompt 是 todo-first 提示词档位（空 = 不覆盖会话现值）。
	todoPrompt string
	notice     bool           // 这条 user 消息是**注入的提示条**（不是用户说的话——会话统计的轮数按它排除）
	images     []llm.ImageRef // 这条 user 消息附带的图片引用（视觉请求；恒为文件引用）
	// attachments 是这条 user 消息附带的附件**展示元数据**（前端渲染附件卡——
	// 与 images 的分工见 llm.AttachmentRef 的注释；恒为引用，base64 绝不经此路径）。
	attachments []llm.AttachmentRef
}

// WithEffort 指定本轮推理强度（模型须声明 reasoning 能力才真正生效）。
func WithEffort(e string) SendOpt { return func(c *sendConfig) { c.effort = e } }

// WithApproval 指定本轮工具执行的权限模式（空/未指定 = confirm）。
func WithApproval(a string) SendOpt { return func(c *sendConfig) { c.approval = a } }

// WithNotice 声明这条消息是**注入的提示条**（后台任务通告——见 notify.go 的
// flushNotices：空闲时它走 Send 开一轮，那条 user 消息不是用户说的话）。
// 会话统计的「轮数」只数真实用户消息，所以这个标记必须从投递路径一路带到落库。
func WithNotice() SendOpt { return func(c *sendConfig) { c.notice = true } }

// WithAgent 指定本轮的执行 Agent（名单 id；空 = 旧语境——全局默认
// 提示词与 default 角色模型，兼容不接名单的调用方/单测）。
func WithAgent(id string) SendOpt { return func(c *sendConfig) { c.agentID = id } }

// WithModel 指定本轮使用的模型注册表 id（空 = 用会话级选择/Agent 绑定/default）。
//
// 这是**请求级**的临时覆盖，不改会话状态——子会话派发时用它显式继承父会话的模型
// （默认角色可能在派发之后被改，继承死值才不会漂移）。用户切模型走 chat.model
// （Session.SetModel），那条路会落库 + 广播。
func WithModel(id string) SendOpt { return func(c *sendConfig) { c.model = id } }

// WithTodoPrompt 指定本会话的 todo-first 提示词档位（config.TodoPrompt*；
// 空 = 不覆盖）。会话级：Send 时存进会话，本轮与后续轮的提示词组装都用它。
func WithTodoPrompt(mode string) SendOpt { return func(c *sendConfig) { c.todoPrompt = mode } }

// WithImages 给这条 user 消息附带图片引用（视觉请求）：refs 恒为文件引用
// （llm.ImageRef），base64 绝不经过这里。Send 时校验张数上限（≤4），超限
// 报错——校验放内核是最后一道闸（server 已在入口校验过一次，双保险）。
func WithImages(refs []llm.ImageRef) SendOpt { return func(c *sendConfig) { c.images = refs } }

// WithAttachments 给这条 user 消息附带附件展示元数据（llm.AttachmentRef——
// 前端渲染附件卡的数据源，实时事件与历史回放同一份）。恒为引用 + 元数据，
// base64 绝不经过这里。与 WithImages 并存：用户发的图既进视觉通道（Images）
// 也渲染附件卡（Attachments）。
func WithAttachments(refs []llm.AttachmentRef) SendOpt { return func(c *sendConfig) { c.attachments = refs } }

// ModelFor 解析指定 Agent 语境下的当前模型（Agent 绑定优先，回落 default 角色）。
// 导出给 server 做**请求前的能力校验**（vision）：chat.send 带图而模型没声明
// vision 时要在入历史前拒绝——不能静默把图发给不识图的模型。
// agentID 语义与 WithAgent 一致（空 = 主 Agent/旧语境），与 Send 的解析同一条
// 路径，保证「校验的模型」与「这轮实际用的模型」是同一个。
func (s *Session) ModelFor(agentID string) (config.ModelConfig, error) {
	ac, err := s.resolveAgent(agentID)
	if err != nil {
		return config.ModelConfig{}, err
	}
	return s.modelFor(ac)
}

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

// modelFor 解析这一轮实际用的模型，优先级：
//
//	请求级显式指定（cfg.model）> 会话级选择（s.model）> Agent 绑定（AgentDef.Model）
//	> default 角色
//
// 「会话级选择」是 2026-12 修的模型作用域 bug 的落点：模型选择是**会话的状态**
// （与 approval/effort 同一层级），原先只能改全局 default 角色，一个会话切模型
// 所有会话跟着变。空 = 没选过 → 逐级回落，与改动前逐字一致（老会话零影响）。
//
// 解析失败（选中的模型被删/被停用）**不静默回落**：回落到别的模型会让用户以为
// 自己选的那个在用。交给调用方报错（错误信息里点明是会话选择）。
func (s *Session) modelFor(ac *sessiondata.AgentContext) (config.ModelConfig, error) {
	return s.modelForRequest(ac, "")
}

// modelForRequest 是 modelFor 的带请求级覆盖版本（sendConfig.model）。
func (s *Session) modelForRequest(ac *sessiondata.AgentContext, requested string) (config.ModelConfig, error) {
	if requested != "" {
		return s.modelByID(requested, "本轮指定")
	}
	s.mu.Lock()
	picked := s.model
	s.mu.Unlock()
	if picked != "" {
		return s.modelByID(picked, "会话选择")
	}
	if ac != nil && ac.Def.Model != "" {
		return s.modelByID(ac.Def.Model, "Agent 绑定")
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

// modelByID 按注册表 id 取一个可用模型（不存在/停用都报错，来源写进错误信息——
// 用户要能分辨是"我选的模型没了"还是"Agent 绑的模型没了"）。
func (s *Session) modelByID(id, source string) (config.ModelConfig, error) {
	m, ok := s.reg.Get(id)
	if !ok {
		return config.ModelConfig{}, fmt.Errorf("模型 %s 不存在（%s；到设置里重新选择）", id, source)
	}
	if !m.Enabled {
		return config.ModelConfig{}, fmt.Errorf("%w: %s（%s）", ErrModelDisabled, m.ID, source)
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
