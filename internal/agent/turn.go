// 一轮生成的回合循环：压缩触发、流式调用、上下文计账。
// 与 tools.go 的分工：这里只负责「调用模型并处理流事件」，工具的执行在 tools.go。

package agent

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// SystemNoticePrefix 标注「这条消息是系统对模型的注记，不是用户说的，也不要给用户
// 看」——前端按它识别后**不渲染任何块**（用户气泡与提示条都不出），模型侧照常入
// 历史/注入/落库（与 protocol.JobNoticePrefix、agent.RepeatNoticePrefix 同一套
// 文本前缀机制，见 notify.go）。常量放在 agent 而不是 protocol：分层守卫禁止
// agent import protocol（AGENTS.md §4），与 RepeatNoticePrefix 同一条理由；前端的
// 同名常量由 frontend/tests 的对照测试钉住逐字一致。
const SystemNoticePrefix = "[系统通告] "

// userAbortedNotice 是「用户主动停止」时入队的通告（turn.go 的 aborted 分支 /
// dispatch.go 的取消分支）：模型必须知道这轮是被用户停的——半截回答不是完整
// 结论，不要自行续写，等用户的下一条指示。文案用中性的「生成被用户中断」，
// 不区分「用户停的是本会话」与「用户停了父轮导致本子会话连带被停」：
// 对模型来说两者的行动指令一致（停下、别续写、听下一条指示），强分没有收益。
//
// 带 SystemNoticePrefix：对用户隐藏（不渲染成用户气泡/提示条），模型照常可见——
// 三处入队写点（runTurn 的 aborted 分支 + dispatch.go 的三个取消分支）都用本
// 常量，前缀在这里拼一次即全覆盖。
const userAbortedNotice = SystemNoticePrefix + "用户中断了这次生成（刚才算到一半的回答没有完成）。不要自行续写或重试刚才的任务；等用户的下一条指示，按新指示行动。"

// streamWithLLM 默认 LLM 调用：按 default 角色配置建客户端，经 ChatAuto
// （anthropic 永远流式；openai 带工具走非流式回放，见 llm.ChatAuto 注释——
// 该策略来自真机端点实测：部分 openai 兼容端点的流式会丢 tool_calls）。
func streamWithLLM(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
	client, err := llm.New(llm.Config{
		BaseURL: m.BaseURL,
		APIKey:  m.APIKey,
		Model:   m.Model,
		Format:  m.EffectiveFormat(),
	})
	if err != nil {
		return nil, err
	}
	return client.ChatAuto(ctx, msgs, opts...)
}

// recordContextUsage 记录一次主轮请求的占用：res 带 prompt_tokens 就用真实值
// 锚定（分类等比归一），否则保留估算值（Estimated=true）。history 是**这次请求实际
// 发出去的那段历史**——它的估算量当采样基线存下来，供 projectedUsage 算"之后长了多少"。
//
// 为什么顺带落库：占用原先**只在内存里**——后端一重启，重启前跑过的会话打开时
// 指示器就是空的（用户实测「查看会话，他的上下文信息怎么是空的」）。占用是**已知事实**
// （那条会话的历史就摆在那里），不该因为进程重启就变成未知。落库之后 chat.history 的
// 回放与 chat.done 的实时是同一份数字（本仓库为"两条路径分叉"吃过三次亏）。
//
// 每个会话记**自己**的占用：s 是这条会话自己的 Session（子会话是一个真 Session，
// 见 AGENTS.md §2.3），所以子轮记进的是子会话自己的 s.context——主会话的指示器
// 不会被它碰到（那是另一个 Session 对象）。落库也按 s.id（子会话自己的行）。
//
// 2026-09-30 修正：这里原先是 `dispatchID == ""` 才记，理由是"子上下文有自己的窗口"
// ——但**跳过记录**并不能保护主会话（主会话本来就读不到子会话的 s.context），
// 只是让子会话自己的指示器永远空着（用户报「子Agent的会话里…上下文 会话信息这些
// 展示没有」）。正确的收口是"各记各的"，不是"子轮不记"。
func (s *Session) recordContextUsage(window int, est ContextUsage, res *llm.ChatResult, history []llm.Message) {
	used := 0
	if res != nil {
		used = res.PromptTokens
	}
	u := anchoredUsage(est, used)
	u.Window = window
	u.SampledTokens = historyTokens(history)
	s.mu.Lock()
	s.context = u
	st, id := s.st, s.id
	s.mu.Unlock()
	persistContextUsage(st, id, u)
}

// persistContextUsage 把一次占用测量落库（调用方**不持锁**——写盘不该占着会话锁）。
// st 为 nil（纯内存模式）或 id 为空（会话还没建行）时是 no-op。
//
// 失败只记日志：占用是展示信息，不是业务不变量——落库失败不该让整轮对话失败
// （内存才是运行真源，最坏情况是重启后回落成估算值，而不是"这一轮白跑"）。
func persistContextUsage(st Persistence, id string, u ContextUsage) {
	if st == nil || id == "" {
		return
	}
	if err := st.SaveContextUsage(id, u); err != nil {
		log.Printf("上下文占用落库失败（继续运行）: %v", err)
	}
}

// runTurn 跑完整一轮：流式生成 → 工具调用 → 确认 → 执行 → 回填续轮。
// 文件改动（edit/write_file）被收集，轮末汇总发 FilesChangedEvent（产物视图）。
// ac 是本轮 Agent 载荷（nil = 旧语境——全局默认提示词与 default 模型）。
func (s *Session) runTurn(ctx context.Context, cfg sendConfig, ac *sessiondata.AgentContext) {
	// 快照工作目录并注入工具执行（busy 期间不可变——切换与归属入口
	// 都有 busy 守卫，这是单会话架构下的一致性来源）：项目会话的相对
	// 路径、bash 默认目录、确认门解析基准全部对齐项目根。
	s.mu.Lock()
	workDir := s.workDir
	s.dispatchRoot = ac // 主 Agent 载荷（dispatch 的委派名校验读它）
	s.mu.Unlock()
	ctx = tools.WithWorkDir(ctx, workDir)
	// 会话 id 也进 ctx：后台任务（bash 的 run_in_background）要记**归属会话**，
	// 唤醒投递按它找回会话。注册表是进程级单例，多会话并发时不能把归属
	// 挂在注册表上（与 workdir / todo sink 同一条理由）。
	ctx = tools.WithSessionID(ctx, s.SessionID())
	// 时间线归属也进 ctx：子会话起的后台任务要记在**父会话**名下（通告投给父
	// 会话才有人能行动），job 事件也按它路由到父会话的时间线。
	ctx = tools.WithOwnerSessionID(ctx, s.OwnerSessionID())

	// 本轮技能目录注入 read_skill（渐进披露的取数源）：模型按 id 取
	// 完整正文，只暴露白名单内的——没在 ac.Skills 里的它当没有。
	// 经 ctx 注入（不是注册表）：父会话与子会话各有自己的白名单。
	if ac != nil {
		entries := make([]tools.SkillEntry, 0, len(ac.Skills))
		for i := range ac.Skills {
			entries = append(entries, tools.SkillEntry{ID: ac.Skills[i].ID, Desc: ac.Skills[i].Desc, Body: ac.Skills[i].Body})
		}
		ctx = tools.WithSkillSource(ctx, func(context.Context) []tools.SkillEntry { return entries })
	}
	// 本轮 todo 清单的写回口（清单状态归会话，UI 的 TodoList 数据源）
	ctx = tools.WithTodoSink(ctx, func(items []tools.TodoItem) {
		s.mu.Lock()
		s.todos = items
		s.mu.Unlock()
		s.emit(TodoUpdatedEvent{Items: items})
	})
	// 本轮的提问通道（ask_user 工具的等待端）：向用户提问 = 一张 ask 形态的
	// 确认卡，走 awaitConfirm 的既有挂起槽位。经 ctx 注入（与 todo sink /
	// 技能目录同一条理由）：「能不能问、答案给谁」是会话级状态，注册表是
	// 进程级单例——父子会话各有各的提问归属。
	ctx = tools.WithAsker(ctx, s.askUser)

	var fileChanges []FileChange
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.cancel = nil
		s.pending = nil
		s.confirm = nil
		s.dispatchRoot = nil
		done := s.turnDone
		s.turnDone = nil
		s.mu.Unlock()
		s.emit(BusyEvent{Busy: false})
		if len(fileChanges) > 0 {
			s.emit(FilesChangedEvent{Files: fileChanges})
		}
		// 阻塞式跑一轮（SendWait）的收尾信号——放在最后：宿主等到它就知道
		// 本轮（含事件广播）已经结束。
		if done != nil {
			close(done)
		}
		// 收尾后若还有排队的自动通告：开新一轮（空闲 → 开一轮）。
		// 放在 close(done) **之后**：SendWait 等的是它那一轮，不该被新一轮拖住；
		// Send 是非阻塞的（忙检查 + 入历史 + 起 goroutine），不会嵌套轮次。
		s.flushNotices()
	}()

	overflowRetried := false
	// 死循环判据 = 同参数重复调用（见 repeat.go）。**刻意不设轮数上限**：
	// 轮数区分不了「卡住」与「任务本来就长」（2026-09-29 用户拍板去掉 maxToolRounds）。
	guard := &repeatGuard{}
	var repeatHint string
	for round := 0; ; round++ {
		// 轮边界是**唯一安全**的历史插入点：上一轮所有工具结果此时已全部
		// 落进历史，插一条 user 消息不会把 assistant 的 tool_calls 与它的
		// 结果拆开（配对不变量——拆开会 400，还会让压缩切点永久卡死）。
		s.injectNotices()
		// 重复调用提醒也在**轮边界**注入——与 injectNotices 同一个理由（唯一安全的
		// 历史插入点：上一轮工具结果已全部落进历史，见 notify.go 与 toolpair.go）。
		if repeatHint != "" {
			// Notice=true：这是**注入的**提醒，不是用户说的话——会话统计的轮数
			// 按它排除（否则每触发一次死循环提醒就凭空多一轮）
			m := llm.Message{Role: "user", Content: repeatHint, Notice: true}
			s.append(m)
			s.emit(UserMsgEvent{Message: m})
			repeatHint = ""
		}
		// 轮与轮之间是压缩的天然时机：上一轮的真实 prompt_tokens 已记录，
		// 超阈值就先压——否则下一轮请求可能直接撞窗口。
		if round > 0 {
			s.maybeCompact(ctx, ac, workDir)
		}
		// 历史快照（锁内取副本）：主轮写回 s.history（s.append）
		s.mu.Lock()
		histSnap := append([]llm.Message(nil), s.history...)
		s.mu.Unlock()
		res, err := s.streamRound(ctx, workDir, cfg.effort, ac, histSnap, "")
		if err != nil {
			// 端点报超长：强制压一次（保留预算归零）再重试同一轮——这是
			// 会话已经长到发不出去时的唯一出路。只重试一次（压不动就报错，
			// 不做无谓的循环）。
			if !overflowRetried && llm.IsContextOverflow(err) {
				overflowRetried = true
				if r, cerr := s.runCompaction(ctx, ac, workDir, 0); cerr == nil {
					log.Printf("端点报上下文超长：已压缩 %d 条历史（%d → %d tokens），重试本轮",
						r.Shadowed, r.Before, r.After)
					s.emit(CompactedEvent{Result: r})
					continue
				} else {
					log.Printf("端点报上下文超长，但压缩失败: %v", cerr)
				}
			}
			aborted := errors.Is(err, context.Canceled) || ctx.Err() != nil
			note := err.Error()
			if aborted {
				note = "已取消（保留已生成部分）"
			}
			// 中断也保留已生成的部分内容：入历史并随事件带给宿主
			var partial *llm.Message
			if res != nil {
				// 半截 JSON 的调用参数（流被打断、输出被截断）绝不能进历史——
				// 一条坏参数会让这个会话之后每次请求都发不出去，见 sanitizeToolCallArgs
				sanitizeToolCallArgs(res.Message.ToolCalls, "流中断保留的部分内容")
				s.append(res.Message)
				// 这条 assistant 消息可能已经声明了工具调用，而流在这里断了——
				// 那些调用永远不会有执行结果。补上配对结果，否则历史里留下没有
				// 回应的喊话：严格端点会 400，压缩的切点也会永久卡在它之前。
				if len(res.Message.ToolCalls) > 0 {
					skipNote := skippedCancelNote
					if !aborted {
						skipNote = skippedFailureNote
					}
					s.sinkSkippedToolResults(s.append, res.Message.ToolCalls, "", skipNote)
				}
				m := res.Message
				partial = &m
			}
			s.emit(TurnErrorEvent{Message: note, Aborted: aborted, Partial: partial})
			// 让模型知道是**用户**停的（而不是端点故障）：被动通告入队，下一轮
			// 真的开始时（用户说话 / 下一次派发）由轮边界注入——runTurn 收尾的
			// flushNotices 不会把它开成新一轮（那是主动通告的语义；中断注记
			// 自动开轮 = 用户刚点停止模型又自言自语一段）。主会话与子会话走
			// 同一条 runTurn 路径，一处覆盖两类；子会话经父取消（ctx 传播）也
			// 走到这里——runDispatch 只对「没走 aborted 收尾」的取消（确认门
			// 等待期被断）补队，不会重复。
			if aborted {
				_ = s.QueueNoticePassive(userAbortedNotice)
			}
			return
		}
		// 调用参数可能是半截 JSON（输出被 max_tokens 截断）：先修好再入历史，
		// 落库的参数与随后执行用的参数保持一致（tools 执行前修的是同一份实现）
		sanitizeToolCallArgs(res.Message.ToolCalls, "本轮工具调用")
		s.append(res.Message)
		s.emit(TurnDoneEvent{
			Message: res.Message, UsageTokens: res.UsageTokens, FinishReason: res.FinishReason,
			Context: s.ProjectedContextUsage(),
		})
		if len(res.Message.ToolCalls) == 0 {
			return
		}
		if !s.runTools(ctx, res.Message.ToolCalls, &fileChanges, ac, "", s.append) {
			return // 取消
		}
		// 这一轮的调用是不是在原地打转（同工具 + 同参数）？提醒留到**下一个轮边界**
		// 注入——此刻工具结果还没落进历史，插 user 消息会把 tool_calls 与结果拆开。
		hint, stop := guard.observe(res.Message.ToolCalls)
		if stop {
			s.emit(TurnErrorEvent{Message: hint})
			return
		}
		repeatHint = hint
	}
}

// streamRound 跑一轮流式生成，把增量事件转发给宿主，返回最终结果。
// workDir 是本轮快照的会话工作目录（系统提示词里的工作目录说明用它）。
// effort 是本轮请求的推理强度（模型须声明 reasoning 能力才转成 LLM 选项——
// 对不支持的端点传参会直接 400，能力门控是硬需求）。
// ac 是本轮 Agent 载荷（nil = 旧语境）：模型绑定优先 + 四层组合提示词
// + 工具白名单过滤。
// history 是这轮的消息序列（主轮 = s.history 快照；子轮 = 子上下文的
// 独立历史——隔离的核心）。dispatchID 非空 = 子 Agent 执行（事件带
// 归属标记，前端挂 dispatch 卡）。
func (s *Session) streamRound(ctx context.Context, workDir, effort string, ac *sessiondata.AgentContext, history []llm.Message, dispatchID string) (res *llm.ChatResult, err error) {
	m, err := s.modelFor(ac)
	if err != nil {
		return nil, err
	}
	var opts []llm.Option
	if m.MaxOutputTokens > 0 {
		opts = append(opts, llm.WithMaxTokens(m.MaxOutputTokens))
	}
	if effort != "" && m.Capabilities.Reasoning {
		opts = append(opts, llm.WithEffort(effort))
	}
	// 快照：快照后新消息（若有）不影响本轮请求。提示词按 Agent 四层
	// 组合（nil = 全局默认）；工具 wire 声明按白名单过滤。
	allow := agentToolsOf(ac)
	docs := s.projectDocsFor(workDir)
	wt, isChild := s.worktreeInfo()
	var prompt string
	if ac != nil {
		prompt = ComposeSystemPrompt(s.tools, workDir, ac, allow, wt, isChild, docs)
	} else {
		prompt = BuildSystemPrompt(s.tools, workDir, wt, isChild, docs)
	}
	msgs := append([]llm.Message{{Role: "system", Content: prompt}}, history...)

	wireTools := llmToolsFiltered(s.tools, allow)
	// 本轮请求的上下文占用（估算）：provider 回报了真实用量就以它为准，
	// 估算只用于分类拆分（anchoredTo 归一）与「端点不回报 usage」的回落。
	est := estimateContextUsage(prompt, wireTools, history)
	// 记录统一放出口（done / error / 断流多个 return 点）——**子轮也记**：
	// s 是这条会话自己的 Session，记的是它自己的 s.context（主会话是另一个对象，
	// 碰不到它）。不记的后果是子会话页的指示器永远空着（2026-09-30 用户报的）。
	defer func() {
		s.recordContextUsage(m.ContextWindow, est, res, history)
	}()

	opts = append([]llm.Option{llm.WithTools(wireTools)}, opts...)
	// 计时起点 = 请求**发出**前一刻（口径与"未知"的判定见 timing.go）。
	start := time.Now()
	var firstTokenMs int64
	var sawFirstToken bool
	// 计时统一放出口（done / error / 断流多个 return 点）：res 非空就盖章——
	// 流中断保留的部分内容同样带上已测到的数字（"这轮跑到一半断了，首字花了多久"
	// 也是有效观测）。res 为 nil（请求就没发出去）时什么都不写：那是"未知"。
	defer func() {
		if res == nil {
			return
		}
		// 用量簿记从结果盖到消息上（**唯一**落点：output 走 UsageTokens，输入侧
		// 三桶走 json:"-" 的字段）——provider 没回报就是 0（缺席），不估算。
		// 落库与会话统计的折叠都读消息上这一份。
		llm.StampUsage(&res.Message, res)
		stampRoundTiming(&res.Message, m.ID, start, firstTokenMs)
	}()
	ch, err := s.stream(ctx, m, msgs, opts)
	if err != nil {
		return nil, err
	}
	var lastErr error
	var liveContent, liveReasoning strings.Builder
	var liveTools []llm.ToolCall
	for ev := range ch {
		// 首 token = 第一个**文字/思考增量**到达的时刻。刻意不算 tool_call 事件：
		// 它在流里是聚合完成后才下发的（anthropic 要等 content_block_stop），拿它
		// 当"首字"会把延迟报得偏大；而工具轮本来就没有"首字"可言——那时整键
		// 缺席（0），不填 0 冒充"0ms 首字"。
		//
		// Replay（openai 带工具走的非流式回放）不记：那种增量与 done 同一瞬间
		// 到达，记下来就是"首字延迟 == 整轮耗时"的假数据。
		if !sawFirstToken && !ev.Replay && (ev.Type == llm.EventText || ev.Type == llm.EventReasoning) {
			sawFirstToken = true
			firstTokenMs = msSince(start)
		}
		switch ev.Type {
		case llm.EventText:
			liveContent.WriteString(ev.TextDelta)
			s.emit(DeltaEvent{Kind: "text", Text: ev.TextDelta, DispatchID: dispatchID})
		case llm.EventReasoning:
			liveReasoning.WriteString(ev.TextDelta)
			s.emit(DeltaEvent{Kind: "reasoning", Text: ev.TextDelta, DispatchID: dispatchID})
		case llm.EventToolCall:
			liveTools = append(liveTools, ev.ToolCall)
			tc := ev.ToolCall
			s.emit(ToolCallEvent{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments, DispatchID: dispatchID})
		case llm.EventDone:
			return ev.Result, nil
		case llm.EventError:
			if ev.Err != nil {
				lastErr = ev.Err
			} else {
				lastErr = errors.New("流式错误")
			}
			if ev.Result != nil {
				return ev.Result, lastErr
			}
			return nil, lastErr
		}
	}
	// 流结束无 done 事件：可能是取消（ctx 已断流）——把已生成的部分作为
	// partial 返回给 runTurn 的错误路径（保留部分内容的语义）。
	if liveContent.Len() > 0 || liveReasoning.Len() > 0 || len(liveTools) > 0 {
		partial := &llm.ChatResult{
			Message: llm.Message{
				Role: "assistant", Content: liveContent.String(),
				ReasoningContent: liveReasoning.String(), ToolCalls: liveTools,
			},
		}
		if lastErr == nil {
			lastErr = errors.New("流意外结束（无 done 事件）")
		}
		return partial, lastErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("流意外结束（无 done 事件）")
}
