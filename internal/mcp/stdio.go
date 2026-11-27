package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moyunteng/lxcode/internal/winutil"
)

// transport 是一条 MCP 连接的下半层：把一条请求帧送出去、把响应帧收回来。
// 帧的编解码在 rpc.go，这里只管「怎么送」。
//
// 契约（两条传输共同遵守）：
//   - send 返回**属于该 id** 的响应原始字节；不属于的帧（通知、噪音）自己跳过；
//   - 请求/响应是**一问一答**的（同一连接上同时只有一个在飞——见 Client.mu）；
//   - close 之后所有 send 立即失败（不静默重连：连接坏了要让上层看见并重新 Sync）。
type transport interface {
	send(ctx context.Context, req []byte, wantID int64) ([]byte, error)
	// notify 发一条通知（无响应）。
	notify(ctx context.Context, frame []byte) error
	close() error
	// stderrTail 返回子进程 stderr 的尾部（诊断用；HTTP 传输返回空）。
	stderrTail() string
}

// stdioTransport 是 stdio 传输：起一个子进程，用**换行分隔的 JSON-RPC**
// 通信（规范要求帧内不能有换行，所以编码必须紧凑）。
//
// 进程生命周期与连接绑定：进程退出 = 连接失效（上层据此报错并重新 Sync）。
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	errBuf *tailBuffer

	mu      sync.Mutex
	closed  bool
	waitErr error
	done    chan struct{}
	// broken 标记连接已失效：某次请求在等待响应时被取消（ctx.Done）后，读取
	// goroutine 可能仍挂在 stdout 上（等子进程死亡才返回）——此时再起新的
	// 读取就是两个 goroutine 并发读同一个 bufio.Reader（数据竞争）。取消后
	// 置位，send/notify 先检查它、直接报错，不再起新的读取。用 atomic 是
	// 因为 readLine 在 send 持 mu 期间被调用，锁内再锁会死锁。
	broken atomic.Bool
	// killOnce 保证强杀幂等（close 路径与 ctx 取消路径可能先后到达）。
	killOnce sync.Once
}

// maxLineBytes 是单行响应的上限。bufio.Scanner 默认 64KB 会截断大响应
// （tools/list 的工具多时很容易超），所以自己控制缓冲并用 ReadBytes。
const maxLineBytes = 8 << 20

// stderrTailBytes 是保留的 stderr 尾部长度（诊断够用，不会无限增长）。
const stderrTailBytes = 8 << 10

func newStdioTransport(cfg ServerConfig) (*stdioTransport, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, newError(cfg.ID, "transport", "stdio 传输需要 command", nil)
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	// 无窗口：后端在 GUI/服务形态下没有控制台，stdio MCP 服务器若是控制台
	// 程序会往用户桌面弹黑窗（CREATE_NO_WINDOW 让它拿不可见控制台）
	if attr := winutil.NoWindowAttr(); attr != nil {
		cmd.SysProcAttr = attr
	}
	// 非 Windows 让子进程自成进程组，killProcessTreeGroup 的负 pid 组杀才
	// 能覆盖孙进程——否则降级成 p.Kill() 单杀（与 tools.configureProcTree
	// 同一条理由）。Setpgid 不是所有平台都有，故按平台文件分实现。
	configureProcTree(cmd)
	// 环境：继承当前进程 + 服务器自己的配置（用户填的覆盖继承的——
	// 与 mcpServers 事实标准同语义）。
	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, newError(cfg.ID, "transport", "创建 stdin 管道失败", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, newError(cfg.ID, "transport", "创建 stdout 管道失败", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, newError(cfg.ID, "transport", "创建 stderr 管道失败", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, newError(cfg.ID, "transport", "启动 MCP 服务器进程失败", err)
	}
	// 进程残留治理 B1/B5：MCP stdio 服务器是长驻子进程，除 Process.Kill 之外
	// 再入 Job Object——后端被硬杀（任务管理器/SCM 强停）时没人调 close()，
	// Job Object 的 KILL_ON_JOB_CLOSE 让内核代杀（竞态窗口说明见
	// winutil/jobobject_windows.go）。失败只记日志：兜底失败不该让服务器起不来。
	if cmd.Process != nil {
		_ = cmd.Process.WithHandle(func(h uintptr) {
			if err := winutil.AssignToBackendJob(winutil.Handle(h)); err != nil {
				fmt.Fprintf(os.Stderr, "lxcode: MCP 服务器 %s 的 Job Object 兜底未生效: %v\n", cfg.ID, err)
			}
		})
	}

	t := &stdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64<<10),
		errBuf: newTailBuffer(stderrTailBytes),
		done:   make(chan struct{}),
	}
	// stderr 单独收：规范要求日志走 stderr，但现实里有的服务器把日志写
	// stdout——stdout 侧的噪音由 decodeResponse 跳过，stderr 侧留尾部供诊断。
	go func() {
		_, _ = io.Copy(t.errBuf, stderr)
	}()
	go func() {
		t.waitErr = cmd.Wait()
		close(t.done)
	}()
	return t, nil
}

func (t *stdioTransport) send(ctx context.Context, req []byte, wantID int64) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errors.New("连接已关闭")
	}
	if t.broken.Load() {
		return nil, errors.New("连接已失效（上次请求被取消，读取方状态不可信）——请重新连接")
	}
	// 先看进程是不是已经退了（退了就直接报，不必等写失败）。
	select {
	case <-t.done:
		return nil, fmt.Errorf("MCP 服务器进程已退出: %v", t.waitErr)
	default:
	}
	if _, err := t.stdin.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("写入请求失败（服务器进程可能已退出）: %w", err)
	}
	for {
		line, err := t.readLine(ctx)
		if err != nil {
			return nil, err
		}
		resp, ok, rpcErr := decodeResponse(line, wantID)
		if rpcErr != nil {
			return nil, rpcErr
		}
		if !ok {
			continue // 通知/噪音/别人的响应——继续读
		}
		return resp.Result, nil
	}
}

func (t *stdioTransport) notify(_ context.Context, frame []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errors.New("连接已关闭")
	}
	if t.broken.Load() {
		return errors.New("连接已失效（上次请求被取消，读取方状态不可信）——请重新连接")
	}
	if _, err := t.stdin.Write(append(frame, '\n')); err != nil {
		return fmt.Errorf("写入通知失败: %w", err)
	}
	return nil
}

// readLine 读一行，尊重 ctx 取消。
//
// 读 goroutine 泄漏的处理：ctx 取消时**杀掉进程**——被阻塞的 Read 随之返回，
// goroutine 自然结束。不杀的话那个 goroutine 会永远挂在读上（连接也已经不可用）。
func (t *stdioTransport) readLine(ctx context.Context) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := t.reader.ReadBytes('\n')
		ch <- result{line: line, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil && len(r.line) == 0 {
			// 进程退出或管道关闭——把退出原因带上（否则只剩一句 EOF）。
			select {
			case <-t.done:
				return nil, fmt.Errorf("MCP 服务器进程已退出: %v", t.waitErr)
			default:
			}
			return nil, fmt.Errorf("读取响应失败: %w", r.err)
		}
		if len(r.line) > maxLineBytes {
			return nil, fmt.Errorf("单行响应超过 %d 字节上限", maxLineBytes)
		}
		return r.line, nil
	case <-ctx.Done():
		// 取消后读取 goroutine 可能仍挂在 stdout 上（等子进程死亡才返回）——
		// 连接从此不可复用：标记失效，后续 send/notify 拒绝（见 broken 字段注释），
		// 然后杀进程让那个 goroutine 尽快收尾。
		t.broken.Store(true)
		t.kill()
		return nil, ctx.Err()
	}
}

func (t *stdioTransport) close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()

	_ = t.stdin.Close()
	select {
	case <-t.done:
	default:
		t.kill()
		// 等进程真正退（给 3 秒；不退就放弃——Windows 上杀进程不保证
		// 立刻回收，卡在这里会把关闭流程拖死）。
		select {
		case <-t.done:
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

// kill 强杀子进程（幂等）。进程残留治理 B5：只 Process.Kill 杀的是直接子进程，
// stdio MCP 服务器照样可能拉起孙进程（node 服务器 spawn worker）——树杀走
// tools.killProcessTree 同款语义（Windows = taskkill /T，Unix = 进程组 SIGKILL）。
// mcp 包不能 import tools（tools import mcp 会成环）——这里复制那份 15 行的
// 语义而不是引包；两处漂移的代价只是平台降级方式，且兜底仍有 Job Object。
func (t *stdioTransport) kill() {
	t.killOnce.Do(func() {
		if t.cmd.Process == nil {
			return
		}
		if err := killProcessTreeSys(t.cmd.Process); err != nil {
			_ = t.cmd.Process.Kill()
		}
	})
}

// killOnce 字段已挂在 stdioTransport 上（见结构体定义）。

func (t *stdioTransport) stderrTail() string { return t.errBuf.String() }

// tailBuffer 是只保留尾部 N 字节的写入缓冲（stderr 诊断用）。
//
// 不用 bytes.Buffer 直接攒：长时间运行的服务器会刷日志，无界增长会吃内存。
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = b.buf[len(b.buf)-b.max:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}
