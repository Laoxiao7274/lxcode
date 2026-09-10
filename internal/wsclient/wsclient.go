// Package wsclient 是后端的 WebSocket JSON-RPC 客户端：请求-响应按 id 配对，
// 事件走 channel。CLI / 桌面壳 / 探针共用（移植自 local-myt-agent 的 TUI 客户端）。
package wsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/myt-harness/internal/protocol"
)

// Backend 是客户端依赖的后端能力（WS 实现；单测注入假实现）。
type Backend interface {
	// Call 发一次请求并等应答（ctx 取消/连接断开返回错误）。
	Call(ctx context.Context, method string, params any, result any) error
	// Events 是服务端事件流（客户端读循环投递；连接关闭时 channel 关闭）。
	Events() <-chan protocol.Response
	// Close 关闭连接。
	Close()
}

// wsClient 是 WebSocket JSON-RPC 客户端：请求-响应按 id 配对，事件走 channel。
type wsClient struct {
	conn *websocket.Conn

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan protocol.Response

	events chan protocol.Response
	closed chan struct{}
	once   sync.Once
}

// Dial 连接后端（addr 形如 127.0.0.1:7789）。
func Dial(addr string) (Backend, error) {
	url := fmt.Sprintf("ws://%s%s", addr, protocol.Path)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("连接后端 %s 失败: %w", url, err)
	}
	c := &wsClient{
		conn:    conn,
		pending: map[int64]chan protocol.Response{},
		events:  make(chan protocol.Response, 64), // 缓冲：渲染慢时不阻塞读循环
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// readLoop 把应答按 id 路由到等待者，把事件投递到 Events。
func (c *wsClient) readLoop() {
	defer c.Close()
	for {
		var resp protocol.Response
		if err := c.conn.ReadJSON(&resp); err != nil {
			return
		}
		if len(resp.ID) == 0 || string(resp.ID) == "null" {
			// 事件（通知）：投递；缓冲满则丢弃最旧，避免卡住读循环
			select {
			case c.events <- resp:
			default:
				select {
				case <-c.events:
				default:
				}
				select {
				case c.events <- resp:
				default:
				}
			}
			continue
		}
		var id int64
		if err := json.Unmarshal(resp.ID, &id); err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

// Call 发请求并等待应答。
func (c *wsClient) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan protocol.Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	idRaw, _ := json.Marshal(id)
	req := protocol.Request{JSONRPC: "2.0", ID: idRaw, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("序列化参数: %w", err)
		}
		req.Params = b
	}
	c.mu.Lock()
	err := c.conn.WriteJSON(req)
	c.mu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// 连接已断开时优先给可读的错误，而不是把 OS 原始报错
		//（"use of closed network connection"）直接甩给用户
		select {
		case <-c.closed:
			return fmt.Errorf("后端连接已断开")
		default:
		}
		return fmt.Errorf("发送请求失败: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && resp.Result != nil {
			b, err := json.Marshal(resp.Result)
			if err != nil {
				return fmt.Errorf("序列化应答: %w", err)
			}
			if err := json.Unmarshal(b, result); err != nil {
				return fmt.Errorf("解析应答: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return fmt.Errorf("后端连接已断开")
	}
}

func (c *wsClient) Events() <-chan protocol.Response { return c.events }

func (c *wsClient) Close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		close(c.events)
	})
}
