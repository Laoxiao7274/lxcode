package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// sessionSearchDefaultMax / sessionSearchMaxMax 与 search 工具对齐：
// 会话消息往往更长（工具输出、代码块），默认条数更保守。
const (
	sessionSearchDefaultMax = 30
	sessionSearchMaxMax     = 100
	// sessionSearchDefaultContext 是每条命中默认附带的相邻消息条数。
	// 默认不为 0：「上次怎么修的」这类问法里，命中行常常只是**提问**，
	// 答案在它后面几条——只给一行的话模型还得再搜一次才拼得出前因后果。
	sessionSearchDefaultContext = 2
	// sessionSearchMaxContext 是上下文的硬上限（与 store 侧一致）。
	sessionSearchMaxContext = 5
)

// validSearchRole 报告角色名是否合法。
// 非法值直接报错而不是静默当成「全部」——静默会让模型以为过滤生效了
// （它按过滤后的结果下结论，而其实看的是全部角色）。
func validSearchRole(role string) bool {
	switch role {
	case "user", "assistant", "tool":
		return true
	}
	return false
}

// sessionSearchDef：session_search，风险等级 低危（只读）。
//
// 为什么需要它：用户说"上次我们怎么修的""之前讨论过什么"时，模型没有跨会话
// 记忆——没有这个工具它只能让用户复述。搜索历史会话把"回忆"变成一次调用。
// （与 hermes 的 SESSION_SEARCH_GUIDANCE 同一动机："use session_search to
// recall it before asking them to repeat themselves"。）
//
// 实现经注入：JSONL 格式与解析归 backend 所有（AttachSessionStore 时接线），
// 这里只定义工具面（schema + 风险 + 调用约定），避免格式定义漂移出两份。
func sessionSearchDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "搜索模式（正则，如 端口|防火墙 或 failed to bind）"},
			"max": {"type": "integer", "description": "最多返回条数，默认 30，上限 100"},
			"context": {"type": "integer", "description": "每条命中前后各带几条相邻消息（默认 2，上限 5；0 = 只给命中本身）"},
			"role": {"type": "string", "enum": ["user", "assistant", "tool"], "description": "只搜某个角色（默认全部）"}
		},
		"required": ["pattern"]
	}`)
	return &Def{
		Name: "session_search",
		Description: "搜索历史会话的消息内容（含当前会话；按会话时间从近到远）。" +
			"用户提到\"之前/上次/我们讨论过/上次怎么修的\"时，先用它找回上下文，" +
			"不要让用户复述。每条命中带**会话标题与时间**（判断是哪个会话的事），" +
			"并默认附上前后各 2 条相邻消息（context 可调）——「怎么修的」通常就在" +
			"命中后面几条，带上下文才看得出前因后果。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Pattern string `json:"pattern"`
				Max     int    `json:"max"`
				Context *int   `json:"context"` // 指针：区分「没给」（取默认）与「显式给 0」
				Role    string `json:"role"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			if a.Pattern == "" {
				return "", fmt.Errorf("pattern 不能为空")
			}
			if a.Max <= 0 {
				a.Max = sessionSearchDefaultMax
			}
			if a.Max > sessionSearchMaxMax {
				a.Max = sessionSearchMaxMax
			}
			// context 用指针：显式 0 是「只要命中本身」，与「没给」不同。
			ctxN := sessionSearchDefaultContext
			if a.Context != nil {
				ctxN = *a.Context
			}
			if ctxN < 0 {
				ctxN = 0
			}
			if ctxN > sessionSearchMaxContext {
				ctxN = sessionSearchMaxContext
			}
			if a.Role != "" && !validSearchRole(a.Role) {
				return "", fmt.Errorf("role 只能是 user/assistant/tool 之一: %q", a.Role)
			}
			fn := r.getSessionSearch()
			if fn == nil {
				return "", fmt.Errorf("会话存储未启用（后端未挂载会话目录），无法搜索历史")
			}
			return fn(ctx, sessiondata.SearchQuery{
				Pattern: a.Pattern, Max: a.Max, Role: a.Role, Context: ctxN,
			})
		},
	}
}
