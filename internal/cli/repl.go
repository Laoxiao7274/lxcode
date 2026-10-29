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
	if err := r.openInitialSession(); err != nil {
		fmt.Printf("[打开会话失败] %v\n", err)
	}
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
				_ = r.be.Call(ctx, protocol.MethodChatCancel, protocol.ChatSessionParams{SessionID: r.currentSessionID()}, nil)
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
				_ = r.be.Call(ctx, protocol.MethodChatCancel, protocol.ChatSessionParams{SessionID: r.currentSessionID()}, nil)
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

// send 发送一条消息。
func (r *REPL) send(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.be.Call(ctx, protocol.MethodChatSend, protocol.ChatSendParams{SessionID: r.currentSessionID(), Text: text}, nil)
}

// confirm 裁决确认门。
func (r *REPL) confirm(id string, allow bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.be.Call(ctx, protocol.MethodToolConfirm, protocol.ToolConfirmParams{SessionID: r.currentSessionID(), ID: id, Allow: allow}, nil); err != nil {
		fmt.Printf("[%v]\n", err)
	}
}

// openInitialSession 恢复最近的非归档会话；没有历史时创建空会话。
func (r *REPL) openInitialSession() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var sessions []protocol.SessionMeta
	if err := r.be.Call(ctx, protocol.MethodSessionList, nil, &sessions); err != nil {
		return err
	}
	for _, session := range sessions {
		if session.Archived || session.Messages == 0 {
			continue
		}
		if err := r.be.Call(ctx, protocol.MethodSessionResume, protocol.SessionResumeParams{ID: session.ID}, nil); err != nil {
			return err
		}
		r.setCurrentSession(session.ID)
		return nil
	}
	var result protocol.SessionResult
	if err := r.be.Call(ctx, protocol.MethodSessionNew, protocol.SessionNewParams{}, &result); err != nil {
		return err
	}
	if result.SessionID == "" {
		return fmt.Errorf("后端未返回新会话 id")
	}
	r.setCurrentSession(result.SessionID)
	return nil
}

func (r *REPL) currentSessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.session
}

func (r *REPL) setCurrentSession(id string) {
	r.mu.Lock()
	r.session = id
	r.busy = false
	r.pending = nil
	r.mu.Unlock()
}

// syncHistory 拉取会话快照并同步本地状态。
func (r *REPL) syncHistory() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var hist protocol.ChatHistoryResult
	if err := r.be.Call(ctx, protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: r.currentSessionID()}, &hist); err != nil {
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

// clipFirstLine 截断展示（首行优先，rune 安全）。
func clipFirstLine(s string, max int) string {
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
