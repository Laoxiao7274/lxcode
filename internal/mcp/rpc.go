package mcp

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// JSON-RPC 2.0 帧（MCP 的线上格式）。本包只实现客户端需要的一小部分：
// 请求/响应配对 + 通知（服务器 → 客户端的请求与通知一律忽略——我们不声明
// 任何能力，服务器按规范不该发它们；真发了就当噪音丢掉，不因为一条不认识的
// 消息把整条连接判死）。
//
// 帧的编解码集中在这里，是因为它同时被 stdio（换行分隔）与 HTTP（单端点
// POST）两条传输复用——传输只负责「怎么送一行」，帧长什么样只有一份实现。

// rpcRequest 是发出的请求。
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcNotification 是发出的通知（无 id，不需要响应）。
type rpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse 是收到的响应。Result 与 Error 二选一（按规范）。
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
}

// encodeRequest 编码一条请求（**紧凑 JSON**：stdio 传输要求一行一帧，
// 帧内不能有换行——带缩进的编码会破坏分帧）。
func encodeRequest(id int64, method string, params any) ([]byte, error) {
	var raw json.RawMessage
	if params != nil {
		buf, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("编码参数失败: %w", err)
		}
		raw = buf
	}
	return json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: raw})
}

// encodeNotification 编码一条通知。
func encodeNotification(method string, params any) ([]byte, error) {
	var raw json.RawMessage
	if params != nil {
		buf, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("编码参数失败: %w", err)
		}
		raw = buf
	}
	return json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method, Params: raw})
}

// decodeResponse 解析一条响应帧。
//
// 判据是「有没有我们发出的那个 id」而不是「有没有 result 键」：服务器可能
// 先推通知/日志再回响应，我们只认自己那一帧。
//
// 返回 ok=false 表示这一帧不是我们要的（通知、别人的响应、非 JSON 噪音），
// 调用方应当继续读下一帧——**不报错**：stdio 服务器的 stdout 混入非协议
// 输出是常见现实（虽然规范要求只写 stderr），一条噪音不该让整次调用失败。
func decodeResponse(line []byte, wantID int64) (*rpcResponse, bool, error) {
	trimmed := trimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, nil
	}
	var resp rpcResponse
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return nil, false, nil
	}
	// 无 id = 服务器主动发的通知/请求（我们没声明任何能力，按规范它不该发；
	// 真发了就忽略——不回应未知请求是允许的，乱回应反而会出错）。
	if resp.ID == nil {
		return nil, false, nil
	}
	if *resp.ID != wantID {
		// 别人的响应（并发调用或服务器自己发的请求）——跳过。
		return nil, false, nil
	}
	if resp.Error != nil {
		return nil, true, resp.Error
	}
	return &resp, true, nil
}

// idString 把 id 渲染成字符串（日志用）。
func idString(id int64) string { return strconv.FormatInt(id, 10) }

// trimSpace 去掉首尾空白（含 \r：Windows 上子进程可能回 CRLF）。
func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
