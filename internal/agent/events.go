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

// ConfirmRequest 是挂起等待裁决的工具调用。
type ConfirmRequest struct {
	ID         string
	Name       string
	Arguments  string
	Prompt     string
	DispatchID string // 非空 = 子 Agent 执行的确认（归属 dispatch 卡）
}

// BusyEvent：忙闲翻转（一轮开始/结束）。
type BusyEvent struct {
	Busy bool
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
