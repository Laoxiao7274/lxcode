// screen_shell.go —— 壳侧捕屏服务的**后端端**：命令端点（壳轮询）与推流端点
// （壳 POST H.264 Annex B 裸流）。投屏的完整角色划分见 internal/protocol/screen.go。
//
// 为什么是本地回环 HTTP 而不是复用壳↔后端的 WS：壳渲染层的 WS 在渲染进程，
// 主进程另开一条 WS 要改 wsclient 做文本/二进制分路且要处理与 JSON-RPC 抢写；
// 壳本来就要拿后端 HTTP（/health 探测、/remote-access 管理），加两个端点零新依赖。
// 命令端点用**轮询**（在线时 1s、不在线时退避）而不是长连接：投屏启停是秒级
// 事件，轮询的代价（每秒一次 GET）可忽略，换来的是零长连接运维（后端重启/壳
// 重启都自愈，无需重连协议）。
//
// 凭证：两端点仅限回环（壳在本机），另要求壳持有与 remote.json 同源的 token
// （与 /rpc 升级门同一份）——回环上的其他本机进程不应能悄悄开摄像头式的屏幕
// 捕获。remote 未启用（token 空）时不强制。
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// shellPollInterval 桌面轻提示的轮询间隔（screen.go：壳在线时 1s 轮询命令）。
const shellPollInterval = time.Second

// screenCommand 是一次轮询里下发的命令。命令是**电平**而不是边沿：壳按字段
// 现状对齐（want=true 且未跑 → 启动；want=false 且在跑 → 停止），后端重启
// /壳重启不丢意图——不需要 ACK 协议。
type screenCommand struct {
	// Want 运行电平（false = 停）。
	Want bool `json:"want"`
	// Monitor 目标显示器（Want=true 时有效）。
	Monitor int `json:"monitor"`
	// Quality 码率档 low/medium/high。
	Quality string `json:"quality"`
}

// shellState 是壳在轮询里上报的自身状态（ffmpeg 进程与编码器事实源在壳）。
type shellState struct {
	Running bool   `json:"running"`
	Monitor int    `json:"monitor"`
	Encoder string `json:"encoder"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	// Error 非空 = 最近一次失败原因（人话）。
	Error string `json:"error,omitempty"`
	// ErrKind 错误分类（reason/ffmpeg_missing/encoder_unavailable），空 = 普通原因。
	ErrKind string `json:"err_kind,omitempty"`
	// Monitors 随轮询捎带的显示器枚举（壳每次轮询都现枚举，成本可忽略）。
	Monitors []protocol.ScreenMonitor `json:"monitors"`
}

// screenShell 管理壳侧通道的全部状态：命令电平、壳上报缓存、流订阅表。
// 互斥一把（s.mu 换成自己的 mu：屏频道不与 chat 广播路径共享锁）。
type screenShell struct {
	mu sync.Mutex

	// cmd 当前命令电平（.want=false = 无投屏）。
	cmd screenCommand
	// wantQuality/wantMonitor 是 cmd 的持久副本（want=false 时保档位——手机端
	// 只是停了投屏，下次 start 大概率还是同档）。
	// state 壳最近一次上报（nil = 壳从未上线/超时下线）。
	state    *shellState
	lastSeen time.Time

	// receiverId 当前投屏接收者（手机 WS 客户端 id；空 = 无人看）。
	receiverId string

	// streaming 壳推流端点当前是否在途（诊断；壳状态上报的 Running 互为校验）。
	streaming bool
}

func newScreenShell() *screenShell {
	return &screenShell{}
}

// ---- 壳轮询命令端点（GET /screen/ctl） ----

// handleScreenCtl 壳的命令轮询端点。响应即命令；壳的请求体是自身状态上报。
func (s *Server) handleScreenCtl(w http.ResponseWriter, r *http.Request) {
	if !s.screenGate(w, r) {
		return
	}
	if s.shell == nil {
		http.Error(w, "screen 未装配", http.StatusNotImplemented)
		return
	}
	var st shellState
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err == nil && len(b) > 0 {
			if err := json.Unmarshal(b, &st); err != nil {
				http.Error(w, "状态上报不是合法 JSON: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	monitors, changed := s.shell.report(st)
	// 状态变化（启停/编码器/错误）→ 广播 screen.changed（手机与桌面 UI 都据此刷新）。
	if changed {
		s.broadcastScreenChanged()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"cmd":          s.shell.currentCommand(),
		"poll_ms":      shellPollInterval.Milliseconds(),
		"idle_poll_ms": shellIdlePoll.Milliseconds(),
		"monitors":     monitors,
	})
}

// shellIdlePoll 壳侧离线退避轮询间隔（壳连续失败时自退避，此值只是建议）。
const shellIdlePoll = 5 * time.Second

// report 壳状态入库；返回显示器枚举缓存与「对外可见状态是否变化」。
func (sh *screenShell) report(st shellState) ([]protocol.ScreenMonitor, bool) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.lastSeen = time.Now()
	prev := sh.state
	sh.state = &st
	changed := prev == nil ||
		prev.Running != st.Running || prev.Monitor != st.Monitor ||
		prev.Encoder != st.Encoder || prev.Error != st.Error
	return st.Monitors, changed
}

// setCommand 设置命令电平（start/stop 共用）。monitor 仅 want=true 时生效。
func (sh *screenShell) setCommand(want bool, monitor int, quality string) error {
	sh.mu.Lock()
	sh.cmd = screenCommand{Want: want, Monitor: monitor, Quality: quality}
	sh.mu.Unlock()
	return nil
}

// currentCommand 当前命令电平快照。
func (sh *screenShell) currentCommand() screenCommand {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return sh.cmd
}

// shellOnline 壳最近 shellStale 内有上报。
func (sh *screenShell) shellOnline() bool {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return sh.state != nil && time.Since(sh.lastSeen) < shellStale
}

// shellStale 壳上报的保鲜期（超过 = 壳离线，屏幕指示与 start 都按不可用处理）。
const shellStale = 4 * time.Second

// ---- 手机端控制（协议方法） ----

// errScreenShellOffline 壳不在线（后端无从拉 ffmpeg）。
var errScreenShellOffline = errors.New("桌面端壳不在线——请确认电脑上 Lxcode 正在运行")

// startScreen 手机请求开始投屏。前提：壳在线。成功即设置命令电平并返回目标状态。
func (s *Server) startScreen(req *protocol.Request, params json.RawMessage) *protocol.Response {
	if s.shell == nil {
		return protocol.NewError(req.ID, protocol.CodeScreenUnavailable, "投屏未装配")
	}
	var p protocol.ScreenStartParams
	if err := json.Unmarshal(params, &p); err != nil {
		return protocol.NewError(req.ID, protocol.CodeInvalidParams, "参数解析失败: "+err.Error())
	}
	// 壳在线校验在锁外做（纯读）。
	if !s.shell.shellOnline() {
		return protocol.NewError(req.ID, protocol.CodeScreenUnavailable, errScreenShellOffline.Error())
	}
	if err := s.shell.setCommand(true, p.Monitor, normalizeQuality(p.Quality)); err != nil {
		return protocol.NewError(req.ID, protocol.CodeScreenNoMonitor, err.Error())
	}
	return protocol.NewResult(req.ID, s.screenState())
}

// stopScreen 手机请求停止投屏。
func (s *Server) stopScreen(req *protocol.Request, _ json.RawMessage) *protocol.Response {
	if s.shell == nil {
		return protocol.NewError(req.ID, protocol.CodeScreenUnavailable, "投屏未装配")
	}
	s.shell.setCommand(false, 0, "")
	return protocol.NewResult(req.ID, s.screenState())
}

// screenStateQuery screen.state 查询。
func (s *Server) screenStateQuery(req *protocol.Request, _ json.RawMessage) *protocol.Response {
	return protocol.NewResult(req.ID, s.screenState())
}

// screenMonitors 显示器枚举（壳上报缓存；壳不在线时给空表 + shell_online=false）。
func (s *Server) screenMonitors(req *protocol.Request, _ json.RawMessage) *protocol.Response {
	res := protocol.ScreenMonitorsResult{}
	if s.shell != nil {
		s.shell.mu.Lock()
		if s.shell.state != nil && time.Since(s.shell.lastSeen) < shellStale {
			res.Monitors = s.shell.state.Monitors
			res.ShellOnline = true
		}
		s.shell.mu.Unlock()
	}
	if res.Monitors == nil {
		res.Monitors = []protocol.ScreenMonitor{}
	}
	return protocol.NewResult(req.ID, res)
}

// screenState 装配对外状态（真实值来自壳上报；壳不在 = 不活跃）。
func (s *Server) screenState() protocol.ScreenState {
	st := protocol.ScreenState{}
	if s.shell == nil {
		return st
	}
	s.shell.mu.Lock()
	defer s.shell.mu.Unlock()
	if sh := s.shell.state; sh != nil && time.Since(s.shell.lastSeen) < shellStale {
		st.Active = sh.Running
		st.Monitor = sh.Monitor
		st.Encoder = sh.Encoder
		st.Width = sh.Width
		st.Height = sh.Height
		st.Error = sh.Error
	}
	st.Receiver = s.shell.receiverId
	return st
}

// broadcastScreenChanged 广播 screen.changed（当前状态快照）。
func (s *Server) broadcastScreenChanged() {
	s.broadcast(protocol.EventScreenChanged, protocol.ScreenChangedParams{State: s.screenState()})
}

// normalizeQuality 码率档归一（未知值回落 medium）。
func normalizeQuality(q string) string {
	switch q {
	case "low", "high":
		return q
	default:
		return "medium"
	}
}

// ---- 壳推流端点（POST /screen/stream） ----

// handleScreenStream 壳把 ffmpeg 的 stdout（H.264 Annex B 裸流）按 AU 切帧后
// POST 上来。请求体 = 13 字节帧头 + NAL 载荷，逐帧一发（壳负责切帧与背压丢帧）。
// 响应结束（壳写完当前管道缓冲）即断——壳按错误重试下一帧。
func (s *Server) handleScreenStream(w http.ResponseWriter, r *http.Request) {
	if !s.screenGate(w, r) {
		return
	}
	if s.shell == nil {
		http.Error(w, "screen 未装配", http.StatusNotImplemented)
		return
	}
	s.shell.receiverActive()
	defer s.shell.receiverInactive()
	w.Header().Set("Content-Type", "application/octet-stream")
	rc := http.NewResponseController(w)
	// **必须 EnableFullDuplex**（2026-12 真机「收 1 帧后全停」根因）：Go http.Server
	// 默认半双工——请求体未读完之前，响应缓冲（~4KB）一旦写满，w.Write 阻塞直到
	// 请求体读完。本端点是「边读边转发」的长体回环流：壳的 ClientRequest 在读响应
	// 前先把帧写进推流管道 → 服务端响应缓冲满阻塞 → 回读停 → 壳写停 → 死锁，
	// **首帧之后的流全部卡死**（手机端只见首关键帧，其余帧堆在死锁的管道里）。
	// 半双工保护是给「先收完再回」的普通 handler 用的，这里是全双工场景，必须
	// 显式解除（Go 1.20+；本机 Go 版本实测：解除后 50 块全通，不解除只回 ~1-3KB）。
	if err := rc.EnableFullDuplex(); err != nil {
		// HTTP/1.1 over TCP 恒支持，理论不可达；失败维持旧行为（首帧可达）。
		log.Printf("screen/stream EnableFullDuplex 失败（维持半双工）: %v", err)
	}
	_ = rc.SetWriteDeadline(time.Time{})
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64*1024)
	// 帧边界切分器：Read 不保证按壳的写边界返回（一次 Read 可能含多帧或半帧），
	// 按 13 字节帧头切完整帧再转发（screenDeframer.feed）——整块透传会让接收端
	// 丢粘连帧里的关键帧（csd 拿不到 → 黑屏 2s）。
	df := &screenDeframer{}
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			// 转发给接收者（手机 WS 二进制帧，逐帧）。转发是**非阻塞**的
			//（接收者队列满即丢帧，见 screen.go）。
			df.feed(buf[:n], s.screenFrameForwarder)
		}
		if err != nil {
			return
		}
	}
}

// receiverActive / receiverInactive：推流连接的存活标记（壳推流端点在途 =
// 壳确实在推流；壳的状态上报也带 Running，两者互为校验——当前只用于诊断）。
func (sh *screenShell) receiverActive() bool {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	prev := sh.streaming
	sh.streaming = true
	return prev
}

func (sh *screenShell) receiverInactive() {
	sh.mu.Lock()
	sh.streaming = false
	sh.mu.Unlock()
}

// screenGate 屏端点的门：回环 + token（与 /rpc 同一份 remote.json 门）。
// token 为空（远程访问未开启）时不强制——回环单用户形态与 /health 同级。
func (s *Server) screenGate(w http.ResponseWriter, r *http.Request) bool {
	if !isLoopback(r) {
		http.Error(w, "屏端点仅限本机", http.StatusForbidden)
		return false
	}
	ra := s.remoteConfig()
	if ra.Token != "" {
		tok := r.URL.Query().Get("token")
		if tok == "" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			tok = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(ra.Token)) != 1 {
			http.Error(w, "屏端点令牌无效", http.StatusUnauthorized)
			return false
		}
	}
	return true
}
