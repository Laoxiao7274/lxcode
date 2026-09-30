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
		MethodChatCancel, MethodChatHistory, MethodToolConfirm, MethodChatApproval,
		MethodSessionList, MethodSessionNew, MethodSessionResume, MethodSessionWorktreeRelease,
		MethodJobList, MethodJobKill, MethodJobLog}
	events := []string{EventReady, EventUserMsg, EventDelta, EventToolCall, EventToolRslt,
		EventConfirm, EventDone, EventError, EventBusy, EventApproval, EventModels, EventTodo, EventSessionChanged,
		EventJobStarted, EventJobSettled}
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

// TestChatRewindPayloads：chat.rewind 的 wire 形状（参数/结果/事件键名与往返），以及
// seq 必须**同时**出现在两条给前端的路径上（chat.history 的 messages[] 与
// chat.userMessage 的 message）。字段改名不编译报错、只静默丢字段——所以形状由测试钉住。
func TestChatRewindPayloads(t *testing.T) {
	t.Run("方法名与事件名不同名且命名不漂移", func(t *testing.T) {
		if MethodChatRewind == EventRewound {
			t.Fatal("方法名与事件名不得重名（dispatch 与事件处理会撞车）")
		}
		if MethodChatRewind != "chat.rewind" || EventRewound != "chat.rewound" {
			t.Fatalf("命名漂移: %s / %s", MethodChatRewind, EventRewound)
		}
	})

	t.Run("ChatRewindParams（session_id + seq 键名与往返）", func(t *testing.T) {
		b := mustMarshal(t, ChatRewindParams{SessionID: "s1", Seq: 7})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["session_id"] != "s1" || m["seq"].(float64) != 7 {
			t.Fatalf("参数键名不符: %s", b)
		}
		var got ChatRewindParams
		mustUnmarshal(t, b, &got)
		if got.SessionID != "s1" || got.Seq != 7 {
			t.Fatalf("参数往返丢字段: %+v", got)
		}
	})

	t.Run("ChatRewindResult（removed 恒发，0 也要有键）", func(t *testing.T) {
		b := mustMarshal(t, ChatRewindResult{Removed: 3})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["removed"].(float64) != 3 {
			t.Fatalf("结果键名不符: %s", b)
		}
		// 幂等空操作（removed=0）也要显式回 0：客户端据此确认"确实没有东西可删"，
		// 整键缺席会被当成服务端没实现这个字段
		b0 := mustMarshal(t, ChatRewindResult{})
		if !strings.Contains(string(b0), `"removed":0`) {
			t.Fatalf("removed 应恒发: %s", b0)
		}
	})

	t.Run("ChatRewoundParams（seq/removed + context 未知时整键缺席）", func(t *testing.T) {
		b := mustMarshal(t, ChatRewoundParams{
			SessionID: "s1", Seq: 7, Removed: 3,
			Context: &ContextUsage{Used: 900, Window: 32768, Messages: 900},
		})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["session_id"] != "s1" || m["seq"].(float64) != 7 || m["removed"].(float64) != 3 {
			t.Fatalf("事件键名不符: %s", b)
		}
		// context 用的是**同一个** ContextUsage 形状（前端不必认第二个结构）
		ctx, ok := m["context"].(map[string]any)
		if !ok || ctx["used"].(float64) != 900 || ctx["window"].(float64) != 32768 {
			t.Fatalf("context 应为同一个 ContextUsage 形状: %s", b)
		}
		// 未知（本会话还没跑过主轮 / 纯内存模式）时整键缺席——发零值等于显示 0%，
		// 那是编出来的假信息（前端显示中性态）
		b2 := mustMarshal(t, ChatRewoundParams{SessionID: "s1", Seq: 7})
		if strings.Contains(string(b2), "context") {
			t.Fatalf("未知占用应整键缺席: %s", b2)
		}
	})

	t.Run("seq 在历史消息与实时用户消息上都在（同一个字段名）", func(t *testing.T) {
		b := mustMarshal(t, ChatHistoryResult{Messages: []llm.Message{{Role: "user", Content: "hi", Seq: 3}}})
		var hist map[string]any
		mustUnmarshal(t, b, &hist)
		msgs, ok := hist["messages"].([]any)
		if !ok || len(msgs) != 1 || msgs[0].(map[string]any)["seq"].(float64) != 3 {
			t.Fatalf("chat.history 的 messages[].seq 丢了: %s", b)
		}
		b2 := mustMarshal(t, UserMessageParams{SessionID: "s1", Message: llm.Message{Role: "user", Content: "hi", Seq: 4}})
		var ev map[string]any
		mustUnmarshal(t, b2, &ev)
		if ev["message"].(map[string]any)["seq"].(float64) != 4 {
			t.Fatalf("chat.userMessage 的 message.seq 丢了: %s", b2)
		}
		// 序号 0 = 还没落库的消息：不发键，不能编一个 0 号让前端当撤回锚点
		b3 := mustMarshal(t, llm.Message{Role: "assistant", Content: "没落库"})
		if strings.Contains(string(b3), "seq") {
			t.Fatalf("未落库的消息不应带 seq: %s", b3)
		}
	})
}

// TestChatApprovalPayloads：chat.approval 的 wire 形状（参数/结果/事件键名与往返）。
// 字段改名不编译报错、只静默丢字段——所以形状必须由测试钉住。
func TestChatApprovalPayloads(t *testing.T) {
	t.Run("方法名与事件名不同名且命名不漂移", func(t *testing.T) {
		if MethodChatApproval == EventApproval {
			t.Fatal("方法名与事件名不得重名（dispatch 与事件处理会撞车）")
		}
		if MethodChatApproval != "chat.approval" || EventApproval != "chat.approvalChanged" {
			t.Fatalf("命名漂移: %s / %s", MethodChatApproval, EventApproval)
		}
	})

	t.Run("ChatApprovalParams 键名与往返", func(t *testing.T) {
		b := mustMarshal(t, ChatApprovalParams{SessionID: "s1", Approval: ApprovalAuto})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["session_id"] != "s1" || m["approval"] != ApprovalAuto {
			t.Fatalf("参数键名不符: %s", b)
		}
		var got ChatApprovalParams
		mustUnmarshal(t, b, &got)
		if got.SessionID != "s1" || got.Approval != ApprovalAuto {
			t.Fatalf("参数往返丢字段: %+v", got)
		}
		// approval 不是 omitempty：空串也要显式发出去（服务端据此规范化成 confirm）
		b2 := mustMarshal(t, ChatApprovalParams{SessionID: "s1"})
		if !strings.Contains(string(b2), `"approval"`) {
			t.Fatalf("approval 应恒发（空 = 回落 confirm）: %s", b2)
		}
	})

	t.Run("ChatApprovalResult 键名与往返", func(t *testing.T) {
		b := mustMarshal(t, ChatApprovalResult{Approval: ApprovalStrict})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["approval"] != ApprovalStrict {
			t.Fatalf("结果键名不符: %s", b)
		}
		var got ChatApprovalResult
		mustUnmarshal(t, b, &got)
		if got.Approval != ApprovalStrict {
			t.Fatalf("结果往返丢字段: %+v", got)
		}
	})

	t.Run("ApprovalChangedParams 键名与往返", func(t *testing.T) {
		b := mustMarshal(t, ApprovalChangedParams{SessionID: "s1", Approval: ApprovalConfirm})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["session_id"] != "s1" || m["approval"] != ApprovalConfirm {
			t.Fatalf("事件键名不符: %s", b)
		}
		var got ApprovalChangedParams
		mustUnmarshal(t, b, &got)
		if got.SessionID != "s1" || got.Approval != ApprovalConfirm {
			t.Fatalf("事件往返丢字段: %+v", got)
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
		{"chat.approval", ChatApprovalParams{SessionID: "s1", Approval: ApprovalAuto}},
		{"chat.rewind", ChatRewindParams{SessionID: "s1", Seq: 1}},
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
			ApprovalChangedParams{SessionID: "s1"},
			ChatRewoundParams{SessionID: "s1"},
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

// TestJobPayloads：后台任务的 wire 形状（docs/jobs.md §4）。前端按这些键名
// 消费（JobInfo 一份形状同时服务 job.list 与两个事件）；改名不编译报错、只静默
// 丢字段，所以必须有测试钉住。
func TestJobPayloads(t *testing.T) {
	t.Run("JobInfo 全字段往返（snake_case 键名）", func(t *testing.T) {
		b := mustMarshal(t, JobInfo{
			ID: "job-1", Kind: "bash", Label: "go test ./...", Status: "running",
			SessionID: "s1", StartedAt: "2026-09-29T10:00:00Z",
			OutputTail: "ok\n", OutputPath: "/x/jobs/job-1.log",
		})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		for _, key := range []string{"id", "kind", "label", "status", "session_id", "started_at", "output_tail", "output_path"} {
			if _, ok := m[key]; !ok {
				t.Fatalf("JobInfo 缺键 %s: %s", key, b)
			}
		}
		// 未结束 / 未归属时不产生键（前端据此判断"还在跑"）
		if strings.Contains(string(b), "finished_at") || strings.Contains(string(b), "ended_by") {
			t.Fatalf("未结束不该有 finished_at/ended_by: %s", b)
		}
		var got JobInfo
		mustUnmarshal(t, b, &got)
		if got.ID != "job-1" || got.Label != "go test ./..." || got.Status != "running" {
			t.Fatalf("JobInfo 往返失真: %+v", got)
		}
		settled := mustMarshal(t, JobInfo{
			ID: "job-1", Status: "killed", EndedBy: "user", Detail: "已取消",
			FinishedAt: "2026-09-29T10:00:05Z",
		})
		var got2 JobInfo
		mustUnmarshal(t, settled, &got2)
		if got2.EndedBy != "user" || got2.FinishedAt == "" || got2.Detail != "已取消" {
			t.Fatalf("结束归属往返失真（前端据此显示「你停的」）: %+v", got2)
		}
	})

	t.Run("job.list / job.kill / job.log 参数与结果键名", func(t *testing.T) {
		if b := mustMarshal(t, JobListParams{}); strings.Contains(string(b), "session_id") {
			t.Fatalf("空 session_id 应省略（默认列全部）: %s", b)
		}
		var gotList JobListResult
		mustUnmarshal(t, mustMarshal(t, JobListResult{Jobs: []JobInfo{{ID: "job-1"}}}), &gotList)
		if len(gotList.Jobs) != 1 || gotList.Jobs[0].ID != "job-1" {
			t.Fatalf("JobListResult 往返失真: %+v", gotList)
		}
		// owner_session_id 是**时间线归属**（子 Agent 起的任务挂在父会话上，前端
		// 据此把它放进父会话的时间线）；与执行会话 session_id 是两回事，都要发。
		var gotOwner JobInfo
		mustUnmarshal(t, mustMarshal(t, JobInfo{ID: "job-1", SessionID: "s-child", OwnerSessionID: "s-owner"}), &gotOwner)
		if gotOwner.SessionID != "s-child" || gotOwner.OwnerSessionID != "s-owner" {
			t.Fatalf("owner_session_id 往返失真: %+v", gotOwner)
		}
		if b := mustMarshal(t, JobInfo{ID: "job-1"}); strings.Contains(string(b), "owner_session_id") {
			t.Fatalf("空 owner_session_id 应省略: %s", b)
		}
		b := mustMarshal(t, JobKillParams{ID: "job-1"})
		if string(b) != `{"id":"job-1"}` {
			t.Fatalf("JobKillParams JSON = %s", b)
		}
		var gotKill JobKillResult
		mustUnmarshal(t, mustMarshal(t, JobKillResult{Job: JobInfo{ID: "job-1", Status: "stopping", EndedBy: "user"}}), &gotKill)
		if gotKill.Job.Status != "stopping" || gotKill.Job.EndedBy != "user" {
			t.Fatalf("JobKillResult 往返失真: %+v", gotKill.Job)
		}
		var gotLog JobLogResult
		mustUnmarshal(t, mustMarshal(t, JobLogResult{Data: "out", Truncated: true}), &gotLog)
		if gotLog.Data != "out" || !gotLog.Truncated {
			t.Fatalf("JobLogResult 往返失真: %+v", gotLog)
		}
		// 恒发 truncated（false 也显式发）：前端不必区分"键缺席"与"没截断"两种状态
		if b := mustMarshal(t, JobLogResult{Data: "out"}); !strings.Contains(string(b), `"truncated":false`) {
			t.Fatalf("truncated 应恒发（false 也显式）: %s", b)
		}
	})

	t.Run("通告前缀（前端据此区别于用户气泡）", func(t *testing.T) {
		if JobNoticePrefix == "" || !strings.HasSuffix(JobNoticePrefix, " ") {
			t.Fatalf("通告前缀应非空且以空格结尾（拼接文本时不会粘连）: %q", JobNoticePrefix)
		}
	})
}

// TestRoundTimingPayloads：每轮计时/模型在 wire 上的形状（字段改名不编译报错、
// 只静默丢字段——形状必须由测试钉住），以及"未知"时整键缺席。
//
// 两条路径必须同名同义：chat.done 的顶层三个键（live）与 message 里的同名字段
// （chat.history 回放读的就是它）。前端只要认一套键名就能同时处理实时与刷新。
func TestRoundTimingPayloads(t *testing.T) {
	t.Run("chat.done 带 first_token_ms / duration_ms / model", func(t *testing.T) {
		b := mustMarshal(t, DoneParams{
			SessionID: "s1", Message: llm.Message{Role: "assistant", Content: "hi"},
			UsageTokens: 12, FinishReason: llm.FinishStop,
			FirstTokenMs: 210, DurationMs: 1500, Model: "m1",
		})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["first_token_ms"].(float64) != 210 || m["duration_ms"].(float64) != 1500 || m["model"] != "m1" {
			t.Fatalf("chat.done 的计时键名不符: %s", b)
		}
		// 未知（工具轮没有首 token / provider 不回报用量）时整键缺席——发 0 会被
		// 前端显示成"0ms 首字"，那是编出来的信息
		b2 := mustMarshal(t, DoneParams{SessionID: "s1", Message: llm.Message{Role: "assistant", Content: "hi"}})
		var m2 map[string]any
		mustUnmarshal(t, b2, &m2)
		for _, k := range []string{"first_token_ms", "duration_ms", "model"} {
			if _, hit := m2[k]; hit {
				t.Fatalf("未知的 %s 应整键缺席: %s", k, b2)
			}
		}
	})

	t.Run("历史消息上同名同义（回放路径）", func(t *testing.T) {
		b := mustMarshal(t, llm.Message{
			Role: "assistant", Content: "hi",
			FirstTokenMs: 210, DurationMs: 1500, Model: "m1", UsageTokens: 12,
		})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["first_token_ms"].(float64) != 210 || m["duration_ms"].(float64) != 1500 ||
			m["model"] != "m1" || m["usage_tokens"].(float64) != 12 {
			t.Fatalf("消息上的计时字段不符: %s", b)
		}
		// 零值（未知）整键缺席：0 不是"很快"，是"没测到"
		b2 := mustMarshal(t, llm.Message{Role: "assistant", Content: "hi"})
		for _, k := range []string{"first_token_ms", "duration_ms", "model", "usage_tokens"} {
			if strings.Contains(string(b2), "\""+k+"\"") {
				t.Fatalf("未知的 %s 应整键缺席: %s", k, b2)
			}
		}
	})

	t.Run("chat.history 带 model（未知时整键缺席）", func(t *testing.T) {
		b := mustMarshal(t, ChatHistoryResult{SessionID: "s1", Model: "m2"})
		var m map[string]any
		mustUnmarshal(t, b, &m)
		if m["model"] != "m2" {
			t.Fatalf("chat.history 的 model 键不符: %s", b)
		}
		// 解析不出模型（Agent 没了/没绑模型）时不编一个模型名
		b2 := mustMarshal(t, ChatHistoryResult{SessionID: "s1"})
		if strings.Contains(string(b2), "model") {
			t.Fatalf("未知模型应整键缺席: %s", b2)
		}
	})
}

func mustUnmarshal(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, b)
	}
}
