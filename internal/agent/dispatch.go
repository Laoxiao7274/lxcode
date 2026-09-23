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
	"strings"
	"sync"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

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
	// 子会话的事件按 dispatch_id 归属进卡；busy/todo/会话切换不上抛（见 childEmitter）
	tap := &childEvents{}
	child.SetEmitter(childEmitter(s.emit, call.DispatchID, tap))
	// 确认门代理给父会话：全应用只有"同时一个挂起确认"，子会话自己持 pending
	// 的话服务端的 tool.confirm 找不到它（会话会卡在 busy）。
	child.SetConfirmProxy(func(cctx context.Context, req *ConfirmRequest) (bool, bool) {
		stamped := *req
		stamped.DispatchID = call.DispatchID
		return s.awaitConfirm(cctx, &stamped)
	})

	s.emit(DispatchStartEvent{
		DispatchID: call.DispatchID, SessionID: childID,
		AgentID: ac.Def.ID, AgentName: ac.Def.Name, AgentColor: ac.Def.Color,
		Task: call.Task,
	})

	// 跑子会话那一轮并等它结束：审批模式**取严**——父轮授权面与子 Agent 自己
	// 的默认取更严的一档（子 Agent 没声明默认 = 继承父轮），子执行面因此不大于
	// 请求方；模型/提示词/工具白名单由子会话按自己的 Agent 载荷组装。
	approval := stricterApproval(string(tools.ApprovalFrom(ctx)), ac.Def.Approval)
	err = child.SendWait(ctx, composeTaskMessage(call),
		WithAgent(ac.Def.ID), WithApproval(approval))
	if err != nil {
		note := err.Error()
		if ctx.Err() != nil {
			note = "已取消（子会话中断）"
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
	st, parentID, stream := s.st, s.id, s.stream
	s.mu.Unlock()
	child := New(s.reg, s.tools, s.emit)
	// 子会话压缩时保护历史第 0 条 = 派发的那条任务说明书（见 Session.protectHead）：
	// 这是子 Agent 唯一的任务依据，被压进摘要后长任务就会跑偏。
	// 在 st == nil 的提前返回之前置位——内存子会话同样有这条头部。
	child.SetProtectHead(true)
	// 继承父会话的 LLM 调用实现：生产是 streamWithLLM（同一份），测试是注入的假流——
	// 不继承的话子会话会绕开宿主注入的流去连真实端点（单测直接炸）。
	child.SetStream(stream)
	child.SetAgentResolver(s.agents)
	child.SetProjectDocs(s.projectDocs)
	// 无存储（纯内存模式/未挂 store 的调用方）：退化成内存子会话——它仍是一个
	// 独立会话（自己的历史、自己的压缩），只是不落库、不能续跑。生产永远有存储。
	if st == nil {
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
	return child, childID, nil
}

// lastAssistantText 取历史里最后一条有正文的 assistant 消息（子会话的结论）。
func lastAssistantText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			if t := strings.TrimSpace(msgs[i].Content); t != "" {
				return t
			}
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
//   - BusyEvent：子会话的忙闲不是主会话的忙闲，上抛会把主界面的"生成中"翻掉；
//   - SessionStartedEvent：子会话行懒建不该让侧栏切过去（子会话不进侧栏列表）；
//   - TodoUpdatedEvent：清单是会话级状态，子会话的清单属于它自己（主会话的
//     计划条只显示主会话的清单）；
//   - TurnErrorEvent：子会话的错误由 runDispatch 收成 DispatchEndEvent（保持
//     卡内呈现语义，不让它跑到主时间线上当一条独立错误）。
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
		case FilesChangedEvent:
			parent(e)
		case TurnErrorEvent:
			tap.mu.Lock()
			errCopy := e
			tap.err = &errCopy
			tap.mu.Unlock()
		case BusyEvent, TodoUpdatedEvent, SessionStartedEvent, DispatchStartEvent, DispatchEndEvent, ConfirmRequestEvent:
			// 不上抛：busy 会翻掉主会话、清单属于子会话自己、确认由父会话代理时发
		}
	}
}
