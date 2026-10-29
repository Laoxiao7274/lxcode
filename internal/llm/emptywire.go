// 空消息清洗：两条 wire 路径（openai / anthropic）的空值处理与配对兜底。
//
// 根因（2026-12 实锤）：模型只调工具不写字时，assistant 消息 Content == ""
// 入历史，openai 路径原样发 `content: ""`；LiteLLM 代理（modify_params=True）
// 对空 content 消息注入占位串 `[System: Empty message content sanitised to
// satisfy protocol]` 并随响应回显/持久化——占位串就这样混进上下文。次源是
// 空 tool 结果同样原样发空串。治本 = 空值不上 wire，让代理无从触发清洗。
package llm

import "log"

// wireEmptyToolResult 是空工具结果的哨兵正文：单个空格。
//
// 为什么是空格而不是空串：空 tool 结果（模型只调工具没产出输出）以
// `content: ""` 发出时同样触发 LiteLLM 的清洗占位（占位串混进上下文），
// 单空格避开清洗又保持“无信息”语义；为什么不是 null：OpenAI 官方 schema
//（openai-python 由官方 spec 生成的 ChatCompletionToolMessageParam）里
// tool 消息的 content 是 Required[str]——null 类型不合法，单空格是类型合法
// 且不会被清洗的最小改动。endpoints 端接受带前导空格的 tool content。
const wireEmptyToolResult = " "

// backfillPlaceholder 是配对兜底合成结果的自解释文案（两条 wire 路径共用，
// 避免各自漂移——anthropic 路径原先的独立文案并入这里）。
const backfillPlaceholder = "（该调用的结果未记录：历史残缺）"

// missingIDs 计算 assistant 消息里**没拿到结果**的调用 id：got 是（按 wire
// 视角）已出现的结果 id 集合。两条 wire 路径的配对兜底共用这一份判定，
// 判定口径只有一份，避免两份实现漂移。
func missingIDs(calls []ToolCall, got map[string]bool) []string {
	var want []string
	for _, tc := range calls {
		if tc.ID != "" && !got[tc.ID] {
			want = append(want, tc.ID)
		}
	}
	return want
}

// backfillOpenAIToolResults 给 openai wire 消息里的“声明了 tool_calls 但没拿到
// 结果”的 assistant 消息补合成 tool 消息。
//
// 为什么要有：严格 OpenAI 兼容端点要求 assistant.tool_calls 的每个 id 在后续
// tool 消息里有对应结果，缺一条就 400 拒收整轮（anthropic 侧同款约束与兜底，
// 见 convertToAnthropic 的 backfillToolResults）。我们的写边界已保证新建历史
// 不缺配对（agent 的 sinkSkippedToolResults），但进程在两次 append 之间被杀等
// 中断、历史遗留数据仍可能留下缺口；读侧兜底与 anthropic 侧同一原则：读侧
// 兜底比让会话变砖好。
//
// 与 anthropic 侧的差异：openai 的 tool 消息是顶层独立消息，所以缺结果时在
// 连续 tool 结果窗口的末尾**插入**合成 tool 消息（既有消息的相对顺序不变）。
// 合成结果正文 = backfillPlaceholder（历史残缺是信息性事件，正文自解释；
// 与 wireEmptyToolResult 的分工：后者只用于“调用真的执行了、只是没输出”的
// 空结果清洗，两者语义不同，不共用正文）。补齐时记日志，不做静默篡改。
//
// 触发条件与 anthropic 侧同一条纪律：只修“窗口里已有部分结果、缺了其中几条”
// 的缺口；assistant 之后**一条结果都没有**（下一条不是 tool 消息或什么都没有）
// = 历史顺序乱了——凭空插一条消息改变语义比让端点报错更糟，不猜（对称于
// backfillToolResults 的 `next.Role != "user" || len(got) == 0 → continue`）。
func backfillOpenAIToolResults(msgs []openaiWireMessage) []openaiWireMessage {
	out := make([]openaiWireMessage, 0, len(msgs)+2)
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		out = append(out, m)
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		// 搬运 assistant 之后连续的 tool 结果窗口，同时收集已出现的结果 id
		got := map[string]bool{}
		j := i + 1
		for j < len(msgs) && msgs[j].Role == "tool" {
			got[msgs[j].ToolCallID] = true
			out = append(out, msgs[j])
			j++
		}
		// 窗口里一条结果都没有：不凭空插消息（见上），跳过整个窗口
		if len(got) == 0 {
			i = j - 1
			continue
		}
		for _, id := range missingIDs(m.ToolCalls, got) {
			log.Printf("历史里工具调用 %s 没有结果记录，已补合成结果后发送（历史残缺）", id)
			out = append(out, openaiWireMessage{
				Role:       "tool",
				Content:    backfillPlaceholder,
				ToolCallID: id,
			})
		}
		i = j - 1 // 窗口已搬运完毕，外层循环从窗口之后继续
	}
	return out
}
