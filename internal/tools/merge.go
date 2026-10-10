// merge_request 工具：起一个**后台合并进程**（jobs 的第二个 producer，Kind="merge"）。
//
// 任务体是内置「合并 Agent」的独立子会话（在集成分支的专用工作树里把本会话分支的
// 改动汇总进去）。工具本身只发起进程、立刻返回——合并结束经**现有唤醒投递**自动通告
// 回主会话（docs/jobs.md §5），这里不写任何通知逻辑。
//
// 实现经注入：注册表只持一个回调（server 装配时 SetMergeStarter），不 import server
// ——与 SetJobs / SetWebSearch / SetSessionSearch 同款模式（tools 是叶子包）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MergeRequestToolName 是合并进程入口工具的 id。
//
// **必须匹配 `^[a-zA-Z0-9_-]{1,64}$`**（与 DispatchToolName 同款约束：严格的
// 网关会对带点号的工具名 400 拒收整轮）。
const MergeRequestToolName = "merge_request"

// MergeStartFn 是「起一个合并进程」的实现约定（server 装配时注入 startMergeJob）。
// 返回任务 id；targetBranch 为空 = 用默认目标分支；pushAfter = 合并成功后把目标
// 分支推到远程 origin（merge_request 工具恒传 false——push 是 workspace_sync 的
// 职责）。实现必须立刻返回（后台任务不阻塞当前轮），并在后台 goroutine 里跑合并
// Agent 子会话。
type MergeStartFn func(sessionID, targetBranch string, pushAfter bool) (string, error)

// SetMergeStarter 注入合并进程的启动实现（server 装配时调用；与 SetJobs 同款模式）。
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

// mergeRequestDef：起一个合并进程（低危——工具本身只发起后台任务，真正的合并由
// 内置合并 Agent 在集成分支工作树里执行，它有各自的权限档与确认面）。
func mergeRequestDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"target_branch": {"type": "string", "description": "目标分支（可选；默认 lxcode/integration）"}
		}
	}`)
	return &Def{
		Name: MergeRequestToolName,
		Description: "起一个合并进程：把本会话分支的改动交给内置的合并 Agent，在集成分支的专用工作树里" +
			"汇总（处理冲突、跑构建测试）。立刻返回任务 id——合并是后台任务，结束后会自动通告你，" +
			"可用 job_output 读输出。同一会话同时只允许一个在跑的合并进程。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				TargetBranch string `json:"target_branch"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			start := r.getMergeStarter()
			if start == nil {
				return "", fmt.Errorf("合并进程未装配（后端未初始化合并 producer）")
			}
			sessionID := SessionID(ctx)
			if sessionID == "" {
				return "", fmt.Errorf("当前会话没有 id，无法起合并进程")
			}
			id, err := start(sessionID, strings.TrimSpace(a.TargetBranch), false)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("合并进程已启动（任务 %s），可在后台任务里查看；结束后会自动通知你。", id), nil
		},
	}
}
