// approval.go 权限模式：随请求携带的工具执行策略（与 WithWorkDir 同为
// ctx 注入模式——策略属于"这一轮怎么执行"，不属于会话长期状态）。
//
// 三档语义（AGENTS.md §3 风险分级的运行时开关）：
//   - auto     高危自动执行（完全访问——仅隔离环境用）
//   - confirm  低危自动 + 高危确认（默认 = 现行语义）
//   - strict   只读：变更类工具（Def.Mutates）直接拒绝，错误回填模型
//
// 空值与 confirm 同义（CLI 不带参数 = 默认行为，兼容不动）。
package tools

import "context"

// Approval 是权限模式的类型。
type Approval string

const (
	ApprovalAuto    Approval = "auto"
	ApprovalConfirm Approval = "confirm"
	ApprovalStrict  Approval = "strict"
)

type approvalKey struct{}

// WithApproval 把权限模式注入 ctx（一轮工具循环内不变）。
func WithApproval(ctx context.Context, a Approval) context.Context {
	return context.WithValue(ctx, approvalKey{}, a)
}

// ApprovalFrom 读取权限模式；空值/缺省归一为 confirm（默认语义单点定义）。
func ApprovalFrom(ctx context.Context) Approval {
	if a, ok := ctx.Value(approvalKey{}).(Approval); ok {
		return a
	}
	return ApprovalConfirm
}
