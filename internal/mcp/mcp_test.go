package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// 假 MCP 服务器：用「测试二进制自我 re-exec」实现（不引外部依赖）。
//
// 手法：TestMain 检测到 MCP_FAKE_SERVER 环境变量时不再跑测试，而是变成
// 一个 stdin/stdout 上的 JSON-RPC 服务器——stdio 传输的 spawn 因此能指向
// 自己（`os.Args[0]`），不需要额外编译一个假服务器二进制。
func TestMain(m *testing.M) {
	if os.Getenv("MCP_FAKE_SERVER") != "" {
		runFakeServer(os.Getenv("MCP_FAKE_SERVER"))
		return
	}
	os.Exit(m.Run())
}

// runFakeServer 是假服务器主体（mode 决定行为变体）。
func runFakeServer(mode string) {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64<<10), 8<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	reply := func(id int64, result any) {
		buf, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		out.Write(buf)
		out.WriteByte('\n')
		out.Flush()
	}
	replyErr := func(id int64, code int, msg string) {
		buf, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": id,
			"error": map[string]any{"code": code, "message": msg},
		})
		out.Write(buf)
		out.WriteByte('\n')
		out.Flush()
	}

	switch mode {
	case "noisy":
		// 规范要求 stdout 只写协议，但现实里有服务器把日志混进 stdout——
		// 传输层必须跳过这些噪音而不是判死。
		fmt.Fprintln(out, "starting fake mcp server...")
		fmt.Fprintln(out, "[info] ready")
		out.Flush()
	case "stderr":
		fmt.Fprintln(os.Stderr, "fake server: warning from stderr")
	case "exit":
		// 直接退出：连接必须立即失效。
		os.Exit(3)
	}

	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		// 通知（无 id）：按规范不回响应。
		if req.ID == nil {
			continue
		}
		id := *req.ID
		switch req.Method {
		case "initialize":
			reply(id, map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "9.9"},
			})
		case "tools/list":
			reply(id, map[string]any{"tools": []map[string]any{
				{
					"name":        "web.search", // 点号：必须被净化
					"description": "搜索",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"q": map[string]any{"type": "string"}},
					},
				},
				{"name": "echo", "description": "回显", "inputSchema": map[string]any{"type": "object"}},
			}})
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			// 回带收到的**原名**：测试据此断言「暴露名 → 原名」的换算正确。
			reply(id, map[string]any{"content": []map[string]any{
				{"type": "text", "text": "called " + p.Name + " args=" + string(p.Arguments)},
			}})
		default:
			replyErr(id, -32601, "Method not found: "+req.Method)
		}
	}
}

// stdioCfg 构造指向假服务器的 stdio 配置（mode 选行为变体）。
func stdioCfg(id, mode string) ServerConfig {
	return ServerConfig{
		ID:        id,
		Transport: TransportStdio,
		Command:   os.Args[0],
		Env:       map[string]string{"MCP_FAKE_SERVER": mode},
	}
}

// ---------- 工具名净化 ----------

func TestExposedNameSanitizesToolIDs(t *testing.T) {
	cases := []struct{ server, tool, want string }{
		// 点号必须净化——原名上 wire 会被严格网关 400 拒收整轮（§5 坑 13）
		{"exa", "web.search", "exa_web_search"},
		{"my-server", "do:thing", "my-server_do_thing"},
		{"a b", "c/d", "a_b_c_d"},
		// 合法字符原样保留
		{"srv", "tool_name-1", "srv_tool_name-1"},
	}
	for _, c := range cases {
		if got := ExposedName(c.server, c.tool); got != c.want {
			t.Errorf("ExposedName(%q, %q) = %q，期望 %q", c.server, c.tool, got, c.want)
		}
	}
}

func TestExposedNameTruncatesToGatewayLimit(t *testing.T) {
	long := strings.Repeat("x", 80)
	got := ExposedName(long, long)
	if len(got) != 64 {
		t.Errorf("长度 = %d，期望截到 64（网关硬上限）", len(got))
	}
	if !toolIDOK(got) {
		t.Errorf("净化结果 %q 仍不匹配网关字符集", got)
	}
}

// toolIDOK 校验网关的工具名字符集（与 tools 包的守卫同一条规则）。
func toolIDOK(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// ---------- JSON-RPC 帧 ----------

func TestDecodeResponseIgnoresNoiseAndNotifications(t *testing.T) {
	// 不是我们要的 id → 跳过（ok=false，不报错）
	if _, ok, err := decodeResponse([]byte(`{"jsonrpc":"2.0","id":99,"result":{}}`), 1); ok || err != nil {
		t.Errorf("别人的响应应被跳过，实际 ok=%v err=%v", ok, err)
	}
	// 通知（无 id）→ 跳过
	if _, ok, err := decodeResponse([]byte(`{"jsonrpc":"2.0","method":"notifications/x"}`), 1); ok || err != nil {
		t.Errorf("通知应被跳过，实际 ok=%v err=%v", ok, err)
	}
	// 非 JSON 噪音 → 跳过（stdout 混日志是现实）
	if _, ok, err := decodeResponse([]byte(`starting server...`), 1); ok || err != nil {
		t.Errorf("非 JSON 噪音应被跳过，实际 ok=%v err=%v", ok, err)
	}
	// 我们的 id + 空行 → 跳过
	if _, ok, _ := decodeResponse([]byte("   \r\n"), 1); ok {
		t.Error("空行应被跳过")
	}
	// 我们的 id → 命中
	if _, ok, err := decodeResponse([]byte(`{"jsonrpc":"2.0","id":1,"result":{"a":1}}`), 1); !ok || err != nil {
		t.Errorf("应命中我们的响应，实际 ok=%v err=%v", ok, err)
	}
	// 我们的 id + error → 报错
	_, ok, err := decodeResponse([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"nope"}}`), 1)
	if !ok || err == nil {
		t.Error("JSON-RPC error 应被报出来")
	}
	if err != nil && !strings.Contains(err.Error(), "nope") {
		t.Errorf("错误消息应含服务器的说明，实际 %v", err)
	}
}

// ---------- stdio 传输 + 客户端 ----------

func TestStdioHandshakeListAndCall(t *testing.T) {
	cfg := stdioCfg("fake", "ok")
	client, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer client.Close()

	if client.ServerInfo().Name != "fake" {
		t.Errorf("服务器身份 = %+v", client.ServerInfo())
	}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("列举工具失败: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("工具数 = %d，期望 2", len(tools))
	}
	// 点号被净化，原名保留
	var search *ToolEntry
	for i := range tools {
		if tools[i].MCPName == "web.search" {
			search = &tools[i]
		}
	}
	if search == nil {
		t.Fatalf("没找到 web.search: %+v", tools)
	}
	if search.Name != "fake_web_search" {
		t.Errorf("暴露名 = %q，期望 fake_web_search", search.Name)
	}
	if !toolIDOK(search.Name) {
		t.Errorf("暴露名 %q 不匹配网关字符集", search.Name)
	}

	// 用**暴露名**调用，服务器应收到**原名**
	res, err := client.CallTool(context.Background(), "fake_web_search", json.RawMessage(`{"q":"go"}`))
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	text := res.Text()
	if !strings.Contains(text, "called web.search") {
		t.Errorf("服务器应收到原名 web.search，实际: %q", text)
	}
	if !strings.Contains(text, `{"q":"go"}`) {
		t.Errorf("参数应原样传给服务器，实际: %q", text)
	}
}

func TestStdioSkipsStdoutNoise(t *testing.T) {
	client, err := Dial(context.Background(), stdioCfg("noisy", "noisy"))
	if err != nil {
		t.Fatalf("stdout 有噪音时握手仍应成功: %v", err)
	}
	defer client.Close()
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("噪音不该让列举失败: %v", err)
	}
}

func TestStdioCapturesStderrTail(t *testing.T) {
	client, err := Dial(context.Background(), stdioCfg("err", "stderr"))
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer client.Close()
	// stderr 是异步读的：给它一点时间落进缓冲。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(client.StderrTail(), "warning from stderr") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("应捕获 stderr 尾部，实际 %q", client.StderrTail())
}

func TestStdioProcessExitInvalidatesConnection(t *testing.T) {
	// 进程直接退出：握手就该失败（不能挂在那里等超时）。
	start := time.Now()
	_, err := Dial(context.Background(), stdioCfg("dead", "exit"))
	if err == nil {
		t.Fatal("进程退出时握手应失败")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("失败得太慢（%v）——应在进程退出时立即返回", elapsed)
	}
}

func TestStdioBadJSONArgumentFallsBackToEmptyObject(t *testing.T) {
	client, err := Dial(context.Background(), stdioCfg("badargs", "ok"))
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer client.Close()
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("列举失败: %v", err)
	}
	// 坏 JSON 参数兜底成 {}（与工具注册表的既有纪律一致）——不该报错
	res, err := client.CallTool(context.Background(), "badargs_echo", json.RawMessage(`{"q":`))
	if err != nil {
		t.Fatalf("坏参数不该让调用失败: %v", err)
	}
	if !strings.Contains(res.Text(), "args={}") {
		t.Errorf("坏参数应兜底成 {}，实际: %q", res.Text())
	}
}

// ---------- HTTP 传输（Streamable HTTP） ----------

// httpFakeServer 起一个假 MCP 端点（sse 决定响应形态）。
func httpFakeServer(t *testing.T, sse bool) (*httptest.Server, *httpCallLog) {
	t.Helper()
	log := &httpCallLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		log.add(r, body)
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		// 通知：规范回 202 无响应体
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": ProtocolVersion,
				"serverInfo":      map[string]any{"name": "httpfake", "version": "1.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "ping", "description": "pong", "inputSchema": map[string]any{"type": "object"}},
			}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "http ok"}}}
		}
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
		// 会话 id 由服务器给（客户端必须回带）
		w.Header().Set("Mcp-Session-Id", "sess-123")
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = w.Write([]byte("event: message\ndata: " + string(payload) + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

// httpCallLog 记录收到的 HTTP 请求（断言请求头用）。
type httpCallLog struct {
	mu    sync.Mutex
	items []httpLogItem
}

type httpLogItem struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

func (l *httpCallLog) add(r *http.Request, body []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, httpLogItem{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
}

func (l *httpCallLog) all() []httpLogItem {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]httpLogItem, len(l.items))
	copy(out, l.items)
	return out
}

func TestHTTPTransportJSONResponse(t *testing.T) {
	srv, log := httpFakeServer(t, false)
	client, err := Dial(context.Background(), ServerConfig{ID: "h", Transport: TransportHTTP, URL: srv.URL})
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("列举失败: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "h_ping" {
		t.Fatalf("工具 = %+v", tools)
	}
	res, err := client.CallTool(context.Background(), "h_ping", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if res.Text() != "http ok" {
		t.Errorf("结果 = %q", res.Text())
	}

	items := log.all()
	if len(items) < 3 {
		t.Fatalf("请求数 = %d，期望 ≥3（initialize + initialized 通知 + tools/list + tools/call）", len(items))
	}
	// 必须声明同时接受两种响应形态（只写 application/json 会让服务器无法用 SSE 作答）
	if got := items[0].Header.Get("Accept"); !strings.Contains(got, "application/json") || !strings.Contains(got, "text/event-stream") {
		t.Errorf("Accept = %q，应同时接受 JSON 与 SSE", got)
	}
	if got := items[0].Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	// initialize 必须是第一个交互（规范要求）
	var first struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(items[0].Body, &first)
	if first.Method != "initialize" {
		t.Errorf("第一个请求应是 initialize，实际 %q", first.Method)
	}
	// 会话 id 由服务器给出后，后续请求必须回带
	var sawSession bool
	for _, it := range items[1:] {
		if it.Header.Get("Mcp-Session-Id") == "sess-123" {
			sawSession = true
		}
	}
	if !sawSession {
		t.Error("后续请求应回带 Mcp-Session-Id")
	}
	// 协议版本头必须带上（规范要求后续请求带 MCP-Protocol-Version）
	if got := items[1].Header.Get("MCP-Protocol-Version"); got != ProtocolVersion {
		t.Errorf("MCP-Protocol-Version = %q，期望 %q", got, ProtocolVersion)
	}
}

func TestHTTPTransportSSEResponse(t *testing.T) {
	srv, _ := httpFakeServer(t, true)
	client, err := Dial(context.Background(), ServerConfig{ID: "s", Transport: TransportHTTP, URL: srv.URL})
	if err != nil {
		t.Fatalf("SSE 响应下握手失败: %v", err)
	}
	defer client.Close()
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("SSE 响应下列举失败: %v", err)
	}
	if len(tools) != 1 {
		t.Errorf("工具 = %+v", tools)
	}
}

func TestHTTPTransportErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad token"))
	}))
	t.Cleanup(srv.Close)
	_, err := Dial(context.Background(), ServerConfig{ID: "e", Transport: TransportHTTP, URL: srv.URL})
	if err == nil {
		t.Fatal("401 应让握手失败")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("错误应含状态码，实际: %v", err)
	}
}

func TestHTTPTransportRejectsBadURL(t *testing.T) {
	if _, err := Dial(context.Background(), ServerConfig{ID: "u", Transport: TransportHTTP, URL: "ftp://x"}); err == nil {
		t.Error("非 http(s) 地址应被拒")
	}
	if _, err := Dial(context.Background(), ServerConfig{ID: "u", Transport: TransportHTTP, URL: ""}); err == nil {
		t.Error("空地址应被拒")
	}
}

// 上次没连上的服务器**必须重试**：否则一个启动时连不上的服务器会永远不再
// 尝试（用户修好命令后重连也没用——界面一直显示失败而配置明明对）。
// 同时钉住「失败条目没有 client，清理路径不能空指针」。
func TestManagerRetriesFailedConnection(t *testing.T) {
	m := NewManager()
	defer m.Close()
	bad := ServerConfig{ID: "x", Transport: TransportStdio, Command: "definitely-not-a-real-binary-xyz"}
	if errs := m.Sync(context.Background(), []ServerConfig{bad}); len(errs) != 1 {
		t.Fatalf("首次对账应报错: %v", errs)
	}
	if st := m.Statuses()["x"]; st.State != StateError {
		t.Fatalf("状态应为 error: %+v", st)
	}
	// 第二次对账（配置**没变**）：必须再试一次（而不是因为指纹相同就保持失败态）。
	// 判据：仍然报错（真去连了才会再次失败）；若被当成「保持」则 errs 为空。
	if errs := m.Sync(context.Background(), []ServerConfig{bad}); len(errs) != 1 {
		t.Fatalf("失败的服务器每次对账都应重试，实际 errs=%v", errs)
	}
	// 改成能连上的配置 → 恢复（这条同时覆盖了「失败态被清掉」）
	good := stdioCfg("x", "ok")
	if errs := m.Sync(context.Background(), []ServerConfig{good}); len(errs) != 0 {
		t.Fatalf("改成可连配置后不该报错: %v", errs)
	}
	if st := m.Statuses()["x"]; st.State != StateConnected || st.ToolCount != 2 {
		t.Fatalf("应恢复连接: %+v", st)
	}
}

// ---------- Manager 对账 ----------

func TestManagerSyncIsIdempotent(t *testing.T) {
	m := NewManager()
	defer m.Close()
	specs := []ServerConfig{stdioCfg("a", "ok")}

	if errs := m.Sync(context.Background(), specs); len(errs) != 0 {
		t.Fatalf("首次对账不该有错误: %v", errs)
	}
	st := m.Statuses()
	if st["a"].State != StateConnected || st["a"].ToolCount != 2 {
		t.Fatalf("状态 = %+v", st["a"])
	}
	tools := m.Tools()
	if len(tools) != 2 {
		t.Fatalf("工具数 = %d", len(tools))
	}
	got, err := m.Call(context.Background(), "a_echo", json.RawMessage(`{}`))
	if err != nil || got.Text() == "" {
		t.Errorf("应能按暴露名调用，实际 err=%v text=%q", err, got.Text())
	}

	// 第二次对账（配置没变）：**不该重连**——重连会打断正在进行的调用。
	// 判据：假服务器进程只被起过一次（用进程数看不出来，所以用「状态仍然
	// 连接 + 工具仍在」间接确认，并额外确认 Sync 不返回错误）。
	if errs := m.Sync(context.Background(), specs); len(errs) != 0 {
		t.Fatalf("重复对账不该有错误: %v", errs)
	}
	if len(m.Tools()) != 2 {
		t.Error("重复对账后工具应仍在")
	}
	if got, err := m.Call(context.Background(), "a_echo", json.RawMessage(`{}`)); err != nil || got.Text() == "" {
		t.Errorf("重复对账后应仍能调用（说明连接被保持了），实际 err=%v", err)
	}
}

func TestManagerReconnectsOnConfigChange(t *testing.T) {
	m := NewManager()
	defer m.Close()
	if errs := m.Sync(context.Background(), []ServerConfig{stdioCfg("a", "ok")}); len(errs) != 0 {
		t.Fatalf("首次对账失败: %v", errs)
	}
	// 改配置（换行为变体）：必须重连（沿用旧连接等于配置没生效）
	changed := stdioCfg("a", "stderr")
	if errs := m.Sync(context.Background(), []ServerConfig{changed}); len(errs) != 0 {
		t.Fatalf("改配置后对账失败: %v", errs)
	}
	if got, err := m.Call(context.Background(), "a_echo", json.RawMessage(`{}`)); err != nil || got.Text() == "" {
		t.Errorf("重连后应仍能调用，实际 err=%v", err)
	}
	// 重连后的 stderr 变体应留下 stderr
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(m.Statuses()["a"].Stderr, "warning from stderr") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("重连后应捕获新进程的 stderr，实际 %q", m.Statuses()["a"].Stderr)
}

func TestManagerDropsRemovedServer(t *testing.T) {
	m := NewManager()
	defer m.Close()
	if errs := m.Sync(context.Background(), []ServerConfig{stdioCfg("a", "ok"), stdioCfg("b", "ok")}); len(errs) != 0 {
		t.Fatalf("对账失败: %v", errs)
	}
	if len(m.Tools()) != 4 {
		t.Fatalf("工具数 = %d，期望 4", len(m.Tools()))
	}
	// 移除 b：工具必须一并消失（用户删了服务器 = 能力没了）
	if errs := m.Sync(context.Background(), []ServerConfig{stdioCfg("a", "ok")}); len(errs) != 0 {
		t.Fatalf("对账失败: %v", errs)
	}
	if len(m.Tools()) != 2 {
		t.Errorf("移除后工具数 = %d，期望 2", len(m.Tools()))
	}
	if _, ok := m.Statuses()["b"]; ok {
		t.Error("移除的服务器不该还在状态里")
	}
	if _, err := m.Call(context.Background(), "b_echo", json.RawMessage(`{}`)); err == nil {
		t.Error("移除后调用应报错")
	}
}

// 一个服务器连不上不影响其余（坏配置不该让整份 MCP 面消失）。
func TestManagerOneBadServerDoesNotBlockOthers(t *testing.T) {
	m := NewManager()
	defer m.Close()
	bad := ServerConfig{ID: "bad", Transport: TransportStdio, Command: "definitely-not-a-real-binary-xyz"}
	errs := m.Sync(context.Background(), []ServerConfig{bad, stdioCfg("good", "ok")})
	if len(errs) != 1 {
		t.Fatalf("应只有一个错误，实际 %v", errs)
	}
	if _, ok := errs["bad"]; !ok {
		t.Errorf("错误应记在 bad 上: %v", errs)
	}
	if st := m.Statuses(); st["bad"].State != StateError || st["bad"].LastError == "" {
		t.Errorf("bad 的状态应带失败原因: %+v", st["bad"])
	}
	if st := m.Statuses(); st["good"].State != StateConnected {
		t.Errorf("good 应仍然连接: %+v", st["good"])
	}
	if len(m.Tools()) != 2 {
		t.Errorf("good 的工具应仍然可用，实际 %d 个", len(m.Tools()))
	}
}

// 服务器报错的 tools/call（isError=true）是**工具执行失败**而不是协议错误——
// 上层据此把错误文本回填模型（与工具注册表的约定一致）。
func TestCallResultIsErrorIsNotProtocolError(t *testing.T) {
	var res CallResult
	if err := json.Unmarshal([]byte(`{"content":[{"type":"text","text":"boom"}],"isError":true}`), &res); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !res.IsError {
		t.Error("isError 应被解析出来")
	}
	if res.Text() != "boom" {
		t.Errorf("文本 = %q", res.Text())
	}
}

// 同一服务器的工具名净化后撞名 → 报错（名字必须稳定，不能悄悄加后缀）。
func TestClientRejectsToolNameCollision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": ProtocolVersion, "serverInfo": map[string]any{"name": "c"}}
		case "tools/list":
			// 两个名字净化后相同
			result = map[string]any{"tools": []map[string]any{
				{"name": "a.b"}, {"name": "a:b"},
			}}
		}
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	client, err := Dial(context.Background(), ServerConfig{ID: "c", Transport: TransportHTTP, URL: srv.URL})
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	defer client.Close()
	if _, err := client.ListTools(context.Background()); err == nil {
		t.Error("净化后撞名应报错（不能悄悄加后缀让名字不稳定）")
	}
}
