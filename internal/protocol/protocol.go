// Package protocol 定义智能体后端与客户端（CLI / 桌面壳 / 未来 Web UI）之间的
// WebSocket JSON-RPC 2.0 协议：帧结构、方法名、事件名与各方法的参数结构。
// 客户端与服务端共享此包——协议只有一处定义，避免两端漂移。
// 形态移植自 local-myt-agent（同作者的项目），扩展了 todo 事件。
package protocol

import (
	"encoding/json"
)

// Version 是协议版本（hello 握手交换；不兼容变更时递增）。
const Version = "2"

// Path 是 WS 端点路径。
const Path = "/rpc"

// Request / Response 是 JSON-RPC 2.0 帧。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 是 JSON-RPC 2.0 帧：请求的应答（带 ID + Result/Error），
// 或服务端事件（带 Method + Params、无 ID —— 即通知）。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// NewEvent 构造事件帧（无 id → 客户端按通知处理）。
func NewEvent(method string, params any) *Response {
	return &Response{JSONRPC: "2.0", Method: method, Params: params}
}

// NewResult / NewError 构造请求应答。
func NewResult(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Result: result}
}

func NewError(id json.RawMessage, code int, message string) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Error: &Error{Code: code, Message: message}}
}

type HelloParams struct {
	Client  string `json:"client"`  // 客户端标识：cli / desktop / web / probe
	Version string `json:"version"` // 客户端协议版本
}

type HelloResult struct {
	Server  string `json:"server"`
	Version string `json:"version"`
	Busy    bool   `json:"busy"`
}
