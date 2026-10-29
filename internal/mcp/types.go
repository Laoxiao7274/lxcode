// Package mcp 是 MCP（Model Context Protocol）客户端：把外部 MCP 服务器暴露
// 的工具接进本仓库的工具注册表。
//
// 设计要点：
//
//   - **只依赖标准库**。传输有两种——stdio（起子进程，换行分隔的 JSON-RPC）
//     与 Streamable HTTP（单端点 POST，响应可能是 JSON 或 SSE）。两者都不需要
//     第三方库，所以不引依赖（本仓对新增依赖从严）。
//   - **消费方定义配置形状**（ServerConfig 而不是 sessiondata.McServerSpec）：
//     分层规则要求叶子包不反向依赖上层（store/sessiondata）。server 侧做一次
//     映射即可，换来本包可独立测试。
//   - **工具名净化是必须的**：MCP 允许工具名带点号（如 `web.search`），而
//     模型网关把工具名约束为 ^[a-zA-Z0-9_-]{1,64}$（AGENTS.md §5 坑 13）——
//     不净化会让整轮请求被 400 拒收。所以暴露给模型的名字统一走 ExposedName。
//   - **注解一律不信任**：MCP 规范明说「clients MUST consider tool annotations
//     to be untrusted unless they come from trusted servers」——服务器可以自称
//     readOnlyHint 来换取自动执行。本包的 Annotations 只作展示信息，**不参与
//     风险定级**（风险由 server 侧统一按高危处理）。
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 超时分级：握手与列举要快（用户在等界面），调用给足时间（服务器可能真的
// 在跑任务——上游 Exa 一类工具本身就是一次搜索）。
const (
	handshakeTimeout = 15 * time.Second
	listTimeout      = 15 * time.Second
	callTimeout      = 120 * time.Second
)

// ProtocolVersion 是我们声明的 MCP 协议版本。
//
// 规范要求 initialize 必须第一个交互，且客户端带上自己支持的版本——服务器
// 不支持时回自己的版本，由客户端决定兼不兼容。我们声明一个较新的版本，
// 对端回什么都能继续（不因版本不同就拒绝：那会让老服务器完全不可用）。
const ProtocolVersion = "2025-06-18"

// ClientName / ClientVersion 是我们在 initialize 里的自我介绍。
const (
	ClientName    = "lxcode"
	ClientVersion = "1.0"
)

// ServerConfig 是连接一个 MCP 服务器所需的配置（消费方定义形状）。
type ServerConfig struct {
	ID string
	// Transport 是传输方式：stdio（起子进程）| sse（Streamable HTTP 端点）。
	//
	// 磁盘上的取值是 stdio|sse（SQLite 有 CHECK 约束）。这里的 sse 按
	// **Streamable HTTP** 语义实现——那是 sse 传输的现行形态（单端点 POST +
	// 响应可为 SSE 流），所以老配置里的 sse 直接可用，不必改表约束。
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
}

// TransportStdio / TransportHTTP 是 Transport 的两个取值。
const (
	TransportStdio = "stdio"
	TransportHTTP  = "sse"
)

// Tool 是 MCP 服务器暴露的一个工具。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Annotations 是服务器自报的注解（**不可信**，只作展示）。
	Annotations *Annotations `json:"annotations,omitempty"`
}

// Annotations 是 MCP 工具注解。规范默认值：readOnlyHint=false、
// destructiveHint=true、idempotentHint=false、openWorldHint=true——
// 也就是说「不声明 = 按最危险理解」，与本仓的风险定级方向一致。
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// ToolEntry 是「暴露给模型的名字」与「MCP 原名」的配对。
type ToolEntry struct {
	// Name 是暴露给模型的名字（<server>_<tool>，字符集已净化）。
	Name string
	// MCPName 是 MCP 服务器上的原名（调用时用它）。
	MCPName string
	// ServerID 是来源服务器 id（物化进目录、确认提示、诊断都要它）。
	ServerID string
	Tool     Tool
}

// Content 是 tools/call 结果里的一段内容。只处理文本——图片/资源类内容
// 需要另一套呈现（模型上下文里塞 base64 没有意义）。
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// CallResult 是 tools/call 的结果。
type CallResult struct {
	Content []Content `json:"content"`
	// IsError 是**工具执行失败**（不是协议错误）：模型应当看到错误文本并
	// 自己决定下一步，所以这里不转成 error 上抛（与工具注册表的约定一致）。
	IsError bool `json:"isError,omitempty"`
}

// Text 把结果里的文本内容拼起来（工具结果的回填形态）。
func (r CallResult) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type != "text" || strings.TrimSpace(c.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Text)
	}
	return b.String()
}

// Status 是一个服务器的连接状态（上报给界面）。
type Status struct {
	// State：connected | error | stopped。
	State string
	// ToolCount 是已列举到的工具数。
	ToolCount int
	// LastError 是最近一次失败的原因（成功时为空）。
	LastError string
	// Stderr 是子进程 stderr 的尾部（stdio 专用，诊断用；成功时可能仍有值）。
	Stderr string
}

// 状态取值。
const (
	StateConnected = "connected"
	StateError     = "error"
	StateStopped   = "stopped"
)

// ExposedName 把 MCP 服务器的工具名净化成模型网关能接受的工具 id。
//
// 规则：`<server>_<tool>`，把两个名字里所有非 [A-Za-z0-9_-] 的字符换成 `_`，
// 整体截到 64 字符（网关的硬上限）。
//
// 为什么必须做：MCP 的工具名允许点号（`web.search`），而 OpenAI 与 Anthropic
// 都把工具名约束为 ^[a-zA-Z0-9_-]{1,64}$——原名直接上 wire 会被严格网关
// 400 拒收**整轮**请求（AGENTS.md §5 坑 13 的实际事故）。
//
// 截断可能撞名（两个长名字前 64 字符相同）——调用方（manager）负责检测
// 冲突并报错，不在这里悄悄加后缀（那会让名字不稳定：每次列举都可能变）。
func ExposedName(serverID, toolName string) string {
	name := sanitizeToolID(serverID) + "_" + sanitizeToolID(toolName)
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// sanitizeToolID 把名字里不合法的字符换成 `_`。
func sanitizeToolID(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Error 是 MCP 层的错误（带服务器 id，便于界面与日志定位）。
type Error struct {
	Server  string
	Op      string // initialize | tools/list | tools/call | transport
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("MCP %s %s 失败: %s: %v", e.Server, e.Op, e.Message, e.Err)
	}
	return fmt.Sprintf("MCP %s %s 失败: %s", e.Server, e.Op, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func newError(server, op, msg string, err error) *Error {
	return &Error{Server: server, Op: op, Message: msg, Err: err}
}

// rpcError 是 JSON-RPC 层的错误（服务器返回的 error 对象）。
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("JSON-RPC %d: %s", e.Code, e.Message)
}
