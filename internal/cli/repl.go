// Package cli 是后端的终端客户端：REPL 渲染与输入循环（经 WS JSON-RPC
// 与后端通信）。刻意零依赖、不着色——CLI 是验收面（真实端到端跑通），
// 不是最终 UI；Windows 桌面壳（规划中）才是正式交互形态。
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/wsclient"
)

// REPL 持有终端客户端的全部状态。busy/pending 从后端事件流同步——
// 后端才是会话状态的唯一事实来源（多客户端一致）。
type REPL struct {
	be        wsclient.Backend
	reader    *bufio.Reader
	showThink bool

	mu      sync.Mutex // 输出互斥：事件 goroutine 与主循环不抢屏
	busy    bool
	pending *protocol.ConfirmRequest
	session string
}

// New 构造 REPL（连接后端 be）。
func New(be wsclient.Backend) *REPL {
	return &REPL{be: be, reader: bufio.NewReader(os.Stdin)}
}

// Run 进入主循环（阻塞直至退出）。返回值固定 nil（错误就地打印）。
func (r *REPL) Run() error {
	// 初始同步：chat.history（后端可能已在服务（别的客户端聊过/恢复的会话））
	if err := r.syncHistory(); err != nil {
		fmt.Printf("[同步会话失败] %v\n", err)
	}
	r.startEvents()
	r.printPrompt()

	// Ctrl+C：生成中 → 取消当前轮；空闲 → 退出。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range sig {
			if r.isBusy() {
				fmt.Printf("\n[取消当前生成…]\n")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = r.be.Call(ctx, protocol.MethodChatCancel, nil, nil)
				cancel()
			} else {
				fmt.Printf("\n再见。\n")
				r.be.Close()
				os.Exit(0)
			}
		}
	}()

	for {
		line, err := r.reader.ReadString('\n')
		if err != nil {
			// EOF（Ctrl+Z / 管道结束）：取消在途生成并等它收尾——
			// 直接走人会丢"已生成但未落事件"的部分内容
			if r.isBusy() {
				fmt.Println("\n[输入结束，等待在途生成收尾…]")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = r.be.Call(ctx, protocol.MethodChatCancel, nil, nil)
				cancel()
				for r.isBusy() {
					time.Sleep(50 * time.Millisecond)
				}
			}
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			r.printPrompt()
			continue
		}
		if strings.HasPrefix(line, "/") {
			if r.runCommand(line) {
				return nil // /quit
			}
			r.printPrompt()
			continue
		}
		// 挂起确认时 y/n 裁决（其他输入提示但不吞——保留用户重新看的余地）
		if pending := r.getPending(); pending != nil {
			switch strings.ToLower(line) {
			case "y", "yes", "是":
				r.confirm(pending.ID, true)
			case "n", "no", "否":
				r.confirm(pending.ID, false)
			default:
				fmt.Println("[等待确认中：y 允许 / n 拒绝]")
			}
			r.printPrompt()
			continue
		}
		if r.isBusy() {
			fmt.Println("[生成中——先等待或 Ctrl+C 取消；斜杠命令仍可用]")
			r.printPrompt()
			continue
		}
		if err := r.send(line); err != nil {
			fmt.Printf("[发送失败] %v\n", err)
		}
		r.printPrompt()
	}
}

// startEvents 把后端事件接到终端渲染。
func (r *REPL) startEvents() {
	go func() {
		for ev := range r.be.Events() {
			r.render(ev)
		}
		// 事件流关闭 = 后端断开（崩溃/被杀）
		fmt.Printf("\n[后端连接断开——退出]\n")
		os.Exit(1)
	}()
}

// render 渲染单个协议事件（持锁防与主循环抢屏）。
func (r *REPL) render(ev protocol.Response) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
		fmt.Printf("\n→ %s %s\n", p.Name, clipStr(p.Arguments, 120))
	case protocol.EventToolRslt:
		var p protocol.ToolResultParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		fmt.Printf("↳ %s\n", clipStr(p.Content, 200))
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
	case protocol.EventSessionChanged:
		// 会话被切换（本端或另一客户端）：重拉历史同步视图
		var p protocol.SessionChangedParams
		if unmarshalParams(ev.Params, &p) != nil {
			return
		}
		fmt.Printf("\n[会话已切换（%s）]\n", p.Reason)
		_ = r.syncHistory()
	case protocol.EventModels:
		fmt.Println("\n[模型配置已变更，/model 查看]")
	case protocol.EventReady:
		// 连接时单发，初始同步已处理
	}
}

// send 发送一条消息。
func (r *REPL) send(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.be.Call(ctx, protocol.MethodChatSend, protocol.ChatSendParams{Text: text}, nil)
}

// confirm 裁决确认门。
func (r *REPL) confirm(id string, allow bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.be.Call(ctx, protocol.MethodToolConfirm, protocol.ToolConfirmParams{ID: id, Allow: allow}, nil); err != nil {
		fmt.Printf("[%v]\n", err)
	}
}

// syncHistory 拉取会话快照并同步本地状态。
func (r *REPL) syncHistory() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var hist protocol.ChatHistoryResult
	if err := r.be.Call(ctx, protocol.MethodChatHistory, nil, &hist); err != nil {
		return err
	}
	r.mu.Lock()
	r.busy = hist.Busy
	r.pending = hist.Pending
	r.session = hist.SessionID
	r.mu.Unlock()
	if hist.SessionID != "" && len(hist.Messages) > 0 {
		fmt.Printf("当前会话 %s（%d 条消息）；/new 开新会话\n", hist.SessionID, len(hist.Messages))
	}
	if hist.Pending != nil {
		fmt.Printf("⚠ 有挂起的确认未处理：%s\n[y/n] ", hist.Pending.Prompt)
	}
	return nil
}

// runCommand 处理斜杠命令；返回 true 表示退出。
func (r *REPL) runCommand(line string) bool {
	fields := strings.Fields(line)
	cmd := strings.TrimPrefix(fields[0], "/")
	rest := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))

	switch cmd {
	case "quit", "exit", "q":
		return true
	case "new":
		if err := r.requireIdle(); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.be.Call(ctx, protocol.MethodSessionNew, nil, nil); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		fmt.Println("[新会话已开始]")
	case "sessions":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var list []protocol.SessionMeta
		if err := r.be.Call(ctx, protocol.MethodSessionList, nil, &list); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		if len(list) == 0 {
			fmt.Println("[没有历史会话]")
			return false
		}
		r.mu.Lock()
		cur := r.session
		r.mu.Unlock()
		for i, m := range list {
			current := ""
			if m.ID == cur {
				current = " ←当前"
			}
			fmt.Printf("  %d. %s  %s  %d条  %s%s\n", i+1, m.ID, m.UpdatedAt, m.Messages, m.Title, current)
		}
		fmt.Println("[/resume <序号或id> 恢复]")
	case "resume":
		if err := r.requireIdle(); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		if rest == "" {
			fmt.Println("[用法: /resume <序号或会话id>；先用 /sessions 查看]")
			return false
		}
		id := rest
		// 序号 → id（重拉列表做映射）
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var list []protocol.SessionMeta
		if err := r.be.Call(ctx, protocol.MethodSessionList, nil, &list); err == nil {
			if n, err := strconv.Atoi(rest); err == nil && n >= 1 && n <= len(list) {
				id = list[n-1].ID
			}
		}
		cancel()
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.be.Call(ctx, protocol.MethodSessionResume, protocol.SessionResumeParams{ID: id}, nil); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
	case "compact":
		// 手动压缩：不受阈值约束（空闲即可）——长会话撞窗口前的主动手段
		if err := r.requireIdle(); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var res protocol.CompactResult
		if err := r.be.Call(ctx, protocol.MethodChatCompact, protocol.CompactParams{}, &res); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		if !res.Compacted {
			fmt.Printf("[%s]\n", orDash(res.Reason))
			return false
		}
		fmt.Printf("[已压缩 %d 条历史：%d → %d tokens]\n", res.Shadowed, res.Before, res.After)
	case "model":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var ml protocol.ModelListResult
		if err := r.be.Call(ctx, protocol.MethodModelList, nil, &ml); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		fmt.Printf("default → %s\n", orDash(ml.Roles["default"]))
		fmt.Printf("vision  → %s\n", orDash(ml.Roles["vision"]))
		for _, m := range ml.Models {
			mark := " "
			if ml.Roles["default"] == m.ID {
				mark = "*"
			}
			fmt.Printf(" %s %s  %s  %s  %s\n", mark, m.ID, m.Model, m.EffectiveFormat(), enabledText(m.Enabled))
		}
	case "think":
		r.showThink = !r.showThink
		fmt.Printf("[思考链显示: %v]\n", r.showThink)
	case "help", "":
		fmt.Print(`命令：
  /new            开新会话（旧的保留可 resume）
  /sessions       列出历史会话
  /resume <n|id>  恢复历史会话
  /compact        压缩早期历史（长会话撞窗口前主动压一次）
  /model          查看模型与角色绑定
  /think          切换思考链显示（默认隐藏）
  /quit           退出
生成中 Ctrl+C 取消当前轮；空闲 Ctrl+C 退出。
`)
	default:
		fmt.Printf("[未知命令 /%s；/help 查看可用命令]\n", cmd)
	}
	return false
}

// requireIdle 切换会话前置检查（busy/挂起确认时拒绝）。
func (r *REPL) requireIdle() error {
	if r.isBusy() {
		return fmt.Errorf("生成中不能切换会话，先 Ctrl+C 取消")
	}
	if r.getPending() != nil {
		return fmt.Errorf("有挂起的确认，先处理（y/n）")
	}
	return nil
}

func (r *REPL) isBusy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy
}

func (r *REPL) getPending() *protocol.ConfirmRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

func (r *REPL) printPrompt() {
	if r.isBusy() {
		fmt.Print("… ")
	} else if p := r.getPending(); p != nil {
		fmt.Print("确认[y/n] ")
	} else {
		fmt.Print("> ")
	}
}

// clipStr 截断展示（首行优先，rune 安全）。
func clipStr(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// unmarshalParams 把事件载荷解到目标结构（协议 Params 是 any）。
func unmarshalParams(params any, v any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func orDash(s string) string {
	if s == "" {
		return "（未绑定）"
	}
	return s
}

func enabledText(b bool) string {
	if b {
		return "启用"
	}
	return "停用"
}
