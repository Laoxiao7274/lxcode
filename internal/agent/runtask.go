// run_task 的内核执行体（助手模式 P0）：助手 Agent 把项目任务派进项目执行。
//
// 与 runDispatch（agent_dispatch）的分工：那条路是**主 Agent 的委派**（子会话
// workspace 继承父）；这条路是**助手的桥**——助手会话没有项目归属（workspace
// 为空），而任务要跑在**项目根**里，所以子会话的 workspace 不能继承父，必须
// 显式落项目 id（CreateChild 的 SQL 继承会拷到空值，落在 AttachTo 之后用
// SessionWorkspace 覆盖）。项目解析（ProjectByID）在开子会话**之前**做：
// 项目不存在时报人话错误，不开孤儿子会话行。
//
// 子会话是一个真 *agent.Session（openChildSession + SendWait 复用）：压缩、
// 溢出兜底、确认门代理（SetConfirmProxy → 助手会话裁决、带 dispatch_id）、
// 取消补配对（P5 不变量，SendWait 的取消路径已在内核收口）全部免费继承。
// 结论 = 子会话最后一条 assistant 正文（lastAssistantText 模式，复用既有判定
// ——不能往回扫，中间轮的开场白不是结论），尾部带子会话 id。
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/tools"
)

// RunProjectTaskResult 是一次项目任务派发的结果（内核侧——server/工具循环
// 把它转成回填文本）。
type RunProjectTaskResult struct {
	Output    string // 结论文本（给助手 Agent 的工具结果正文）
	IsError   bool
	SessionID string // 子会话 id（结果文本已带；结构化回带供将来 UI 用）
}

// RunProjectTask 执行一次 run_task：解析项目 → 开/接子会话（workspace=项目）
// → 等它跑完 → 返回结论。
//
// agentID 空 = 按任务性质选默认执行者（占位：内置名单里第一个非 main 的启用
// Agent——P0 的内置名单就是 coder/researcher/tester，写代码类默认 coder。
// 不引入「任务分类器」这种过度设计，注释说清楚语义即可）。sessionID 非空 =
// 续跑既有子会话（必须属于本项目派出的——openChildSession 的续跑归属校验按
// OwnerSessionID 判等，助手会话 id 即发起方，天然挡跨项目续跑）。
func (s *Session) RunProjectTask(ctx context.Context, projectID, task, agentID, sessionID string) (RunProjectTaskResult, error) {
	// agent 空时选默认执行者；选定后与显式指定的走同一条解析（存在性/启用校验
	// 都在 resolveAgent 里）。
	if agentID == "" {
		agentID = s.defaultProjectAgent()
		if agentID == "" {
			return RunProjectTaskResult{}, fmt.Errorf("没有可用的执行 Agent（名单里没有启用的非主 Agent）——请在 Agent 设置里启用至少一个执行 Agent")
		}
	}
	ac, err := s.resolveAgent(agentID)
	if err != nil {
		return RunProjectTaskResult{}, err
	}
	// 主 Agent 不可被派（与 runDispatch 同一条两类制纪律：它是调度中枢）。
	if ac.Def.IsMain {
		return RunProjectTaskResult{}, fmt.Errorf("主 Agent 不可被派发项目任务（它是调度中枢，不亲自执行）——请指定执行 Agent 或留空用默认")
	}

	// 项目解析在开子会话**之前**：不存在就不开行（人话错误，无孤儿）。
	if s.st == nil {
		return RunProjectTaskResult{}, fmt.Errorf("会话存储未启用，无法派发项目任务")
	}
	// 助手会话行必须先存在（CreateChild 的父挂靠点）。正常链路 assistant.send
	// 已把用户消息落库（懒建触发）；这里兜住「无行直接派发」的调用（防御——
	// 与 openChildSession 的续跑防御同一口径，但方向相反：让父行存在而不是拒绝）。
	s.mu.Lock()
	if err := s.ensureSessionLocked(); err != nil {
		s.mu.Unlock()
		return RunProjectTaskResult{}, fmt.Errorf("会话行创建失败: %w", err)
	}
	s.mu.Unlock()
	meta, found, err := s.st.ProjectByID(projectID)
	if err != nil {
		return RunProjectTaskResult{}, fmt.Errorf("查询项目失败: %w", err)
	}
	// 按名兜底（2026-12 助手项目全景）：id 是不可读的时间戳随机串，模型只能
	// 从注入段 / project_list 拿到；用户口中的常常是项目名——id 未命中且参数
	// 看着像名字时按名精确匹配。三条纪律：
	//   - 唯一命中 → 用之（对用户说「demo」指到 demo 项目是最自然的意图）；
	//   - 多候选 → 报重名消歧（列候选，请用户/模型给出 project_id——猜一个
	//     的代价是任务派错项目，比问一句高得多）；
	//   - 零命中 → 维持原报错（不把名字猜测扩大成模糊匹配）。
	if !found {
		byName, err := s.st.ProjectsByName(projectID)
		if err != nil {
			return RunProjectTaskResult{}, fmt.Errorf("按名查询项目失败: %w", err)
		}
		switch len(byName) {
		case 1:
			meta, found = byName[0], true
			projectID = meta.ID // 子会话归属必须落**项目 id**，不是名字
		case 0:
			return RunProjectTaskResult{}, fmt.Errorf("项目 %q 不存在——请核对 project_id（可用项目清单或 project_list 里的 id），不要猜", projectID)
		default:
			var cands []string
			for _, p := range byName {
				cands = append(cands, fmt.Sprintf("%s（%s）", p.ID, p.Path))
			}
			return RunProjectTaskResult{}, fmt.Errorf(
				"「%s」是重名项目，请给出 project_id 消歧：%s",
				projectID, strings.Join(cands, "、"))
		}
	}

	dispatchID := newConfirmID() // 事件归属键（本轮这次派发的 tool_call id 不经此路径，用请求级唯一 id）
	call := tools.DispatchCall{
		Agent: agentID, Task: task,
		Session:        sessionID,
		DispatchID:     dispatchID,
		OwnerSessionID: s.SessionID(), // 续跑归属校验：只允许续跑本助手会话派出的子会话
	}
	child, childID, _, err := s.openChildSession(call, ac)
	if err != nil {
		return RunProjectTaskResult{}, err
	}
	// 子会话的 workspace 覆盖为项目 id：CreateChild 的 SQL 继承从助手会话拷来
	// 的是空值（助手会话没有项目归属），而项目任务的子会话必须归属项目——
	// workspace 是工具相对路径与 bash 默认目录的解析源，落错值子会话就跑错目录。
	// 失败不静默：归属错了的历史行比失败难收拾（直接失败，不开错账）。
	if childID != "" {
		if err := s.st.SessionWorkspace(childID, projectID); err != nil {
			return RunProjectTaskResult{}, fmt.Errorf("子会话归属项目落库失败: %w", err)
		}
	}
	// AttachTo 已经把 workspace 解析成项目根（st.WorkspaceOf → ProjectByID →
	// 项目目录）；这里显式 SetWorkDir 钉死同一份解析（openChildSession 的
	// SetWorkDir 用的是**助手会话的**工作目录——助手的 workspace 为空时它是
	// 后端默认目录，必须被项目根覆盖）。
	if err := child.SetWorkDir(meta.Path); err != nil {
		return RunProjectTaskResult{}, err
	}
	// 子会话不在助手会话的项目目录里继承工作树说明（助手会话分支信息对项目
	// 子会话是错的）——项目根由任务/守则说明，清掉继承值。
	child.SetWorktreeInfo("", "", false)

	// 运行时登记（与 runDispatch 同款）：用户可从子会话标签页停止（assistant.cancel
	// 带子会话 id → 助手会话 CancelChild）；defer 注销覆盖 SendWait 全部收尾路径。
	key := childID
	if key == "" {
		key = dispatchID
	}
	s.registerChild(key, child)
	s.registerDispatch(RunningDispatch{DispatchID: dispatchID, ChildSessionID: childID,
		AgentName: ac.Def.Name, Task: task})
	defer s.unregisterChild(key)
	defer s.unregisterDispatch(dispatchID)

	// 确认门代理：子会话的确认请求交给助手会话裁决并打 dispatch_id——
	// 全应用「同时一个挂起确认」的不变式，子会话自己持 pending 的话
	// 服务端的 tool.confirm 找不到它（会话卡死在 busy）。
	child.SetConfirmProxy(func(cctx context.Context, req *ConfirmRequest) (ConfirmOutcome, bool) {
		stamped := *req
		stamped.DispatchID = dispatchID
		return s.awaitConfirm(cctx, &stamped)
	})
	// 子会话事件带 dispatch_id 归属上抛（前端建子会话标签页、双投实时流）；
	// busy/todo/切换不上抛（childEmitter 既有过滤）。
	tap := &childEvents{}
	child.SetEmitter(childEmitter(s.emit, dispatchID, tap))
	s.emit(DispatchStartEvent{
		DispatchID: dispatchID, SessionID: childID,
		AgentID: ac.Def.ID, AgentName: ac.Def.Name, AgentColor: ac.Def.Color,
		Task: task,
		// run_task 特有：带目标项目名（派发卡显示「coder · lxcode」——用户一看
		// 就知道这活是派进项目里的，不是助手自己拉了个代码 Agent 干私活）。
		ProjectName: meta.Name,
	})
	// 权限档**实时取严**（与 runDispatch 同款，2026-09-29 的取严语义）：把助手
	// 会话的实时档位以回调交给子会话，每次工具调用现算 stricterApproval(助手此刻,
	// 子 Agent 默认)——用户在助手会话里中途改档（assistant.confirm 之外尚无改档
	// 方法，但语义不能在这里掉）对正在跑的子会话立即生效。确认门请求经
	// SetConfirmProxy 上抛助手会话（带 dispatch_id，前端挂进派发卡）。
	child.SetApprovalSource(func() string { return s.LiveApproval() })
	child.SetApprovalDefault(ac.Def.Approval)

	err = child.SendWait(ctx, task, WithAgent(agentID), WithTodoPrompt(s.TodoPromptMode()))

	// 收卡：错误口径与 runDispatch 一致——SendWait 在 done 关闭时恒返回 nil，
	// 真错误看 tap.err；ctx 取消时 SendWait 内部已 child.Cancel() 并等收尾，
	// 未走 aborted 收尾的取消由这里补「用户中断」通告（P5：下一轮模型才知道
	// 这轮为什么没有结论）。
	endNote := ""
	endIsErr := false
	if err != nil || tap.err != nil {
		switch {
		case err != nil && ctx.Err() != nil:
			endNote = "已取消（用户中断）"
			if tap.err == nil || !tap.err.Aborted {
				_ = child.QueueNoticePassive(userAbortedNotice)
			}
		case err != nil:
			endNote = err.Error()
		case tap.err != nil && ctx.Err() != nil:
			endNote = "已取消（用户中断）"
			if !tap.err.Aborted {
				_ = child.QueueNoticePassive(userAbortedNotice)
			}
		default:
			endNote = tap.err.Message
			if tap.err.Aborted {
				endNote = "已取消（保留已生成部分）"
			}
		}
		endIsErr = true
	} else {
		endNote = lastAssistantText(child.History().Messages)
		if endNote == "" {
			endNote = "（项目任务没有产出文本结论）"
		}
	}
	s.emit(DispatchEndEvent{
		DispatchID: dispatchID, Result: endNote, IsError: endIsErr, SessionID: childID,
		FinalTodos: child.Todos(),
	})
	if endIsErr {
		if ctx.Err() != nil {
			return RunProjectTaskResult{Output: endNote, IsError: true, SessionID: childID}, ctx.Err()
		}
		return RunProjectTaskResult{Output: endNote, IsError: true, SessionID: childID}, fmt.Errorf("%s", endNote)
	}
	// 尾部带子会话 id（与 agent_dispatch 的结果同款措辞——只续跑，不重派）。
	endNote += fmt.Sprintf("\n\n[子会话 id: %s —— 只在这次**没做完**时填进 session 参数续跑（限本项目派出的子会话）；已经给出结论就别再派]", childID)
	return RunProjectTaskResult{Output: endNote, SessionID: childID}, nil
}

// defaultProjectAgent 选 run_task 不带 agent 参数时的默认执行者：名单里第一个
// 启用的非主 Agent（内置名单 coder 在前——写代码是项目任务的大头）。没有
// 助手会话的名字解析语境（agents == nil）时返回空串（调用方报人话错误）。
func (s *Session) defaultProjectAgent() string {
	if s.agents == nil {
		return ""
	}
	// 解析主 Agent 的名字语境（resolveAgent 的空 id 语义）拿到完整名单——
	// AgentResolver 接口只按 id 查，先借 main 当钥匙。
	ac, ok := s.agents.Resolve("main")
	if !ok {
		return ""
	}
	for _, d := range ac.Delegates {
		if d.Enabled {
			return d.ID
		}
	}
	// 委派名单空/全停用：按名单 id 逐个试（AgentResolver 只有按 id 的接口，
	// 没有全量列举——助手域的解析器保证 assistant/执行 Agent 可按 id 解析，
	// 这里从常见 id 起试，第一个能解析且启用的即默认）。找不到返回空串
	//（调用方报人话错误）。
	for _, candidate := range []string{"coder", "tester", "researcher", "repo-manager", "lxagent"} {
		if ac, ok := s.agents.Resolve(candidate); ok && ac.Def.Enabled && !ac.Def.IsMain {
			return candidate
		}
	}
	return ""
}
