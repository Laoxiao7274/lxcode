package protocol

// 方法名（客户端 → 服务端）。
const (
	MethodHello = "connection.hello"
	// MethodPing 心跳探测：参数可空，纯内存应答 {pong:true}（~0ms）。客户端
	// 靠它主动判定连接活性——断线只靠被动 onclose 发现时，Chromium 的后台
	// 节流会把重连推迟到窗口恢复那一刻（最小化期间断的线，回来才重连）。
	MethodPing        = "connection.ping"
	MethodModelList   = "model.list"
	MethodModelAdd    = "model.add"
	MethodModelUpdate = "model.update"
	MethodModelRemove = "model.remove"
	MethodModelEnable = "model.enable"
	MethodRoleSet     = "role.set"

	// ---- 模型目录与端点探测（models.dev 目录，与 pi-ai 的生成目录同源）----
	//
	// 目录回答「有哪些模型可选」（厂商 → 模型元数据：上下文窗口、能力位；免 key，
	// 可用磁盘缓存离线服务）；探测回答「这个端点**实际**提供什么」（自建端点不在
	// 目录里）。两者都**不改注册表**——注册表仍是唯一事实源，用户勾选后才写进去。
	//
	// 都在后端做：CSP 的 connect-src 不放行外域，且对外使用 API key 的位置只能
	// 是后端（前端只传 provider id 或用户正在填的端点）。
	MethodModelCatalogList   = "model.catalog.list"
	MethodModelCatalogModels = "model.catalog.models"
	MethodModelDiscover      = "model.discover"
	MethodChatSend           = "chat.send"
	MethodChatCancel         = "chat.cancel"
	MethodChatHistory        = "chat.history"
	MethodToolConfirm        = "tool.confirm"
	// MethodChatCompact 手动压缩历史（空闲才允许——服务端返回 ErrBusy 映射的
	// 错误码）。参数可带 agent（与 chat.send 同语义：空 = 主 Agent）。
	MethodChatCompact = "chat.compact"
	// MethodChatModel 切换本会话**选中的模型**（会话级状态：落库 + 广播，只改
	// 这一条会话）。与 role.set 的分工：那个改的是全局 default 角色（= 新会话的
	// 默认模型，设置页用），这个改的是当前会话——原先模型选择只能走 role.set，
	// 于是一个会话切模型所有会话（含之后新建的）全跟着变。
	MethodChatModel = "chat.model"
	// MethodChatApproval 中途改本会话的权限档（立刻生效于**运行中的**一轮）。
	// 与 chat.send 的 approval 参数的区别：那个是「这一轮用哪一档」，这个是
	// 「从现在起这个会话用哪一档」——用户的当前意图是会话级实时状态。
	MethodChatApproval = "chat.approval"
	// MethodChatRewind 撤回（rewind）：把 seq 那条消息**及其之后的全部历史**
	// 从会话里删掉（那条消息的正文由客户端自己留在输入框里，用户改完重发）。
	// 空闲才允许——正在跑的一轮手里握着历史快照，抽掉它等于让模型按一份
	// 已经不存在的上下文继续（与 chat.compact 同款纪律，服务端回 ErrBusy）。
	MethodChatRewind = "chat.rewind"
	// MethodChatMergeRequest 是后台任务面板「合并请求」入口的协议方法（**遗留**：
	// 2026-10 架构修订——合并进程退役，会话直连主检出，没有分支合并流程）。
	// 保留方法名以免协议漂移；被调用时以错误码回带退役说明，不启动任何任务。
	MethodChatMergeRequest = "chat.mergeRequest"

	// 会话管理（持久化 + 切换）
	MethodSessionList    = "session.list"
	MethodSessionNew     = "session.new"
	MethodSessionResume  = "session.resume"
	MethodSessionRename  = "session.rename"
	MethodSessionArchive = "session.archive"
	// MethodSessionWorktreeRelease 已随直连模式移除（2026-10 架构修订废除
	// per-session worktree，「释放工作区」失去主场景——用户 2026-12 拍板移除）。
	// 不保留方法名：老客户端调用会得到标准的「未知方法」错误（CodeInvalidRequest），
	// 旧会话的 worktree 目录/分支由 temp/ 一次性清理脚本处理，不走协议。

	// 项目管理（workspace 分组）
	MethodProjectAdd  = "project.add"
	MethodProjectList = "project.list"
	// 项目守则（项目根 AGENTS.md）读写：**只按项目 id 寻址**，客户端不传路径——
	// 越权面在结构上为零（固定文件名 + 项目根由服务端解析）。
	MethodProjectInstructionsGet  = "project.instructions.get"
	MethodProjectInstructionsSave = "project.instructions.save"

	// ---- Agent 注册表与拓展目录（M1——docs/backend-roadmap.md）----

	MethodAgentList   = "agent.list"
	MethodAgentAdd    = "agent.add"
	MethodAgentUpdate = "agent.update"
	MethodAgentRemove = "agent.remove"

	MethodCatalogModuleList   = "catalog.modules.list"
	MethodCatalogModuleAdd    = "catalog.modules.add"
	MethodCatalogModuleUpdate = "catalog.modules.update"
	MethodCatalogModuleRemove = "catalog.modules.remove"

	MethodCatalogToolList   = "catalog.tools.list"
	MethodCatalogToolAdd    = "catalog.tools.add"
	MethodCatalogToolUpdate = "catalog.tools.update"
	MethodCatalogToolRemove = "catalog.tools.remove"

	MethodCatalogMcpList   = "catalog.mcp.list"
	MethodCatalogMcpAdd    = "catalog.mcp.add"
	MethodCatalogMcpUpdate = "catalog.mcp.update"
	MethodCatalogMcpRemove = "catalog.mcp.remove"

	// ---- 网页搜索渠道（M4——渠道配置存 config/search.json，见 docs/backend-roadmap.md）----
	//
	// 渠道是**环境配置**不是拓展目录：它描述这台机器能连哪些搜索服务，
	// 与「有哪些工具」是两件事（拍板记录见路线图 M4）。所以方法名用 search.*
	// 而不是 catalog.search.*。
	MethodSearchChannelsList = "search.channels.list"
	MethodSearchChannelSave  = "search.channel.save"
	// MethodSearchChannelRemove 删除渠道配置（回到未配置状态）。
	MethodSearchChannelRemove = "search.channel.remove"
	// MethodSearchPrimarySet 设主渠道（降级链的第一个）。
	MethodSearchPrimarySet = "search.primary.set"
	// MethodSearchTest 只测一个渠道、不降级——用户点「测试」就是想验证这一个，
	// 降级会把「这个渠道坏了」测成「搜索正常」。
	MethodSearchTest = "search.test"

	// ---- 行为开关（用户在设置里选的内置模板——后端级配置，存 config/behavior.json）----
	//
	// get 返回当前档位；set 校验并原子写回 + 广播 settings.todoPrompt.changed。
	// 提示词注入在下一轮生效（服务端每次 chat.send 现读——不需要重启或重开会话）。
	MethodTodoPromptGet = "settings.todoPrompt.get"
	MethodTodoPromptSet = "settings.todoPrompt.set"

	// 新会话默认审批档（与 todoPrompt 同一条模式：存 config/behavior.json）。
	// 语义是「新会话的初始档」——改它不影响既有会话（那些是各自的落库值）；
	// 会话内的实时改档走 chat.approval（会话级状态，不是这里的设置）。
	MethodDefaultApprovalGet = "settings.defaultApproval.get"
	MethodDefaultApprovalSet = "settings.defaultApproval.set"

	// ---- Git 管理页（前端 Git 工作台的只读查询）----
	//
	// 两个方法全部只读：读主检出的状态 / 分支 / 最近提交与单文件 diff。
	// Git 页面**不提供任何写主检出的操作**（提交由会话内检查点与
	// workspace_sync 负责）。原先的写入口 session.worktree.release 已随直连
	// 模式移除（2026-12 用户拍板）。
	MethodGitOverview = "git.overview"
	MethodGitDiff     = "git.diff"

	// ---- 后台任务（jobs——docs/jobs.md §4）----
	//
	// 三个方法 + 两个事件。job.kill 就是**用户点「结束」**：后端走
	// Manager.Kill(id, EndedUser)，与 agent 的 job_kill 工具是同一条路径，
	// 只有 by 不同——两条路径的行为永远一致，不会出现「工具杀不唤醒、
	// 按钮杀唤醒」这种分叉。
	MethodJobList = "job.list"
	MethodJobKill = "job.kill"
	MethodJobLog  = "job.log"
)

// 事件名（服务端 → 全部客户端广播；无 id 的 JSON-RPC 消息）。
const (
	EventReady    = "connection.ready" // 连接建立时单发
	EventUserMsg  = "chat.userMessage" // 用户消息已被后端接受（多客户端同步）
	EventDelta    = "chat.delta"       // 流式增量：kind=text|reasoning
	EventToolCall = "chat.toolCall"    // 模型发起工具调用
	EventToolRslt = "chat.toolResult"  // 工具执行完成（结果已入历史）
	EventConfirm  = "chat.confirmRequest"
	EventDone     = "chat.done"  // 一轮 assistant 消息完成
	EventError    = "chat.error" // 出错或中断（aborted=true 表示用户取消）
	EventBusy     = "chat.busy"  // 忙闲状态变化（多客户端同步）
	// EventModelChanged 会话**选中模型**变更（多客户端同步，与 chat.approvalChanged
	// 同款）：壳与浏览器/手机同时看同一条会话时显示必须一致。与 model.changed 的
	// 分工：那个是全局模型注册表变了（列表/角色绑定），这个是某会话换了模型。
	EventModelChanged = "chat.modelChanged"
	// EventApproval 权限档变化（多客户端同步，与 chat.busy 同款）：壳与浏览器
	// 同时开着时两边的档位必须一致，否则用户在一侧改成 auto、另一侧还显示 confirm。
	EventApproval = "chat.approvalChanged"
	EventModels   = "model.changed"
	EventTodo     = "todo.updated" // 任务清单变更（客户端渲染 TodoList）

	EventSessionChanged = "session.changed"    // 会话元数据变化（created/started/renamed/archived/compacted）
	EventFiles          = "files.changed"      // 一轮的文件改动汇总（产物卡——验收视图）
	EventProjectChanged = "project.changed"    // 项目增删——客户端重拉 project.list
	EventAgentChanged   = "agent.changed"      // Agent 名单变更——客户端重拉 agent.list
	EventCatalogChanged = "catalog.changed"    // 拓展目录变更——客户端重拉对应 kind 的 catalog.*.list
	EventDispatchStart  = "chat.dispatchStart" // 主 Agent 派发子 Agent——客户端渲染 dispatch 卡
	EventDispatchEnd    = "chat.dispatchEnd"   // 子 Agent 执行收尾——dispatch 卡定格带结果
	EventCompacted      = "chat.compacted"     // 历史被压缩（前缀替换成摘要检查点）——客户端插标记块
	EventRewound        = "chat.rewound"       // 会话被撤回（seq 及其之后的历史已删除）——客户端截断时间线
	EventSearchChanged  = "search.changed"     // 搜索渠道配置变更——客户端重拉 search.channels.list
	// EventTodoPromptChanged 行为开关（todo-first 档位）变更——多客户端同步：
	// 壳与浏览器同时开着时两边的设置显示必须一致（与 search.changed 同款语义）。
	EventTodoPromptChanged = "settings.todoPrompt.changed"
	// EventDefaultApprovalChanged 新会话默认审批档变更——多客户端同步（与
	// settings.todoPrompt.changed 同款语义）。
	EventDefaultApprovalChanged = "settings.defaultApproval.changed"
	// EventJobStarted / EventJobSettled 是后台任务的状态广播（载荷 = JobInfo）：
	// settled 带 EndedBy，前端据此显示「你停的 / 它挂了 / 超时 / 后端重启中断」。
	EventJobStarted = "job.started"
	EventJobSettled = "job.settled"
)

// 错误码：JSON-RPC 标准码 + 本应用码。
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603

	// 应用错误码
	CodeNoDefaultModel  = 1001 // 未绑定 default 角色模型
	CodeModelDisabled   = 1002 // default 模型已停用
	CodeBusy            = 1003 // 会话正在生成中
	CodeNoPending       = 1004 // 没有待确认的工具调用
	CodeAgentNotFound   = 1005 // Agent 不在名单中
	CodeAgentDisabled   = 1006 // Agent 已停用
	CodeVersionMismatch = 1007 // 客户端与服务端协议版本不兼容
)
