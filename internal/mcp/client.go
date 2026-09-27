package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Client 是一条已握手的 MCP 连接。
//
// 握手（规范要求 initialize **必须是第一个交互**）：
//
//	initialize（声明协议版本 + 能力 + 身份）→ 服务器回它的版本与能力
//	→ notifications/initialized（通知，无响应）
//
// **同连接串行**（mu 覆盖整个请求/响应往返）：MCP 允许并发请求（靠 id 配对），
// 但我们的使用面是「启动时列举一次 + 工具调用」——串行换来的是实现简单与
// 不会有两处同时读同一 stdout 的竞态。代价是同一服务器的并发工具调用会排队，
// 这在本仓的使用面上可以接受（工具循环本身是顺序的）。
type Client struct {
	cfg       ServerConfig
	transport transport
	server    serverInfo
	// protocolVer 是服务器回的协议版本（我们声明的是 ProtocolVersion；
	// 服务器可以回自己的——不因版本不同就拒绝，那会让老服务器完全不可用）。
	protocolVer string
	mu          sync.Mutex
	nextID      int64
	closed      bool
	// tools 是按暴露名索引的工具（列举时建立，调用时用它做反向查找）。
	tools map[string]ToolEntry
}

// serverInfo 是服务器在 initialize 里自报的身份（嵌套在结果的 serverInfo 键下）。
//
// 形状（规范）：
//
//	{"protocolVersion":"2025-06-18","capabilities":{...},
//	 "serverInfo":{"name":"...","version":"..."}}
//
// 注意 name/version 是**嵌套**的，不是顶层——按扁平结构解析会静默拿到空名字。
type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Title   string `json:"title,omitempty"`
}

// Dial 连接并握手一个 MCP 服务器。
func Dial(ctx context.Context, cfg ServerConfig) (*Client, error) {
	var tr transport
	var err error
	switch cfg.Transport {
	case TransportStdio, "":
		tr, err = newStdioTransport(cfg)
	case TransportHTTP:
		tr, err = newHTTPTransport(cfg)
	default:
		return nil, newError(cfg.ID, "transport", fmt.Sprintf("未知传输 %q", cfg.Transport), nil)
	}
	if err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg, transport: tr, tools: map[string]ToolEntry{}}
	if err := c.initialize(ctx); err != nil {
		_ = tr.close()
		return nil, err
	}
	return c, nil
}

// initialize 走握手（规范要求它是第一个交互）。
func (c *Client) initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": ClientName, "version": ClientVersion},
	}
	result, err := c.call(ctx, "initialize", params, handshakeTimeout)
	if err != nil {
		return err
	}
	var payload struct {
		ProtocolVersion string     `json:"protocolVersion"`
		ServerInfo      serverInfo `json:"serverInfo"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return newError(c.cfg.ID, "initialize", "解析服务器身份失败", err)
	}
	c.server = payload.ServerInfo
	c.protocolVer = payload.ProtocolVersion
	// 按规范发 initialized 通知（服务器可能等它才真正开始服务）。
	frame, err := encodeNotification("notifications/initialized", map[string]any{})
	if err != nil {
		return newError(c.cfg.ID, "initialize", "编码 initialized 通知失败", err)
	}
	if err := c.transport.notify(ctx, frame); err != nil {
		return newError(c.cfg.ID, "initialize", "发送 initialized 通知失败", err)
	}
	return nil
}

// ServerInfo 返回服务器在握手时自报的身份。
func (c *Client) ServerInfo() serverInfo { return c.server }

// NegotiatedProtocolVersion 返回服务器回的协议版本。
func (c *Client) NegotiatedProtocolVersion() string { return c.protocolVer }

// ListTools 列举服务器暴露的工具（建立暴露名 → MCP 原名的映射）。
func (c *Client) ListTools(ctx context.Context) ([]ToolEntry, error) {
	result, err := c.call(ctx, "tools/list", map[string]any{}, listTimeout)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, newError(c.cfg.ID, "tools/list", "解析工具清单失败", err)
	}
	out := make([]ToolEntry, 0, len(payload.Tools))
	seen := map[string]string{}
	for _, t := range payload.Tools {
		name := ExposedName(c.cfg.ID, t.Name)
		if name == "" {
			continue
		}
		// 净化 + 截断可能撞名（两个长名字前 64 字符相同）。撞名时**报错**
		// 而不是悄悄加后缀：名字必须稳定（每次列举都一样），否则模型上一轮
		// 学到的名字下一轮就不存在了。
		if prev, dup := seen[name]; dup {
			return nil, newError(c.cfg.ID, "tools/list",
				fmt.Sprintf("工具 %q 与 %q 净化后撞名（%s）——请给其中一个改短名字", t.Name, prev, name), nil)
		}
		seen[name] = t.Name
		entry := ToolEntry{Name: name, MCPName: t.Name, ServerID: c.cfg.ID, Tool: t}
		out = append(out, entry)
		c.tools[name] = entry
	}
	return out, nil
}

// CallTool 调用一个工具（用**暴露名**定位，内部换成 MCP 原名）。
func (c *Client) CallTool(ctx context.Context, exposedName string, args json.RawMessage) (CallResult, error) {
	entry, ok := c.tools[exposedName]
	if !ok {
		return CallResult{}, newError(c.cfg.ID, "tools/call", fmt.Sprintf("未知工具 %q（先列举再调用）", exposedName), nil)
	}
	// 参数是模型给的 JSON：坏 JSON 在这里兜底成空对象（与工具注册表的
	// 既有纪律一致——参数坏不该让整轮失败，交给服务器报「缺参数」）。
	var raw json.RawMessage = args
	if len(raw) == 0 || !json.Valid(raw) {
		raw = json.RawMessage(`{}`)
	}
	params := map[string]any{"name": entry.MCPName, "arguments": raw}
	result, err := c.call(ctx, "tools/call", params, callTimeout)
	if err != nil {
		return CallResult{}, err
	}
	var out CallResult
	if err := json.Unmarshal(result, &out); err != nil {
		return CallResult{}, newError(c.cfg.ID, "tools/call", "解析调用结果失败", err)
	}
	return out, nil
}

// Close 关闭连接（幂等）。
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.transport.close()
}

// StderrTail 返回子进程 stderr 的尾部（诊断用）。
func (c *Client) StderrTail() string { return c.transport.stderrTail() }

// call 发一条请求并等响应（timeout ≤ 0 表示用默认）。
func (c *Client) call(ctx context.Context, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, newError(c.cfg.ID, method, "连接已关闭", nil)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	id := c.nextID + 1
	c.nextID = id
	frame, err := encodeRequest(id, method, params)
	if err != nil {
		return nil, newError(c.cfg.ID, method, "编码请求失败", err)
	}
	result, err := c.transport.send(ctx, frame, id)
	if err != nil {
		return nil, newError(c.cfg.ID, method, err.Error(), err)
	}
	return result, nil
}
