package protocol

import "github.com/moyunteng/lxcode/internal/sessiondata"

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

// FileRevealParams 是 file.reveal 的参数：把工作区里的一个文件交给客户端的
// 编辑器/文件管理器打开（产物卡与附件卡的「在编辑器打开/显示在文件夹」动作）。
type FileRevealParams struct {
	SessionID string `json:"session_id"`
	// Path 是会话工作目录内的相对路径（也接受绝对路径——但必须净化后仍
	// 在工作目录之内；穿越一律拒绝）。
	Path string `json:"path"`
}

// FileRevealResult 是 file.reveal 的结果：Abs 是校验通过后的绝对路径——
// 打开动作本身由客户端的壳执行（后端不碰 GUI），渲染层拿它调壳桥。
type FileRevealResult struct {
	Abs string `json:"abs"`
}

// ProjectAddParams 是 project.add 的参数（path 为本地目录绝对路径）。
type ProjectAddParams struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ProjectRemoveParams 是 project.remove 的参数（只给项目 id）。
type ProjectRemoveParams struct {
	ProjectID string `json:"project_id"`
}

// ProjectRemoveResult 是 project.remove 的结果。
//
// 语义（用户拍板）：**只从 lxcode 的项目列表移除注册**——不删磁盘目录、不删
// 会话。Sessions 是该项目下保留在库里的会话数（这些会话的归属被清空、归入
// 「未分组」，历史一条不丢），调用方据此提示用户「N 条会话保留在库里」。
type ProjectRemoveResult struct {
	Removed  bool `json:"removed"`
	Sessions int  `json:"sessions"`
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

// SessionFulltextParams 是 session.fulltext 的参数：按**子串**搜历史消息正文
// （UI 友好——用户输入不需要懂正则；正则语义保留给 agent 的 session_search 工具）。
type SessionFulltextParams struct {
	Query string `json:"query"`
	// Max 最多返回条数；0 = 后端缺省 30，>100 截到 100（与 store.Search 同钳制）。
	Max int `json:"max,omitempty"`
}

// SessionFulltextHit 是一条消息命中（复用 sessiondata.SearchHit 的 wire 形状——
// 每条命中已带会话 id/标题/时间/角色/内容，UI 分组与片段高亮都够用）。
type SessionFulltextHit = sessiondata.SearchHit

// SessionFulltextResult 是 session.fulltext 的结果。
type SessionFulltextResult struct {
	Hits []SessionFulltextHit `json:"hits"`
	// Total 是命中总数（可能大于 len(hits)——超出 max 的只数不回）。
	Total int `json:"total"`
	// TimedOut=true 表示扫描未完整（全库扫描有 2500ms 限时）——UI 提示
	//「结果可能不全」，不把它伪装成全集。
	TimedOut bool `json:"timed_out,omitempty"`
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
