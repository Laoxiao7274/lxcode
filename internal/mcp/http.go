package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// httpTransport 是 Streamable HTTP 传输：单个端点 + POST，响应可能是
// 纯 JSON 或 SSE 流（`event: message` + `data: {...}` 行）。
//
// 与老式「HTTP+SSE」双端点传输的差别（本包实现的是现行形态）：
//   - 只要一个 URL，不需要先 GET 拿 endpoint；
//   - 会话 id 由服务器在响应头 `Mcp-Session-Id` 里给（可选——Exa 这类
//     无状态服务器不给，那就每次请求独立）；
//   - 后续请求带上 `MCP-Protocol-Version` 头。
//
// 这就是磁盘上 `transport=sse` 的语义：老配置直接可用，不必改表约束
// （SQLite 对 transport 有 CHECK IN ('stdio','sse')）。
type httpTransport struct {
	url    string
	client *http.Client
	header http.Header

	mu        sync.Mutex
	closed    bool
	sessionID string
}

// httpMaxBytes 是单次响应体的读取上限（防某个服务器回一个巨型 HTML 错误页）。
const httpMaxBytes = 8 << 20

func newHTTPTransport(cfg ServerConfig) (*httpTransport, error) {
	url := strings.TrimSpace(cfg.URL)
	if url == "" {
		return nil, newError(cfg.ID, "transport", "sse 传输需要 url", nil)
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, newError(cfg.ID, "transport", "url 必须是 http(s) 开头的完整地址", nil)
	}
	header := http.Header{}
	// 服务器配置里可能带 Authorization（远程 MCP 常见的鉴权方式）。
	// Env 在 HTTP 传输里当请求头用（stdio 那边是进程环境变量）。
	for k, v := range cfg.Env {
		header.Set(k, v)
	}
	return &httpTransport{
		url:    url,
		client: &http.Client{},
		header: header,
	}, nil
}

func (t *httpTransport) send(ctx context.Context, req []byte, wantID int64) ([]byte, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errClosed
	}
	sessionID := t.sessionID
	t.mu.Unlock()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(req))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// 规范要求同时接受两种响应形态；只写 application/json 会让服务器
	// 无法用 SSE 作答（部分实现会 406）。
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}
	httpReq.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	for k, vs := range t.header {
		for _, v := range vs {
			httpReq.Header.Set(k, v)
		}
	}

	resp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 服务器可能在此返回会话 id（无状态服务器不给）。
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 512))
	}
	// 202 = 已接受（规范里通知的应答），没有响应体。
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fmt.Errorf("响应体为空（HTTP %d）", resp.StatusCode)
	}

	payload, err := extractPayload(body, resp.Header.Get("Content-Type"), wantID)
	if err != nil {
		return nil, err
	}
	_, ok, rpcErr := decodeResponse(payload, wantID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if !ok {
		return nil, fmt.Errorf("响应里没有匹配 id=%s 的帧", idString(wantID))
	}
	var out struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	return out.Result, nil
}

func (t *httpTransport) notify(ctx context.Context, frame []byte) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errClosed
	}
	sessionID := t.sessionID
	t.mu.Unlock()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(frame))
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", sessionID)
	}
	httpReq.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("发送通知失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, httpMaxBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("通知被拒：HTTP %d", resp.StatusCode)
	}
	return nil
}

func (t *httpTransport) close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}

func (t *httpTransport) stderrTail() string { return "" }

var errClosed = fmt.Errorf("连接已关闭")

// extractPayload 从响应体里取出**属于 wantID 的 JSON-RPC 帧**。
//
// 两种形态：纯 JSON（整段就是一个帧）与 SSE（`data:` 行，可能有多条）。
// 取第一条能解析出该 id 的帧——服务器可能先推别的消息。
func extractPayload(body []byte, contentType string, wantID int64) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	isSSE := strings.Contains(strings.ToLower(contentType), "text/event-stream") ||
		bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:"))
	if !isSSE {
		return trimmed, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64<<10), httpMaxBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
		if len(payload) == 0 || payload[0] != '{' {
			continue
		}
		if _, ok, _ := decodeResponse(payload, wantID); ok {
			return payload, nil
		}
	}
	return nil, fmt.Errorf("SSE 响应里没有匹配 id=%s 的帧", idString(wantID))
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…(已截断)"
}
