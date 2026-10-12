// default_approval_ws_test.go —— settings.defaultApproval.* 的协议往返 +
// 「新会话用默认档初始化」的服务端行为：
//   - get 缺省 confirm；set 落盘 + 广播 changed；
//   - set 不丢 todoPrompt（两个行为开关并存，只改一个）；
//   - session.new / 首条 chat.send 建的会话初始档 = 默认档，且已落库
//     （重启后恢复同一条会话读列，不依赖「创建时的默认」还成立）。
package server

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func TestDefaultApprovalGetDefault(t *testing.T) {
	_, client, _ := newBehaviorStack(t)
	resp := client.call(protocol.MethodDefaultApprovalGet, map[string]any{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("get 失败: %+v", resp)
	}
	res := decode[protocol.DefaultApprovalResult](t, resp.Result)
	if res.Approval != config.ApprovalConfirm {
		t.Fatalf("缺省默认档应为 confirm，got %q", res.Approval)
	}
}

func TestDefaultApprovalSetRoundtripAndBroadcast(t *testing.T) {
	_, client, path := newBehaviorStack(t)
	resp := client.call(protocol.MethodDefaultApprovalSet, protocol.DefaultApprovalSetParams{Approval: config.ApprovalStrict})
	if resp == nil || resp.Error != nil {
		t.Fatalf("set 失败: %+v", resp)
	}
	// 广播必须真的到达客户端（前端多端显示一致的契约）。
	if ev := client.waitEvent(protocol.EventDefaultApprovalChanged); ev == nil {
		t.Fatal("set 后应广播 settings.defaultApproval.changed")
	}
	res := decode[protocol.DefaultApprovalResult](t, client.call(protocol.MethodDefaultApprovalGet, map[string]any{}).Result)
	if res.Approval != config.ApprovalStrict {
		t.Fatalf("set 后 get 应返回 strict，got %q", res.Approval)
	}
	// 落盘（重启后仍在）。
	if b := config.LoadBehavior(path); b.Approval != config.ApprovalStrict {
		t.Fatalf("配置未落盘: %+v", b)
	}
}

func TestDefaultApprovalSetInvalidMode(t *testing.T) {
	_, client, _ := newBehaviorStack(t)
	resp := client.call(protocol.MethodDefaultApprovalSet, protocol.DefaultApprovalSetParams{Approval: "yolo"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("非法档位应报参数错误，实际 %+v", resp)
	}
}

func TestDefaultApprovalSetKeepsTodoPrompt(t *testing.T) {
	// 两个行为开关并存：set 只改一个时另一个必须原样保留（saveBehavior 整包
	// 写回，漏了这条就是把用户另一个设置静默归零）。
	_, client, path := newBehaviorStack(t)
	if resp := client.call(protocol.MethodTodoPromptSet, protocol.TodoPromptSetParams{TodoPrompt: config.TodoPromptNormal}); resp == nil || resp.Error != nil {
		t.Fatalf("set todoPrompt 失败: %+v", resp)
	}
	if resp := client.call(protocol.MethodDefaultApprovalSet, protocol.DefaultApprovalSetParams{Approval: config.ApprovalAuto}); resp == nil || resp.Error != nil {
		t.Fatalf("set approval 失败: %+v", resp)
	}
	b := config.LoadBehavior(path)
	if b.TodoPrompt != config.TodoPromptNormal || b.Approval != config.ApprovalAuto {
		t.Fatalf("set 应只改一个字段: %+v", b)
	}
}

// 新会话的初始档 = behavior 默认档，且已落库（重启恢复读列）。
func TestNewSessionUsesDefaultApproval(t *testing.T) {
	srv, client, _ := newBehaviorStack(t)
	if resp := client.call(protocol.MethodDefaultApprovalSet, protocol.DefaultApprovalSetParams{Approval: config.ApprovalStrict}); resp == nil || resp.Error != nil {
		t.Fatalf("set 默认档失败: %+v", resp)
	}
	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.new 失败: %+v", resp)
	}
	res := decode[protocol.SessionResult](t, resp.Result)
	sess, err := srv.session(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.LiveApproval(); got != config.ApprovalStrict {
		t.Fatalf("新会话初始档应为默认档 strict，got %q", got)
	}
	// 已落库：直接读存储验证（恢复路径 AttachTo 读的就是这一列）。
	stored, err := srv.st.ApprovalOf(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored != config.ApprovalStrict {
		t.Fatalf("新会话默认档应落库，got %q", stored)
	}
}

// 首条 chat.send 隐式建会话同样用默认档（不是写死 confirm）。
func TestChatSendImplicitSessionUsesDefaultApproval(t *testing.T) {
	srv, client, _ := newBehaviorStack(t)
	if resp := client.call(protocol.MethodDefaultApprovalSet, protocol.DefaultApprovalSetParams{Approval: config.ApprovalStrict}); resp == nil || resp.Error != nil {
		t.Fatalf("set 默认档失败: %+v", resp)
	}
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{Text: "你好"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	m := decode[map[string]any](t, resp.Result)
	id, _ := m["session_id"].(string)
	if id == "" {
		t.Fatalf("chat.send 应答缺少 session_id: %v", m)
	}
	sess, err := srv.session(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.LiveApproval(); got != config.ApprovalStrict {
		t.Fatalf("隐式新建会话初始档应为默认档 strict，got %q", got)
	}
}

// 老会话（空列）恢复时回落 behavior 默认档——newRuntime 的空列回落路径。
func TestRuntimeRestoreFallsBackToBehaviorDefault(t *testing.T) {
	srv, client, path := newBehaviorStack(t)
	// 先建一条会话（此时默认档还是 confirm → 列里落的是 confirm？不——
	// createSession 落的是「当时的默认档」confirm，列为非空）。要造空列，
	// 直接用存储层写一条没有审批档的会话（模拟本功能上线前的老会话）。
	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.new 失败: %+v", resp)
	}
	res := decode[protocol.SessionResult](t, resp.Result)
	// 手工把列清空（模拟老数据），再改默认档为 strict，最后重建运行时。
	if err := srv.st.SaveApproval(res.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveBehavior(path, config.Behavior{TodoPrompt: config.TodoPromptStrict, Approval: config.ApprovalStrict}); err != nil {
		t.Fatal(err)
	}
	srv.sessionsMu.Lock()
	delete(srv.sessions, res.SessionID) // 强制下次 session() 重建运行时
	srv.sessionsMu.Unlock()
	sess, err := srv.session(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.LiveApproval(); got != config.ApprovalStrict {
		t.Fatalf("空列老会话恢复应回落 behavior 默认档 strict，got %q", got)
	}
}
