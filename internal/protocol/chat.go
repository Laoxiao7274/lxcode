package protocol

import (
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// 推理强度档位（chat.send 可选参数；空 = 模型默认）。
// 值域对齐 OpenAI reasoning_effort（minimal/low/medium/high）；Anthropic 侧
// 由 llm 层映射为 thinking budget（见 llm.WithEffort）。
const (
	EffortMinimal = "minimal"
	EffortLow     = "low"
	EffortMedium  = "medium"
	EffortHigh    = "high"
)

// 权限模式（chat.send 可选参数；空 = confirm）。
// 工具执行的三档策略——auto 高危自动执行（仅隔离环境）、confirm 低危自动
// + 高危确认（默认，AGENTS.md §3 的现行语义）、strict 只读（变更类工具直接
// 拒绝，错误回填模型）。
const (
	ApprovalAuto    = "auto"
	ApprovalConfirm = "confirm"
	ApprovalStrict  = "strict"
)

// ChatSendImage 是 chat.send 携带的一张图片：Data 为 base64（**不带** data: 前缀）。
// 服务端校验（mime 白名单 / ≤5MB / ≤4 张）后落盘到附件目录，历史里只存文件引用
// ——base64 绝不入库（2026-10 用户拍板）。
//
// **传输形态（2026-12 用户拍板：本地路径优先，base64 兜底）**：前后端同机时，
// 客户端可携带 Path（本地真实路径）代替 Data——服务端直接按路径读盘，WS 帧里
// 没有文件内容。两形态互斥（同时携带报参数错误）；Path 形态的信任边界与读取
// 限制见 docs/attachments.md 与 server 侧 readLocalAttachment。
type ChatSendImage struct {
	Mime string `json:"mime"`
	Data string `json:"data"`
	// Name 是客户端提供的展示名（可选，向后兼容：老客户端不带 → 服务端回落
	// "图片"；Path 形态没带 → 回落路径 basename）。它只出现在附件卡的展示
	// 元数据里（llm.AttachmentRef.Name），永不成为磁盘文件名（磁盘名恒为随机 hex）。
	Name string `json:"name,omitempty"`
	// Path 非空 = 本地路径形态：服务端按此路径读盘（大小上限 / 存在性校验同
	// base64 形态），Data 必须为空。Mime 可空 → 服务端按扩展名推导（推导不出
	// 报参数错误）。仅本机单用户场景（Electron 壳）使用；浏览器模式恒 base64。
	Path string `json:"path,omitempty"`
}

// ChatSendFile 是 chat.send 携带的一个文件附件：Name 为客户端提供的原名
// （服务端净化后只用于消息文本与扩展名），Data 为 base64（**不带** data: 前缀）。
// 服务端校验（≤20MB / ≤4 个 / 名字净化）后落盘到同一附件目录，历史里只体现为
// 消息文本的「[附件] 名字 → attachments/...」行（无新列、无视觉通道）——Agent
// 用 read_file 自己读（2026-10 图片批次 B）。
//
// **传输形态**与 ChatSendImage 同款：Path 非空 = 服务端按路径读盘，Data 必须为空
// （Name 也可省 → 回落路径 basename）。
//
// **RelPath（文件夹相对路径，2026-12 拖文件夹改造）**：拖入文件夹时内部文件按
// 文件夹根的相对路径发送（如 `docs/api/auth.md`），服务端把它记进消息元数据与
// 「[附件]」行——落盘名仍是随机 hex，展示名/相对路径与磁盘名解耦。空 = 散文件
// （现状形态，老客户端不带此键零影响）。不净化分隔符以外的部分——相对路径要
// 原样呈现给用户；穿越片段（..）会随净化剥掉。
type ChatSendFile struct {
	Name string `json:"name"`
	Data string `json:"data"`
	// Path 非空 = 本地路径形态（同 ChatSendImage.Path 的语义与限制）。
	Path string `json:"path,omitempty"`
	// RelPath 非空 = 文件夹内相对路径（展示用；不参与落盘寻址）。
	RelPath string `json:"rel_path,omitempty"`
}

type ChatSendParams struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Effort    string `json:"effort,omitempty"`   // 推理强度（可选；模型须声明 reasoning 能力才生效）
	Approval  string `json:"approval,omitempty"` // 权限模式（可选；空 = Agent 默认/confirm）
	Agent     string `json:"agent,omitempty"`    // 执行 Agent 的名单 id（可选；空 = 主 Agent/旧语境）
	// Images 是可选的图片附件（视觉请求）：空 = 无图（omitempty——老客户端零影响，
	// 协议 Version 不变）。校验失败的语义见 server 侧（CodeInvalidParams + 人话文案）。
	Images []ChatSendImage `json:"images,omitempty"`
	// Files 是可选的文件附件（给 Agent 读的，不进视觉通道）：空 = 无文件。
	// 落盘后以「[附件] ...」行追加进消息文本，历史无新列（协议 Version 不变）。
	Files []ChatSendFile `json:"files,omitempty"`
}

type ChatSessionParams struct {
	SessionID string `json:"session_id"`
}

type SessionResult struct {
	SessionID string `json:"session_id"`
}

// ValidateEffort 校验 effort 值域（空串合法 = 不指定）。
func ValidateEffort(e string) bool {
	switch e {
	case "", EffortMinimal, EffortLow, EffortMedium, EffortHigh:
		return true
	}
	return false
}

// ValidateApproval 校验 approval 值域（空串合法 = 默认 confirm）。
func ValidateApproval(a string) bool {
	switch a {
	case "", ApprovalAuto, ApprovalConfirm, ApprovalStrict:
		return true
	}
	return false
}

type ToolConfirmParams struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id"`
	Allow     bool   `json:"allow"`
	// Answer 非空 = 对 ask_user 提问的文本回答（走会话的 Answer 路径，
	// 而不是二元确认）；空 = 二元批准/拒绝（现状语义，向后兼容）。
	Answer string `json:"answer,omitempty"`
}

// ChatHistoryParams 指定要读取的会话；客户端焦点不属于服务端全局状态。
type ChatHistoryParams struct {
	SessionID string `json:"session_id"`
	// Tail 可选：只返回**尾部** N 条（当前历史下标语义，非库 seq——配平逻辑
	// 在 agent 层做，见 Session.TailHistory）。0/缺省 = 全量（老客户端不受
	// 影响，wire 向后兼容）。
	//
	// 背景（2026-10 公网性能排查）：chat.history 是全量载荷，大会话 1.4~1.7MB
	// 经 frp 公网隧道（限速 10 Mbit/s）要 1.2~4.8s，安卓端固定 10s 超时偶尔
	// 直接失败。慢链路客户端先拉尾部、上滑再增量补头部（协议侧配合项）。
	Tail int `json:"tail,omitempty"`
}

// ChatHistoryResult 是指定会话的同步载荷。
type ChatHistoryResult struct {
	// Messages 是回放的历史：带图 user 消息的 Images 是**文件引用**
	//（llm.ImageRef——相对附件根的路径 + mime），不含 base64；前端经
	// GET /attachments/<sessionID>/<file> 取缩略图（图片批次 B 接 UI）。
	Messages  []llm.Message    `json:"messages"`
	Busy      bool             `json:"busy"`
	Pending   *ConfirmRequest  `json:"pending,omitempty"`
	SessionID string           `json:"session_id,omitempty"` // 当前会话 id（session.changed 后重拉可拿到新值）
	Todos     []tools.TodoItem `json:"todos,omitempty"`      // 任务清单（客户端渲染 TodoList）
	Context   *ContextUsage    `json:"context,omitempty"`    // 上下文占用（无 = 未知——刚切会话/后端刚重启）
	// Stats 是整段会话的统计（无 = 还没有任何一步——新会话/纯内存模式）。
	// 与 context 的区别：context 是**此刻窗口里有多少**，stats 是**这条会话一共花了多少**
	//（压缩/翻页都改不了它，见 SessionStats 的说明）。
	Stats *SessionStats `json:"stats,omitempty"`
	// Checkpoints 是压缩检查点在 Messages 里的下标：这些消息要渲染成
	// 「已压缩历史」块，而不是用户气泡（内容是摘要正文，不是用户说的话）。
	Checkpoints []int `json:"checkpoints,omitempty"`
	// Model 是该会话**实际使用**的模型注册表 id（子会话 = 它自己 Agent 的模型：
	// AgentDef.Model 优先，否则 default 角色）。空 = 未知（Agent 没了/没绑模型/
	// 没有 default）——整键缺席，前端显示中性态，**不编一个模型名**。
	// 与 Messages[].model 的区别：那是**每轮**实际用的（历史事实），这是**此刻**
	// 解析出来的（会话还没跑过任何一轮时也有值）。
	Model string `json:"model,omitempty"`
	// Approval 是该会话**此刻**的权限档（LiveApproval：子会话 = 取严(父实时, 自身默认)）。
	// 前端据此做「主会话之间隔离」的档位显示——每个会话显示它自己后端里的真实档位，
	// 而不是上一会话残留的本地值。空 = 后端没报（老后端），前端回落 confirm。
	Approval string `json:"approval,omitempty"`
}

// ContextUsage 是上下文占用的 wire 形态（agent.ContextUsage 的映射——内核类型
// 不过协议边界）。used/window 是压力与环形依据（used 优先真实 prompt 总量），
// 五个分类是估算拆分（已归一：分类之和 == used）。
type ContextUsage struct {
	Used        int `json:"used"`
	Window      int `json:"window,omitempty"`
	System      int `json:"system,omitempty"`
	Tools       int `json:"tools,omitempty"`
	ToolResults int `json:"tool_results,omitempty"`
	Messages    int `json:"messages,omitempty"`
	Reasoning   int `json:"reasoning,omitempty"`
	// Estimated 为真 = 这个数字是**估算**（按固定密度折算），不是 provider 回报的真实用量：
	// 端点没回报 usage，或库里没有真实测量、按已加载的历史回落估算（老会话）。客户端必须
	// 把它和真实用量**在 UI 上区分开**（「估」标记）——用户看不出区别就会拿它做预算判断。
	// 假值 omitempty（真实用量的常见情形不多一个键）。
	Estimated bool `json:"estimated,omitempty"`
}

// SessionStats 是**整段会话**的统计 wire 形态（sessiondata.SessionStats 的映射）。
//
// 为什么整键缺席（指针 + omitempty）而不是发一排 0：零值 = 还没有任何一步（新会话/
// 纯内存模式/读不到库），而「0 轮 0 步」在界面上是个**假事实**——客户端据整键缺席
// 不渲染统计胶囊（DSH 同款：steps == 0 且没有 token 时不渲染）。本仓库既有纪律：
// 未知就显示中性态，不编数字。
//
// 口径与 DSH 的 sessionStats + tokenUsage 两个投影一致：它折叠的是**整段日志**
// （含被压缩检查点影子掉的消息），所以压缩、翻页都改不了这些数字；撤回真删了行，
// 数字跟着变小才是对的。
type SessionStats struct {
	Turns int `json:"turns"`
	Steps int `json:"steps"`
	// 墙钟（毫秒）：llm_ms = 各步请求耗时之和；tool_ms = 工具执行耗时之和。
	LLMMs  int64 `json:"llm_ms"`
	ToolMs int64 `json:"tool_ms"`
	// 首字：ttft_ms / ttft_steps = 均值（有首字可测的步才有值）。
	TTFTMs    int64 `json:"ttft_ms"`
	TTFTSteps int   `json:"ttft_steps"`
	// 解码：decode_ms = 首字 → 收尾的纯生成耗时，decode_tokens = 同期输出 token。
	// 生成速度 = decode_tokens / decode_ms（扣掉 prefill 才是"吐字速度"）。
	DecodeMs     int64 `json:"decode_ms"`
	DecodeTokens int   `json:"decode_tokens"`
	// 计费四桶（provider 回报；未回报 = 0）：未缓存输入 / 缓存读 / 缓存写 / 输出。
	InputTokens      int `json:"input_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	OutputTokens     int `json:"output_tokens"`
	// 早期记录（本功能上线前落库的行）的 token 之和：那时的口径是 provider 的
	// total_tokens（输入+输出），与上面四桶不同——单独给出，让 UI 如实说明而不是混算。
	LegacyTokens int `json:"legacy_tokens,omitempty"`
}

// 事件载荷。

type UserMessageParams struct {
	SessionID string      `json:"session_id"`
	Message   llm.Message `json:"message"`
	// DispatchID 非空 = 子会话的消息（派发方注入的任务说明书）：前端投进子会话
	// 自己的 state，主时间线不画（dispatch 卡 = 一行摘要）。
	DispatchID string `json:"dispatch_id,omitempty"`
}

type DeltaParams struct {
	SessionID  string `json:"session_id"`
	Kind       string `json:"kind"` // text | reasoning
	Text       string `json:"text"`
	DispatchID string `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 的增量（归属 dispatch 卡）
}

type ToolCallParams struct {
	SessionID  string `json:"session_id"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	DispatchID string `json:"dispatch_id,omitempty"`
}

type ToolResultParams struct {
	SessionID  string `json:"session_id"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
	DispatchID string `json:"dispatch_id,omitempty"`
	// Attachments 非空 = 这次工具执行共享了附件（share_file）：与结果消息
	// llm.Message.Attachments 同一份元数据（前端渲染附件卡——实时事件与
	// chat.history 回放同一形状）。空 = 无（omitempty：老事件零影响）。
	Attachments []llm.AttachmentRef `json:"attachments,omitempty"`
}

// ConfirmRequest 是需要人工介入的挂起请求（确认门）；客户端须回 tool.confirm。
// Kind 为空 = 高危工具确认（批准/拒绝）；Kind == "ask" = ask_user 的提问——
// 用户以文本回答（tool.confirm 带 answer）或跳过（allow=false）。
type ConfirmRequest struct {
	SessionID  string `json:"session_id"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
	Prompt     string `json:"prompt"`
	DispatchID string `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 的确认（归属 dispatch 卡）
	// Kind 标记形态：空 = 工具确认（现状语义）；"ask" = 提问（用户打字回答）。
	// 可选字段（向后兼容——老客户端忽略未知键，Version 不变）。
	Kind string `json:"kind,omitempty"`
	// Options 是提问的预设答案（可空）——前端渲染成可直接点选的选项按钮。
	Options []string `json:"options,omitempty"`
}

type DoneParams struct {
	SessionID    string      `json:"session_id"`
	Message      llm.Message `json:"message"`
	UsageTokens  int         `json:"usage_tokens"`
	FinishReason string      `json:"finish_reason"`
	DispatchID   string      `json:"dispatch_id,omitempty"` // 非空 = 子 Agent 轮完成
	// Context 是本轮之后的上下文占用（仅主轮携带——子轮的占用不进主指示器）。
	Context *ContextUsage `json:"context,omitempty"`
	// Stats 是**整段会话**的统计（仅主轮携带，口径见 SessionStats）。它是"到目前为止
	// 一共花了多少"，所以每条 done 都带一份最新的：一轮里的中间步（工具轮）也会带，
	// 客户端取最后收到的那份即可（与 DSH 的投影随事件推进同语义）。
	Stats *SessionStats `json:"stats,omitempty"`
	// 本轮计时（口径见 llm.Message 的同名字段）。与 message 里那份是**同一组数字**
	//（emit 时从同一条消息取，不另算一遍）：单独列出来只是让 chat.done 自解释——
	// 前端不必从 message 里挖。零值 = 未知（工具轮没有首 token / provider 不回报
	// 用量），wire 上整键缺席。usage_tokens 已在上面（那一版就有）。
	FirstTokenMs int64  `json:"first_token_ms,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	Model        string `json:"model,omitempty"`
}

type ErrorParams struct {
	SessionID string       `json:"session_id"`
	Message   string       `json:"message"`
	Aborted   bool         `json:"aborted"`
	Partial   *llm.Message `json:"partial,omitempty"` // 中断时已生成的部分内容（客户端应入历史）
}

type BusyParams struct {
	SessionID string `json:"session_id"`
	Busy      bool   `json:"busy"`
	// DispatchID 非空 = 子会话的忙闲翻转（2026-10-10 起：子会话页的「生成中」
	// 行与停止钮靠它出现）。前端主时间线侧按它过滤（主会话忙闲不被子会话翻动），
	// 双投路径把它归约进子会话自己的 state。omitempty：主会话的 busy 不带此键，
	// wire 向后兼容。
	DispatchID string `json:"dispatch_id,omitempty"`
}

// CompactParams 是 chat.compact 的参数（与 chat.send 同语义：agent 空 = 主 Agent）。
type CompactParams struct {
	SessionID string `json:"session_id"`
	Agent     string `json:"agent,omitempty"`
}

// CompactResult 是 chat.compact 的结果：Compacted=false 表示没有可压的收益
// （历史太短，或摘要并不比被压缩段更小）——不是错误，Reason 说明为什么没压，
// 客户端直接显示给人看。
type CompactResult struct {
	Compacted bool   `json:"compacted"`
	Reason    string `json:"reason,omitempty"`
	Before    int    `json:"before,omitempty"`   // 压缩前上下文占用（估算）
	After     int    `json:"after,omitempty"`    // 压缩后
	Shadowed  int    `json:"shadowed,omitempty"` // 被替换的历史条数
}

// CompactedParams 是 chat.compacted 事件的载荷：宿主据此插一条「已压缩历史」
// 标记块（Summary 供展开查看），并刷新上下文指示器。DispatchID 非空 = 子会话
// 自己的压缩（归属进 dispatch 卡，不进主时间线）。
type CompactedParams struct {
	SessionID  string `json:"session_id"`
	Before     int    `json:"before"`
	After      int    `json:"after"`
	Shadowed   int    `json:"shadowed"`
	Summary    string `json:"summary"`
	Manual     bool   `json:"manual,omitempty"`
	DispatchID string `json:"dispatch_id,omitempty"`
}

// ChatRewindParams 是 chat.rewind 的参数：seq 是要撤回的那条消息的序号
// （= chat.history 的 messages[].seq，也是 chat.userMessage 的 params.message.seq）。
// 前端把自己那条消息的 seq 当锚点发回来——服务端不需要正文（撤回不改内容，
// 那条消息的文本本来就在用户的输入框里）。
type ChatRewindParams struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
}

// ChatRewindResult 是 chat.rewind 的结果：Removed = 从会话历史里删掉的条数
// （那条消息及其之后的全部历史）。重复撤回同一条返回 removed=0——那是幂等空操作，
// 不是错误（客户端重试/两个客户端同时点撤回都不该报错）。
type ChatRewindResult struct {
	Removed int `json:"removed"`
}

// ChatMergeRequestParams 是 chat.mergeRequest 的参数：从后台任务面板直接起一个
// 合并进程（与 merge_request 工具同一条后端路径）。SessionID 空 = 客户端当前会话；
// TargetBranch 空 = 默认 lxcode/integration。
type ChatMergeRequestParams struct {
	SessionID    string `json:"session_id,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
}

// MergeRequestResult 是 chat.mergeRequest 的结果：JobID 是新起的后台任务 id
// （job.list / job.log 按 它寻址；任务结束经唤醒投递自动通告回会话）。
type MergeRequestResult struct {
	JobID string `json:"job_id"`
}

// ChatRewoundParams 是 chat.rewound 事件的载荷：宿主据此**截断时间线**
// （seq 之前的保留、seq 及其之后的丢弃）并刷新上下文指示器。
//
// 为什么是广播事件而不是「让客户端收到结果后自己重拉历史」：撤回是一个
// **多客户端可见的状态变更**（壳与浏览器同时开着时两边必须一致），广播让每个
// 客户端都在同一时刻收到同一份事实；而「重拉」要求每个客户端自己知道去拉、
// 并且在与流式增量交错时自己算清该丢哪一段（重拉与增量并发时会闪回旧内容）。
// 载荷带 seq/removed 而不是只给一个"变了"的信号：客户端本地就能精确截断，
// 一次网络往返都不需要。
type ChatRewoundParams struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
	Removed   int    `json:"removed"`
	// Context 是**重算后**的上下文占用，与 chat.done / ChatHistoryResult.context
	// 同一个结构与同一套语义（撤回删了一段历史，撤回前的数字一定是错的——
	// 客户端拿它直接刷新指示器，不必等下一轮，也不必自己按 -removed 猜）。
	// 未知（纯内存模式 / 本会话还没跑过主轮）时整键缺席：发零值等于显示 0%，
	// 那是编出来的假信息（本仓库既有纪律：未知就显示中性态）。
	Context *ContextUsage `json:"context,omitempty"`
	// Stats 是**重算后**的整段会话统计：撤回真删了行，步数/token 会跟着变小，
	// 客户端拿它直接刷新统计胶囊（与 Context 同一条理由：这是一次改写历史的事务，
	// 客户端必须被通知而不是自己猜）。
	Stats *SessionStats `json:"stats,omitempty"`
}

// TodoUpdatedParams 是 todo.updated 事件的载荷：完整清单（全量替换语义）。
// DispatchID 非空 = 子会话的清单更新（带归属——前端据此更新主时间线派发卡上的
// 进度徽标；子会话页由既有双投吃到）。空 = 主会话自己的清单（按 session_id 路由）。
type TodoUpdatedParams struct {
	SessionID  string           `json:"session_id"`
	Items      []tools.TodoItem `json:"items"`
	DispatchID string           `json:"dispatch_id,omitempty"`
}

// ChatApprovalParams 是 chat.approval 的参数（中途改权限档）。
// Approval 空 = 未指定（服务端按 confirm 规范化后回填）。
type ChatApprovalParams struct {
	SessionID string `json:"session_id"`
	Approval  string `json:"approval"` // auto/confirm/strict（空 = 回落 confirm）
}

// ChatApprovalResult 是 chat.approval 的结果：回**规范化后**的档位，客户端据此
// 对齐本地设置（不自己再猜一遍空值该落哪一档）。
type ChatApprovalResult struct {
	Approval string `json:"approval"`
}

// ApprovalChangedParams 是 chat.approvalChanged 事件的载荷（多客户端同步）。
type ApprovalChangedParams struct {
	SessionID string `json:"session_id"`
	Approval  string `json:"approval"`
}

// DispatchStartParams 是 chat.dispatchStart 的载荷。OwnerSessionID 是派发者
// （时间线持有者），SessionID 是子 Agent 的独立会话 id（可续跑、可回放）。
type DispatchStartParams struct {
	OwnerSessionID string `json:"owner_session_id"`
	DispatchID     string `json:"dispatch_id"`
	SessionID      string `json:"session_id,omitempty"` // 子会话 id
	AgentID        string `json:"agent_id"`
	AgentName      string `json:"agent_name"`
	AgentColor     string `json:"agent_color"`
	Task           string `json:"task"`
	// Todos 非空 = 主 Agent 预拆的清单初稿（todo 契约——卡上第一秒显示计划）。
	Todos []tools.TodoItem `json:"todos,omitempty"`
}

// DispatchEndParams 是 chat.dispatchEnd 的载荷。OwnerSessionID 标明结果归属
// 的父会话；SessionID 标明子会话（主 Agent 可据此续跑）。
type DispatchEndParams struct {
	OwnerSessionID string `json:"owner_session_id"`
	DispatchID     string `json:"dispatch_id"`
	SessionID      string `json:"session_id,omitempty"` // 子会话 id
	Result         string `json:"result"`
	IsError        bool   `json:"is_error"`
	UsageTokens    int    `json:"usage_tokens,omitempty"`
	// FinalTodos 是派发结束时子会话清单的定格值（nil = 没建过清单）。
	FinalTodos []tools.TodoItem `json:"final_todos,omitempty"`
}
