// chat.draft 的 WS 集成测试：落库往返（chat.draft 写 → chat.history 读回 draft 键）/
// 空草稿整键缺席 / 不存在的会话报错 / 存草稿不广播（打字高频写，广播是回声）。
package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

func TestChatDraftOverWS(t *testing.T) {
	_, client, _ := newTestServer(t, nil)

	// 开一条会话（草稿寻址要真实会话）
	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.new 失败: %+v", resp)
	}
	sess := decode[protocol.SessionResult](t, resp.Result)
	if sess.SessionID == "" {
		t.Fatalf("session.new 应回 session_id: %+v", sess)
	}

	// 写草稿 → history 读回
	if w := client.call(protocol.MethodChatDraft, protocol.ChatDraftParams{
		SessionID: sess.SessionID, Text: "没写完的想法"}); w == nil || w.Error != nil {
		t.Fatalf("chat.draft 失败: %+v", w)
	}
	h := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sess.SessionID})
	if h == nil || h.Error != nil {
		t.Fatalf("chat.history 失败: %+v", h)
	}
	hist := decode[protocol.ChatHistoryResult](t, h.Result)
	if hist.Draft != "没写完的想法" {
		t.Fatalf("history 应回带落库草稿: %+v", hist)
	}

	// 覆盖写（后写覆盖）→ 读到新值
	if w := client.call(protocol.MethodChatDraft, protocol.ChatDraftParams{
		SessionID: sess.SessionID, Text: "改口后的草稿"}); w == nil || w.Error != nil {
		t.Fatalf("chat.draft 覆盖写失败: %+v", w)
	}
	h = client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sess.SessionID})
	if h == nil || h.Error != nil {
		t.Fatalf("chat.history 失败: %+v", h)
	}
	hist = decode[protocol.ChatHistoryResult](t, h.Result)
	if hist.Draft != "改口后的草稿" {
		t.Fatalf("后写应覆盖: %+v", hist.Draft)
	}

	// 清空（text=""）→ draft 键整键缺席
	if w := client.call(protocol.MethodChatDraft, protocol.ChatDraftParams{
		SessionID: sess.SessionID, Text: ""}); w == nil || w.Error != nil {
		t.Fatalf("chat.draft 清空失败: %+v", w)
	}
	h = client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sess.SessionID})
	if h == nil || h.Error != nil {
		t.Fatalf("chat.history 失败: %+v", h)
	}
	raw, _ := json.Marshal(h.Result)
	if strings.Contains(string(raw), `"draft"`) {
		t.Fatalf("清空后 draft 应整键缺席（客户端回落本地记账）: %s", raw)
	}

	// 不存在的会话：报错而不是静默丢草稿
	bad := client.call(protocol.MethodChatDraft, protocol.ChatDraftParams{
		SessionID: "没有这个会话", Text: "x"})
	if bad == nil || bad.Error == nil || bad.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("不存在的会话应报 CodeInvalidParams: %+v", bad)
	}
}

func TestChatDraftDoesNotBroadcast(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.new 失败: %+v", resp)
	}
	sess := decode[protocol.SessionResult](t, resp.Result)

	if w := client.call(protocol.MethodChatDraft, protocol.ChatDraftParams{
		SessionID: sess.SessionID, Text: "草稿"}); w == nil || w.Error != nil {
		t.Fatalf("chat.draft 失败: %+v", w)
	}
	// 打字高频写不该有事件回声（回声会覆盖另一端正在打的字）——短窗口内
	// 不应出现任何广播事件。
	for i := 0; i < 10; i++ {
		ev := client.waitEventAny()
		if ev == nil {
			return // 5 秒窗口无事件 = 没广播，符合预期
		}
		if ev.Method == "chat.draftChanged" {
			t.Fatalf("chat.draft 不该广播: %+v", ev)
		}
	}
}
