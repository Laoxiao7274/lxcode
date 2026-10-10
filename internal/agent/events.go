package agent

import (
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// Event 是内核推给宿主的类型化事件。宿主（CLI/桌面壳）按类型渲染：
// 流式增量、工具卡片、确认卡、todo 清单、轮次收尾。
//
// 为什么是 sealed interface 而不是一个大 struct：事件种类还会增长
// （投屏、审批扩展），接口让 switch 编译期穷尽——加新事件忘了处理某个
// 消费点是编译错误，不是运行时漏渲染。
type Event interface{ isEvent() }

// UserMsgEvent：用户消息入会话（宿主渲染自己的消息，回显确认）。
type UserMsgEvent struct {
	Message llm.Message
}

// DeltaEvent：流式增量。Kind = "text"（正文）| "reasoning"（思考链）。
// DispatchID 非空 = 子 Agent 执行的增量（前端归属进 dispatch 卡）。
type DeltaEvent struct {
	Kind       string
	Text       string
	DispatchID string
}

// ToolCallEvent：模型发起了工具调用（执行前；确认门在其后）。
// DispatchID 非空 = 子 Agent 的调用。
type ToolCallEvent struct {
	ID         string
	Name       string
	Arguments  string
	DispatchID string
}

// ToolResultEvent：工具执行完成（IsError = 拒绝/失败）。
// DispatchID 非空 = 子 Agent 的结果。
type ToolResultEvent struct {
	ID         string
	Name       string
	Content    string
	IsError    bool
	DispatchID string
}

// ConfirmRequestEvent：高危工具等待人工裁决（宿主弹确认卡并回 Confirm）。
// Request.DispatchID 非空 = 子 Agent 的确认（前端把卡放进 dispatch 卡内）。
type ConfirmRequestEvent struct {
	Request *ConfirmRequest
}

// ConfirmRequest 是挂起等待裁决的请求。两种形态：
//   - Kind == ""（默认）：高危工具确认——用户批准/拒绝；
//   - Kind == "ask"：ask_user 的提问——用户以文本回答（Answer），或跳过。
type ConfirmRequest struct {
	ID         string
	Name       string
	Arguments  string
	Prompt     string
	DispatchID string // 非空 = 子 Agent 执行的确认（归属 dispatch 卡）
	// Kind 标记形态：空 = 高危工具确认（现状语义）；"ask" = ask_user 的提问。
	Kind string
	// Options 是 ask 提问的预设答案（可空）——前端渲染成可直接点选的选项按钮。
	Options []string
}

// BusyEvent：忙闲翻转（一轮开始/结束）。
// DispatchID 非空 = 子会话的忙闲（2026-10-10 起上抛：前端按归属投进子会话
// 自己的 state——子会话页的「生成中」行与停止钮靠它出现；主时间线侧按
// dispatchId 过滤，主会话的忙闲不被子会话翻动）。
type BusyEvent struct {
	Busy bool
	// DispatchID 非空 = 这是子会话的忙闲翻转（归属键 = dispatch 卡 id，
	// 与 DeltaEvent/ToolCallEvent 同一套）。
	DispatchID string
}

// TurnDoneEvent：一轮生成的最终消息（含 usage/finish）。
// DispatchID 非空 = 子 Agent 轮完成（主轮的 busy 不翻转）。
// Context 是本轮请求后的上下文占用测量（主轮才有——子轮的占用不进主指示器）。
type TurnDoneEvent struct {
	Message      llm.Message
	UsageTokens  int
	FinishReason string
	DispatchID   string
	Context      ContextUsage
}

// TurnErrorEvent：一轮以错误收尾（Aborted = 用户取消，已生成部分在 Partial）。
type TurnErrorEvent struct {
	Message string
	Aborted bool
	Partial *llm.Message
}

// TodoUpdatedEvent：任务清单更新（宿主渲染 TodoList 卡片）。
type TodoUpdatedEvent struct {
	Items []tools.TodoItem
}

// SessionStartedEvent：会话行懒建完成（首条消息落库时）——宿主据此刷新
// 会话列表（「发消息 → 新对话出现在侧栏」的信号）。
type SessionStartedEvent struct {
	ID string
}

// FileChange 是一个文件的改动摘要（产物视图——验收「这轮改了什么」）。
type FileChange struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
	Diff    string `json:"diff"`
}

// FilesChangedEvent：一轮里文件改动的汇总（轮结束时发一次；宿主渲染
// 产物卡——Codex 的验收视图。纯读/纯聊的轮次不发）。
type FilesChangedEvent struct {
	Files []FileChange
}

// DispatchStartEvent：主 Agent 把任务派给子 Agent（M3——子上下文隔离
// 的开端）。宿主渲染 dispatch 卡（子执行的事件按 DispatchID 归属进卡）。
// SessionID = 子会话 id（2026-09-22 起子 Agent 是独立会话：可续跑、可回放）。
type DispatchStartEvent struct {
	DispatchID string
	SessionID  string
	AgentID    string
	AgentName  string
	AgentColor string
	Task       string
}

// DispatchEndEvent：子 Agent 执行收尾（Result = 最终回复——主 Agent 的
// 验收输入；IsError = 子执行以错误收尾）。SessionID = 子会话 id（续跑用）。
type DispatchEndEvent struct {
	DispatchID  string
	SessionID   string
	Result      string
	IsError     bool
	UsageTokens int
}

// CompactedEvent：一次压缩事务收尾（历史的前缀已被摘要检查点替换）。
// 宿主广播给客户端（前端插一条「已压缩历史」标记），Manual = 用户主动触发。
// DispatchID 非空 = **子会话自己的压缩**（归属进 dispatch 卡，不进主时间线）。
type CompactedEvent struct {
	Result     CompactResult
	Manual     bool
	DispatchID string
}

// RewoundEvent：一次撤回收尾（历史里 seq 及其之后的消息已从内存与库中删除）。
// 宿主广播给客户端（前端据此截断时间线、刷新上下文指示器）——与 CompactedEvent
// 同款：这是一次**改变历史**的事务收尾，客户端必须收到通知而不是自己猜。
type RewoundEvent struct {
	Seq     int64
	Removed int
	// Context 是**重算后**的上下文占用：撤回删掉了一段历史，旧数字一定是错的，
	// 客户端拿它直接刷新指示器而不必等下一轮。零值 = 未知（纯内存模式 / 本会话
	// 还没跑过主轮），wire 上整键缺席——客户端显示中性态而不是编一个数
	//（与 chat.done / ChatHistoryResult 的 context 同一套语义）。
	Context ContextUsage
}

func (UserMsgEvent) isEvent()        {}
func (DeltaEvent) isEvent()          {}
func (ToolCallEvent) isEvent()       {}
func (ToolResultEvent) isEvent()     {}
func (ConfirmRequestEvent) isEvent() {}
func (BusyEvent) isEvent()           {}
func (TurnDoneEvent) isEvent()       {}
func (TurnErrorEvent) isEvent()      {}
func (TodoUpdatedEvent) isEvent()    {}
func (FilesChangedEvent) isEvent()   {}
func (SessionStartedEvent) isEvent() {}
func (DispatchStartEvent) isEvent()  {}
func (DispatchEndEvent) isEvent()    {}
func (CompactedEvent) isEvent()      {}
func (RewoundEvent) isEvent()        {}

// Snapshot 是宿主初始化/重连时的会话同步载荷（History 的返回值）。
type Snapshot struct {
	Messages  []llm.Message
	Busy      bool
	Pending   *ConfirmRequest
	SessionID string
	Todos     []tools.TodoItem
	// Context 是最近一次主轮请求的上下文占用（零值 = 未知——刚切会话或
	// 后端刚重启，客户端应显示中性态）。
	Context ContextUsage
	// Checkpoints 是压缩检查点在 Messages 里的下标（前端把这些消息渲染成
	// 「已压缩历史」块，而不是用户气泡——检查点内容是普通 user 消息，
	// 没有类型字段可依赖，所以按内容标记识别）。
	Checkpoints []int
}
