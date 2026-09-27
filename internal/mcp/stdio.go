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
	"time"
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

// kill 强杀子进程（幂等）。
func (t *stdioTransport) kill() {
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
}

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
