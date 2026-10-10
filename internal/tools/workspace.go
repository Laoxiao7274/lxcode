// workspace_status / workspace_sync / workspace_rollback 三个工具：项目工作区的
// 查询（只读）、提交推送链路、会话分支回滚。全部是主 Agent 的工具（子 Agent
// 白名单不含它们——子会话不做工作区操作）。
//
// 实现经注入（WorkspaceOps）：注册表只持回调，不 import server——与 merge_request
// 的 SetMergeStarter 同款模式（tools 是叶子包，server 装配时 SetWorkspaceOps）。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 工具名。**必须匹配 `^[a-zA-Z0-9_-]{1,64}$`**（与 merge_request 同款约束：
// 严格的网关会对带点号的工具名 400 拒收整轮）。
const (
	WorkspaceStatusToolName   = "workspace_status"
	WorkspaceSyncToolName     = "workspace_sync"
	WorkspaceRollbackToolName = "workspace_rollback"
	WorkspacePublishToolName  = "workspace_publish"
)

// WorkspaceOps 是三个工具的 server 侧实现（装配期注入一次，运行期只读）。
// 全部以「当前会话 id」为第一参数——会话归属项目由 server 侧解析。
type WorkspaceOps struct {
	// Status 汇报项目工作区状态（只读）：主检出 + 各会话分支 + 集成分支。
	Status func(sessionID, projectID string) (string, error)
	// Sync 执行「提交 → 起合并进程（→ 合并成功后 push）」。
	Sync func(sessionID, message string, push bool) (string, error)
	// SyncConfirm 组 sync 的确认门文案（说清将执行哪些动作）。
	SyncConfirm func(sessionID string, push bool) string
	// Rollback 回滚本会话分支到 target（last-turn / session-start / commit hash）。
	Rollback func(sessionID, target string) (string, error)
	// RollbackConfirm 组 rollback 的确认门文案（丢弃哪些提交、涉及哪些文件）。
	RollbackConfirm func(sessionID, target string) string
	// Publish 把会话工作树内的 source（相对路径，文件或目录）复制到主检出
	// 的 target（相对路径；空串 = 与 source 相同）；exclude 是目录同步的
	// 排除目录名。返回人话摘要（发布了多少文件、到了哪里）。
	Publish func(sessionID, source, target string, exclude []string) (string, error)
	// PublishConfirm 组 publish 的确认门文案（说清将覆盖主检出哪些文件）。
	PublishConfirm func(sessionID, source, target string, exclude []string) string
}

// SetWorkspaceOps 注入三个工具的 server 侧实现（server 装配时调用；
// 与 SetMergeStarter 同款模式）。与 mergeStart 共用 searchMu：同一批
// 「装配期写一次、运行期只读」的注入点。
func (r *Registry) SetWorkspaceOps(ops WorkspaceOps) {
	r.searchMu.Lock()
	r.workspace = ops
	r.searchMu.Unlock()
}

func (r *Registry) getWorkspaceOps() WorkspaceOps {
	r.searchMu.Lock()
	defer r.searchMu.Unlock()
	return r.workspace
}

// workspaceStatusDef：查询项目工作区状态（低危、只读、不确认）。
func workspaceStatusDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"project_id": {"type": "string", "description": "项目 id（可选；默认当前会话归属的项目）"}
		}
	}`)
	return &Def{
		Name: WorkspaceStatusToolName,
		Description: "查询当前项目工作区的状态：主检出的未提交改动、各会话分支的提交情况" +
			"（领先多少、有没有工作树、是否已并入主检出）、集成分支的领先/落后。" +
			"用户问「本地改了哪些东西」「现在什么状态」时用它。只读，不动任何文件。",
		Parameters: schema,
		Risk:       RiskLow,
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				ProjectID string `json:"project_id"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			ops := r.getWorkspaceOps()
			if ops.Status == nil {
				return "", fmt.Errorf("工作区查询未装配（后端未初始化会话存储）")
			}
			return ops.Status(SessionID(ctx), strings.TrimSpace(a.ProjectID))
		},
	}
}

// workspaceSyncDef：提交 → 合并 →（可选）push（中危——每次执行前走确认门）。
func workspaceSyncDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"message": {"type": "string", "description": "提交信息（可选；默认取最近一条用户消息的首行）"},
			"push": {"type": "boolean", "description": "合并成功后是否推送到远程 origin（默认 false）"}
		}
	}`)
	return &Def{
		Name: WorkspaceSyncToolName,
		Description: "提交本会话工作区的改动并起合并进程（提交 → 合并到集成分支；push=true 时" +
			"合并成功后推送到远程 origin）。用户说「提交」「推送」时用它。立刻返回——合并是" +
			"后台任务，结束后会自动通知你。同一会话同时只允许一个在跑的合并进程。",
		Parameters: schema,
		Risk:       RiskHigh,
		Mutates:    true,
		Confirm: func(ctx context.Context, args json.RawMessage) string {
			var a struct {
				Push bool `json:"push"`
			}
			_ = json.Unmarshal(args, &a)
			if ops := r.getWorkspaceOps(); ops.SyncConfirm != nil {
				if txt := ops.SyncConfirm(SessionID(ctx), a.Push); strings.TrimSpace(txt) != "" {
					return txt
				}
			}
			// 未装配/未给出文案时的兜底：确认门照常生效，动作说清楚。
			if a.Push {
				return "将执行：提交本会话工作区的改动 → 起合并进程合并到集成分支 → 合并成功后推送到远程 origin。确认执行？"
			}
			return "将执行：提交本会话工作区的改动 → 起合并进程合并到集成分支。确认执行？"
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Message string `json:"message"`
				Push    bool   `json:"push"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			ops := r.getWorkspaceOps()
			if ops.Sync == nil {
				return "", fmt.Errorf("工作区同步未装配（后端未初始化会话存储）")
			}
			return ops.Sync(SessionID(ctx), a.Message, a.Push)
		},
	}
}

// workspaceRollbackDef：回滚本会话分支（中危——每次执行前走确认门）。
func workspaceRollbackDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {"type": "string", "description": "回滚目标：\"last-turn\"（默认，上一轮结束的状态）/ \"session-start\"（会话起点）/ 原始 commit hash（须在本会话分支上）"}
		}
	}`)
	return &Def{
		Name: WorkspaceRollbackToolName,
		Description: "把本会话分支回滚到目标状态（默认上一轮结束时的状态）。工作区有未提交改动时" +
			"会拒绝（绝不丢弃手工改动）；目标提交已合并进集成分支时会警告——那部分要在集成分支上" +
			" revert 才能撤销。绝不 push、绝不动集成分支与主检出。",
		Parameters: schema,
		Risk:       RiskHigh,
		Mutates:    true,
		Confirm: func(ctx context.Context, args json.RawMessage) string {
			var a struct {
				Target string `json:"target"`
			}
			_ = json.Unmarshal(args, &a)
			if ops := r.getWorkspaceOps(); ops.RollbackConfirm != nil {
				if txt := ops.RollbackConfirm(SessionID(ctx), a.Target); strings.TrimSpace(txt) != "" {
					return txt
				}
			}
			// 兜底：确认门照常生效，具体丢弃内容在执行时报给模型。
			return "将把本会话分支回滚到目标状态（丢弃其后的检查点提交，不可逆）。确认执行？"
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Target string `json:"target"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			ops := r.getWorkspaceOps()
			if ops.Rollback == nil {
				return "", fmt.Errorf("工作区回滚未装配（后端未初始化会话存储）")
			}
			return ops.Rollback(SessionID(ctx), a.Target)
		},
	}
}

// workspacePublishDef：把会话工作树里的产物复制到项目主检出（走确认门——
// 会话 worktree 里的构建产物在 gitignore 里、不随合并走，用户的项目文件夹
// 里看不到它们；这个工具把产物显式送到主检出对应位置）。
func workspacePublishDef(r *Registry) *Def {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"source": {"type": "string", "description": "会话工作树内的相对路径（文件或目录；绝对路径拒绝）"},
			"target": {"type": "string", "description": "主检出内的相对路径（可选；默认与 source 相同）"},
			"exclude": {"type": "array", "items": {"type": "string"}, "description": "目录同步时排除的目录名（如 [\"node_modules\"]；.git 始终自动排除）"}
		},
		"required": ["source"]
	}`)
	return &Def{
		Name: WorkspacePublishToolName,
		Description: "把本会话工作树里的产物（构建产物、生成的文件或目录）复制到项目主检出对应位置，" +
			"让产物出现在你的项目文件夹里。中危——覆盖主检出文件前会请求确认。" +
			"source 必须是工作树内的相对路径（绝对路径拒绝）；target 缺省与 source 相同；" +
			"目录递归复制、保留相对结构，目标父目录不存在会自动创建，同名文件覆盖。",
		Parameters: schema,
		Risk:       RiskHigh,
		Mutates:    true,
		Confirm: func(ctx context.Context, args json.RawMessage) string {
			var a struct {
				Source  string   `json:"source"`
				Target  string   `json:"target"`
				Exclude []string `json:"exclude"`
			}
			_ = json.Unmarshal(args, &a)
			if ops := r.getWorkspaceOps(); ops.PublishConfirm != nil {
				if txt := ops.PublishConfirm(SessionID(ctx), a.Source, a.Target, a.Exclude); strings.TrimSpace(txt) != "" {
					return txt
				}
			}
			// 未装配/未给出文案时的兜底：确认门照常生效，动作说清楚。
			return fmt.Sprintf("将把会话工作树的 %s 复制到项目主检出的对应位置（同名文件将覆盖）。确认执行？",
				strings.TrimSpace(a.Source))
		},
		Exec: func(ctx context.Context, args json.RawMessage) (string, error) {
			var a struct {
				Source  string   `json:"source"`
				Target  string   `json:"target"`
				Exclude []string `json:"exclude"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}
			ops := r.getWorkspaceOps()
			if ops.Publish == nil {
				return "", fmt.Errorf("产物发布未装配（后端未初始化会话存储）")
			}
			return ops.Publish(SessionID(ctx), a.Source, a.Target, a.Exclude)
		},
	}
}
