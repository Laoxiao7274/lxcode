package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestCancelActiveSessionsKillsForegroundCommand（进程残留治理 B2）：
// 会话正在跑前台长命令（工具循环里），优雅退出路径 CancelActiveSessions
// 取消该轮并等到收尾——命令进程被树杀，不残留。
//
// 编排：假 stream 第一轮发 bash 工具调用（命令起一个**可精确定位的子进程**），
// 第二轮（带工具结果）挂住等取消——模拟「工具跑着、流没出字」。工具执行期间
// 命令进程存在；CancelActiveSessions 后断言该轮收尾、命令进程已死。
//
// 探活判据的三条纪律（2026-12 返工，tester 的红项就是原版误报）：
//   - **按精确 PID 探活，不按进程名全机 grep**——原版 lingeringProcs() 扫
//     tasklist 找 "sleep.exe"，本机环境常有无关孤儿 sleep（常驻轮询循环 +
//     MSYS2 孤儿语义污染进程表），探活命中即误报失败；
//   - **PID 必须是 Windows 真实 pid，不是 MSYS pid**——Git Bash 里 `$$` 给的
//     是 cygwin/msys 虚拟 pid（ps 可见、tasklist 找不到；本机实测 $$=382641
//     对应 WINPID=29424）。所以命令主体用 PowerShell：`$PID` 是 Windows 真实
//     pid，探活与被探同一套 pid 空间；
//   - **编排不能等工具结果**——命令真在跑时工具结果永远不会来（上一版等
//     「第二轮启动」就卡死在这）。改为命令把 `$PID` 写进 **marker 文件**：
//     测试轮询 marker 拿到 PID → 精确探活确认它在跑 → 取消 → 断言它死透。
//     这是「前台命令跑到一半被树杀」的真实验证，不需要编造探活目标。
//
// 命令主体 = ping -n 60 127.0.0.1（Windows 真实可执行，不在 Git Bash 的
// /usr/bin——MSYS2 会把退出的 sleep.exe 留在进程表里当孤儿，ping 没这个
// 问题）。消息发给 **coder**（种子白名单含 bash）+ 请求级 approval=auto
// ——默认语境是 main（只派活不亲自执行，白名单没有 bash），发 main 的话
// bash 调用会被白名单防御拒绝；而新会话的初始档 = behavior 默认档（测试里
// 未装配 = confirm），confirm 档下 bash 会弹确认门挂住——编排同样走不到
// 树杀断言。请求级 auto 让这一轮完全无确认（2026-10 拍板的语义），命令直接
// 进执行。
func TestCancelActiveSessionsKillsForegroundCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "foreground.pid")
	longCmd := `powershell -NoProfile -Command "Set-Content -LiteralPath '` + marker +
		`' -Value ('PID=' + \$PID); ping -n 60 127.0.0.1 | Out-Null"`

	secondRound := make(chan struct{})
	srv, client, _ := newTestServer(t, func(ctx context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		// 参数经 json.Marshal 构造（命令含双引号，手拼 JSON 会产出非法参数）
		args, _ := json.Marshal(map[string]any{"command": longCmd, "timeout_sec": 300})
		bashCall := llm.ToolCall{ID: "call-1"}
		bashCall.Function.Name = "bash"
		bashCall.Function.Arguments = string(args)
		go func() {
			defer close(ch)
			if msgs[len(msgs)-1].Role == "user" {
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: bashCall}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					FinishReason: llm.FinishToolCalls,
					Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{bashCall}},
				}}
				return
			}
			// 第二轮（带工具结果）：挂住等取消——模拟「工具跑着、流还没出字」
			close(secondRound)
			<-ctx.Done()
		}()
		return ch, nil
	})

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		Text: "跑个长命令", Agent: "coder", Approval: protocol.ApprovalAuto})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}

	// 等命令真正跑起来：轮询 marker 文件（PowerShell 启动 + bash 拉起有延迟）。
	// 编排失败要当场暴露（白名单拒绝/确认门被拒时 marker 永远不来）——不编
	// 一个探活目标硬探。
	pid := waitMarkerPid(t, marker, 15*time.Second)
	if pid <= 0 {
		t.Fatal("marker 文件没等到 PID=<n>——命令没被执行（检查白名单/确认门/命令体）")
	}
	// 前置自检：取消之前目标进程必须活着（探活判据本身可用）
	if !pidAlive(pid) {
		t.Fatalf("PID=%d 在取消之前就探不到了——探活判据或命令体失效", pid)
	}

	// 优雅退出路径：取消全部活跃会话并等收尾
	start := time.Now()
	srv.CancelActiveSessions()
	if el := time.Since(start); el > 15*time.Second {
		t.Fatalf("CancelActiveSessions 应在会话收尾后返回（耗时 %v）", el)
	}
	// 收尾后该会话不应再 busy
	if srv.busySessions() != nil {
		t.Fatalf("CancelActiveSessions 后不应再有 busy 会话: %v", srv.busySessions())
	}

	// 命令进程已死：按精确 pid 探活（给进程退出后的回收一个短窗口）。
	probeDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(probeDeadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("前台命令（PID=%d）在 CancelActiveSessions 后仍存活——树杀未生效", pid)
}

// waitMarkerPid 轮询 marker 文件直到出现「PID=<n>」行（命令的 PowerShell 把
// 自己的 Windows pid 写进去）。超时返回 0（调用方 Fatal）。
func waitMarkerPid(t *testing.T, marker string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(marker)
		if err == nil {
			if i := strings.Index(string(raw), "PID="); i >= 0 {
				s := strings.TrimSpace(string(raw[i+4:]))
				if n, err := strconv.Atoi(s); err == nil && n > 0 {
					return n
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0
}

// pidAlive 按精确 pid 探活。Windows: tasklist /FI "PID eq N"（探测失败按
// 「不存活」处理，与 jobs.processAlive 同一保守口径）；Unix: kill -0。
// SysProcAttr 经平台 helper（procProbeAttr，见下方两个构建文件）——
// HideWindow 是 Windows 专属字段，直写会让非 Windows 构建编译失败。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		probe := exec.Command("tasklist", "/NH", "/FO", "CSV", "/FI", "PID eq "+strconv.Itoa(pid))
		probe.SysProcAttr = procProbeAttr()
		out, err := probe.Output()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	// Unix：kill -0 探测（失败=进程不存在）
	probe := exec.Command("kill", "-0", strconv.Itoa(pid))
	if err := probe.Run(); err != nil {
		return false
	}
	return true
}
