// agent.dispatch 的执行体（M3——子上下文隔离）：主 Agent 把任务派给
// 名单中的子 Agent，子 Agent 在独立上下文里跑完整工具循环，结果回填
// 主会话（作为该工具调用的结果）。
//
// 隔离语义（2026-09-18 拍板）：子 Agent 只拿到任务描述（+ 可选背景）
// 与自己的四层组合提示词——看不到主对话历史；主上下文不因子执行
// 过程膨胀（子执行的中间消息全部留在子历史里，只有最终结论回去）。
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// dispatchMaxRounds 子循环的轮数上限：独立于主循环（子任务边界明确，
// 16 轮足够；防小模型死循环与主循环共享预算的耦合）。
const dispatchMaxRounds = 16

// runDispatch 执行一次调度：解析目标 → 校验有效委派名单 → 子上下文
// 循环 → 结果回填。同步执行（调用方 runTools 的工具执行位）——取消
// 经 ctx 传播（用户点停止时子循环同断）。
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

	// 子上下文（隔离核心）：任务消息起步 + 子 Agent 四层组合提示词。
	// 主历史不进来——子 Agent 据任务独立工作；需要背景时主 Agent 写
	// 进 context（它做摘要，不是全文转发）。
	taskMsg := llm.Message{Role: "user", Content: composeTaskMessage(call)}
	history := []llm.Message{taskMsg}

	s.emit(DispatchStartEvent{
		DispatchID: call.DispatchID,
		AgentID:    ac.Def.ID, AgentName: ac.Def.Name, AgentColor: ac.Def.Color,
		Task: call.Task,
	})

	// 子上下文的技能目录（read_skill 的取数源——子 Agent 白名单内）：
	// 临时切换到子目录，收尾恢复主语境。
	if len(ac.Skills) > 0 {
		entries := make([]tools.SkillEntry, 0, len(ac.Skills))
		for i := range ac.Skills {
			entries = append(entries, tools.SkillEntry{ID: ac.Skills[i].ID, Desc: ac.Skills[i].Desc, Body: ac.Skills[i].Body})
		}
		saved := tools.GetSkillSource()
		s.tools.SetSkillSource(func(context.Context) []tools.SkillEntry { return entries })
		defer s.tools.SetSkillSource(saved)
	}

	// 子历史写目标：局部 append（不落库——子执行不进主历史）
	subAppend := func(m llm.Message) { history = append(history, m) }

	// 工作目录：子 Agent 与主会话同一基准（项目根）——工具的相对路径
	// 解析在两个上下文里一致。
	s.mu.Lock()
	subWorkDir := s.workDir
	s.mu.Unlock()

	var fileChanges []FileChange
	var usageTotal int
	for round := 0; round < dispatchMaxRounds; round++ {
		res, err := s.streamRound(ctx, subWorkDir, "", ac, history, call.DispatchID)
		if err != nil {
			aborted := ctx.Err() != nil
			if res != nil {
				subAppend(res.Message)
			}
			note := err.Error()
			if aborted {
				note = "已取消（保留已生成部分）"
			}
			s.emit(DispatchEndEvent{
				DispatchID: call.DispatchID, Result: note, IsError: true,
			})
			return tools.DispatchResult{Output: note, IsError: true}
		}
		subAppend(res.Message)
		usageTotal += res.UsageTokens
		if len(res.Message.ToolCalls) == 0 {
			// 子 Agent 收尾：最终回复即调度结果
			result := strings.TrimSpace(res.Message.Content)
			if result == "" {
				result = "（子 Agent 没有产出文本结论）"
			}
			s.emit(TurnDoneEvent{
				Message: res.Message, UsageTokens: res.UsageTokens, FinishReason: res.FinishReason,
				DispatchID: call.DispatchID,
			})
			s.emit(DispatchEndEvent{
				DispatchID: call.DispatchID, Result: result, UsageTokens: usageTotal,
			})
			// 子轮的文件改动并入主轮产物（用户验收的是最终改动面）
			if len(fileChanges) > 0 {
				s.emit(FilesChangedEvent{Files: fileChanges})
			}
			return tools.DispatchResult{Output: result}
		}
		if !s.runTools(ctx, res.Message.ToolCalls, &fileChanges, ac, call.DispatchID, subAppend) {
			// 取消
			s.emit(DispatchEndEvent{
				DispatchID: call.DispatchID, Result: "已取消", IsError: true,
			})
			return tools.DispatchResult{Output: "已取消（子 Agent 执行中断）", IsError: true}
		}
	}
	s.emit(DispatchEndEvent{
		DispatchID: call.DispatchID,
		Result:     fmt.Sprintf("子 Agent 工具循环达上限（%d 轮）", dispatchMaxRounds),
		IsError:    true,
	})
	return tools.DispatchResult{
		Output:  fmt.Sprintf("错误: 子 Agent 工具循环达上限（%d 轮），任务未完成。", dispatchMaxRounds),
		IsError: true,
	}
}

// composeTaskMessage 组任务消息（task + 可选背景）。
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
