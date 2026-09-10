package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// sessionSearchDefaultMax / sessionSearchMaxMax 与 search 工具对齐：
// 会话消息往往更长（工具输出、代码块），默认条数更保守。
const (
	sessionSearchDefaultMax = 30
	sessionSearchMaxMax     = 100
)

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
			"max": {"type": "integer", "description": "最多返回条数，默认 30，上限 100"}
		},
		"required": ["pattern"]
	}`)
	return &Def{
		Name: "session_search",
		Description: "搜索历史会话的消息内容（含当前会话；按会话时间从近到远）。" +
			"用户提到\"之前/上次/我们讨论过/上次怎么修的\"时，先用它找回上下文，" +
			"不要让用户复述。命中按 `[会话id #序号 角色] 内容` 格式返回。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Pattern string `json:"pattern"`
				Max     int    `json:"max"`
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
			fn := r.getSessionSearch()
			if fn == nil {
				return "", fmt.Errorf("会话存储未启用（后端未挂载会话目录），无法搜索历史")
			}
			return fn(ctx, a.Pattern, a.Max)
		},
	}
}
