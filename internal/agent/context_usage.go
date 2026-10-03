package agent

import (
	"encoding/json"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// 上下文占用的估算常量——DSH token-meter 同款固定密度启发式（不引 tokenizer：
// 真实值优先取 provider 回报的 prompt_tokens，估算只在拿不到用量时兜底，
// 以及给 UI 拆分四个分类）。
//
// 注意：按字节算对中文是低估的（UTF-8 一个汉字 3 字节 ≈ 0.75 token，真实
// tokenizer 约 1 token/字）。所以 Used 永远优先用真实用量，估算值只服务
// 「provider 不回报 usage」的回落与分类拆分——不要拿它做精确预算。
const (
	charsPerToken   = 4 // 固定文本密度
	blockOverhead   = 4 // 每个内容块的结构开销（JSON 框架/类型标签）
	messageOverhead = 4 // 每条消息的角色框架
)

// ContextUsage 是一次上下文测量：Used/Window 是压力判定与 UI 环形的依据
// （Used 优先取 provider 回报的真实 prompt_tokens），四个分类是估算拆分
// （已按 Used 归一，所以分类之和恒等于 Used）。Estimated 为真 = 这个数字是估算的
// （端点不回报用量，或库里没有真实测量而按历史回落）——UI 必须标注，不许当真实用量展示。
//
// 为什么是 sessiondata 里那份定义的**别名**而不是在这里另立一份：占用现在要**落库**
// （用户实测：重启前跑过的会话，打开时指示器是空的），而 store 必须能读写它、又不能
// import agent（分层规则，AGENTS.md §4）。别名让三处（agent 测量 / store 落库 /
// server 转 wire）共用同一份字段定义——各写一份必然漂移。
//
// 为什么分类要归一：真实总量与估算拆分来自两个来源，不归一的话 UI 上
// 环形（真实）与占比条（估算）会对不上——同一屏两个数字互相矛盾。
type ContextUsage = sessiondata.ContextUsage

// usageTotal 是五个分类之和。写成自由函数而不是方法：ContextUsage 是别名类型，
// Go 不允许给非本包定义的类型挂方法（别名不是新类型）。
func usageTotal(u ContextUsage) int {
	return u.System + u.Tools + u.ToolResults + u.Messages + u.Reasoning
}

// estimateText 按固定密度估算一段文本的 token 数（空串 = 0）。
func estimateText(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + charsPerToken - 1) / charsPerToken
}

// estimateToolsTokens 估算工具声明（wire 上是一段 JSON Schema 数组）的开销。
// 序列化失败按 0 计——估算值不值得让整轮失败。
func estimateToolsTokens(tools []llm.Tool) int {
	if len(tools) == 0 {
		return 0
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return 0
	}
	return blockOverhead + estimateText(string(b))
}

// estimateMessageTokens 估算单条消息的开销（压缩选区间按条累加保留预算用）。
func estimateMessageTokens(m llm.Message) int {
	t := messageOverhead + estimateText(m.Content) + estimateText(m.ReasoningContent)
	for _, tc := range m.ToolCalls {
		t += blockOverhead + estimateText(tc.Function.Name) + estimateText(tc.Function.Arguments)
	}
	return t
}

// estimateContextUsage 估算一次请求的上下文占用（分类拆分）。
// history 不含 system——system 由调用方按轮组装（prompt 参数）。
//
// 工具声明单列一类（Tools）：它与系统提示词是两样东西（一段是话，一段是 JSON Schema），
// 而用户想知道的正是"工具占了窗口多少"——DSH 的 ContextMeter 也是这么分的
// （system / tools / messages 三类）。混在一起就答不了这个问题。
//
// 结果**一律**带 Estimated=true：这是估算出来的数，不是 provider 回报的用量。真实用量
// 到了会走 anchoredUsage 把它清掉（那才是唯一能把 Estimated 变假的地方）。
func estimateContextUsage(prompt string, tools []llm.Tool, history []llm.Message) ContextUsage {
	u := ContextUsage{
		System:    blockOverhead + estimateText(prompt),
		Tools:     estimateToolsTokens(tools),
		Estimated: true,
	}
	for _, m := range history {
		t := estimateMessageTokens(m)
		switch m.Role {
		case "tool":
			u.ToolResults += t
		case "assistant":
			u.Messages += t
			u.Reasoning += estimateText(m.ReasoningContent)
		default: // user / 其它
			u.Messages += t
		}
	}
	u.Used = usageTotal(u)
	return u
}

// historyTokens 估算**历史部分**的 token 数（不含 system 与工具声明——那两样每轮现组装，
// 测量与展示两条路径都拿不到同一份，只有历史是两边都能算的）。
//
// 与 estimateContextUsage 的区别：那个是"一次请求的全量估算"（含 system/tools，用于分类
// 拆分与端点不回报 usage 时的回落），这个是"只算历史"的尺子——投影要把"测量之后历史长了
// 多少"减出来，两边必须用**同一把尺子**，否则量出来的差值里混着 system 的差。
func historyTokens(history []llm.Message) int {
	total := 0
	for _, m := range history {
		total += estimateMessageTokens(m)
	}
	return total
}

// projectedUsage 把"最后一次测量之后历史又长了多少"折进展示值——DSH 的
// projectedTokens = pressureTokens + surfaceTokens − sampledSurfaceTokens。
//
// 为什么需要它：占用只在**每次请求发出前**才有真实值（provider 回报的 prompt_tokens）。
// 请求之间历史照样在长——工具结果落进历史、后台任务通告被注入、助手回复自己也是一条——
// 那段时间里指示器显示的是**测量那一刻**的数字，工具跑得越久（bash 60s、派发子 Agent
// 几分钟）偏得越久。DSH 的做法是投影：压力值 + 表面积的变化量。
//
// 只做展示，**不进压缩压力判定**（maybeCompact 读的是 s.context 的真实值）：拿一个含估算
// 增量的数去判阈值，会让压缩在"其实还没到"的时候触发。DSH 同样把两者分开
// （pressureTokens 判定、projectedTokens 展示）。
//
// 三条不投影的情形（都是"算不出来"，不是"大约是 0"）：
//   - Used <= 0：本会话还没跑过主轮，没有压力基准；
//   - SampledTokens <= 0：那次测量没留下采样基线（老库里的值），算不出增量；
//   - 历史没变：增量本来就是 0，原样返回省一次重算（也保证"没变就不动"这条可断言）。
func projectedUsage(u ContextUsage, history []llm.Message) ContextUsage {
	if u.Used <= 0 || u.SampledTokens <= 0 {
		return u
	}
	now := historyTokens(history)
	if now == u.SampledTokens {
		return u
	}
	// 复用锚定算术：removedTokens 为负 = 历史变长（reanchoredUsage 只做减法，
	// 负数天然就是加法），分类按当前历史重新估算后归一——分类之和恒等于 Used
	// 这条不变式在投影之后同样成立。
	return reanchoredUsage(u, u.SampledTokens-now, history)
}

// reanchoredUsage 按锚定算术把"历史少了一段"这件事折算进占用测量：
// 新占用 = 旧真实占用 − 被移除段的估算（真实锚点保留，只调整差值），分类按新历史
// 重新估算后等比归一（分类之和恒等于 Used 这条不变式不能破）。旧占用没有真实锚点
// （Used == 0，本会话还没跑过主轮）时按剩余历史重新估算，窗口沿用旧值。
//
// 压缩与撤回共用这一份：两处各写一遍必然漂移，而漂移的表现是"指示器停在改动前的
// 数字"——压缩真链路实测踩过（AGENTS.md §2.2），撤回是同一个坑。
//
// Estimated 跟着**旧值**走：旧值本来就是估算的（端点不回报用量），减去一段估算之后
// 仍然是估算——anchoredUsage 会把标记清成 false，所以这里要还原回去。
func reanchoredUsage(prev ContextUsage, removedTokens int, history []llm.Message) ContextUsage {
	if prev.Used > 0 {
		used := prev.Used - removedTokens
		if used < 0 {
			used = 0
		}
		hist := estimateContextUsage("", nil, history)
		u := ContextUsage{
			System: prev.System, Tools: prev.Tools, ToolResults: hist.ToolResults,
			Messages: hist.Messages, Reasoning: hist.Reasoning,
		}
		u = anchoredUsage(u, used)
		u.Window = prev.Window
		u.Estimated = prev.Estimated
		// 锚点换到了**当前**这段历史上：采样基线跟着重置，否则下一次投影会把
		// 刚折进来的这段变化**再算一遍**（压缩后指示器虚高一截）。
		u.SampledTokens = historyTokens(history)
		return u
	}
	u := estimateContextUsage("", nil, history)
	u.Window = prev.Window
	u.SampledTokens = historyTokens(history)
	return u
}

// anchoredUsage 用真实总量（provider 回报的 prompt_tokens）替换 Used，并把分类
// 等比缩放到该总量——保证「分类之和 == Used」这一不变式在两种来源混合时也成立。
// used <= 0（provider 不回报用量）时原样返回：那个数字是估算的，Estimated 保持为真。
//
// Estimated 为什么在这里定：这是「真实用量」与「估算用量」两条来源的**唯一汇合点**。
// 放到别处判定就会出现同一份数字在一处标估算、在另一处标真实的分叉。
func anchoredUsage(u ContextUsage, used int) ContextUsage {
	if used <= 0 {
		return u
	}
	sum := usageTotal(u)
	u.Used = used
	u.Estimated = false // provider 回报了真实用量
	if sum == 0 {
		// 有真实总量但分类全零（空历史/纯声明）：全部记进 Messages，
		// 不为凑数编造分类
		u.Messages = used
		return u
	}
	if sum == used {
		return u
	}
	// 缩放用向下取整，余数由 Messages 吸收——分类之和恒等于 Used，
	// 且不会出现负值
	scale := func(v int) int { return v * used / sum }
	sys, tools, tool, reason := scale(u.System), scale(u.Tools), scale(u.ToolResults), scale(u.Reasoning)
	u.System, u.Tools, u.ToolResults, u.Reasoning = sys, tools, tool, reason
	u.Messages = used - sys - tools - tool - reason
	return u
}
