// merge_request 工具（**遗留**）：2026-10 架构修订把合并进程退役——会话直接工作
// 在项目主检出与用户当前分支，没有「会话分支 → 集成分支」的合并流程可发起。工具
// 本体保留在注册表里（wire 不变），被调用时如实返回说明，**不启动任何任务**。
//
// 后端的合并实现（mergejob.go 的 startMergeJob / scanProjectBranches / merger Agent）
// 全部保留在库里，只是不再被主链路调用——将来若恢复隔离模式可直接复用；server 装
// 配时仍注入 startMergeJob（SetMergeStarter），装配保持完整。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// MergeRequestToolName 是合并进程入口工具的 id（遗留：保留以免协议/名单漂移）。
//
// **必须匹配 `^[a-zA-Z0-9_-]{1,64}$`**（与 DispatchToolName 同款约束：严格的
// 网关会对带点号的工具名 400 拒收整轮）。
const MergeRequestToolName = "merge_request"

// MergeStartFn 是「起一个合并进程」的实现约定（server 装配时注入 startMergeJob）。
// **遗留**：当前版本没有任何调用方——合并流程已退役，保留装配便于将来恢复隔离模式。
// 返回任务 id；targetBranch 为空 = 用默认目标分支；pushAfter = 合并成功后把目标
// 分支推到远程 origin。实现必须立刻返回（后台任务不阻塞当前轮）。
type MergeStartFn func(sessionID, targetBranch string, pushAfter bool) (string, error)

// SetMergeStarter 注入合并进程的启动实现（server 装配时调用；遗留——注入保留，
// 工具不再调用）。
func (r *Registry) SetMergeStarter(fn MergeStartFn) {
	r.searchMu.Lock()
	r.mergeStart = fn
	r.searchMu.Unlock()
}

func (r *Registry) getMergeStarter() MergeStartFn {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.mergeStart
}

// mergeRequestLegacyMessage 是合并流程退役后的一致答复（工具与 chat.mergeRequest
// 协议方法共用同一条口径——模型与用户看到的说法不能分叉）。
const mergeRequestLegacyMessage = "合并流程已退役：当前版本会话直接工作在项目主检出与用户当前分支，" +
	"没有「会话分支合并到集成分支」的流程可发起。每轮对话结束时系统会自动把改动提交为检查点提交；" +
	"如需提交或推送，用 workspace_sync；如需回滚，用 workspace_rollback。"

// mergeRequestDef：合并流程的遗留入口（低危——只回一条说明，不动任何状态）。
func mergeRequestDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"target_branch": {"type": "string", "description": "（遗留参数，已无效果）"}
		}
	}`)
	return &Def{
		Name: MergeRequestToolName,
		Description: "（遗留）合并流程已退役：会话直接工作在项目主检出，没有分支合并流程。" +
			"调用只会得到说明；提交/推送用 workspace_sync，回滚用 workspace_rollback。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				TargetBranch string `json:"target_branch"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			// 遗留：不启动任何任务，如实说明。starter 装配保留（getMergeStarter），
			// 但当前版本没有可合并的分支语义，绝不调用。
			_ = r.getMergeStarter()
			return mergeRequestLegacyMessage, nil
		},
	}
}

// MergeRequestLegacyMessage 供 server 侧（chat.mergeRequest 协议方法）复用同一口径。
func MergeRequestLegacyMessage() string { return mergeRequestLegacyMessage }
