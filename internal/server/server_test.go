package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// wsTestClient 是测试用的最小 WS 客户端：请求-响应配对 + 事件收集。
// 读循环只有一个 goroutine（gorilla 不允许多 reader），应答按 id 路由。
type wsTestClient struct {
	conn    *websocket.Conn
	mu      sync.Mutex
	nextID  int
	pending map[int]chan *protocol.Response
	events  chan protocol.Response
}

func dialTest(t *testing.T, url string) *wsTestClient {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("连接 WS 失败: %v", err)
	}
	c := &wsTestClient{
		conn:    conn,
		pending: map[int]chan *protocol.Response{},
		events:  make(chan protocol.Response, 256),
	}
	go c.readLoop()
	return c
}

// readLoop 唯一的读者：应答按 id 路由到等待者，事件进 events。
func (c *wsTestClient) readLoop() {
	for {
		var resp protocol.Response
		if err := c.conn.ReadJSON(&resp); err != nil {
			close(c.events)
			return
		}
		if len(resp.ID) == 0 || string(resp.ID) == "null" {
			select {
			case c.events <- resp:
			default: // 满则丢弃（测试事件不应超过缓冲）
			}
			continue
		}
		var id int
		if json.Unmarshal(resp.ID, &id) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- &resp
		}
	}
}

// call 发请求并等应答（30 秒超时）。
func (c *wsTestClient) call(method string, params any) *protocol.Response {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan *protocol.Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	idRaw, _ := json.Marshal(id)
	b, _ := json.Marshal(params)
	c.conn.WriteJSON(protocol.Request{JSONRPC: "2.0", ID: idRaw, Method: method, Params: b})

	select {
	case resp := <-ch:
		return resp
	case <-time.After(30 * time.Second):
		return nil
	}
}

// waitEvent 等待指定事件（超时 5 秒）。
func (c *wsTestClient) waitEvent(method string) *protocol.Response {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-c.events:
			if ev.Method == method {
				return &ev
			}
		case <-deadline:
			return nil
		}
	}
}

// waitEventAny 等任意事件（超时 5 秒）。
func (c *wsTestClient) waitEventAny() *protocol.Response {
	select {
	case ev := <-c.events:
		return &ev
	case <-time.After(5 * time.Second):
		return nil
	}
}

// testStream 类型别名（与 agent.StreamFn 同形；测试编排用）。
type testStream = agent.StreamFn

// newTestServer 起一个绑定临时注册表 + 假 stream 的 WS 服务端。
func newTestServer(t *testing.T, stream testStream) (*Server, *wsTestClient, *config.Registry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "models.json")
	reg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		ContextWindow: 8192, MaxOutputTokens: 1024, Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(reg)
	t.Cleanup(srv.CloseSessions) // Windows：句柄开着会挡住 TempDir 删除
	if stream != nil {
		srv.SetStream(stream)
	}
	// 会话存储：SessionOps 用例需要（临时目录，测完即弃）
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() }) // SQLite 连接必须显式关（Windows 句柄挡 TempDir 删除）
	if err := srv.AttachSessionStore(st); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + protocol.Path
	client := dialTest(t, url)
	t.Cleanup(func() { client.conn.Close() })
	return srv, client, reg
}

// openStore 是 store.Open 的薄包装（测试文件里少打几个字符）。
func openStore(dir string) (st *store.Store, err error) {
	return store.Open(dir)
}

// TestServerHello：握手 + 模型列表 + ready 事件。
func TestServerHello(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	// 连接建立即收到 ready
	ev := client.waitEvent(protocol.EventReady)
	if ev == nil {
		t.Fatal("连接建立应收到 connection.ready")
	}
	var h protocol.HelloResult
	b, _ := json.Marshal(ev.Params)
	json.Unmarshal(b, &h)
	if h.Server != "lxcode" || h.Version != protocol.Version {
		t.Fatalf("ready 载荷不符: %+v", h)
	}

	resp := client.call(protocol.MethodHello, protocol.HelloParams{Client: "test", Version: protocol.Version})
	if resp == nil || resp.Error != nil {
		t.Fatalf("hello 失败: %+v", resp)
	}
	var r protocol.HelloResult
	b, _ = json.Marshal(resp.Result)
	json.Unmarshal(b, &r)
	if r.Server != "lxcode" || r.Version != protocol.Version {
		t.Fatalf("hello 结果不符: %+v", r)
	}
	resp = client.call(protocol.MethodHello, protocol.HelloParams{Client: "old-client", Version: "1"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeVersionMismatch {
		t.Fatalf("旧协议版本应被显式拒绝: %+v", resp)
	}
	// model.list
	resp = client.call(protocol.MethodModelList, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("model.list 失败: %+v", resp)
	}
	var ml protocol.ModelListResult
	b, _ = json.Marshal(resp.Result)
	json.Unmarshal(b, &ml)
	if len(ml.Models) != 1 || ml.Models[0].ID != "m1" || ml.Roles["default"] != "m1" {
		t.Fatalf("模型列表不符: %+v", ml)
	}
}

// TestServerModelOps：增删启停 + model.changed 广播。
func TestServerModelOps(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	// 添加
	resp := client.call(protocol.MethodModelAdd, config.ModelConfig{
		ID: "m2", BaseURL: "http://127.0.0.1:2/v1", Model: "m2", Enabled: true,
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("model.add 失败: %+v", resp)
	}
	// model.changed 广播
	ev := client.waitEvent(protocol.EventModels)
	if ev == nil {
		t.Fatal("应广播 model.changed")
	}
	var ml protocol.ModelListResult
	b, _ := json.Marshal(ev.Params)
	json.Unmarshal(b, &ml)
	if len(ml.Models) != 2 {
		t.Fatalf("广播的模型数不符: %+v", ml)
	}
	// 角色绑定（先绑再停——停用后不能绑）
	resp = client.call(protocol.MethodRoleSet, protocol.RoleSetParams{Role: "vision", ModelID: "m2"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("role.set 失败: %+v", resp)
	}
	// 启停
	resp = client.call(protocol.MethodModelEnable, protocol.ModelEnableParams{ID: "m2", Enabled: false})
	if resp == nil || resp.Error != nil {
		t.Fatalf("model.enable 失败: %+v", resp)
	}
	// 删除（有角色引用应拒绝）
	resp = client.call(protocol.MethodModelRemove, protocol.ModelRemoveParams{ID: "m2"})
	if resp == nil || resp.Error == nil {
		t.Fatal("被角色引用时删除应拒绝")
	}
	// 解绑后再删
	client.call(protocol.MethodRoleSet, protocol.RoleSetParams{Role: "vision", ModelID: ""})
	resp = client.call(protocol.MethodModelRemove, protocol.ModelRemoveParams{ID: "m2"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("解绑后删除应成功: %+v", resp)
	}
}

// TestServerChatFlow：发送 → 流式事件 → done → busy=false（假 stream）。
func TestServerChatFlow(t *testing.T) {
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventReasoning, TextDelta: "想想"}
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "你好"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message:      llm.Message{Role: "assistant", Content: "你好！", ReasoningContent: "想想"},
				FinishReason: llm.FinishStop,
			}}
		}()
		return ch, nil
	})

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "嗨"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	// 等事件序列：userMessage → busy(true) → delta×2 → done → busy(false)。
	seq := []string{}
	for i := 0; i < 6; i++ {
		ev := client.waitEventAny()
		if ev == nil {
			t.Fatalf("事件不足（%d/6）: %v", i, seq)
		}
		seq = append(seq, ev.Method)
	}
	for _, want := range []string{protocol.EventUserMsg, protocol.EventBusy, protocol.EventDelta, protocol.EventDelta, protocol.EventDone, protocol.EventBusy} {
		found := false
		for _, s := range seq {
			if s == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("事件序列缺 %q: %v", want, seq)
		}
	}
	// chat.history 同步
	resp = client.call(protocol.MethodChatHistory, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.history 失败: %+v", resp)
	}
	var hist protocol.ChatHistoryResult
	b, _ := json.Marshal(resp.Result)
	json.Unmarshal(b, &hist)
	if len(hist.Messages) != 2 || hist.Messages[0].Content != "嗨" || hist.Messages[1].Content != "你好！" {
		t.Fatalf("历史不符: %+v", hist.Messages)
	}
	if hist.Busy {
		t.Fatal("应已空闲")
	}
}

// TestServerConfirmFlow：工具调用 → 确认门 → y → 执行 → 结果回填续轮。
func TestServerConfirmFlow(t *testing.T) {
	round := 0
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		round++
		ch := make(chan llm.StreamEvent, 4)
		go func() {
			defer close(ch)
			if round == 1 {
				// 第一轮：发起 bash 工具调用（高危 → 确认门）
				tc := llm.ToolCall{ID: "c1"}
				tc.Function.Name = "bash"
				tc.Function.Arguments = `{"command":"echo ws-test"}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
					FinishReason: llm.FinishToolCalls,
				}}
			} else {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message:      llm.Message{Role: "assistant", Content: "命令输出: ws-test"},
					FinishReason: llm.FinishStop,
				}}
			}
		}()
		return ch, nil
	})

	// M3 语义：不带 agent 的消息走主 Agent（只有 dispatch 工具）——
	// bash 确认门测试直选 coder（白名单含 bash）
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "跑个命令", Agent: "coder"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	// 等 chat.confirmRequest
	ev := client.waitEvent(protocol.EventConfirm)
	if ev == nil {
		t.Fatal("应收到确认请求")
	}
	var req protocol.ConfirmRequest
	b, _ := json.Marshal(ev.Params)
	json.Unmarshal(b, &req)
	if req.Name != "bash" || !strings.Contains(req.Prompt, "echo ws-test") {
		t.Fatalf("确认请求不符: %+v", req)
	}
	// y 允许
	resp = client.call(protocol.MethodToolConfirm, protocol.ToolConfirmParams{ID: req.ID, Allow: true})
	if resp == nil || resp.Error != nil {
		t.Fatalf("tool.confirm 失败: %+v", resp)
	}
	// 收集后续事件：toolResult → done（第二轮）→ busy(false)
	var events []protocol.Response
	deadline := time.After(5 * time.Second)
	for len(events) < 24 {
		select {
		case ev := <-client.events:
			events = append(events, ev)
			if ev.Method == protocol.EventBusy {
				var bp protocol.BusyParams
				b, _ = json.Marshal(ev.Params)
				json.Unmarshal(b, &bp)
				if !bp.Busy {
					goto collected
				}
			}
		case <-deadline:
			goto collected
		}
	}
collected:
	foundToolResult := false
	var finalDone *protocol.Response
	for _, e := range events {
		if e.Method == protocol.EventToolRslt {
			foundToolResult = true
		}
		if e.Method == protocol.EventDone {
			finalDone = &e
		}
	}
	if !foundToolResult {
		t.Fatal("应有 chat.toolResult 事件")
	}
	if finalDone == nil {
		t.Fatal("应有最终 chat.done")
	}
	var dp protocol.DoneParams
	b, _ = json.Marshal(finalDone.Params)
	json.Unmarshal(b, &dp)
	if !strings.Contains(dp.Message.Content, "ws-test") {
		t.Fatalf("最终回复应含命令输出: %+v", dp.Message)
	}
}

// TestServerTodoFlow：todo 工具 → todo.updated 事件 + history 携带清单。
func TestServerTodoFlow(t *testing.T) {
	round := 0
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		round++
		ch := make(chan llm.StreamEvent, 4)
		go func() {
			defer close(ch)
			if round == 1 {
				tc := llm.ToolCall{ID: "c1"}
				tc.Function.Name = "todo"
				tc.Function.Arguments = `{"items":[{"content":"第一步","status":"active"},{"content":"第二步","status":"pending"}]}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message:      llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}},
					FinishReason: llm.FinishToolCalls,
				}}
			} else {
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message:      llm.Message{Role: "assistant", Content: "清单建好了"},
					FinishReason: llm.FinishStop,
				}}
			}
		}()
		return ch, nil
	})

	// M3 语义：todo 在 coder 的白名单里（主 Agent 只有 dispatch）
	client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "建个清单", Agent: "coder"})
	ev := client.waitEvent(protocol.EventTodo)
	if ev == nil {
		t.Fatal("todo 工具应触发 todo.updated 事件")
	}
	var tp protocol.TodoUpdatedParams
	b, _ := json.Marshal(ev.Params)
	json.Unmarshal(b, &tp)
	if len(tp.Items) != 2 || tp.Items[0].Status != "active" {
		t.Fatalf("todo 事件载荷不符: %+v", tp)
	}
	// 等收尾，然后 history 应带 todos
	deadline := time.After(5 * time.Second)
	for {
		e := client.waitEventAny()
		if e == nil {
			break
		}
		if e.Method == protocol.EventBusy {
			var bp protocol.BusyParams
			b, _ = json.Marshal(e.Params)
			json.Unmarshal(b, &bp)
			if !bp.Busy {
				break
			}
		}
		_ = deadline
	}
	resp := client.call(protocol.MethodChatHistory, nil)
	var hist protocol.ChatHistoryResult
	b, _ = json.Marshal(resp.Result)
	json.Unmarshal(b, &hist)
	if len(hist.Todos) != 2 {
		t.Fatalf("history 应带 todos: %+v", hist.Todos)
	}
}

// TestServerChatCancel：取消生成 → aborted 错误事件 + 保留部分内容。
func TestServerChatCancel(t *testing.T) {
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "部分"}
			<-ctx.Done() // 挂住等取消
			ch <- llm.StreamEvent{Type: llm.EventError, Err: context.Canceled,
				Result: &llm.ChatResult{Message: llm.Message{Role: "assistant", Content: "部分"}}}
		}()
		return ch, nil
	})

	client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "慢慢生成"})
	time.Sleep(100 * time.Millisecond) // 等 stream 启动
	client.call(protocol.MethodChatCancel, nil)

	ev := client.waitEvent(protocol.EventError)
	if ev == nil {
		t.Fatal("取消后应有 chat.error")
	}
	var ep protocol.ErrorParams
	b, _ := json.Marshal(ev.Params)
	json.Unmarshal(b, &ep)
	if !ep.Aborted {
		t.Fatalf("应为 aborted: %+v", ep)
	}
	if ep.Partial == nil || ep.Partial.Content != "部分" {
		t.Fatalf("应保留部分内容: %+v", ep.Partial)
	}
}

// TestServerBusyReject：忙时拒绝新消息（错误码映射）。
func TestServerBusyReject(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	_, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		go func() {
			defer close(ch)
			<-block
		}()
		return ch, nil
	})

	client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "第一条"})
	time.Sleep(100 * time.Millisecond)
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "第二条"})
	if resp == nil || resp.Error == nil {
		t.Fatal("忙时应拒绝（CodeBusy）")
	}
	if resp.Error.Code != protocol.CodeBusy {
		t.Fatalf("错误码应为 CodeBusy: %+v", resp.Error)
	}
}

// TestServerNoModelError：未绑定 default 模型 → 应用错误码。
func TestServerNoModelError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "models.json")
	reg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(reg) // 空注册表：default 未绑定
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	client := dialTest(t, "ws"+strings.TrimPrefix(ts.URL, "http")+protocol.Path)
	t.Cleanup(func() { client.conn.Close() })

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "hi"})
	if resp == nil || resp.Error == nil {
		t.Fatal("未绑定模型应报错")
	}
	if resp.Error.Code != protocol.CodeNoDefaultModel {
		t.Fatalf("错误码应为 CodeNoDefaultModel: %+v", resp.Error)
	}
}

// TestServerSessionOps：session.new / session.list / session.resume + changed 广播。
func TestServerSessionOps(t *testing.T) {
	srv, client, _ := newTestServer(t, func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "ok"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	})
	_ = srv

	// 发一条消息产生历史
	client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "旧会话消息"})
	time.Sleep(200 * time.Millisecond) // 等轮次收尾

	// session.new
	resp := client.call(protocol.MethodSessionNew, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.new 失败: %+v", resp)
	}
	if client.waitEvent(protocol.EventSessionChanged) == nil {
		t.Fatal("创建会话应广播 session.changed 以刷新列表")
	}
	var created protocol.SessionResult
	b, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(b, &created); err != nil || created.SessionID == "" {
		t.Fatalf("session.new 应返回稳定 session_id: %+v, %v", created, err)
	}

	// session.list 应有 1 条
	resp = client.call(protocol.MethodSessionList, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.list 失败: %+v", resp)
	}
	var list []protocol.SessionMeta
	b, _ = json.Marshal(resp.Result)
	json.Unmarshal(b, &list)
	if len(list) != 1 {
		t.Fatalf("应列出 1 个会话: %+v", list)
	}

	// session.resume
	resp = client.call(protocol.MethodSessionResume, protocol.SessionResumeParams{ID: list[0].ID})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.resume 失败: %+v", resp)
	}
	// resume 只切换当前连接焦点，不向其他客户端广播全局焦点变化。
	// history 恢复
	resp = client.call(protocol.MethodChatHistory, nil)
	var hist protocol.ChatHistoryResult
	b, _ = json.Marshal(resp.Result)
	json.Unmarshal(b, &hist)
	if len(hist.Messages) == 0 {
		t.Fatal("resume 后历史应恢复")
	}
}
