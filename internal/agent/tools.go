// 工具调用的执行与落历史：权限门（白名单/strict/确认）、并行 dispatch、
// 结果回填（含取消时的合成结果，配对不变量见 §2.2）。

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/moyunteng/lxcode/internal/jsonrepair"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// runTools 执行本轮工具调用，按权限模式门控：
//   - strict 只读：变更类工具（IsMutating）直接拒绝，错误回填模型
//   - auto 完全访问：高危跳过确认门
//   - confirm（默认）：高危先确认（现行语义）
//
// Agent 白名单之外的调用直接拒绝（错误自解释——模型看到的工具清单
// 已按白名单过滤，正常不会越界；这里是防御层）。
// dispatchID 非空 = 子 Agent 执行（事件带归属）；写目标经 sink 抽象
// （主轮 = s.append 进会话历史并落库；子轮 = 写局部历史，隔离）。
// 返回 false 表示被取消。fileChanges 收集文件改动供轮末产物汇总。
func (s *Session) runTools(ctx context.Context, calls []llm.ToolCall, fileChanges *[]FileChange, ac *sessiondata.AgentContext, dispatchID string, sink func(llm.Message)) bool {
	policy := tools.ApprovalFrom(ctx)
	allowed := toolSet(agentToolsOf(ac))
	// finished 标记哪些调用已经落了结果：取消时剩下的（含已登记但还没跑的 dispatch）
	// 都要补合成结果，配对不变量不能破（见 sinkSkippedToolResults）。
	finished := make([]bool, len(calls))
	// 本轮 dispatch 的结果按下标暂存：阶段二并行跑、阶段三按原顺序回填。
	dispatched := make([]toolOutcome, len(calls))
	type dispatchJob struct {
		idx  int
		call tools.DispatchCall
	}
	var dispatches []dispatchJob

	// 阶段一（顺序）：权限门 + 非 dispatch 工具**就地执行并立即落结果**。
	// 非 dispatch 的就地落结果不能推到阶段三——emit 回调里取消会话（用户点停止）
	// 必须能立刻停住本批剩下的工具，推迟落结果就等于"取消也照样跑完"。
	// dispatch 只登记参数，攒到阶段二一起并行跑（见下面的注释）。
	for i, tc := range calls {
		if ctx.Err() != nil {
			s.sinkSkippedToolResults(sink, pendingCalls(calls, finished), dispatchID, skippedCancelNote)
			return false
		}
		// 白名单防御：清单外的工具拒绝（ac 非 nil 才有白名单语义）。
		// agent_dispatch 在子语境天然被挡（子 Agent 白名单不含它——
		// 两类制深度恒 1 的运行时保证）。
		if allowed != nil && !allowed[tc.Function.Name] {
			reject := fmt.Sprintf(
				"错误: 工具 %s 不在本 Agent 的白名单内（可用: %s）。如需该能力，请让用户在 Agent 组装里勾选。",
				tc.Function.Name, strings.Join(agentToolsOf(ac), ", "))
			s.finishToolCall(tc, reject, "", true, fileChanges, dispatchID, sink)
			finished[i] = true
			continue
		}
		// strict：变更类工具拒绝（不进确认门——只读模式没有"确认放行"语义；
		// 错误信息自解释，模型可换读取类工具或向用户说明）。
		if policy == tools.ApprovalStrict && s.tools.IsMutating(tc.Function.Name) {
			reject := fmt.Sprintf(
				"错误: 当前为只读模式（strict），已禁用 %s。请改用 read_file / search 等读取类工具，或提示用户切换权限模式。",
				tc.Function.Name)
			s.finishToolCall(tc, reject, "", true, fileChanges, dispatchID, sink)
			finished[i] = true
			continue
		}
		if prompt := s.tools.Confirm(ctx, tc); prompt != "" && policy != tools.ApprovalAuto {
			req := &ConfirmRequest{
				ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments, Prompt: prompt,
				DispatchID: dispatchID, // 子 Agent 的确认归属（前端挂 dispatch 卡内）
			}
			allow, ok := s.awaitConfirm(ctx, req)
			if !ok {
				// 等待确认期间被取消：本调用与它后面的都没执行
				s.sinkSkippedToolResults(sink, pendingCalls(calls, finished), dispatchID, skippedCancelNote)
				return false // 取消
			}
			if !allow {
				s.finishToolCall(tc,
					"用户拒绝了这次工具调用（未执行）。请改用其他方式完成任务，或向用户说明需要该操作的原因。",
					"用户拒绝执行", true, fileChanges, dispatchID, sink)
				finished[i] = true
				continue
			}
		}
		// agent_dispatch 走内核直连（工具声明的 Exec 是未接线兜底）：
		// 子循环需要 tc.ID 做事件归属 + 委派名单校验，Exec 的入参形状
		//（json.RawMessage）给不了——在调用位展开。这里只解析参数，执行留到阶段二。
		if tc.Function.Name == tools.DispatchToolName {
			var p struct {
				Agent   string `json:"agent"`
				Task    string `json:"task"`
				Context string `json:"context"`
				Session string `json:"session"`
			}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &p); err != nil {
				s.finishToolCall(tc,
					fmt.Sprintf("错误: %s 参数解析失败: %v", tools.DispatchToolName, err),
					"", true, fileChanges, dispatchID, sink)
				finished[i] = true
				continue
			}
			dispatches = append(dispatches, dispatchJob{idx: i, call: tools.DispatchCall{
				DispatchID: tc.ID, Agent: p.Agent, Task: p.Task, Context: p.Context,
				Session: p.Session,
			}})
			continue
		}
		s.finishToolCall(tc, s.tools.Execute(ctx, tc), "", false, fileChanges, dispatchID, sink)
		finished[i] = true
	}

	// 阶段二（并行）：本轮的 dispatch 调用一起跑。
	//
	// 为什么必须并行：模型表达"并行需求"的唯一方式就是一条 assistant 消息里发多个
	// dispatch 调用；串行跑的话模型以为并行了、实际一个接一个，墙钟是 N 倍——用户
	// 实测反馈「好像模型觉得并行了，但还是串行」（两个各 250ms 的子会话实测 507ms）。
	// 为什么安全：子 Agent 本来就是独立会话（§2.3），单进程内每个 session_id 有
	// 自己的生成 goroutine，本来就能并发（§2.1 多活跃会话）——只有这条工具循环是串的。
	// 结果按下标攒，阶段三按原顺序回填，历史配对顺序与 tool_calls 一致。
	if len(dispatches) > 0 {
		var wg sync.WaitGroup
		for _, j := range dispatches {
			wg.Add(1)
			go func(j dispatchJob) {
				defer wg.Done()
				res := s.runDispatch(ctx, j.call)
				out := res.Output
				if res.SessionID != "" {
					// 把子会话 id 交给主 Agent：下一轮要接着它跑就填进 session 参数
					out += fmt.Sprintf("\n\n[子会话 id: %s —— 需要接着这次进度继续时，把它填进 session 参数重派]", res.SessionID)
				}
				dispatched[j.idx] = toolOutcome{result: out, isError: res.IsError, executed: true}
			}(j)
		}
		wg.Wait()
	}

	// 阶段三（顺序）：dispatch 的结果按原下标顺序落历史 + 发事件。
	// dispatch 期间被取消的，runDispatch 已回带取消说明（子会话中断），这里照常落。
	for _, j := range dispatches {
		if !dispatched[j.idx].executed {
			continue
		}
		s.finishToolCall(calls[j.idx], dispatched[j.idx].result, dispatched[j.idx].uiText,
			dispatched[j.idx].isError, fileChanges, dispatchID, sink)
		finished[j.idx] = true
	}
	return true
}

// finishToolCall 落一条工具结果：文件改动汇总 + 超长截断 + 写历史 + 发事件。
// uiText 是事件里的短文案（空 = 用正文）——用户拒绝那种要落历史的正文比 UI 长。
func (s *Session) finishToolCall(tc llm.ToolCall, result, uiText string, isError bool, fileChanges *[]FileChange, dispatchID string, sink func(llm.Message)) {
	collectFileChange(fileChanges, tc, result)
	if r := []rune(result); len(r) > maxToolResultBytes {
		result = string(r[:maxToolResultBytes]) +
			fmt.Sprintf("\n…（结果过长已截断，全文共 %d 字符）", len(r))
	}
	if uiText == "" {
		uiText = result
	}
	sink(llm.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
	s.emit(ToolResultEvent{
		ID: tc.ID, Name: tc.Function.Name, Content: uiText,
		IsError: isError, DispatchID: dispatchID,
	})
}

// pendingCalls 收集还没落结果的调用（按原下标顺序）——取消时给它们补合成结果。
// 为什么不能只从断点往后取：并行 dispatch 之前登记、还没轮到跑的调用也在断点之前。
func pendingCalls(calls []llm.ToolCall, finished []bool) []llm.ToolCall {
	var out []llm.ToolCall
	for i := range calls {
		if !finished[i] {
			out = append(out, calls[i])
		}
	}
	return out
}

// toolOutcome 是一次工具调用的结果：正文落历史、短文案发事件（空 = 用正文）、
// 是否错误、是否真的执行过（取消中断的没执行 → 补合成结果）。
type toolOutcome struct {
	result   string
	uiText   string
	isError  bool
	executed bool
}

// 未执行调用的合成结果文案（取消与流式失败两条路径的口径）。写成常量是为了
// 单测能按语义断言，而不是按某处拼出来的字符串。
const (
	skippedCancelNote  = "已取消，未执行——用户中断了这次生成。需要时请重新发起该调用。"
	skippedFailureNote = "本轮生成失败，该调用未执行。需要时请重新发起。"
)

// sinkSkippedToolResults 给「不会执行」的调用补上合成结果。
//
// 为什么必须补：历史里 assistant 声明的每个 tool_call 都要有配对的 tool 结果。
// 缺配对不只是"不好看"——严格端点会直接 400 拒收整轮（assistant 的 tool_calls
// 必须有配对结果），而且 AnalyzeToolPairing 的游标在缺配对处之后再不平衡，
// 压缩的切点会永久卡在它之前：那个会话从此再也压不动，上下文一路涨到撞窗口。
// 取消（用户点停止）与流式失败两条路径共用这一份补齐逻辑——理由与「工具被拒绝
// 也要回填结果」完全相同（见上面白名单/strict/用户拒绝三处的同款写法）。
func (s *Session) sinkSkippedToolResults(sink func(llm.Message), calls []llm.ToolCall, dispatchID, note string) {
	for _, tc := range calls {
		sink(llm.Message{Role: "tool", ToolCallID: tc.ID, Content: note})
		s.emit(ToolResultEvent{
			ID: tc.ID, Name: tc.Function.Name, Content: note, IsError: true, DispatchID: dispatchID,
		})
	}
}

// sanitizeToolCallArgs 保证写进历史的工具调用参数是合法 JSON。
//
// 为什么必须在写边界做（2026-09-23 线上事故）：模型输出被 max_tokens 截断时，
// arguments 会是半截 JSON（字符串与括号都没闭合）。这种消息一旦落进历史，之后
// **每一次**请求都会在组装阶段被端点适配器拒绝（anthropic 适配器对此是硬校验：
// `工具 X 的 arguments 不是合法 JSON`）——整个会话永久发不出请求，用户只能新开
// 会话。与「取消时补配对」是同一类不变量：历史必须始终良构。
//
// 策略：先保守修复（internal/jsonrepair，与 tools / llm 适配器同一份实现），
// 修不动就降级成 "{}"。**不删调用**——删掉会让它的结果变成孤儿 tool 消息（配对
// 不变量更硬），降级成空参数至少保住结构合法，模型也能看出参数丢了。where 只用于
// 日志（说明是哪条路径修的）。
func sanitizeToolCallArgs(calls []llm.ToolCall, where string) {
	for i := range calls {
		args := calls[i].Function.Arguments
		if strings.TrimSpace(args) == "" || json.Valid([]byte(args)) {
			continue
		}
		next, how := "{}", "降级为空参数"
		if repaired, ok := jsonrepair.Repair(args); ok {
			next, how = repaired, "保守补全"
		}
		calls[i].Function.Arguments = next
		log.Printf("工具调用参数不是合法 JSON，写历史前已%s：%s（%s，原参数 %d 字节）",
			how, calls[i].Function.Name, where, len(args))
	}
}

// collectFileChange 从 edit/write_file 调用提取改动摘要（同文件多次改动
// 逐步合并——产物卡按文件聚合，行数累计，diff 追加最新块）。
func collectFileChange(out *[]FileChange, tc llm.ToolCall, _ string) {
	var path, oldStr, newStr string
	switch tc.Function.Name {
	case "edit":
		var a struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &a) != nil || a.Path == "" {
			return
		}
		path, oldStr, newStr = a.Path, a.OldString, a.NewString
	case "write_file":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &a) != nil || a.Path == "" {
			return
		}
		path, newStr = a.Path, a.Content
	default:
		return
	}
	added, deleted := 0, 0
	if oldStr != "" {
		deleted = len(strings.Split(strings.TrimSuffix(oldStr, "\n"), "\n"))
	}
	if newStr != "" {
		added = len(strings.Split(strings.TrimSuffix(newStr, "\n"), "\n"))
	}
	var diff strings.Builder
	diff.WriteString("@@ " + path + "\n")
	for _, l := range strings.Split(oldStr, "\n") {
		diff.WriteString("-" + l + "\n")
	}
	for _, l := range strings.Split(newStr, "\n") {
		diff.WriteString("+" + l + "\n")
	}
	// 同文件合并：行数累计 + diff 换块
	for i := range *out {
		if (*out)[i].Path == path {
			(*out)[i].Added += added
			(*out)[i].Deleted += deleted
			(*out)[i].Diff += "\n" + diff.String()
			return
		}
	}
	*out = append(*out, FileChange{Path: path, Added: added, Deleted: deleted, Diff: diff.String()})
}
