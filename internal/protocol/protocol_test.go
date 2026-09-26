package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/tools"
)

// TestFrameShapes 校验三种帧的线格式契约。
// 客户端全靠这些形状区分"应答"与"事件"，任一字段多出/缺失都会让对端猜错。
func TestFrameShapes(t *testing.T) {
	t.Run("事件帧无 id（通知），字段名与协议一致", func(t *testing.T) {
		b, err := json.Marshal(NewEvent(EventDelta, DeltaParams{Kind: "text", Text: "hi"}))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m["jsonrpc"] != "2.0" {
			t.Fatalf("jsonrpc 必须为 2.0: %v", m["jsonrpc"])
		}
		// 有 id 的帧会被客户端当应答;事件必须是通知
		if _, hasID := m["id"]; hasID {
			t.Fatalf("事件帧不应带 id: %s", b)
		}
		if m["method"] != EventDelta {
			t.Fatalf("method 不符: %v", m["method"])
		}
		params, ok := m["params"].(map[string]any)
		if !ok || params["kind"] != "text" || params["text"] != "hi" {
			t.Fatalf("事件载荷不符: %v", m["params"])
		}
		// 应答专属字段不应出现
		if _, has := m["result"]; has {
			t.Fatalf("事件帧不应带 result: %s", b)
		}
	})

	t.Run("结果帧带 id 与 result，且无 method", func(t *testing.T) {
		id := json.RawMessage(`7`)
		b, _ := json.Marshal(NewResult(id, map[string]any{"accepted": true}))
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if string(mustMarshal(t, m["id"])) != "7" {
			t.Fatalf("id 不符: %v", m["id"])
		}
		if _, has := m["method"]; has {
			t.Fatalf("应答帧不应带 method: %s", b)
		}
		if _, has := m["error"]; has {
			t.Fatalf("成功应答不应带 error: %s", b)
		}
	})

	t.Run("错误帧带 error 且无 result", func(t *testing.T) {
		b, _ := json.Marshal(NewError(json.RawMessage(`1`), CodeBusy, "会话正在生成中"))
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if _, has := m["result"]; has {
			t.Fatalf("错误帧不应带 result: %s", b)
		}
		e, ok := m["error"].(map[string]any)
		if !ok {
			t.Fatalf("错误帧应带 error 对象: %s", b)
		}
		if int(e["code"].(float64)) != CodeBusy || e["message"] != "会话正在生成中" {
			t.Fatalf("错误内容不符: %v", e)
		}
	})
}

// TestRequestRoundTrip 校验请求帧（含 params 省略/空值）的往返。
func TestRequestRoundTrip(t *testing.T) {
	t.Run("无 params 时不产生 params 字段", func(t *testing.T) {
		req := Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: MethodModelList}
		b, _ := json.Marshal(req)
		if strings.Contains(string(b), `"params"`) {
			t.Fatalf("omitempty 失效: %s", b)
		}
		var back Request
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		if back.Method != MethodModelList || string(back.ID) != "1" {
			t.Fatalf("往返不符: %+v", back)
		}
	})

	t.Run("params 原样透传（服务端再按方法解）", func(t *testing.T) {
		params := mustMarshal(t, ChatSendParams{Text: "你好"})
		req := Request{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: MethodChatSend, Params: params}
		b, _ := json.Marshal(req)
		var back Request
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		var got ChatSendParams
		if err := json.Unmarshal(back.Params, &got); err != nil {
			t.Fatal(err)
		}
		if got.Text != "你好" {
			t.Fatalf("params 往返丢内容: %+v", got)
		}
	})
}

// TestEventPayloadRoundTrip 逐个事件载荷往返——载荷字段名是两端共享的契约，
// 改名会让客户端静默丢字段（而不是编译报错），所以必须有测试钉住。
func TestEventPayloadRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		event  string
		params any
		verify func(t *testing.T, raw []byte)
	}{
		{"ready", EventReady, HelloResult{Server: "lxcode", Version: Version, Busy: true},
			func(t *testing.T, raw []byte) {
				var v Response
				mustUnmarshal(t, raw, &v)
				b := mustMarshal(t, v.Params)
				var h HelloResult
				mustUnmarshal(t, b, &h)
				if h.Server != "lxcode" || h.Version != Version || !h.Busy {
					t.Fatalf("ready 载荷不符: %+v", h)
				}
			}},
		{"delta", EventDelta, DeltaParams{Kind: "reasoning", Text: "思考中"},
			func(t *testing.T, raw []byte) {
				var v Response
				mustUnmarshal(t, raw, &v)
				var p DeltaParams
				mustUnmarshal(t, mustMarshal(t, v.Params), &p)
				if p.Kind != "reasoning" || p.Text != "思考中" {
					t.Fatalf("delta 载荷不符: %+v", p)
				}
			}},
		{"toolCall", EventToolCall, ToolCallParams{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`},
			func(t *testing.T, raw []byte) {
				var v Response
				mustUnmarshal(t, raw, &v)
				var p ToolCallParams
				mustUnmarshal(t, mustMarshal(t, v.Params), &p)
				if p.Name != "bash" || !strings.Contains(p.Arguments, "ls") {
					t.Fatalf("toolCall 载荷不符: %+v", p)
				}
			}},
		{"toolResult", EventToolRslt, ToolResultParams{ID: "c1", Name: "bash", Content: "out", IsError: true},
			func(t *testing.T, raw []byte) {
				var v Response
				mustUnmarshal(t, raw, &v)
				var p ToolResultParams
				mustUnmarshal(t, mustMarshal(t, v.Params), &p)
				if !p.IsError || p.Content != "out" {
					t.Fatalf("toolResult 载荷不符: %+v", p)
				}
			}},
		{"confirmRequest", EventConfirm, ConfirmRequest{ID: "c1", Name: "bash", Arguments: "{}", Prompt: "将执行命令: ls"},
			func(t *testing.T, raw []byte) {
				var v Response
				mustUnmarshal(t, raw, &v)
				var p ConfirmRequest
				mustUnmarshal(t, mustMarshal(t, v.Params), &p)
				if !strings.Contains(p.Prompt, "ls") || p.ID != "c1" {
					t.Fatalf("confirmRequest 载荷不符: %+v", p)
				}
			}},
		{"done", EventDone, DoneParams{
			Message:      llm.Message{Role: "assistant", Content: "完成"},
			UsageTokens:  42,
			FinishReason: "stop",
		}, func(t *testing.T, raw []byte) {
			var v Response
			mustUnmarshal(t, raw, &v)
			var p DoneParams
			mustUnmarshal(t, mustMarshal(t, v.Params), &p)
			if p.Message.Content != "完成" || p.UsageTokens != 42 || p.FinishReason != "stop" {
				t.Fatalf("done 载荷不符: %+v", p)
			}
		}},
		{"error(aborted)", EventError, ErrorParams{
			Message: "已取消",
			Aborted: true,
			Partial: &llm.Message{Role: "assistant", Content: "半截"},
		}, func(t *testing.T, raw []byte) {
			var v Response
			mustUnmarshal(t, raw, &v)
			var p ErrorParams
			mustUnmarshal(t, mustMarshal(t, v.Params), &p)
			if !p.Aborted || p.Partial == nil || p.Partial.Content != "半截" {
				t.Fatalf("aborted 载荷不符: %+v", p)
			}
		}},
		{"busy", EventBusy, BusyParams{Busy: true},
			func(t *testing.T, raw []byte) {
				var v Response
				mustUnmarshal(t, raw, &v)
				var p BusyParams
				mustUnmarshal(t, mustMarshal(t, v.Params), &p)
				if !p.Busy {
					t.Fatalf("busy 载荷不符: %+v", p)
				}
			}},
		{"todo", EventTodo, TodoUpdatedParams{Items: []tools.TodoItem{
			{Content: "第一步", Status: "done"},
			{Content: "第二步", Status: "active"},
		}}, func(t *testing.T, raw []byte) {
			var v Response
			mustUnmarshal(t, raw, &v)
			var p TodoUpdatedParams
			mustUnmarshal(t, mustMarshal(t, v.Params), &p)
			if len(p.Items) != 2 || p.Items[1].Status != "active" {
				t.Fatalf("todo 载荷不符: %+v", p)
			}
		}},
		{"sessionChanged", EventSessionChanged, SessionChangedParams{ID: "s1", Reason: "new"},
			func(t *testing.T, raw []byte) {
				var v Response
				mustUnmarshal(t, raw, &v)
				var p SessionChangedParams
				mustUnmarshal(t, mustMarshal(t, v.Params), &p)
				if p.ID != "s1" || p.Reason != "new" {
					t.Fatalf("sessionChanged 载荷不符: %+v", p)
				}
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(NewEvent(tc.event, tc.params))
			if err != nil {
				t.Fatal(err)
			}
			// 事件帧必须能被 Response 解出来（客户端就是这么收的）
			var v Response
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatalf("事件帧无法解析: %v (%s)", err, raw)
			}
			if v.Method != tc.event {
				t.Fatalf("method 往返不符: %q", v.Method)
			}
			tc.verify(t, raw)
		})
	}
}

// TestChatHistoryPayload：history 是重连同步的载荷，pending/todos 必须可省略。
func TestChatHistoryPayload(t *testing.T) {
	t.Run("无待确认时 pending 省略", func(t *testing.T) {
		b, _ := json.Marshal(ChatHistoryResult{
			Messages: []llm.Message{{Role: "user", Content: "hi"}},
			Busy:     false,
		})
		if strings.Contains(string(b), "pending") {
			t.Fatalf("pending 为 nil 时应省略: %s", b)
		}
		if strings.Contains(string(b), "todos") {
			t.Fatalf("todos 为 nil 时应省略: %s", b)
		}
		var back ChatHistoryResult
		mustUnmarshal(t, b, &back)
		if len(back.Messages) != 1 || back.Messages[0].Content != "hi" {
			t.Fatalf("历史往返不符: %+v", back)
		}
	})
	t.Run("有待确认与 todo 时带上", func(t *testing.T) {
		b, _ := json.Marshal(ChatHistoryResult{
			Busy:    true,
			Pending: &ConfirmRequest{ID: "c9", Name: "bash", Prompt: "将执行命令: ls"},
			Todos:   []tools.TodoItem{{Content: "干活", Status: "active"}},
		})
		var back ChatHistoryResult
		mustUnmarshal(t, b, &back)
		if back.Pending == nil || back.Pending.ID != "c9" || !back.Busy {
			t.Fatalf("pending 往返不符: %+v", back)
		}
		if len(back.Todos) != 1 || back.Todos[0].Content != "干活" {
			t.Fatalf("todos 往返不符: %+v", back.Todos)
		}
	})
}

// TestErrorCodesStable：错误码是与客户端的契约，改了会静默破坏对端判断。
func TestErrorCodesStable(t *testing.T) {
	if CodeParseError != -32700 || CodeInvalidRequest != -32600 || CodeMethodNotFound != -32601 ||
		CodeInvalidParams != -32602 || CodeInternal != -32603 {
		t.Fatal("标准 JSON-RPC 错误码被改动（对端按标准码判断，不能变）")
	}
	if CodeNoDefaultModel != 1001 || CodeModelDisabled != 1002 || CodeBusy != 1003 || CodeNoPending != 1004 {
		t.Fatal("应用错误码被改动（客户端按这些值判断）")
	}
}

// TestErrorString：Error 实现 error 接口，便于客户端直接向上返回。
func TestErrorString(t *testing.T) {
	var err error = &Error{Code: CodeBusy, Message: "会话正在生成中"}
	if err.Error() != "会话正在生成中" {
		t.Fatalf("Error() 应返回 Message: %q", err.Error())
	}
}

// TestProtocolConstants：路径与版本是握手契约。
func TestProtocolConstants(t *testing.T) {
	if Path != "/rpc" {
		t.Fatalf("WS 端点路径被改动: %s（客户端都硬编码了它）", Path)
	}
	if Version != "2" {
		t.Fatalf("多会话路由与事件载荷的破坏性变更须使用协议版本 2，实际 %q", Version)
	}
	// 方法名与事件名不得重名，否则 dispatch 与事件处理会撞车
	methods := []string{MethodHello, MethodModelList, MethodModelAdd, MethodModelUpdate,
		MethodModelRemove, MethodModelEnable, MethodRoleSet, MethodChatSend,
		MethodChatCancel, MethodChatHistory, MethodToolConfirm,
		MethodSessionList, MethodSessionNew, MethodSessionResume, MethodSessionWorktreeRelease}
	events := []string{EventReady, EventUserMsg, EventDelta, EventToolCall, EventToolRslt,
		EventConfirm, EventDone, EventError, EventBusy, EventModels, EventTodo, EventSessionChanged}
	seen := map[string]bool{}
	for _, m := range methods {
		if seen[m] {
			t.Fatalf("方法名重复: %s", m)
		}
		seen[m] = true
		if !strings.Contains(m, ".") {
			t.Fatalf("方法名应遵循 namespace.action 风格: %s", m)
		}
	}
	for _, e := range events {
		if seen[e] {
			t.Fatalf("事件名与方法名冲突: %s", e)
		}
		seen[e] = true
		if !strings.Contains(e, ".") {
			t.Fatalf("事件名应遵循 namespace.action 风格: %s", e)
		}
	}
}

func TestSessionWorktreeReleaseProtocolShape(t *testing.T) {
	params := mustMarshal(t, SessionWorktreeReleaseParams{ID: "session-1"})
	if string(params) != `{"id":"session-1"}` {
		t.Fatalf("release params JSON = %s", params)
	}
	var got SessionWorktreeReleaseParams
	mustUnmarshal(t, params, &got)
	if got.ID != "session-1" {
		t.Fatalf("release params round-trip = %+v", got)
	}
	result := mustMarshal(t, SessionWorktreeReleaseResult{Released: true})
	if string(result) != `{"released":true}` {
		t.Fatalf("release result JSON = %s", result)
	}
}

// TestChatSendParamsRoundTrip：session_id 显式路由；effort/approval 仍可省略，
// 旧客户端缺 session_id 时由服务端连接焦点回退兼容。
func TestChatSendParamsRoundTrip(t *testing.T) {
	t.Run("省略可选 effort/approval", func(t *testing.T) {
		b := mustMarshal(t, ChatSendParams{Text: "你好"})
		if strings.Contains(string(b), "effort") || strings.Contains(string(b), "approval") {
			t.Fatalf("omitempty 失效（旧客户端兼容被破坏）: %s", b)
		}
	})
	t.Run("新字段往返", func(t *testing.T) {
		b := mustMarshal(t, ChatSendParams{Text: "你好", Effort: EffortHigh, Approval: ApprovalStrict, Agent: "coder"})
		var got ChatSendParams
		mustUnmarshal(t, b, &got)
		if got.Text != "你好" || got.Effort != EffortHigh || got.Approval != ApprovalStrict || got.Agent != "coder" {
			t.Fatalf("往返丢字段: %+v", got)
		}
		// 旧形状（不带 agent）不产生键
		b2 := mustMarshal(t, ChatSendParams{Text: "你好"})
		if strings.Contains(string(b2), `"agent"`) {
			t.Fatalf("agent 空时不应产生键: %s", b2)
		}
	})
	t.Run("值域校验", func(t *testing.T) {
		for _, ok := range []string{"", EffortMinimal, EffortLow, EffortMedium, EffortHigh} {
			if !ValidateEffort(ok) {
				t.Fatalf("合法 effort 被拒: %q", ok)
			}
		}
		if ValidateEffort("xhigh") || ValidateEffort("extreme") {
			t.Fatal("非法 effort 应被拒（档位收敛为 4 档，防拼错静默无效果）")
		}
		for _, ok := range []string{"", ApprovalAuto, ApprovalConfirm, ApprovalStrict} {
			if !ValidateApproval(ok) {
				t.Fatalf("合法 approval 被拒: %q", ok)
			}
		}
		if ValidateApproval("yolo") {
			t.Fatal("非法 approval 应被拒")
		}
	})
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	return b
}

// TestAgentCatalogPayloads：agent.*/catalog.* 载荷的 wire 形状契约
// （M1——前端 AgentAdminSource 按这些键名消费；字段改名不编译报错、
// 只静默丢字段，测试钉住形状）。
func TestAgentCatalogPayloads(t *testing.T) {
	t.Run("AgentEntry 全字段往返（snake_case 键名）", func(t *testing.T) {
		a := AgentEntry{
			ID: "coder", Name: "代码 Agent", Desc: "写代码", Color: "#3b82f6",
			Model: "myt", Tools: []string{"read_file"}, Workflow: "minimal-change",
			Skills: []string{"gsap"}, Delegates: []string{}, Approval: "confirm",
			Enabled: true, Prompt: "你是代码 Agent", Custom: true,
		}
		b := mustMarshal(t, a)
		var m map[string]any
		mustUnmarshal(t, b, &m)
		for _, key := range []string{"id", "name", "desc", "color", "model", "tools", "workflow", "skills", "delegates", "approval", "enabled", "prompt", "protocol", "custom"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("AgentEntry 缺键 %s: %s", key, b)
			}
		}
		var got AgentEntry
		mustUnmarshal(t, b, &got)
		if got.ID != a.ID || len(got.Tools) != 1 || got.Tools[0] != "read_file" || got.Workflow != a.Workflow {
			t.Fatalf("AgentEntry 往返丢字段: %+v", got)
		}
	})

	t.Run("AgentAddParams 包 agent 键（add/update 同形）", func(t *testing.T) {
		b := mustMarshal(t, AgentAddParams{Agent: AgentEntry{ID: "x", Name: "n"}})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if _, ok := m["agent"]; !ok {
			t.Fatalf("应包裹 agent 键: %s", b)
		}
	})

	t.Run("ToolEntry 键名与 omitempty（params/doc/command 空时不出现）", func(t *testing.T) {
		b := mustMarshal(t, ToolEntry{ID: "bash", Desc: "d", Risk: "high", Source: "builtin", Custom: false})
		s := string(b)
		for _, key := range []string{"params", "doc", "server", "command", "example", "package_file"} {
			if strings.Contains(s, `"`+key+`"`) {
				t.Fatalf("空字段应 omitempty: %s", b)
			}
		}
		full := mustMarshal(t, ToolEntry{ID: "x", Desc: "d", Risk: "low", Source: "binary",
			Params:  []ToolParamEntry{{Name: "input", Type: "string", Required: true, Desc: "输入"}},
			Command: "x {input}", Example: "x foo", PackageFile: "x.zip", Custom: true})
		var got ToolEntry
		mustUnmarshal(t, full, &got)
		if len(got.Params) != 1 || !got.Params[0].Required || got.Params[0].Desc != "输入" {
			t.Fatalf("ToolEntry params 往返失真: %+v", got.Params)
		}
	})

	t.Run("McServerEntry 键名（stdio/sse 双形态）", func(t *testing.T) {
		stdio := mustMarshal(t, McServerEntry{ID: "fs", Transport: "stdio", Command: "npx",
			Args: []string{"-y", "@mcp/fs"}, Env: map[string]string{"K": "V"}, Enabled: true, Custom: true})
		var got McServerEntry
		mustUnmarshal(t, stdio, &got)
		if got.Transport != "stdio" || len(got.Args) != 2 || got.Env["K"] != "V" {
			t.Fatalf("McServer stdio 往返失真: %+v", got)
		}
		sse := mustMarshal(t, McServerEntry{ID: "r", Transport: "sse", URL: "https://x/sse", Enabled: false, Custom: true})
		var got2 McServerEntry
		mustUnmarshal(t, sse, &got2)
		if got2.Transport != "sse" || got2.URL != "https://x/sse" {
			t.Fatalf("McServer sse 往返失真: %+v", got2)
		}
	})

	t.Run("CatalogChangedParams 标 kind（客户端只重拉对应目录）", func(t *testing.T) {
		b := mustMarshal(t, CatalogChangedParams{Kind: "tools", Reason: "add"})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["kind"] != "tools" || m["reason"] != "add" {
			t.Fatalf("载荷不符: %s", b)
		}
	})
}

// TestDispatchPayloads：M3 dispatch 事件的 wire 形状（dispatch_id 归属
// 键 + Start/End 载荷——前端 DispatchCard 按这些键消费）。
func TestDispatchPayloads(t *testing.T) {
	t.Run("子事件 dispatch_id omitempty（主时间线无键——旧客户端形状不变）", func(t *testing.T) {
		b := mustMarshal(t, DeltaParams{Kind: "text", Text: "hi"})
		if strings.Contains(string(b), "dispatch_id") {
			t.Fatalf("主时间线的 delta 不应带 dispatch_id: %s", b)
		}
		b2 := mustMarshal(t, DeltaParams{Kind: "text", Text: "hi", DispatchID: "d1"})
		var got DeltaParams
		mustUnmarshal(t, b2, &got)
		if got.DispatchID != "d1" {
			t.Fatalf("dispatch_id 往返失真: %+v", got)
		}
	})

	t.Run("DispatchStartParams 有独立的 owner 与 child session 字段", func(t *testing.T) {
		b := mustMarshal(t, DispatchStartParams{
			OwnerSessionID: "parent-1", DispatchID: "d1", SessionID: "child-1", AgentID: "coder", AgentName: "代码 Agent", AgentColor: "#3b82f6", Task: "跑测试",
		})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		for _, key := range []string{"owner_session_id", "session_id", "dispatch_id", "agent_id", "agent_name", "agent_color", "task"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("DispatchStartParams 缺键 %s: %s", key, b)
			}
		}
		var got DispatchStartParams
		mustUnmarshal(t, b, &got)
		if got.OwnerSessionID != "parent-1" || got.SessionID != "child-1" {
			t.Fatalf("owner 与 child session 混淆: %+v", got)
		}
	})

	t.Run("DispatchEndParams（result/is_error/usage_tokens）", func(t *testing.T) {
		b := mustMarshal(t, DispatchEndParams{OwnerSessionID: "parent-1", SessionID: "child-1", DispatchID: "d1", Result: "全绿", UsageTokens: 730})
		var got DispatchEndParams
		mustUnmarshal(t, b, &got)
		if got.Result != "全绿" || got.UsageTokens != 730 || got.IsError || got.OwnerSessionID != "parent-1" || got.SessionID != "child-1" {
			t.Fatalf("DispatchEnd 往返失真: %+v", got)
		}
		// usage_tokens omitempty（0 不产生键——错误收尾通常没有用量）
		b2 := mustMarshal(t, DispatchEndParams{DispatchID: "d1", Result: "失败", IsError: true})
		if strings.Contains(string(b2), "usage_tokens") {
			t.Fatalf("0 用量不应产生键: %s", b2)
		}
	})
}

// TestContextUsagePayloads：P1 计账的 wire 形状（done 与 history 携带
// context——前端指示器按这些键消费；未知时整块缺席，不是 used=0）。
func TestContextUsagePayloads(t *testing.T) {
	t.Run("DoneParams 带 context（子轮的 done 不带——omitempty）", func(t *testing.T) {
		b := mustMarshal(t, DoneParams{
			Message: llm.Message{Role: "assistant", Content: "好"}, UsageTokens: 12, FinishReason: "stop",
			Context: &ContextUsage{Used: 777, Window: 32768, System: 300, ToolResults: 200, Messages: 277},
		})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		ctx, ok := m["context"].(map[string]any)
		if !ok {
			t.Fatalf("主轮 done 应带 context: %s", b)
		}
		for _, key := range []string{"used", "window", "system", "tool_results", "messages"} {
			if _, ok := ctx[key]; !ok {
				t.Fatalf("context 缺键 %s: %s", key, b)
			}
		}
		if ctx["used"].(float64) != 777 || ctx["window"].(float64) != 32768 {
			t.Fatalf("context 数值失真: %s", b)
		}

		// 子轮 done：不带 context 键（子上下文占用不进主指示器）
		b2 := mustMarshal(t, DoneParams{Message: llm.Message{Role: "assistant"}, DispatchID: "d1"})
		if strings.Contains(string(b2), "context") {
			t.Fatalf("子轮 done 不应带 context: %s", b2)
		}
	})

	t.Run("ChatHistoryResult 带 context（未知时整键缺席）", func(t *testing.T) {
		b := mustMarshal(t, ChatHistoryResult{SessionID: "s1", Context: &ContextUsage{Used: 100, Window: 8192}})
		var got ChatHistoryResult
		mustUnmarshal(t, b, &got)
		if got.Context == nil || got.Context.Used != 100 || got.Context.Window != 8192 {
			t.Fatalf("history context 往返失真: %+v", got.Context)
		}
		b2 := mustMarshal(t, ChatHistoryResult{SessionID: "s1"})
		if strings.Contains(string(b2), "context") {
			t.Fatalf("未知占用应整键缺席（客户端显示中性态）: %s", b2)
		}
	})
}

// TestCompactionPayloads：P3 压缩的 wire 形状（手动方法/事件/历史检查点下标）。
func TestCompactionPayloads(t *testing.T) {
	t.Run("chat.compact 方法名与 chat.compacted 事件名不同名", func(t *testing.T) {
		if MethodChatCompact == EventCompacted {
			t.Fatal("方法名与事件名不得重名（dispatch 与事件处理会撞车）")
		}
		if MethodChatCompact != "chat.compact" || EventCompacted != "chat.compacted" {
			t.Fatalf("命名漂移: %s / %s", MethodChatCompact, EventCompacted)
		}
	})

	t.Run("CompactParams agent omitempty（空 = 主 Agent）", func(t *testing.T) {
		b := mustMarshal(t, CompactParams{})
		if strings.Contains(string(b), "agent") {
			t.Fatalf("空 agent 不应产生键: %s", b)
		}
		b2 := mustMarshal(t, CompactParams{Agent: "coder"})
		var got CompactParams
		mustUnmarshal(t, b2, &got)
		if got.Agent != "coder" {
			t.Fatalf("agent 往返失真: %+v", got)
		}
	})

	t.Run("CompactResult（compacted=false 时无统计键）", func(t *testing.T) {
		b := mustMarshal(t, CompactResult{Compacted: true, Before: 8000, After: 2400, Shadowed: 12})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		for _, key := range []string{"compacted", "before", "after", "shadowed"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("CompactResult 缺键 %s: %s", key, b)
			}
		}
		b2 := mustMarshal(t, CompactResult{})
		if !strings.Contains(string(b2), `"compacted":false`) {
			t.Fatalf("无区间应显式回 compacted=false: %s", b2)
		}
		if strings.Contains(string(b2), "shadowed") {
			t.Fatalf("无区间不应带统计键: %s", b2)
		}
	})

	t.Run("CompactedParams（before/after/shadowed/summary + manual omitempty）", func(t *testing.T) {
		b := mustMarshal(t, CompactedParams{Before: 8000, After: 2400, Shadowed: 12, Summary: "摘要"})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		for _, key := range []string{"before", "after", "shadowed", "summary"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("CompactedParams 缺键 %s: %s", key, b)
			}
		}
		if strings.Contains(string(b), "manual") {
			t.Fatalf("自动压缩不应带 manual 键: %s", b)
		}
	})

	t.Run("ChatHistoryResult.Checkpoints（下标数组；空时整键缺席）", func(t *testing.T) {
		b := mustMarshal(t, ChatHistoryResult{SessionID: "s1", Checkpoints: []int{0}})
		var got ChatHistoryResult
		mustUnmarshal(t, b, &got)
		if len(got.Checkpoints) != 1 || got.Checkpoints[0] != 0 {
			t.Fatalf("检查点下标往返失真: %+v", got.Checkpoints)
		}
		b2 := mustMarshal(t, ChatHistoryResult{SessionID: "s1"})
		if strings.Contains(string(b2), "checkpoints") {
			t.Fatalf("无检查点应整键缺席: %s", b2)
		}
	})
}

func TestSessionScopedWireFields(t *testing.T) {
	requestCases := []struct {
		name   string
		params any
	}{
		{"chat.send", ChatSendParams{SessionID: "s1", Text: "hello"}},
		{"chat.cancel", ChatSessionParams{SessionID: "s1"}},
		{"chat.history", ChatHistoryParams{SessionID: "s1"}},
		{"chat.compact", CompactParams{SessionID: "s1"}},
		{"tool.confirm", ToolConfirmParams{SessionID: "s1", ID: "c1", Allow: true}},
	}
	for _, tc := range requestCases {
		t.Run(tc.name, func(t *testing.T) {
			var params map[string]any
			mustUnmarshal(t, mustMarshal(t, tc.params), &params)
			if params["session_id"] != "s1" {
				t.Fatalf("request missing explicit session_id: %v", params)
			}
		})
	}

	t.Run("普通事件带所属 session_id", func(t *testing.T) {
		for _, params := range []any{
			UserMessageParams{SessionID: "s1"}, DeltaParams{SessionID: "s1"},
			BusyParams{SessionID: "s1"}, CompactedParams{SessionID: "s1"},
		} {
			var payload map[string]any
			mustUnmarshal(t, mustMarshal(t, params), &payload)
			if payload["session_id"] != "s1" {
				t.Fatalf("event missing session_id: %v", payload)
			}
		}
	})

	t.Run("dispatch 同时保留 owner 与 child session", func(t *testing.T) {
		for _, params := range []any{
			DispatchStartParams{OwnerSessionID: "parent", SessionID: "child"},
			DispatchEndParams{OwnerSessionID: "parent", SessionID: "child"},
		} {
			var payload map[string]any
			mustUnmarshal(t, mustMarshal(t, params), &payload)
			if payload["owner_session_id"] != "parent" || payload["session_id"] != "child" {
				t.Fatalf("dispatch owner/child ids are ambiguous: %v", payload)
			}
		}
	})
}

func mustUnmarshal(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, b)
	}
}
