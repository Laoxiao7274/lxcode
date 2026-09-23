package agent

import (
	"encoding/json"

	"github.com/moyunteng/lxcode/internal/llm"
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
// （已按 Used 归一，所以分类之和恒等于 Used）。
//
// 为什么分类要归一：真实总量与估算拆分来自两个来源，不归一的话 UI 上
// 环形（真实）与占比条（估算）会对不上——同一屏两个数字互相矛盾。
type ContextUsage struct {
	Used        int // 已用 token（真实用量优先）
	Window      int // 模型上下文窗口（models.json 的 context_window；0 = 未知）
	System      int // 系统提示词 + 工具声明
	ToolResults int // 工具结果
	Messages    int // 用户/助手正文与工具调用声明
	Reasoning   int // 思考链
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
func estimateContextUsage(prompt string, tools []llm.Tool, history []llm.Message) ContextUsage {
	u := ContextUsage{
		System: blockOverhead + estimateText(prompt) + estimateToolsTokens(tools),
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
	u.Used = u.total()
	return u
}

// total 是四个分类之和。
func (u ContextUsage) total() int {
	return u.System + u.ToolResults + u.Messages + u.Reasoning
}

// anchoredTo 用真实总量（provider 回报的 prompt_tokens）替换 Used，并把分类
// 等比缩放到该总量——保证「分类之和 == Used」这一不变式在两种来源混合时也成立。
// used <= 0（provider 不回报用量）时原样返回（Used 保持估算值）。
func (u ContextUsage) anchoredTo(used int) ContextUsage {
	if used <= 0 {
		return u
	}
	sum := u.total()
	u.Used = used
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
	sys, tool, reason := scale(u.System), scale(u.ToolResults), scale(u.Reasoning)
	u.System, u.ToolResults, u.Reasoning = sys, tool, reason
	u.Messages = used - sys - tool - reason
	return u
}
