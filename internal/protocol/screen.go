// screen.go —— 投屏（第一阶段：只看）协议面：方法/事件/参数结构。
//
// 角色划分（session 0 约束的落点）：lxcode 后端跑在 SCM 服务（session 0）抓不了
// 用户桌面——ffmpeg 由桌面壳（用户会话内）拉起。后端只做**调度与转发**：
//   手机（WS JSON-RPC + 二进制视频帧）
//     ↕ 同一条 WS：文本帧 = JSON-RPC（控制），二进制帧 = 视频（分路见下）
//   后端（帧头打包/订阅表/背压丢帧）
//     ↕ 本地回环 HTTP：GET /screen/ctl（壳轮询命令）/ POST /screen/stream（壳推流）
//   壳（ffmpeg 进程管理，shell/src/screen.ts）
//
// WS 帧分路：文本帧 = JSON-RPC（与既有协议共用一条连接）；二进制帧 = 视频包，
// 头 13 字节自描述（FrameHeader）。浏览器/CLI 只发文本帧，行为不变；老后端不会
// 发二进制帧，老客户端也不需要认——分路是纯增量。
package protocol

// 方法名（手机端 → 后端）。
const (
	// MethodScreenStart 请求开始投屏（params: ScreenStartParams）。
	// 应答 result: ScreenState——分辨率/码率档协商成功即返回，视频帧随后以
	// 二进制帧到达。
	MethodScreenStart = "screen.start"
	// MethodScreenStop 停止投屏（params: {}）。应答 result: {"stopped": true}。
	MethodScreenStop = "screen.stop"
	// MethodScreenState 查询当前投屏状态（{}）。应答 result: ScreenState（
	// active=false = 没在投）。
	MethodScreenState = "screen.state"
	// MethodScreenMonitors 显示器枚举（{}）。应答 result: ScreenMonitorsResult。
	// 注意：这个查询的**权威数据在壳**（桌面会话内才看得到显示器），后端回缓存的
	// 上一次壳上报值；壳不在时 active 布尔全空。
	MethodScreenMonitors = "screen.monitors"
)

// 事件名（后端 → 客户端广播）。
const (
	// EventScreenChanged 投屏状态变化（start/stop/error，壳崩溃/退出也会推）。
	EventScreenChanged = "screen.changed"
)

// 通用错误码（JSON-RPC 应用码，与 methods.go 的段位区分开）。
const (
	// CodeScreenUnavailable 壳不在（后端无从拉 ffmpeg）或壳侧捕屏模块不可用。
	CodeScreenUnavailable = -32080
	// CodeScreenFfmpegMissing PATH/配置路径里找不到 ffmpeg 可执行文件。
	CodeScreenFfmpegMissing = -32081
	// CodeScreenEncoderUnavailable 指定硬件编码器不可用（nvenc/qsv/amf 全回落
	// 失败，或用户点名的编码器不支持）。
	CodeScreenEncoderUnavailable = -32082
	// CodeScreenNoMonitor 请求的显示器索引不存在。
	CodeScreenNoMonitor = -32083
)

// 码率档（Stage 1 三档；kbit）。
const (
	BitrateLow    = 1500
	BitrateMedium = 3000
	BitrateHigh   = 6000
)

// 帧率（定帧率，Stage 1 不做动态调整；ddagrab 与安卓低延迟解码都以恒定帧率最稳）。
const ScreenFPS = 30

// ResolutionParams 分辨率/码率档（Stage 1：分辨率定 1080p 档，只开码率三档）。
type ResolutionParams struct {
	// Quality：low / medium / high（BitrateLow/Medium/High）。空 = medium。
	Quality string `json:"quality"`
}

// ScreenStartParams screen.start 的参数。
type ScreenStartParams struct {
	// Monitor 显示器索引（screen.monitors 返回的 id）。空 = 主显示器（0）。
	Monitor int `json:"monitor,omitempty"`
	// Quality 码率档（low/medium/high；空 = medium）。
	Quality string `json:"quality,omitempty"`
}

// ScreenMonitorsResult 显示器枚举（screen.monitors）。
type ScreenMonitorsResult struct {
	Monitors []ScreenMonitor `json:"monitors"`
	// ShellOnline 壳侧捕屏模块是否在线（壳轮询命令端点成功才算）。
	ShellOnline bool `json:"shell_online"`
}

// ScreenMonitor 单个显示器。
type ScreenMonitor struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Primary  bool   `json:"primary"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Attached bool   `json:"attached"`
}

// ScreenState 当前投屏状态（start 应答 / state 应答 / changed 事件载荷）。
type ScreenState struct {
	Active  bool   `json:"active"`
	Monitor int    `json:"monitor,omitempty"`
	Quality string `json:"quality,omitempty"`
	// Encoder 实际选中的编码器名（h264_nvenc / h264_qsv / ...；壳上报）。
	Encoder string `json:"encoder,omitempty"`
	// Width/Height 实际输出分辨率（壳上报；0 = 未知）。
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Error 最近一次失败的人话原因（active=false 时可能非空——上一轮为什么停）。
	Error string `json:"error,omitempty"`
	// Receiver 手机接收者连接 id（内部诊断；对手机端透明也行，先带上）。
	Receiver string `json:"receiver,omitempty"`
}

// ScreenChangedParams screen.changed 事件载荷。
type ScreenChangedParams struct {
	State ScreenState `json:"state"`
}

// FrameHeader 是 WS 二进制帧的自定义头（13 字节），紧跟其后的 payload 是一个
// 访问单元（AU）的 H.264 Annex B 数据（含该 AU 的所有 NAL：SPS/PPS/IDR 同帧）。
//
// 为什么自带头而不是裸 H.264：① AU 长度——一个 WS 帧可能粘连多个 AU（TCP 分块
// 与帧边界无关），手机端按 len 精确切分，不依赖扫描 start code（start code 序列
// 可能偶然出现在熵编码数据中间——NAL 转义后不会，但 AU 内多个 NAL 的边界扫描
// 仍然繁琐）；② 关键帧标记——断线重连时手机端跳过非关键帧直到下一个 IDR；
// ③ 时间戳——抖动诊断与重连后重置解码器判定；④ 流结束标记（Stop 标记带
// reason 文本，手机端据此收尾 UI 而不是干等 WS 关闭）。
type FrameHeader struct {
	// Version 头版本（当前 1）。
	Version byte
	// Flags bit0 = 关键帧（IDR）；bit1 = 流结束（payload 为 reason 文本）。
	Flags byte
	// Len24 是 payload 长度（大端 3 字节；上限 16MB，实际为单帧 ~几十 KB）。
	Len24 [3]byte
	// TimestampMs 帧时间戳（壳侧 30fps 恒定计数，毫秒）。
	TimestampMs uint64
}

// 头大小与位定义。
const (
	FrameHeaderSize   = 13 // 1 version + 1 flags + 3 len + 8 ts
	FrameFlagKeyframe = 0x01
	FrameFlagStop     = 0x02
	FrameVersion1     = 1
)

// FrameMaxPayload 单帧载荷上限（16MB 理论上限；实际由 AU 大小决定）。
const FrameMaxPayload = 1<<24 - 1

// PutLen24 / GetLen24 是 Len24 字段的编解码。
func PutLen24(n int) [3]byte {
	return [3]byte{byte(n >> 16), byte(n >> 8), byte(n)}
}

func GetLen24(b [3]byte) int {
	return int(b[0])<<16 | int(b[1])<<8 | int(b[2])
}

// Marshal 编码 FrameHeader 到 dst 前 13 字节。
func (h FrameHeader) Marshal(dst []byte) {
	dst[0] = h.Version
	dst[1] = h.Flags
	dst[2], dst[3], dst[4] = h.Len24[0], h.Len24[1], h.Len24[2]
	ts := h.TimestampMs
	for i := 0; i < 8; i++ {
		dst[5+i] = byte(ts >> (56 - 8*i))
	}
}

// UnmarshalFrameHeader 从 b 解析帧头（b 不足 13 字节 = 非法帧）。
func UnmarshalFrameHeader(b []byte) (FrameHeader, bool) {
	if len(b) < FrameHeaderSize || b[0] != FrameVersion1 {
		return FrameHeader{}, false
	}
	var ts uint64
	for i := 0; i < 8; i++ {
		ts = ts<<8 | uint64(b[5+i])
	}
	var len24 [3]byte
	copy(len24[:], b[2:5])
	return FrameHeader{Version: b[0], Flags: b[1], Len24: len24, TimestampMs: ts}, true
}
