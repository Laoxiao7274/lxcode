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
	// BeforeSeq 可选（2026-12 分页加载，安卓端）：返回**这条消息之前**（不含它）
	// 最多 Tail 条——「向上翻页」的 anchor 语义。tail 是「从尾部数 N 条」，
	// before_seq 是「从中间某处往前数 N 条」，两者互补：
	//   - tail=50                    → 进会话的首次加载（最新 50 条）；
	//   - before_seq=<最旧一条 seq>, tail=50 → 上滑到顶再补更早 50 条。
	// seq 是 llm.Message.Seq（store 分配的库序号，chat.history 的每条消息都带），
	// 客户端原样记录再原样传回。不在历史里 / ≤0 / n≤0 → CodeInvalidParams
	//（错误的 anchor 会切出重复或空洞的窗口，静默回落只会把错位藏起来）。
	// 与 Tail 一样配平与检查点平移在内核（Session.HistoryBefore）。缺省 0 =
	// 不分页（tail 语义不变，老客户端零影响——协议 Version 不变）。
	BeforeSeq int64 `json:"before_seq,omitempty"`
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
	// HasMore 仅分页应答（before_seq 请求）携带：true = anchor 之前还有更早的
	// 历史（客户端继续上滑还能拉）；false = 已经到头（显示「已加载全部历史」）。
	// 非分页应答整键缺席（omitempty——老客户端与桌面端零影响）。
	HasMore bool `json:"has_more,omitempty"`
	// FirstSeq 仅分页应答携带：本窗口**首条**消息的 Seq（下一次 before_seq 的
	// anchor）。空窗口（anchor 已是历史第一条）为 0 且 has_more=false——客户端
	// 不该拿 0 去发起下一次请求。与 has_more 同批（2026-12 安卓分页加载）。
	FirstSeq int64 `json:"first_seq,omitempty"`
	// LeafSeq 是这次回放对应的叶（0 = 空路径）。老客户端忽略（未知键）；
	// 新客户端拿它对齐树面板的高亮与「回到最新」的落点。
	LeafSeq int64 `json:"leaf_seq,omitempty"`
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
	// Files = true 时同时还原文件（fail-closed）。
	// **作用域（2026-12 拍板）**：父会话 + 它派出的**全部子会话**在这一段里的文件改动
	// 一起还原；子会话的**对话历史不动**。客户端不需要（也无法）按会话挑选——
	// 「父会话回到这一轮之前」在文件系统上就是一次跨会话的整体回滚。
	Files bool `json:"files,omitempty"`
}

// ChatRewindResult 是 chat.rewind 的结果：Removed = 从会话历史里删掉的条数
// （那条消息及其之后的全部历史）。重复撤回同一条返回 removed=0——那是幂等空操作，
// 不是错误（客户端重试/两个客户端同时点撤回都不该报错）。
type ChatRewindResult struct {
	Removed int `json:"removed"`
	// LeafSeq 是移动后的叶（0 = 空路径）。老客户端忽略（未知键）。
	LeafSeq int64 `json:"leaf_seq,omitempty"`
	// Branched = true 表示历史**保留为分支**（新语义的诚实告知：老客户端以为删了，
	// 其实一条没删——它看不到找回入口，但数据还在）。
	Branched bool `json:"branched,omitempty"`
	// Files 非空 = 本次带了文件还原的结果（含子会话：每项带 session_id）。
	Files *FileRestoreResult `json:"files,omitempty"`
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

// ChatSetLeafParams 切到任意历史节点：**leaf 直接指向该节点**，继续对话即从它长出新分支。
//
// Seq == 0 = **空路径**（回到第一条消息之前）——这是 chat.rewind 撤回第一条消息时的
// 落点，用户必须能到达；因此「回到最新」不是一个特殊值，而是客户端传树里的最大 seq
// （chat.tree 的 nodes 末条）。**不给 0 兼两个语义**：那是本仓库反复吃亏的那类歧义。
//
// 与 chat.rewind 的分工：rewind 把 leaf 移到 parent(anchor)（锚点自己也离开路径，
// 因为它是用户要改写的那条消息）；setLeaf 把 leaf 移到 seq 本身（「从这里接着长」）。
// 文件切段也随之差一格：rewind 还原 owner_seq >= anchor，setLeaf 还原 owner_seq > seq
// （**严格大于**——seq 这条消息自己的改动属于「目标状态」，不该被还原掉）。
type ChatSetLeafParams struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
	// Files = true 时先还原文件快照再切 leaf（fail-closed：还原失败/有冲突则不切）。
	// 缺省 false（老客户端不传 → 纯历史指针移动，零文件副作用）。
	// 作用域与 chat.rewind 的 files 完全一致：**父会话 + 全部子会话**在这一段里的
	// 文件改动（按 owner_session_id/owner_seq 收集），子会话对话历史不动。
	Files bool `json:"files,omitempty"`
}

// ChatSetLeafResult 是 chat.setLeaf 的结果。
//
// 刻意**不**回 messages：切 leaf 之后客户端要重载的东西不止消息（树、统计、上下文、
// 检查点下标、分页游标），在 setLeaf 的应答里再复制一份必然与 chat.history 分叉
// （本仓库的老坑）。客户端统一走一次 chat.history —— 多一个往返，少一类 bug。
type ChatSetLeafResult struct {
	SessionID string `json:"session_id,omitempty"`
	LeafSeq   int64  `json:"leaf_seq"`
	// Removed = 离开路径的条数（与 chat.rewind 的 removed 同口径、同数值）。
	Removed int `json:"removed"`
	// 文件还原结果（只在 files:true 时出现；冲突时 conflicts 非空且 leaf 未变）。
	Files *FileRestoreResult `json:"files,omitempty"`
}

// ChatTreeParams 是 chat.tree 的参数。Include 缺省 "compact"。
type ChatTreeParams struct {
	SessionID string `json:"session_id"`
	Include   string `json:"include,omitempty"`   // compact(默认) | full
	MaxNodes  int    `json:"max_nodes,omitempty"` // 缺省 2000；超出只回最近 N 个并置 truncated
}

// ChatTreeResult 是会话树骨架（按 seq 升序；前端自己按 parent 建树）。
type ChatTreeResult struct {
	LeafSeq int64      `json:"leaf_seq"`
	Nodes   []TreeNode `json:"nodes"`
	// Total 是全量节点数（截断时用于「还有 N 个」提示）。
	Total     int  `json:"total"`
	Truncated bool `json:"truncated,omitempty"`
}

// TreeNode 是树里的一个节点。
type TreeNode struct {
	Seq       int64 `json:"seq"`
	ParentSeq int64 `json:"parent_seq"` // 0 = root
	// Kind = user | assistant | tool | checkpoint | notice
	Kind string `json:"kind"`
	// Turn 是轮次序号（user/notice 才有；与 SessionStats.turns 同口径）。
	Turn int `json:"turn,omitempty"`
	// Summary 是 ≤120 字符的单行摘要（不含正文全文）。
	Summary string `json:"summary,omitempty"`
	// Children 是直接子节点数（>1 = 分支点）。
	Children int `json:"children"`
	// OnPath = 是否在当前 leaf 路径上（废弃分支 = false）。
	OnPath bool `json:"on_path"`
	// Orphan = 父节点缺失 / 成环（畸形数据的兜底：不丢节点、不抛异常）。
	Orphan bool `json:"orphan,omitempty"`
	// Files = 该轮（owner_seq 落在这一轮的区间内）**可还原**的文件改动数，
	// 含子会话的改动。批次 3 填充；为 0 时整键缺席（前端不渲染徽标，不显示 0）。
	Files int `json:"files,omitempty"`
	// ChildFiles = 其中来自子会话的改动数（Files 的子集）。
	ChildFiles int `json:"child_files,omitempty"`
	// External = 该轮无法快照的写（bash/MCP/自定义工具，含子会话里的 bash）。
	External int `json:"external,omitempty"`
	// Shadowed = checkpoint 的影子条数。
	Shadowed int `json:"shadowed,omitempty"`
}

// ChatLeafChangedParams 是 chat.leafChanged 事件的载荷（rewind 与 setLeaf 都发）。
type ChatLeafChangedParams struct {
	SessionID string `json:"session_id"`
	LeafSeq   int64  `json:"leaf_seq"`
	// PrevLeafSeq 是移动前的叶（客户端据此判断"是不是别人动的"）。
	PrevLeafSeq int64 `json:"prev_leaf_seq,omitempty"`
	Removed     int   `json:"removed"`
	// Context 是**重算后**的上下文占用（零值 = 未知 → 整键缺席）。
	Context *ContextUsage `json:"context,omitempty"`
	// Files 是本次带文件还原时的结果（含子会话）。
	Files *FileRestoreResult `json:"files,omitempty"`
}

// FileRestoreResult 是文件还原结果（chat.setLeaf / chat.rewind 共用）。
// 覆盖范围含**子会话**的改动（2026-12 用户拍板）：每项带 session_id 标明是谁改的。
type FileRestoreResult struct {
	Restored  []FileRestoreItem `json:"restored,omitempty"`
	Deleted   []FileDeleteItem  `json:"deleted,omitempty"`   // 回滚到「当时还不存在」= 删掉
	Conflicts []FileConflict    `json:"conflicts,omitempty"` // 非空 = 整批未执行（fail-closed）
	Skipped   []FileSkip        `json:"skipped,omitempty"`   // 大文件/二进制：如实报「没抓」
	// ChildSessions 是本次回滚涉及到的子会话（父会话自己不算）。
	// 前端据此显示「本次还原涉及 N 个子 Agent 的改动，它们的对话未回滚」。
	ChildSessions []string `json:"child_sessions,omitempty"`
}

// FileDeleteItem 是一项「回滚 = 删掉这个文件」（它在回滚段里是 agent 新建的）。
type FileDeleteItem struct {
	Path string `json:"path"`
	// SessionID = 谁新建的这个文件（多半是子会话）。
	SessionID string `json:"session_id,omitempty"`
}

// FileRestoreItem 是一项文件还原（写回快照内容）。
type FileRestoreItem struct {
	Path string `json:"path"`
	// FromSeq 是取的是哪个快照的 before（**写它的那个会话**的 seq）。
	FromSeq int64 `json:"from_seq"`
	// FromOwnerSeq 是那次写属于父会话的哪一轮之后。
	FromOwnerSeq int64 `json:"from_owner_seq,omitempty"`
	// SessionID = 写这个文件的那个会话（父会话自己 = 顶层 id；否则是子会话 id）。
	SessionID string `json:"session_id,omitempty"`
	Bytes     int64  `json:"bytes"`
	// Recreated = 磁盘上本来没有，按快照写回（见 §C.5）。
	Recreated bool `json:"recreated,omitempty"`
}

// FileConflict 是一项冲突（磁盘现状与快照期望不符）。
type FileConflict struct {
	Path     string `json:"path"`
	Expected string `json:"expected_hash"`
	Actual   string `json:"actual_hash"`
	// SessionID = 期望状态是**哪个会话**写的（冲突提示要说清「这是子 Agent 改的文件」）。
	SessionID string `json:"session_id,omitempty"`
	Reason    string `json:"reason"`
}

// FileSkip 是一项「当时没抓快照，回滚不到」。
type FileSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
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
	// LeafSeq 是移动后的叶（0 = 空路径）。新客户端用它对齐；老客户端忽略。
	LeafSeq int64 `json:"leaf_seq,omitempty"`
	// Context 是**重算后**的上下文占用，与 chat.done / ChatHistoryResult.context
	// 同一个结构与同一套语义（叶移动后旧数字一定是错的——客户端拿它直接刷新
	// 指示器，不必等下一轮，也不必自己按 -removed 猜）。
	// 未知（纯内存模式 / 本会话还没跑过主轮）时整键缺席：发零值等于显示 0%，
	// 那是编出来的假信息（本仓库既有纪律：未知就显示中性态）。
	Context *ContextUsage `json:"context,omitempty"`
	// Stats 是整段会话统计（口径**不变**：仍折叠整段日志——切分支不改数字，
	// 见拍板项 3）。客户端拿它刷新统计胶囊，与 chat.done 同一份来源。
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

// ChatModelParams 是 chat.model 的参数（切换本会话选中的模型）。
//
// ModelID 是模型**注册表 id**（model.list 的 models[].id）；空 = 参数错误
// （不提供"清空回默认"的隐式语义——那会让选择器传空串时静默回落到 default 角色，
// 用户看到的是"选了但没生效"）。想回默认就显式传 default 角色的模型 id。
type ChatModelParams struct {
	SessionID string `json:"session_id"`
	ModelID   string `json:"model_id"`
}

// ChatModelResult 是 chat.model 的结果：回**注册表里实际生效**的模型 id（服务端
// 校验后的规范化值），客户端据此对齐本地选择态（不自己再猜一遍）。
type ChatModelResult struct {
	SessionID string `json:"session_id,omitempty"`
	ModelID   string `json:"model_id"`
}

// ModelChangedParams 是 chat.modelChanged 事件的载荷（会话级模型变更，多客户端同步）。
// 与 model.changed 的分工：那个是全局注册表/角色绑定变了（客户端重拉 model.list），
// 这个是某条会话换了模型——客户端只更新那条会话的选择态，不动注册表缓存。
type ModelChangedParams struct {
	SessionID string `json:"session_id"`
	ModelID   string `json:"model_id"`
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
