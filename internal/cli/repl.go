// Package cli 是内核的终端宿主：REPL 渲染与输入循环。刻意零依赖、不着色——
// CLI 是内核的验收面（真实端到端跑通），不是最终 UI；桌面壳（Wails/React）
// 才是正式交互形态，到时本包退役为调试入口。
package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moyunteng/myt-harness/internal/agent"
	"github.com/moyunteng/myt-harness/internal/config"
)

// REPL 持有终端会话的全部状态。
type REPL struct {
	sess      *agent.Session
	reg       *config.Registry
	reader    *bufio.Reader
	showThink bool

	mu      sync.Mutex // 输出互斥：事件 goroutine 与主循环不抢屏
	pending map[string]bool
}

// New 构造 REPL（emit 接线在 Run 里，见 startEvents）。
func New(sess *agent.Session, reg *config.Registry) *REPL {
	return &REPL{
		sess:   sess,
		reg:    reg,
		reader: bufio.NewReader(os.Stdin),
	}
}

// Run 进入主循环（阻塞直至退出）。返回值固定 nil（错误就地打印）。
func (r *REPL) Run() error {
	r.startEvents()
	snap := r.sess.History()
	if snap.SessionID != "" {
		fmt.Printf("已恢复会话 %s（%d 条消息）；/new 开新会话\n", snap.SessionID, len(snap.Messages))
	}
	if snap.Pending != nil {
		fmt.Printf("⚠ 有挂起的确认未处理：%s\n", snap.Pending.Prompt)
	}
	r.printPrompt()

	// Ctrl+C：生成中 → 取消当前轮；空闲 → 退出。
	// 信号处理放主循环外（Notify 只装一次），语义在 handler 里区分。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range sig {
			if r.sess.Busy() {
				fmt.Printf("\n[取消当前生成…]\n")
				r.sess.Cancel()
			} else {
				fmt.Printf("\n再见。\n")
				os.Exit(0)
			}
		}
	}()

	for {
		line, err := r.reader.ReadString('\n')
		if err != nil {
			// EOF（Ctrl+Z / 管道结束）：取消在途生成并等它收尾——
			// 直接走人会丢"已生成但未落事件"的部分内容
			if r.sess.Busy() {
				fmt.Println("\n[输入结束，等待在途生成收尾…]")
				r.sess.Cancel()
				for r.sess.Busy() {
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
		if pending := r.sess.History().Pending; pending != nil {
			switch strings.ToLower(line) {
			case "y", "yes", "是":
				if err := r.sess.Confirm(pending.ID, true); err != nil {
					fmt.Printf("[%v]\n", err)
				}
			case "n", "no", "否":
				if err := r.sess.Confirm(pending.ID, false); err != nil {
					fmt.Printf("[%v]\n", err)
				}
			default:
				fmt.Println("[等待确认中：y 允许 / n 拒绝]")
			}
			r.printPrompt()
			continue
		}
		if r.sess.Busy() {
			fmt.Println("[生成中——先等待或 Ctrl+C 取消；斜杠命令仍可用]")
			r.printPrompt()
			continue
		}
		if err := r.sess.Send(line); err != nil {
			fmt.Printf("[发送失败] %v\n", err)
			r.printPrompt()
			continue
		}
		// Send 异步；事件回调里渲染。主循环继续读输入（busy 拦截在下一轮）
		r.printPrompt()
	}
}

// startEvents 把内核事件接到终端渲染。
func (r *REPL) startEvents() {
	r.sess.SetEmitter(func(ev agent.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch e := ev.(type) {
		case agent.DeltaEvent:
			if e.Kind == "text" {
				fmt.Print(e.Text)
			} else if r.showThink {
				// 思考链 dim 不可用（零依赖），用 ANSI 2m 尽力
				fmt.Print("\x1b[2m" + e.Text + "\x1b[0m")
			}
		case agent.ToolCallEvent:
			fmt.Printf("\n→ %s %s\n", e.Name, clipStr(e.Arguments, 120))
		case agent.ToolResultEvent:
			fmt.Printf("↳ %s\n", clipStr(e.Content, 200))
		case agent.ConfirmRequestEvent:
			fmt.Printf("\n⚠ 需要确认：%s\n[y/n] ", e.Request.Prompt)
		case agent.TurnDoneEvent:
			fmt.Println()
			if e.UsageTokens > 0 {
				fmt.Printf("[%s · %d tokens]\n", e.FinishReason, e.UsageTokens)
			}
		case agent.TurnErrorEvent:
			fmt.Printf("\n[错误] %s\n", e.Message)
		case agent.TodoUpdatedEvent:
			fmt.Printf("📋 清单（%d 项）\n", len(e.Items))
			for _, it := range e.Items {
				mark := map[string]string{"done": "✓", "active": "▶", "pending": "·"}[it.Status]
				fmt.Printf("  %s %s\n", mark, it.Content)
			}
		}
	})
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
		if _, err := r.sess.SwitchNew(); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		fmt.Println("[新会话已开始]")
	case "sessions":
		list := r.sess.SessionList()
		if len(list) == 0 {
			fmt.Println("[没有历史会话]")
			return false
		}
		for i, m := range list {
			current := ""
			if m.ID == r.sess.SessionID() {
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
		list := r.sess.SessionList()
		if n, err := strconv.Atoi(rest); err == nil && n >= 1 && n <= len(list) {
			id = list[n-1].ID
		}
		if err := r.sess.SwitchTo(id); err != nil {
			fmt.Printf("[%v]\n", err)
			return false
		}
		h := r.sess.History()
		fmt.Printf("[已恢复 %s（%d 条消息）]\n", id, len(h.Messages))
	case "model":
		bindings := r.reg.RoleBindings()
		fmt.Printf("default → %s\n", orDash(bindings[config.RoleDefault]))
		fmt.Printf("vision  → %s\n", orDash(bindings[config.RoleVision]))
		for _, m := range r.reg.List() {
			mark := " "
			if bindings[config.RoleDefault] == m.ID {
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
	if r.sess.Busy() {
		return fmt.Errorf("生成中不能切换会话，先 Ctrl+C 取消")
	}
	if r.sess.History().Pending != nil {
		return fmt.Errorf("有挂起的确认，先处理（y/n）")
	}
	return nil
}

func (r *REPL) printPrompt() {
	if r.sess.Busy() {
		fmt.Print("… ")
	} else if r.sess.History().Pending != nil {
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
