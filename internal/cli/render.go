// 事件渲染：把后端推来的协议事件打到终端（与命令处理分开，见 commands.go）。

package cli

import (
	"fmt"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func eventOwnerSession(ev protocol.Response) (string, bool) {
	switch ev.Method {
	case protocol.EventUserMsg, protocol.EventDelta, protocol.EventToolCall, protocol.EventToolRslt,
		protocol.EventConfirm, protocol.EventDone, protocol.EventError, protocol.EventBusy,
		protocol.EventTodo, protocol.EventCompacted, protocol.EventDispatchStart, protocol.EventDispatchEnd:
		var p struct {
			SessionID      string `json:"session_id"`
			OwnerSessionID string `json:"owner_session_id"`
		}
		if unmarshalParams(ev.Params, &p) != nil {
			return "", true
		}
		if p.OwnerSessionID != "" {
			return p.OwnerSessionID, true
		}
		return p.SessionID, true
	default:
		return "", false
	}
}

// render 渲染单个协议事件（持锁防与主循环抢屏）。
func (r *REPL) render(ev protocol.Response) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ev.Method == protocol.EventSessionChanged {
		var p protocol.SessionChangedParams
		if unmarshalParams(ev.Params, &p) != nil || p.ID != r.session {
			return
		}
		fmt.Printf("\n[当前会话状态已变更（%s）]\n", p.Reason)
		return
	}
	if sessionID, scoped := eventOwnerSession(ev); scoped && sessionID != r.session {
		return
	}
	switch ev.Method {
	case protocol.EventUserMsg:
		// 用户消息回显在 busy 提示前打过了，这里不再重复打印
	case protocol.EventDelta:
		var p protocol.DeltaParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		if p.Kind == "text" {
			fmt.Print(p.Text)
		} else if r.showThink {
			fmt.Print("\x1b[2m" + p.Text + "\x1b[0m")
		}
	case protocol.EventToolCall:
		var p protocol.ToolCallParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		fmt.Printf("\n→ %s %s\n", p.Name, clipFirstLine(p.Arguments, 120))
	case protocol.EventToolRslt:
		var p protocol.ToolResultParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		fmt.Printf("↳ %s\n", clipFirstLine(p.Content, 200))
	case protocol.EventConfirm:
		var p protocol.ConfirmRequest
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		r.pending = &p
		fmt.Printf("\n⚠ 需要确认：%s\n[y/n] ", p.Prompt)
	case protocol.EventDone:
		var p protocol.DoneParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		fmt.Println()
		if p.UsageTokens > 0 {
			fmt.Printf("[%s · %d tokens]\n", p.FinishReason, p.UsageTokens)
		}
	case protocol.EventError:
		var p protocol.ErrorParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		fmt.Printf("\n[错误] %s\n", p.Message)
	case protocol.EventBusy:
		var p protocol.BusyParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		r.busy = p.Busy
		if !p.Busy {
			r.pending = nil // 轮次收尾：清确认态（取消也走这）
		}
	case protocol.EventTodo:
		var p protocol.TodoUpdatedParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		fmt.Printf("📋 清单（%d 项）\n", len(p.Items))
		for _, it := range p.Items {
			mark := map[string]string{"done": "✓", "active": "▶", "pending": "·"}[it.Status]
			fmt.Printf("  %s %s\n", mark, it.Content)
		}
	case protocol.EventModels:
		fmt.Println("\n[模型配置已变更，/model 查看]")
	case protocol.EventReady:
		// 连接时单发，初始同步已处理
	}
}
