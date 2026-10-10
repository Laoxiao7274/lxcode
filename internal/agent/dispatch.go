// agent_dispatch 的执行体（M3 子上下文隔离 → 2026-09-22 升级为**独立子会话**）：
// 主 Agent 把任务派给名单中的子 Agent，子 Agent 在**自己的会话**里跑完整工具
// 循环，最终结论回填主会话（作为该工具调用的结果）。
//
// 隔离语义（2026-09-18 拍板）：子 Agent 只拿到任务描述（+ 可选背景）与自己的
// 四层组合提示词——看不到主对话历史；主上下文不因子执行过程膨胀（子执行的中间
// 消息全部留在**子会话自己的历史**里，只有最终结论回去）。
//
// 2026-09-22 升级（用户拍板）：子 Agent = **独立会话**（自己的行 + 自己的消息
// 历史 + 自己的压缩检查点），不再是"内存里的一段临时上下文"。于是：
//   - 进度天然可续：历史在库里，续跑附着同一个 id 继续跑（"他自己也能接上"）；
//   - 压缩同款：子会话就是会话，走同一套 runCompaction（阈值/检查点/影子区间）；
//   - 可回放/可对账：哪次调度开了哪个子会话，sessions.dispatch_id 记着。
package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// taskLogArgMax 是任务日志里单条工具参数/结果的字符上限（后台任务的日志是给人看的，
// 一条 edit 的完整正文没必要全进 job 日志——超长会淹没真正的进展）。
const taskLogArgMax = 400

// RunAgentTask 在一个**独立子会话**里跑一个内置 Agent 的任务，把子会话的实时输出写进
// out（后台任务句柄），返回它的最终结论。
//
// 与 runDispatch 的分工：那条路是**主 Agent 的委派**（受委派名单校验、事件带 dispatch_id
// 归属进卡、结论回填主上下文）；这条路是**宿主拉起的后台进程**（如合并进程拉起 merger）
// ——agentID 不经过委派名单校验（merger 不在主 Agent 的 Delegates 里），不回填主上下文。
// 事件走**双路**（2026-10 用户拍板「合并进程要像子 Agent 一样有标签页」）：一份进 job
// 日志（job_output 读），一份带 dispatch_id 归属上抛父会话（chat.dispatchStart/End 挂卡
// + 实时流）——前端照子 Agent 的同一套归约建卡、开子会话标签页、双投实时事件。
// 两者共用 openChildSession + SendWait（不复制一套子会话逻辑）：子会话
// 是一个真 *Session，压缩/溢出兜底/工具循环全部免费继承。
//
// workDir 非空 = 子会话在**另一个工作树**里工作（合并进程的集成分支工作树）：此时清掉
// 从父会话继承来的工作树提示词（那条说「改动与父会话共享」，对集成分支是错的），分支与
// 路径由任务说明书说清。权限档取该 Agent 自己的默认（后台进程没人看着确认门）。
func (s *Session) RunAgentTask(ctx context.Context, agentID, task, workDir string, out io.Writer) (string, error) {
	ac, err := s.resolveAgent(agentID)
	if err != nil {
		return "", err
	}
	child, childID, err := s.openChildSession(tools.DispatchCall{Agent: agentID, Task: task}, ac)
	if err != nil {
		return "", err
	}
	if workDir != "" {
		child.SetWorktreeInfo("", "")
		if err := child.SetWorkDir(workDir); err != nil {
			return "", err
		}
	}
	// 确认门/提问同样代理给父会话（与 runDispatch 同一条通道）：合并进程的子会话
	// 遇到冲突抉择时经 ask_user 向用户提问——请求带子会话 id 作 dispatch_id 上抛
	//（父会话 emit chat.confirmRequest，前端把提问卡呈现出来），用户回答经
	// tool.confirm{answer} 打到父会话、再由父通道转回子会话的等待处。merger 的
	// 权限默认是 auto（高危不弹确认），但 ask 提问**不受档位豁免**——提问必须等用户。
	// 纯内存子会话（无存储）没有会话 id：退一个随机 id 保住「非空 dispatch_id」
	// 这条前端归属的前提（生产永远有存储，走的都是上面的会话 id）。
	//
	// dispatch_id 取子会话 id（不是另造一个）：确认/提问代理上抛的请求带的也是
	// 它——卡、提问、双投三处共用同一个归属键，前端「按 dispatch_id 反查卡 →
	// 卡上有子会话 id」的既有链路不用改。
	askDispatchID := childID
	if askDispatchID == "" {
		askDispatchID = newConfirmID()
	}
	// 子会话运行时登记进父会话（与 runDispatch 同款）：合并子会话同样可以被
	// 用户从标签页停止（chat.cancel 带子会话 id → CancelChild）。defer 注销
	// 覆盖 SendWait 的全部收尾路径。
	s.registerChild(askDispatchID, child)
	defer s.unregisterChild(askDispatchID)
	child.SetConfirmProxy(func(cctx context.Context, req *ConfirmRequest) (ConfirmOutcome, bool) {
		stamped := *req
		stamped.DispatchID = askDispatchID
		return s.awaitConfirm(cctx, &stamped)
	})
	// 子会话事件双路（见函数注释）：job 日志照旧写，同时经 childEmitter 带
	// dispatch_id 上抛父会话——父时间线挂出这张合并卡，前端据此建子会话标签页、
	// 双投实时流（提问卡也落进标签页内，用户在标签页里回答）。 TurnErrorEvent
	// 两路都收（job 日志要写、tap.err 要记），重复赋同一份值无害。
	tap := &childEvents{}
	jobLog := taskEmitter(out, tap)
	forward := childEmitter(s.emit, askDispatchID, tap)
	child.SetEmitter(func(ev Event) {
		jobLog(ev)
		forward(ev)
	})
	// 挂卡：合并任务在父时间线上是一张 dispatch 卡（一行摘要，点开进子会话标签页）
	// ——与 agent_dispatch 的卡同一份前端归约，不新写页面。
	s.emit(DispatchStartEvent{
		DispatchID: askDispatchID, SessionID: childID,
		AgentID: ac.Def.ID, AgentName: ac.Def.Name, AgentColor: ac.Def.Color,
		Task: task,
	})
	err = child.SendWait(ctx, task, WithAgent(agentID), WithApproval(ac.Def.Approval))
	// 收卡：结论 / 错误 / 取消都以 DispatchEnd 定格（错误口径与 runDispatch 一致——
	// SendWait 在 done 关闭时恒返回 nil，真错误看 tap.err）。
	endNote := ""
	endIsErr := false
	if err != nil {
		if ctx.Err() != nil {
			endNote = "已取消（合并进程中断）"
			// 与 runDispatch 同款：子轮若没走 aborted 收尾（确认门等待期间被断），
			// 这里补「用户中断」通告——模型下一轮才知道这轮为什么没有结论。
			if tap.err == nil || !tap.err.Aborted {
				_ = child.QueueNoticePassive(userAbortedNotice)
			}
		} else {
			endNote = err.Error()
		}
		endIsErr = true
	} else if tap.err != nil {
		if ctx.Err() != nil {
			endNote = "已取消（合并进程中断）"
			if tap.err == nil || !tap.err.Aborted {
				_ = child.QueueNoticePassive(userAbortedNotice)
			}
		} else {
			endNote = tap.err.Message
		}
		endIsErr = true
	} else {
		endNote = lastAssistantText(child.History().Messages)
		if endNote == "" {
			endNote = "（没有产出结论）"
		}
	}
	s.emit(DispatchEndEvent{
		DispatchID: askDispatchID, SessionID: childID, Result: endNote, IsError: endIsErr,
	})
	if endIsErr {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s", endNote)
	}
	return endNote, nil
}

// taskEmitter 把子会话的输出转成后台任务日志（**不往主会话时间线上抛任何事件**）。
//
// 与 childEmitter（dispatch 卡）的区别：那条路把子会话事件带 dispatch_id 归属进卡、让父
// 会话时间线实时可见；这条路是**后台进程**——输出进 job 日志（job_output 读），主会话
// 时间线只看到一条 job 卡与结束通告，看不到子会话的实时流。busy/todo/会话切换/压缩/产物
// 都不属于主时间线，一律拦下。
func taskEmitter(out io.Writer, tap *childEvents) Emitter {
	return func(ev Event) {
		switch e := ev.(type) {
		case DeltaEvent:
			if e.Kind == "text" && e.Text != "" {
				_, _ = io.WriteString(out, e.Text)
			}
		case ToolCallEvent:
			fmt.Fprintf(out, "\n[tool] %s %s\n", e.Name, clipForTaskLog(e.Arguments))
		case ToolResultEvent:
			fmt.Fprintf(out, "[result] %s\n", clipForTaskLog(e.Content))
		case TurnErrorEvent:
			fmt.Fprintf(out, "\n[错误] %s\n", e.Message)
			tap.mu.Lock()
			errCopy := e
			tap.err = &errCopy
			tap.mu.Unlock()
		case TurnDoneEvent, BusyEvent, TodoUpdatedEvent, SessionStartedEvent,
			DispatchStartEvent, DispatchEndEvent, ConfirmRequestEvent, CompactedEvent, FilesChangedEvent:
			// 不上抛：后台任务不进主会话时间线（子会话的忙闲/清单/切换/压缩/产物都不属于它）
		}
	}
}

// clipForTaskLog 截断后台任务日志里的单条内容（工具参数/结果可能很长）。
func clipForTaskLog(s string) string {
	r := []rune(s)
	if len(r) <= taskLogArgMax {
		return s
	}
	return string(r[:taskLogArgMax]) + "…"
}

// runDispatch 执行一次调度：解析目标 → 校验有效委派名单 → 开/接子会话 →
// 等它跑完 → 把结论回填成工具结果。
//
// 同步执行（调用方 runTools 的工具执行位）：主 Agent 要等子会话给出结论才继续。
// 取消经 ctx 传播（用户点停止时子会话同断）。
func (s *Session) runDispatch(ctx context.Context, call tools.DispatchCall) tools.DispatchResult {
	// 目标解析：名单存在 + 启用 + 在主 Agent 的有效委派名单内
	//（主 ac 的 Delegates——server 已解析「默认 ∩ 启用」）。
	ac, err := s.resolveAgent(call.Agent)
	if err != nil {
		return tools.DispatchResult{Output: fmt.Sprintf("错误: %v", err), IsError: true}
	}
	if ac.Def.IsMain {
		return tools.DispatchResult{Output: "错误: 主 Agent 不可被委派（它是唯一调度者——两类制）。请从可委派名单里选执行 Agent。", IsError: true}
	}
	// 委派名单校验：拿到主语境（当前轮的主 Agent 载荷）的有效名单。
	// 无主语境（旧调用方直发子 Agent 再 dispatch？不可能——子白名单
	// 不含本工具）时跳过（防御性放行，日志可见）。
	if s.dispatchRoot != nil {
		allowed := false
		for _, d := range s.dispatchRoot.Delegates {
			if d.ID == call.Agent {
				allowed = true
				break
			}
		}
		if !allowed {
			return tools.DispatchResult{Output: fmt.Sprintf(
				"错误: %s 不在有效委派名单内（可用: %s）。名单在 Agent 组装或会话委派面板里调整。",
				call.Agent, delegateNames(s.dispatchRoot)), IsError: true}
		}
	}

	// 开子会话（或按 call.Session 续跑既有子会话）
	child, childID, err := s.openChildSession(call, ac)
	if err != nil {
		return tools.DispatchResult{Output: fmt.Sprintf("错误: %v", err), IsError: true}
	}
	// 子会话运行时登记进父会话（CancelChild 的寻址依据）：服务端 chat.cancel
	// 对子会话 id 取不到 server.sessions（子会话运行时不注册在那里——生命周期
	// 跟着这次派发走），先查父会话的登记才能停掉真正在跑的子轮。SendWait 的
	// 三条收尾路径（正常 / 取消 / panic）都经下面的 defer 注销。
	// 纯内存子会话没有会话 id，用 dispatch id 当键（确认代理同款键）——
	// 外部虽然多半寻址不到它，但注销路径的对称性不破。
	childKey := childID
	if childKey == "" {
		childKey = call.DispatchID
	}
	s.registerChild(childKey, child)
	defer s.unregisterChild(childKey)
	// 子会话的事件按 dispatch_id 归属进卡；busy/todo/会话切换不上抛（见 childEmitter）
	tap := &childEvents{}
	child.SetEmitter(childEmitter(s.emit, call.DispatchID, tap))
	// 确认门代理给父会话：全应用只有"同时一个挂起确认"，子会话自己持 pending
	// 的话服务端的 tool.confirm 找不到它（会话会卡在 busy）。ask 提问走同一条
	// 代理（子会话的 ask_user 请求带 dispatch_id 上抛父会话，答案经父通道转回）。
	child.SetConfirmProxy(func(cctx context.Context, req *ConfirmRequest) (ConfirmOutcome, bool) {
		stamped := *req
		stamped.DispatchID = call.DispatchID
		return s.awaitConfirm(cctx, &stamped)
	})

	s.emit(DispatchStartEvent{
		DispatchID: call.DispatchID, SessionID: childID,
		AgentID: ac.Def.ID, AgentName: ac.Def.Name, AgentColor: ac.Def.Color,
		Task: call.Task,
	})

	// 子会话的权限档**不在派发这一刻算死值**：把父会话的实时档位以回调形式交给
	// 子会话，子会话每次工具调用现算 stricterApproval(父此刻, 子 Agent 默认)。
	//
	// 为什么（2026-09-29 用户实测）：原先算好再 WithApproval 传进去，父会话中途
	// 改档传不到**正在跑的**子会话——而"跑 dev 的是子 Agent"，那正是用户看到的
	// 现象。取严语义（子执行面不大于请求方）不变，只是从"派发那一刻"改成"每次
	// 现算"。闭包里现调 s.LiveApproval()，不是闭包外的变量。
	// 模型/提示词/工具白名单仍由子会话按自己的 Agent 载荷组装。
	child.SetApprovalSource(func() string { return s.LiveApproval() })
	child.SetApprovalDefault(ac.Def.Approval)
	err = child.SendWait(ctx, composeTaskMessage(call), WithAgent(ac.Def.ID))
	// 子会话这一轮的错误**不能只看 SendWait 的返回值**：SendWait 在 done 关闭时恒
	// 返回 nil（即使这一轮以错误收尾——比如工具循环达上限），真正的错误记在 tap.err 里。
	//
	// 只看 err 的后果（2026-09-29 用户实测「第一个子 Agent 没有返回结果，主 Agent
	// 拿它的会话 id 重新做了一遍」）：达上限那种收尾静默走进结论分支，而结论取到的是
	// 中间轮的**开场白**（"I'll start by …"）——一句没兑现的承诺被当成结果回填，
	// 主 Agent 既不知道子 Agent 没做完，又看到"需要接着这次进度继续时把它填进 session
	// 参数重派"，于是自己又派了一遍。
	if err != nil || tap.err != nil {
		note := "（子 Agent 没有产出结论）"
		if err != nil {
			note = err.Error()
		}
		if ctx.Err() != nil {
			note = "已取消（子会话中断）"
			// 用户停掉父轮 → 子轮连带被取消。子轮若走了 aborted 收尾（流式阶段
			// 被断），它自己的 runTurn 已经入队「用户中断」通告（见 turn.go）；
			// 走不到那条路的取消（如确认门等待期间被断，runTools 静默返回 false）
			// 在这里补——通告留在子会话自己的队列里，下一轮边界注入，模型才知道
			// 这一轮为什么没有结论。不重复入队：同一轮取消只说一遍。
			if tap.err == nil || !tap.err.Aborted {
				_ = child.QueueNoticePassive(userAbortedNotice)
			}
		} else if tap.err != nil {
			note = tap.err.Message
			if tap.err.Aborted {
				note = "已取消（保留已生成部分）"
			}
		}
		s.emit(DispatchEndEvent{
			DispatchID: call.DispatchID, Result: note, IsError: true, SessionID: childID,
		})
		return tools.DispatchResult{Output: note, IsError: true, SessionID: childID}
	}

	// 结论：子会话最后的 assistant 文本（与升级前一致——只把结论带回主上下文，
	// 子会话的中间消息留在它自己的历史里）
	result := lastAssistantText(child.History().Messages)
	if result == "" {
		result = "（子 Agent 没有产出文本结论）"
	}
	s.emit(DispatchEndEvent{
		DispatchID: call.DispatchID, Result: result, SessionID: childID,
	})
	return tools.DispatchResult{Output: result, SessionID: childID}
}

// openChildSession 开/接子会话并挂好存储：call.Session 非空 = 续跑既有子会话
// （历史在库里，接着跑）；否则新开一个（自己的行 + 自己的历史）。
func (s *Session) openChildSession(call tools.DispatchCall, ac *sessiondata.AgentContext) (*Session, string, error) {
	s.mu.Lock()
	st, parentID, stream, workDir := s.st, s.id, s.stream, s.workDir
	branch, base := s.worktreeBranch, s.worktreeBase
	s.mu.Unlock()
	child := New(s.reg, s.tools, s.emit)
	// 子会话在父会话的工作树里工作（SetWorkDir 继承同一个目录），提示词要如实说
	// 「改动与父会话共享」而不是「你有独立分支」——分支信息同样从父会话继承。
	child.SetWorktreeInfo(branch, base)
	// 子会话压缩时保护历史第 0 条 = 派发的那条任务说明书（见 Session.protectHead）：
	// 这是子 Agent 唯一的任务依据，被压进摘要后长任务就会跑偏。
	// 在 st == nil 的提前返回之前置位——内存子会话同样有这条头部。
	child.SetProtectHead(true)
	// 时间线归属 = 父会话的时间线：子会话起的后台任务要挂在父会话上（通告投给
	// 父会话才有人能行动——子会话不进侧栏，见 Session.ownerID）
	child.SetOwner(s.OwnerSessionID())
	// 继承父会话的 LLM 调用实现：生产是 streamWithLLM（同一份），测试是注入的假流——
	// 不继承的话子会话会绕开宿主注入的流去连真实端点（单测直接炸）。
	child.SetStream(stream)
	child.SetAgentResolver(s.agents)
	child.SetProjectDocs(s.projectDocs)
	// 子会话归属它自己的 Agent（会话页显示模型时按它解析：子 Agent 可以绑自己的
	// 模型，回落主 Agent 会显示错）。SendWait 的 WithAgent 也会记一遍，但那是
	// "跑过一轮之后"才有——刷新后只读历史（chat.history）时也要能答出模型。
	child.SetAgentID(ac.Def.ID)
	// 无存储（纯内存模式/未挂 store 的调用方）：退化成内存子会话——它仍是一个
	// 独立会话（自己的历史、自己的压缩），只是不落库、不能续跑。生产永远有存储。
	if st == nil {
		if err := child.SetWorkDir(workDir); err != nil {
			return nil, "", err
		}
		return child, "", nil
	}
	childID := call.Session
	if childID == "" {
		id, err := st.CreateChild(parentID, ac.Def.ID, call.DispatchID)
		if err != nil {
			return nil, "", err
		}
		childID = id
	}
	if err := child.AttachTo(st, childID); err != nil {
		return nil, "", err
	}
	if err := child.SetWorkDir(workDir); err != nil {
		return nil, "", err
	}
	return child, childID, nil
}

// lastAssistantText 取子会话的结论：**最后一条 assistant 消息**的正文。
//
// 为什么不能往回扫到"最近一条有正文的"（2026-09-29 用户实测）：多轮工具循环里模型
// 每轮都会先写一句开场白再发工具调用（"I'll start by …"），那些中间轮的开场白**不是
// 结论**。往回扫会把一句没兑现的承诺当成结论回填——主 Agent 既不知道子 Agent 没做完，
// 又看到"接着这次进度继续时填 session 参数重派"，于是自己又派了一遍（用户实测：第一个
// 子 Agent 没返回结果，主 Agent 拿它的会话 id 重新做了一遍）。
// 最后一条是工具调用轮（正文为空）时如实返回空串：调用方据此报"没有产出结论"，而不是
// 拿一句开场白冒充结果。
func lastAssistantText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}

// composeTaskMessage 组任务消息（task + 可选背景）。子会话的第一条 user 消息
// 就是它——子 Agent 的全部依据（它看不到主对话历史）。
func composeTaskMessage(call tools.DispatchCall) string {
	var b strings.Builder
	b.WriteString(call.Task)
	if ctx := strings.TrimSpace(call.Context); ctx != "" {
		b.WriteString("\n\n背景：\n")
		b.WriteString(ctx)
	}
	return b.String()
}

// delegateNames 列出有效委派名单的 id（错误信息用）。
func delegateNames(root *sessiondata.AgentContext) string {
	names := make([]string, 0, len(root.Delegates))
	for _, d := range root.Delegates {
		names = append(names, d.ID)
	}
	if len(names) == 0 {
		return "（空）"
	}
	return strings.Join(names, ", ")
}

// childEvents 是子会话事件的可观测面：捕获子会话的收尾错误（给 dispatch 结果用），
// 同时把不该上抛的事件拦下来。
type childEvents struct {
	mu  sync.Mutex
	err *TurnErrorEvent
}

// childEmitter 包装父会话的事件出口：子会话的事件带上 dispatch_id 归属进卡，
// 同时拦掉不该上抛的几类。
//
// 为什么要拦：
//   - SessionStartedEvent：子会话行懒建不该让侧栏切过去（子会话不进侧栏列表）；
//   - TodoUpdatedEvent：清单是会话级状态，子会话的清单属于它自己（主会话的
//     计划条只显示主会话的清单）；
//   - TurnErrorEvent：子会话的错误由 runDispatch 收成 DispatchEndEvent（保持
//     卡内呈现语义，不让它跑到主时间线上当一条独立错误）。
//
// BusyEvent **上抛但带归属**（2026-10-10 用户要「子 Agent 里也要正在生成中的
// 样式」）：带 dispatch_id 的 busy 在前端被两条守卫拦在主时间线之外（reduce 的
// busy 分支与 store 的自动发送边界都跳过带归属的事件），同时经双投进子会话
// 自己的 state——子会话标签页的「生成中」行与停止钮由此出现。主会话自己的
// busy（无归属）语义逐字节不变。
func childEmitter(parent Emitter, dispatchID string, tap *childEvents) Emitter {
	return func(ev Event) {
		switch e := ev.(type) {
		case DeltaEvent:
			e.DispatchID = dispatchID
			parent(e)
		case ToolCallEvent:
			e.DispatchID = dispatchID
			parent(e)
		case ToolResultEvent:
			e.DispatchID = dispatchID
			parent(e)
		case TurnDoneEvent:
			e.DispatchID = dispatchID
			parent(e)
		case CompactedEvent:
			e.DispatchID = dispatchID
			parent(e)
		case BusyEvent:
			e.DispatchID = dispatchID
			parent(e)
		case FilesChangedEvent:
			parent(e)
		case TurnErrorEvent:
			tap.mu.Lock()
			errCopy := e
			tap.err = &errCopy
			tap.mu.Unlock()
		case TodoUpdatedEvent, SessionStartedEvent, DispatchStartEvent, DispatchEndEvent, ConfirmRequestEvent:
			// 不上抛：清单属于子会话自己、确认由父会话代理时发、
			// start/end 由派发方自己发
		}
	}
}
