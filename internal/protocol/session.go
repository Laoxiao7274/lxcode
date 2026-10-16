package protocol

// ---- 会话管理（持久化 + 切换）----

// SessionResumeParams 是 session.resume 的参数。
type SessionResumeParams struct {
	ID string `json:"id"`
}

// SessionNewParams 是 session.new 的可选参数（会话归属项目）。
type SessionNewParams struct {
	Workspace string `json:"workspace,omitempty"` // 归属项目 id（空 = 未分组）
}

// SessionRenameParams 是 session.rename 的参数。
type SessionRenameParams struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SessionArchiveParams 是 session.archive 的参数。
type SessionArchiveParams struct {
	ID       string `json:"id"`
	Archived bool   `json:"archived"`
}

// SessionArchiveResult 是 session.archive 的结果。归档是主操作——它失败时以
// JSON-RPC 错误返回，不走这里。
// （原 ReleasedWorktree/ReleaseError 两字段随直连模式移除：归档不再顺带释放
// worktree 目录，2026-12 用户拍板。）
type SessionArchiveResult struct {
	Archived bool `json:"archived"`
}

// ProjectAddParams 是 project.add 的参数（path 为本地目录绝对路径）。
type ProjectAddParams struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ProjectMeta 是 project.list 的条目。
type ProjectMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// ProjectInstructionsParams 是 project.instructions.get/save 的参数：只给项目 id，
// 文件由服务端定位（项目根 + 固定文件名 AGENTS.md）。
type ProjectInstructionsParams struct {
	ProjectID string `json:"project_id"`
	// Content 只在 save 时使用（get 忽略）。
	Content string `json:"content,omitempty"`
}

// ProjectInstructionsResult 是 project.instructions.get 的结果。
// Exists=false = 该项目还没有守则文件（正常态，不是错误）；Note 非空 = 读取异常说明。
type ProjectInstructionsResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Exists  bool   `json:"exists"`
	Note    string `json:"note,omitempty"`
}

// SessionMeta 是 session.list 的条目（resume 选择器的数据源）。
type SessionMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`      // 第一条 user 消息截断（空会话为占位）
	UpdatedAt string `json:"updated_at"` // 最后修改时间
	Messages  int    `json:"messages"`
	Archived  bool   `json:"archived"`            // 归档态——侧栏不显示，设置归档区可恢复
	Workspace string `json:"workspace,omitempty"` // 归属项目 id（空 = 未分组）
	// Running 是**此刻**是否在生成（server 运行时的事实，与 busy 广播同源）。
	// 背景：chat.busy 只在轮次边界各发一次——新连上的客户端错过那次 busy=true，
	// 列表的运行指示要点进会话（chat.history 带 busy）才补上。session.list 直接
	// 带 running，客户端建投影不再依赖「恰好在线时收到广播」。老后端缺席 = 空闲
	//（不编状态）；子会话不进 session.list（List 只认顶层），运行中的子会话经
	// busy_sessions 快照与 chat.busy 广播投影。
	Running bool `json:"running,omitempty"`
}

// SessionChangedParams 是会话元数据变化通知；它不代表客户端焦点变化，
// 也不要求重载某个会话的生成状态。
type SessionChangedParams struct {
	ID     string `json:"id"`
	Reason string `json:"reason"` // created | started | renamed | archived | compacted | rewound
}

// FileChangeParams 是 files.changed 事件的载荷：一轮的文件改动汇总
// （产物卡——验收视图。前端 FileChange 结构 1:1 对应）。
type FileChangeParams struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Diff    string `json:"diff"`
}

// FilesChangedParams 是 files.changed 事件的载荷（文件改动数组）。
type FilesChangedParams struct {
	SessionID string             `json:"session_id"`
	Files     []FileChangeParams `json:"files"`
}
