package protocol

// ---- Git 管理页（git.overview / git.diff）----

// GitOverviewParams 是 git.overview 的参数。ProjectID 空 = 当前会话归属的项目
// （未分组会话报「当前会话没有归属项目」）。
type GitOverviewParams struct {
	ProjectID string `json:"project_id,omitempty"`
}

// GitChange 是主检出的一条未提交/未跟踪变更。
type GitChange struct {
	Path string `json:"path"`
	// Kind 是变更类别：modified | added | deleted | untracked。
	Kind string `json:"kind"`
}

// GitBranchInfo 是一条分支：主检出当前分支（Current=true）或某会话分支。
// 会话分支带会话标题 / 工作树状态；Ahead/Behind 相对主检出当前分支
// （当前分支自身相对其上游 origin/<branch>，无上游时两者为 0）。
type GitBranchInfo struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	Ahead   int    `json:"ahead"`
	Behind  int    `json:"behind"`
	// 会话分支才有下面这些字段（主检出分支整键缺席）。
	SessionID    string `json:"session_id,omitempty"`
	SessionTitle string `json:"session_title,omitempty"`
	Archived     bool   `json:"archived,omitempty"` // 归档会话的分支同样列出，如实标注
	HasWorktree  bool   `json:"has_worktree,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	DirtyCount   int    `json:"dirty_count,omitempty"`
	Merged       bool   `json:"merged,omitempty"` // 分支已并入主检出当前分支
}

// GitCommitInfo 是主检出的一条提交（git log 条目）。
type GitCommitInfo struct {
	Hash    string `json:"hash"`
	Message string `json:"message"` // 标题行（首行）
	Author  string `json:"author"`
	When    string `json:"when"` // ISO 8601（RFC3339）；相对时间由前端折算
}

// GitOverviewResult 是 git.overview 的结果：项目主检出的只读快照。
type GitOverviewResult struct {
	Path     string          `json:"path"` // 主检出路径
	Branch   string          `json:"branch"`
	Dirty    []GitChange     `json:"dirty"`
	Branches []GitBranchInfo `json:"branches"`
	Commits  []GitCommitInfo `json:"commits"` // 最近 20 条
}

// GitDiffParams 是 git.diff 的参数：主检出工作区里单个文件的差异。
// Path 必须是仓库内的相对路径（由 git.overview 的 dirty 清单给出，客户端不得
// 自行拼路径——含路径分隔符跳转（..）的输入直接拒绝）。
type GitDiffParams struct {
	ProjectID string `json:"project_id,omitempty"`
	Path      string `json:"path"`
}

// GitDiffResult 是 git.diff 的结果。Diff 为空串 = 文件没有未提交差异。
type GitDiffResult struct {
	Diff string `json:"diff"`
}
