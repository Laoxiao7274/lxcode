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
type DeltaEvent struct {
	Kind string
	Text string
}

// ToolCallEvent：模型发起了工具调用（执行前；确认门在其后）。
type ToolCallEvent struct {
	ID        string
	Name      string
	Arguments string
}

// ToolResultEvent：工具执行完成（IsError = 拒绝/失败）。
type ToolResultEvent struct {
	ID      string
	Name    string
	Content string
	IsError bool
}

// ConfirmRequestEvent：高危工具等待人工裁决（宿主弹确认卡并回 Confirm）。
type ConfirmRequestEvent struct {
	Request *ConfirmRequest
}

// ConfirmRequest 是挂起等待裁决的工具调用。
type ConfirmRequest struct {
	ID        string
	Name      string
	Arguments string
	Prompt    string
}

// BusyEvent：忙闲翻转（一轮开始/结束）。
type BusyEvent struct {
	Busy bool
}

// TurnDoneEvent：一轮生成的最终消息（含 usage/finish）。
type TurnDoneEvent struct {
	Message      llm.Message
	UsageTokens  int
	FinishReason string
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

func (UserMsgEvent) isEvent()        {}
func (DeltaEvent) isEvent()          {}
func (ToolCallEvent) isEvent()       {}
func (ToolResultEvent) isEvent()     {}
func (ConfirmRequestEvent) isEvent() {}
func (BusyEvent) isEvent()           {}
func (TurnDoneEvent) isEvent()       {}
func (TurnErrorEvent) isEvent()      {}
func (TodoUpdatedEvent) isEvent()    {}

// Snapshot 是宿主初始化/重连时的会话同步载荷（History 的返回值）。
type Snapshot struct {
	Messages  []llm.Message
	Busy      bool
	Pending   *ConfirmRequest
	SessionID string
	Todos     []tools.TodoItem
}
