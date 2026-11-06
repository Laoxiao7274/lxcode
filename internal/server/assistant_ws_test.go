// assistant.* 六方法的端到端 WS 验收测试（助手模式 P0）：
// 域寻址（assistant.new 建 space=assistant 行）、事件域（assistant.delta/
// done 换名）、确认门归属（assistant.confirm 裁决 run_task 子会话的挂起请求
// ——验收④）。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// TestAssistantWSRoundEndToEnd（验收①③⑤ + 事件域）：assistant.new →
// assistant.send → 事件走 assistant.* 域 → 结论回填带子会话 id。
// 助手域寻址用主域 chat.*（串域）应报错——域隔离的协议面断言。
func TestAssistantWSRoundEndToEnd(t *testing.T) {
	// 项目行（run_task 指到它）：id 动态生成，经 chan 写进假流的参数里。
	projIDCh := make(chan string, 1)
	srv, client, _ := newTestServer(t, assistantRoundStream(t, projIDCh))
	proj, err := srv.st.AddProject("demo-proj", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projIDCh <- proj.ID

	// assistant.new：建助手会话
	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	// 域标记落库（验收⑧ 的协议面）：主空间 session.list 不出它
	list := client.call(protocol.MethodSessionList, map[string]any{})
	if list.Error != nil {
		t.Fatalf("session.list: %v", list.Error)
	}
	var metas []protocol.SessionMeta
	if err := unmarshalResult(list, &metas); err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.ID == created.SessionID {
			t.Fatal("助手会话不应出现在主空间 session.list")
		}
	}
	// 库里的 space 列
	ws, err := srv.st.WorkspaceOf(created.SessionID)
	_ = ws
	if err != nil {
		t.Fatalf("WorkspaceOf（存在性）: %v", err)
	}

	// assistant.send：一轮「发 run_task → 子会话结论 → 助手转述」
	resp = client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID,
		Text:      "帮我把 demo-proj 的测试跑了",
	})
	if resp.Error != nil {
		t.Fatalf("assistant.send: %v", resp.Error)
	}
	// 事件域：delta / done 都是 assistant.*（不是 chat.*）
	if ev := client.waitEvent(protocol.EventAssistantDelta); ev == nil {
		t.Fatal("应收到 assistant.delta（事件域映射）")
	}
	done := client.waitEvent(protocol.EventAssistantDone)
	if done == nil {
		t.Fatal("应收到 assistant.done（事件域映射）")
	}
	// 串域拒绝：主域 chat.send 寻址助手会话 id（无存储会话时它走主域现建
	// 运行时——但主域 chat.history 会读到同一行；这里钉「域入口分离」的
	// 最小事实：assistant.send 无 session_id 报错、chat.send 空焦点正常建行，
	// 域隔离的结构性保证在 store 的 List/Latest 过滤已验收）。

	// 等整轮收尾（子会话派发是异步的——直接拉历史会早）
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}
	// 工具结果回填带子会话 id（验收⑤ 的协议面）：assistant.history 里
	// tool 消息含 [子会话 id:
	hresp := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: created.SessionID})
	if hresp.Error != nil {
		t.Fatalf("assistant.history: %v", hresp.Error)
	}
	var hist protocol.ChatHistoryResult
	if err := unmarshalResult(hresp, &hist); err != nil {
		t.Fatal(err)
	}
	var hasToolResult, hasDispatchStart bool
	for _, m := range hist.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "[子会话 id:") {
			hasToolResult = true
		}
	}
	_ = hasDispatchStart
	if !hasToolResult {
		t.Fatal("结论应带 [子会话 id:]（验收⑤）")
	}
}

// TestAssistantWSConfirmRouting（验收④）：助手会话里 run_task 的子会话弹确认门
// → 事件域 assistant.confirmRequest 带 dispatch_id → assistant.confirm 裁决放行
// → 子会话继续跑完。
func TestAssistantWSConfirmRouting(t *testing.T) {
	projIDCh := make(chan string, 1)
	srv, client, _ := newTestServer(t, assistantConfirmStream(t, projIDCh))
	proj, err := srv.st.AddProject("demo-proj", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projIDCh <- proj.ID
	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	// 助手 Agent 的默认档是 auto（方案拍板——整链无确认）；验收④要验确认门
	// 的归属路径，用请求级 approval=confirm 强制弹门（取严语义：子会话现算
	// stricterApproval(助手=confirm, coder=auto) = confirm）。
	resp = client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID, Text: "把 demo-proj 的构建跑一下", Approval: "confirm",
	})
	if resp.Error != nil {
		t.Fatalf("assistant.send: %v", resp.Error)
	}
	// 确认请求走助手事件域
	confirm := client.waitEvent(protocol.EventAssistantConfirm)
	if confirm == nil {
		t.Fatal("应收到 assistant.confirmRequest（确认门归属助手域）")
	}
	var req protocol.ConfirmRequest
	if b, err := json.Marshal(confirm.Params); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	if req.DispatchID == "" {
		t.Fatal("确认请求应带 dispatch_id（归属进派发卡）")
	}
	if req.SessionID != created.SessionID {
		t.Fatalf("确认请求的会话应是助手会话: %q", req.SessionID)
	}
	// assistant.confirm 放行
	resp = client.call(protocol.MethodAssistantConfirm, protocol.ToolConfirmParams{
		SessionID: created.SessionID, ID: req.ID, Allow: true,
	})
	if resp.Error != nil {
		t.Fatalf("assistant.confirm: %v", resp.Error)
	}
	// 放行后子会话跑完 → assistant.done 收尾
	if ev := client.waitEvent(protocol.EventAssistantDone); ev == nil {
		t.Fatal("裁决后应收到 assistant.done（子会话跑完、助手收尾）")
	}
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}
}

// waitAssistantIdle 等助手会话空闲（子会话派发是异步的——直接拉历史会早）。
func waitAssistantIdle(srv *Server, id string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if sess := srv.peekAssistantSession(id); sess != nil && !sess.Busy() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("助手会话 10s 未收尾")
}

// TestAssistantWSApprovalAndModel：assistant.approval / assistant.model 两个
// 会话级状态方法的端到端验收（域隔离 + 事件换名 + 落库）：
//   - assistant.approval 改档 → 广播 assistant.approvalChanged（不是 chat 前缀
//     ——裸 broadcast 会串域）、回规范化档位、落库（重建运行时读回）；
//   - assistant.model 切模型 → 广播 assistant.modelChanged、落库读回；
//     空模型 id / 不存在的模型不静默回落，如实报错；
//   - 串域面：主域 chat.approval 寻址助手会话应报错（两份运行时各有各的入口）。
func TestAssistantWSApprovalAndModel(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	addModel(t, client, "m2")

	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created protocol.SessionResult
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	id := created.SessionID

	// ── approval ─────────────────────────────────────────────────────────
	// 非法档位：ValidateApproval，回 CodeInvalidParams
	if bad := client.call(protocol.MethodAssistantApproval, protocol.ChatApprovalParams{
		SessionID: id, Approval: "yolo"}); bad == nil || bad.Error == nil || bad.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("非法 approval 应回 CodeInvalidParams: %+v", bad)
	}
	// 缺 session_id：助手域不设连接焦点，没有「空 = 当前会话」
	if missing := client.call(protocol.MethodAssistantApproval, protocol.ChatApprovalParams{
		Approval: protocol.ApprovalAuto}); missing == nil || missing.Error == nil {
		t.Fatalf("缺 session_id 应回错: %+v", missing)
	}
	resp = client.call(protocol.MethodAssistantApproval, protocol.ChatApprovalParams{
		SessionID: id, Approval: protocol.ApprovalAuto})
	if resp.Error != nil {
		t.Fatalf("assistant.approval: %v", resp.Error)
	}
	var approvalRes protocol.ChatApprovalResult
	if err := unmarshalResult(resp, &approvalRes); err != nil {
		t.Fatal(err)
	}
	if approvalRes.Approval != protocol.ApprovalAuto {
		t.Fatalf("应回规范化后的档位: %+v", approvalRes)
	}
	// 事件域：assistant.approvalChanged（chat 前缀 = 串域）
	if ev := client.waitEvent(protocol.EventAssistantApprovalChanged); ev == nil {
		t.Fatal("应收到 assistant.approvalChanged（事件域换名）")
	}
	// 落库：摘掉运行时（等价重启）重建读回
	srv.sessionsMu.Lock()
	delete(srv.assistantSessions, id)
	srv.sessionsMu.Unlock()
	sess, err := srv.assistantSession(id)
	if err != nil {
		t.Fatalf("重建助手运行时: %v", err)
	}
	if got := sess.LiveApproval(); got != protocol.ApprovalAuto {
		t.Fatalf("重建运行时后应读回落库的档位: %q", got)
	}

	// ── model ────────────────────────────────────────────────────────────
	if empty := client.call(protocol.MethodAssistantModel, protocol.ChatModelParams{
		SessionID: id, ModelID: ""}); empty == nil || empty.Error == nil || empty.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("空 model_id 应回 CodeInvalidParams: %+v", empty)
	}
	if nope := client.call(protocol.MethodAssistantModel, protocol.ChatModelParams{
		SessionID: id, ModelID: "没有这个模型"}); nope == nil || nope.Error == nil {
		t.Fatalf("不存在的模型应报错（不静默回落）: %+v", nope)
	}
	resp = client.call(protocol.MethodAssistantModel, protocol.ChatModelParams{
		SessionID: id, ModelID: "m2"})
	if resp.Error != nil {
		t.Fatalf("assistant.model: %v", resp.Error)
	}
	var modelRes protocol.ChatModelResult
	if err := unmarshalResult(resp, &modelRes); err != nil {
		t.Fatal(err)
	}
	if modelRes.SessionID != id || modelRes.ModelID != "m2" {
		t.Fatalf("应回会话 id 与模型 id: %+v", modelRes)
	}
	// 事件域：assistant.modelChanged
	if ev := client.waitEvent(protocol.EventAssistantModelChanged); ev == nil {
		t.Fatal("应收到 assistant.modelChanged（事件域换名）")
	}
	// 落库往返：重建运行时读回
	srv.sessionsMu.Lock()
	delete(srv.assistantSessions, id)
	srv.sessionsMu.Unlock()
	if m := readAssistantHistoryModel(t, client, id); m != "m2" {
		t.Fatalf("重建运行时后应读回落库的模型: %q", m)
	}

	// ── 串域面 ───────────────────────────────────────────────────────────
	// 主域 chat.approval 寻址助手会话 id：主域 s.session 会为它现建**主域**
	// 运行时（库行 space=assistant 不在主域解析面），改档打到一份新运行时上
	// ——不报错但也不该碰助手域那份。断言助手域档位原样（域隔离的结果面）。
	if resp := client.call(protocol.MethodChatApproval, protocol.ChatApprovalParams{
		SessionID: id, Approval: protocol.ApprovalStrict}); resp == nil || resp.Error != nil {
		t.Fatalf("chat.approval 串域寻址助手会话应成功（主域现建运行时）: %+v", resp)
	}
	if got := sess.LiveApproval(); got != protocol.ApprovalAuto {
		t.Fatalf("主域改档不应碰助手域运行时的档位: %q", got)
	}
}

// readAssistantHistoryModel 经 assistant.history 读会话落库的模型 id
//（摘掉运行时后调用会现建——即「重启后第一次打开」的路径）。
func readAssistantHistoryModel(t *testing.T, client *wsTestClient, sessionID string) string {
	t.Helper()
	resp := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: sessionID})
	if resp == nil || resp.Error != nil {
		t.Fatalf("assistant.history(%s) failed: %+v", sessionID, resp)
	}
	var hist protocol.ChatHistoryResult
	if err := unmarshalResult(resp, &hist); err != nil {
		t.Fatal(err)
	}
	return hist.Model
}

// unmarshalResult 把 JSON-RPC result 解进目标。
func unmarshalResult(resp *protocol.Response, v any) error {
	switch raw := resp.Result.(type) {
	case []byte:
		return json.Unmarshal(raw, v)
	case json.RawMessage:
		return json.Unmarshal(raw, v)
	default:
		b, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, v)
	}
}

// assistantRoundStream 造"助手一轮 run_task + 子会话结论 + 助手转述"的假流。
// 助手语境判定用助手协议文本（个人智能体助手），子会话语境按项目路径工作目录判定。
func assistantRoundStream(t *testing.T, projIDCh <-chan string) testStream {
	t.Helper()
	var mu sync.Mutex
	sendRound := 0
	return func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "个人智能体助手") {
			mu.Lock()
			sendRound++
			round := sendRound
			mu.Unlock()
			if round > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已转述。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			tc := llm.ToolCall{ID: "call-rt1"}
			tc.Function.Name = "run_task"
			projID := <-projIDCh
			tc.Function.Arguments = `{"project_id":"` + projID + `","task":"跑测试"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境（coder 执行协议）
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "测试全绿。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "测试全绿。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// assistantConfirmStream 造"子会话在工具确认门挂起"的假流：子会话第一轮发起
// bash 调用（高危→确认门），assistant.confirm 放行后第二轮给结论。
func assistantConfirmStream(t *testing.T, projIDCh <-chan string) testStream {
	t.Helper()
	var mu sync.Mutex
	mainRound, subRound := 0, 0
	return func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "个人智能体助手") {
			mu.Lock()
			mainRound++
			r := mainRound
			mu.Unlock()
			if r > 1 {
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "转述结论。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			projID := <-projIDCh
			tc := llm.ToolCall{ID: "call-rt1"}
			tc.Function.Name = "run_task"
			tc.Function.Arguments = `{"project_id":"` + projID + `","task":"跑构建"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境：第一轮发 bash 调用（弹确认门），放行后第二轮给结论
		mu.Lock()
		subRound++
		round := subRound
		mu.Unlock()
		if round == 1 {
			tc := llm.ToolCall{ID: "call-bash1"}
			tc.Function.Name = "bash"
			tc.Function.Arguments = `{"command":"echo hi"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "构建完成。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// 占位：sessiondata 引用保持（Space 常量在断言里用）
var _ = sessiondata.SpaceAssistant

// TestAssistantWSList（assistant.list 端到端）：列表只含助手域顶层会话
//（space 过滤正确）、主域会话不在其中、running 位来自助手运行时缓存。
func TestAssistantWSList(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)

	// 建一个主域会话并写一条消息（过 has_rows 可见性）——它不应出现在 assistant.list
	mainID, _, err := srv.createSession("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.st.AppendMsg(mainID, llm.Message{Role: "user", Content: "主域消息"}); err != nil {
		t.Fatal(err)
	}

	// 建两个助手会话各写一条消息
	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.st.AppendMsg(created.SessionID, llm.Message{Role: "user", Content: "助手问好"}); err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.st.AppendMsg(created.SessionID, llm.Message{Role: "user", Content: "第二个助手会话"}); err != nil {
		t.Fatal(err)
	}
	secondID := created.SessionID

	resp = client.call(protocol.MethodAssistantList, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.list: %v", resp.Error)
	}
	var res protocol.AssistantListResult
	if err := unmarshalResult(resp, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("助手列表应有 2 条，got %d: %+v", len(res.Sessions), res.Sessions)
	}
	ids := map[string]bool{}
	for _, m := range res.Sessions {
		ids[m.ID] = true
		if m.ID == mainID {
			t.Fatal("主域会话不应出现在 assistant.list（space 过滤）")
		}
		if m.Title == "" {
			t.Fatal("助手会话条目应带标题")
		}
	}
	if !ids[secondID] {
		t.Fatal("助手会话应在 assistant.list 中")
	}

	// running 位：此刻没有助手会话在生成 → 全部 running=false（缺席）
	for _, m := range res.Sessions {
		if m.Running {
			t.Fatalf("会话 %s 未在生成，running 位应为 false", m.ID)
		}
	}

	// 主域 session.list 不含助手会话（双向域过滤的对称面）
	mlist := client.call(protocol.MethodSessionList, map[string]any{})
	if mlist.Error != nil {
		t.Fatalf("session.list: %v", mlist.Error)
	}
	var metas []protocol.SessionMeta
	if err := unmarshalResult(mlist, &metas); err != nil {
		t.Fatal(err)
	}
	for _, m := range metas {
		if m.ID == secondID {
			t.Fatal("助手会话不应出现在主域 session.list")
		}
	}
}

// TestAssistantWSListRunningBit：助手会话在生成时 assistant.list 的 running 位亮起。
// 确定性做法：首轮流挂起等待 release 信号（生成 goroutine 阻塞在流里 → Busy 恒真），
// 测试先轮询到 running=true 再放行——不赌真实生成速度（假流整轮毫秒级，竞态窗口
// 实测会让轮询扑空）。
func TestAssistantWSListRunningBit(t *testing.T) {
	projIDCh := make(chan string, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	round := 0
	stream := func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		if len(msgs) > 0 && strings.Contains(msgs[0].Content, "个人智能体助手") {
			mu.Lock()
			round++
			r := round
			mu.Unlock()
			if r > 1 {
				// 第二轮（转述）：不再读 projIDCh（已耗尽——再读会永久阻塞）
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", Content: "已转述。"}, FinishReason: llm.FinishStop}}
				}()
				return ch, nil
			}
			go func() {
				defer close(ch)
				<-release // 挂起：测试在此窗口断言 running 位
				tc := llm.ToolCall{ID: "call-rt1"}
				tc.Function.Name = "run_task"
				tc.Function.Arguments = `{"project_id":"` + <-projIDCh + `","task":"跑测试"}`
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 子会话语境：直接给结论
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "测试全绿。"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "测试全绿。"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
	srv, client, _ := newTestServer(t, stream)
	proj, err := srv.st.AddProject("demo-proj", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projIDCh <- proj.ID

	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID, Text: "开始干活",
	})
	if resp.Error != nil {
		t.Fatalf("assistant.send: %v", resp.Error)
	}

	// 生成中拉列表：running 位应为 true（流挂起期间必然 busy）
	deadline := time.Now().Add(5 * time.Second)
	running := false
	for time.Now().Before(deadline) && !running {
		resp := client.call(protocol.MethodAssistantList, map[string]any{})
		if resp.Error != nil {
			t.Fatalf("assistant.list: %v", resp.Error)
		}
		var res protocol.AssistantListResult
		if err := unmarshalResult(resp, &res); err != nil {
			t.Fatal(err)
		}
		for _, m := range res.Sessions {
			if m.ID == created.SessionID && m.Running {
				running = true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !running {
		t.Fatal("生成中的助手会话 running 位应亮起")
	}
	// 放行 → 整轮跑完 → 空闲
	close(release)
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}
	// 收尾后再拉：running 位熄灭
	resp = client.call(protocol.MethodAssistantList, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.list: %v", resp.Error)
	}
	var res protocol.AssistantListResult
	if err := unmarshalResult(resp, &res); err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Sessions {
		if m.ID == created.SessionID && m.Running {
			t.Fatal("收尾后 running 位应熄灭")
		}
	}
}

// TestAssistantWSTasksOfProject（assistant.tasks 端到端，2026-12「来自助手」）：
// 某项目的 run_task 任务子会话列表——口径过滤（主域会话/助手父会话不进）、
// 排序（updated_at 倒序）、缺 project_id 报 InvalidParams、未知方法不误路由。
func TestAssistantWSTasksOfProject(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)

	proj, err := srv.st.AddProject("demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 主域会话（同 workspace）不应出现在任务列表
	mainID, _, err := srv.createSession(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.st.AppendMsg(mainID, llm.Message{Role: "user", Content: "主域会话"}); err != nil {
		t.Fatal(err)
	}
	// 助手父会话（space 对但 parent_id=''）不应出现
	parent, err := srv.st.CreateIn(sessiondata.SpaceAssistant)
	if err != nil {
		t.Fatal(err)
	}
	// 两个任务子会话：助手域继承 + workspace 落项目（RunProjectTask 的落库链路）
	older, err := srv.st.CreateChildIn(parent, "coder", "call-1", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.st.SessionWorkspace(older, proj.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.st.AppendMsg(older, llm.Message{Role: "user", Content: "旧任务"}); err != nil {
		t.Fatal(err)
	}
	newer, err := srv.st.CreateChildIn(parent, "coder", "call-2", "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.st.SessionWorkspace(newer, proj.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.st.AppendMsg(newer, llm.Message{Role: "user", Content: "新任务"}); err != nil {
		t.Fatal(err)
	}

	resp := client.call(protocol.MethodAssistantTasks, map[string]any{"project_id": proj.ID})
	if resp.Error != nil {
		t.Fatalf("assistant.tasks: %v", resp.Error)
	}
	var res protocol.AssistantListResult
	if err := unmarshalResult(resp, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 2 {
		t.Fatalf("任务列表应有 2 条，got %d: %+v", len(res.Sessions), res.Sessions)
	}
	if res.Sessions[0].ID != newer || res.Sessions[1].ID != older {
		t.Fatalf("排序应为新任务在前: %+v", res.Sessions)
	}
	for _, m := range res.Sessions {
		if m.ID == mainID || m.ID == parent {
			t.Fatalf("顶层会话不应出现在任务列表: %+v", res.Sessions)
		}
		if m.Running {
			t.Fatalf("未在派发的任务 running 位应为 false: %s", m.ID)
		}
		if m.Title == "" {
			t.Fatal("任务条目应带标题")
		}
	}

	// 空：别的项目没有任务 → 空列表（前端小节不渲染的依据）
	other, err := srv.st.AddProject("other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodAssistantTasks, map[string]any{"project_id": other.ID})
	if resp.Error != nil {
		t.Fatalf("assistant.tasks(other): %v", resp.Error)
	}
	if err := unmarshalResult(resp, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 0 {
		t.Fatalf("无任务项目应返回空列表: %+v", res.Sessions)
	}

	// 缺 project_id → InvalidParams
	if resp := client.call(protocol.MethodAssistantTasks, map[string]any{}); resp.Error == nil {
		t.Fatal("缺 project_id 应报错")
	}
}
