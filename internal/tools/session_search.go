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
// 从 2 收紧到 1（2026-12 扫描成本优化）：上下文条数不是扫描成本大头，
// 但少一行输出对长历史更友好；精确回忆时模型可显式调高。
sessionSearchDefaultContext = 1
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

// searchScopeKey 是「本次搜索结果的范围说明」的 ctx 键。
//
// 为什么走 ctx 而不是改 SessionSearchFn 签名：范围说明由**工具层**决定
// （它知道 all_projects/workspace 参数与注入值谁生效），而渲染 FormatSearchHits
// 在接线侧（agent/server）——把说明随 ctx 传过去，接线侧只管取出来放进
// 格式化参数，签名不用动、两个接线点（agent / server）零分叉。
type searchScopeKey struct{}

// WithSearchScope 把范围说明放进 ctx（session_search 工具在调用实现前设置）。
func WithSearchScope(ctx context.Context, note string) context.Context {
	if note == "" {
		return ctx
	}
	return context.WithValue(ctx, searchScopeKey{}, note)
}

// SearchScope 取 ctx 里的范围说明；空串 = 全库扫描（不注明）。
func SearchScope(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	note, _ := ctx.Value(searchScopeKey{}).(string)
	return note
}

// sessionSearchDef：session_search，风险等级 低危（只读）。
//
// 为什么需要它：用户说"上次我们怎么修的""之前讨论过什么"时，模型没有跨会话
// 记忆——没有这个工具它只能让用户复述。搜索历史会话把"回忆"变成一次调用。
// （与 hermes 的 SESSION_SEARCH_GUIDANCE 同一动机："use session_search to
// recall it before asking them to repeat themselves"。）
//
// 默认扫描范围是**当前项目**（会话归属的项目 id 经 ctx 注入，见
// sessionstate.go 的 WithWorkspace）：项目会话（含其子会话——CreateChild
// 拷贝了父的 workspace）限定在项目内，扫描量与结果噪音都小一个量级；
// 未分组会话没有「当前项目」（注入值为空串），此时退回**全库扫描**——
// 语义上未分组会话本就不属于任何项目，「限定当前项目」无从谈起。
// all_projects=true 显式放开到全库。
//
// 实现经注入：JSONL 格式与解析归 backend 所有（AttachSessionStore 时接线），
// 这里只定义工具面（schema + 风险 + 调用约定），避免格式定义漂移出两份。
func sessionSearchDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "搜索模式（正则，如 端口|防火墙 或 failed to bind）"},
			"max": {"type": "integer", "description": "最多返回条数，默认 30，上限 100"},
			"context": {"type": "integer", "description": "每条命中前后各带几条相邻消息（默认 1，上限 5；0 = 只给命中本身）"},
			"role": {"type": "string", "enum": ["user", "assistant", "tool"], "description": "只搜某个角色（默认全部）"},
			"session": {"type": "string", "description": "只搜指定会话（session_id）。默认搜全部未归档会话；历史库大时全库扫描可能超时截断，知道目标会话时建议指定"},
			"days": {"type": "integer", "description": "只搜最近 N 天（按会话更新时间）。默认不限；限定时间范围可缩小扫描量、避免超时截断"},
			"all_projects": {"type": "boolean", "description": "true = 搜索全部项目的历史（全库）。默认 false = 只搜当前项目——未分组会话没有当前项目，此时退回全库"},
			"workspace": {"type": "string", "description": "显式指定要搜的项目 id（优先级高于默认的当前项目，也高于 all_projects）。跨项目找东西但不想全库时用"}
		},
		"required": ["pattern"]
	}`)
	return &Def{
		Name: "session_search",
		Description: "搜索历史会话的消息内容（含当前会话；按会话时间从近到远）。" +
			"默认只搜**当前项目**的会话（含其子会话）；未分组会话没有当前项目，" +
			"此时退回搜全部；all_projects=true 可搜全部项目。" +
			"用户提到\"之前/上次/我们讨论过/上次怎么修的\"时，先用它找回上下文，" +
			"不要让用户复述。每条命中带**会话标题与时间**（判断是哪个会话的事），" +
			"并默认附上前后各 1 条相邻消息（context 可调）——「怎么修的」通常就在" +
			"命中后面几条，带上下文才看得出前因后果。" +
			"全库扫描有超时护栏（超时返回部分结果并注明\"可能不全\"）；" +
			"缩小范围的办法：指定 session（精确会话 id）或 days（最近 N 天）。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Pattern     string `json:"pattern"`
				Max         int    `json:"max"`
				Context     *int   `json:"context"` // 指针：区分「没给」（取默认）与「显式给 0」
				Role        string `json:"role"`
				Session     string `json:"session"`
				Days        int    `json:"days"`
				AllProjects bool   `json:"all_projects"`
				Workspace   string `json:"workspace"`
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
			// 扫描范围解析（优先级：显式 workspace > all_projects > 注入的当前项目）：
			//   - 显式 workspace 非空 → 按它（模型点名了项目，尊重点名）；
			//   - 否则 all_projects=true → 空（全库）；
			//   - 否则取注入值：非空 = 限定当前项目；**空 = 退回全库**——
			//     未分组会话没有当前项目（语义上不属于任何项目），且「空」
			//     在 SearchQuery 里就是「不限」的既有语义，两个空恰好同向。
			workspace := a.Workspace
			scope := ""
			if workspace != "" {
				scope = fmt.Sprintf("已限定项目 %s（workspace 参数显式指定）", workspace)
			} else if !a.AllProjects {
				workspace = Workspace(ctx)
				if workspace != "" {
					scope = fmt.Sprintf("已限定当前项目 %s（含其子会话）；需要跨项目时加 all_projects 参数查全部", workspace)
				}
				// 注入值为空（未分组会话/未注入）：不加过滤，也不注明——
				// 退回全库是此场景的正确行为，见函数注释。
			}
			fn := r.getSessionSearch()
			if fn == nil {
				return "", fmt.Errorf("会话存储未启用（后端未挂载会话目录），无法搜索历史")
			}
			return fn(WithSearchScope(ctx, scope), sessiondata.SearchQuery{
				Pattern: a.Pattern, Max: a.Max, Role: a.Role, Context: ctxN,
				Session: a.Session, Days: a.Days, Workspace: workspace,
			})
		},
	}
}
