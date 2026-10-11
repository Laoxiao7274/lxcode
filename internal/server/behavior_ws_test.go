// settings.todoPrompt.* 的协议往返测试：get/set + set 后广播 + 落盘 +
// chat.send 把档位传进会话（提示词按档位组装的内容断言在 agent 层，这里
// 验证「服务端的档位确实到达会话」）。
package server

import (
	"path/filepath"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// newBehaviorStack 起一个装配了 behavior.json 的完整 WS 服务端（含真实客户端）。
func newBehaviorStack(t *testing.T) (*Server, *wsTestClient, string) {
	t.Helper()
	srv, client, _ := newTestServer(t, nil)
	path := filepath.Join(t.TempDir(), "behavior.json")
	srv.AttachBehavior(path)
	return srv, client, path
}

func TestTodoPromptGetDefault(t *testing.T) {
	_, client, _ := newBehaviorStack(t)
	resp := client.call(protocol.MethodTodoPromptGet, map[string]any{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("get 失败: %+v", resp)
	}
	res := decode[protocol.TodoPromptResult](t, resp.Result)
	if res.TodoPrompt != config.TodoPromptStrict {
		t.Fatalf("缺省档位应为 strict，got %q", res.TodoPrompt)
	}
}

func TestTodoPromptSetRoundtripAndBroadcast(t *testing.T) {
	_, client, path := newBehaviorStack(t)
	resp := client.call(protocol.MethodTodoPromptSet, protocol.TodoPromptSetParams{TodoPrompt: config.TodoPromptNormal})
	if resp == nil || resp.Error != nil {
		t.Fatalf("set 失败: %+v", resp)
	}
	// 广播必须真的到达客户端（前端多端显示一致的契约）。
	if ev := client.waitEvent(protocol.EventTodoPromptChanged); ev == nil {
		t.Fatal("set 后应广播 settings.todoPrompt.changed")
	}
	res := decode[protocol.TodoPromptResult](t, client.call(protocol.MethodTodoPromptGet, map[string]any{}).Result)
	if res.TodoPrompt != config.TodoPromptNormal {
		t.Fatalf("set 后 get 应返回 normal，got %q", res.TodoPrompt)
	}
	// 落盘（重启后仍在）。
	if b := config.LoadBehavior(path); b.TodoPrompt != config.TodoPromptNormal {
		t.Fatalf("配置未落盘: %+v", b)
	}
}

func TestTodoPromptSetInvalidMode(t *testing.T) {
	_, client, _ := newBehaviorStack(t)
	resp := client.call(protocol.MethodTodoPromptSet, protocol.TodoPromptSetParams{TodoPrompt: "aggressive"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("非法档位应报参数错误，实际 %+v", resp)
	}
}

// chat.send 把现读的档位传进会话：off 档下会话组装的提示词不含任何 todo-first
// 指令（提示词内容断言在 agent 层，这里验证服务端 → 会话的传递）。
func TestChatSendAppliesTodoPromptMode(t *testing.T) {
	srv, client, _ := newBehaviorStack(t)
	if resp := client.call(protocol.MethodTodoPromptSet, protocol.TodoPromptSetParams{TodoPrompt: config.TodoPromptOff}); resp == nil || resp.Error != nil {
		t.Fatalf("set off 失败: %+v", resp)
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
	if got := sess.TodoPromptMode(); got != config.TodoPromptOff {
		t.Fatalf("chat.send 应把 behavior.json 的档位传进会话，got %q", got)
	}
}
