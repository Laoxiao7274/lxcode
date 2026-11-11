// logs.tail 域（桌面错误日志查看器——2026-12）：读后端当前日志文件的尾部。
//
// 装配契约：cmd/lxcode 的 runServe 在所有 serve 形态（服务/控制台/壳内直跑）
// 都走 setupFileLogging（io.MultiWriter：文件 + stderr 回显），日志路径由它
// 经 SetLogPath 注入这里。未装配（内存测试模式）= 明确报「日志不可用」，
// 不猜路径、不回空串假装成功。
//
// 只读当前文件——轮转掉的旧文件（.1/.2/.3）不带出：用户要看的是「最近发生
// 了什么」，轮转文件的存在性由落盘目录自己说明。
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// defaultLogsTailBytes 是 logs.tail 的缺省读取量（参数 bytes 缺省时）。
// 16KB ≈ 最近两三百条日志，错误排查足够；更大由前端显式请求。
const defaultLogsTailBytes = 16 << 10

// maxLogsTailBytes 是单次读取的上限（协议面上的防御：调用方传 1GB 不照办）。
const maxLogsTailBytes = 1 << 20

// SetLogPath 装配当前日志路径（runServe 装配期一次；空 = 未装配）。
func (s *Server) SetLogPath(path string) {
	s.logMu.Lock()
	s.logPath = path
	s.logMu.Unlock()
}

// logPathOf 返回当前日志路径（未装配 = 空串）。
func (s *Server) logPathOf() string {
	s.logMu.RLock()
	defer s.logMu.RUnlock()
	return s.logPath
}

// dispatchLogs 是 logs.* 方法域（未命中回落 nil——§5 坑 14 的纪律）。
func (s *Server) dispatchLogs(req *protocol.Request, params json.RawMessage) *protocol.Response {
	if req.Method != protocol.MethodLogsTail {
		return nil
	}
	var p protocol.LogsTailParams
	if err := json.Unmarshal(params, &p); err != nil {
		return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
	}
	res, err := s.logsTail(p.Bytes)
	if err != nil {
		return protocol.NewError(req.ID, protocol.CodeInternal, err.Error())
	}
	return protocol.NewResult(req.ID, res)
}

// logsTail 读日志文件尾部。bytes <= 0 = 缺省 16KB；超上限钳到上限。
//
// 实现：文件有界（16MB 轮转上限），直接 io.ReadAll 的最坏情况 ~16MB 只发生在
// 调用方显式要满轮转大小时——可接受；缺省路径只读尾部 16KB。Truncated 以
// Stat 的大小判定（读前一次判定，不做读后截半的二次裁剪）。
func (s *Server) logsTail(bytes int) (protocol.LogsTailResult, error) {
	path := s.logPathOf()
	if path == "" {
		return protocol.LogsTailResult{}, fmt.Errorf("日志不可用（文件日志未启用）")
	}
	if bytes <= 0 {
		bytes = defaultLogsTailBytes
	}
	if bytes > maxLogsTailBytes {
		bytes = maxLogsTailBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return protocol.LogsTailResult{}, fmt.Errorf("打开日志失败: %w", err)
	}
	defer f.Close()
	// 从尾部偏移 bytes 处读起。文件比 bytes 小（或相等）时从头读（Seek 负偏移
	// 越过文件头在 Windows 上会报错——先 Stat 判大小，不赌 Seek 的平台语义）。
	st, err := f.Stat()
	if err != nil {
		return protocol.LogsTailResult{}, fmt.Errorf("读取日志失败: %w", err)
	}
	truncated := st.Size() > int64(bytes)
	if truncated {
		if _, err := f.Seek(-int64(bytes), io.SeekEnd); err != nil {
			return protocol.LogsTailResult{}, fmt.Errorf("日志定位失败: %w", err)
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return protocol.LogsTailResult{}, fmt.Errorf("读取日志失败: %w", err)
	}
	content := string(data)
	if len(content) > bytes {
		content = content[len(content)-bytes:]
	}
	return protocol.LogsTailResult{Path: path, Content: content, Truncated: truncated}, nil
}
