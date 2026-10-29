// chat.model 的 WS 集成测试：模型选择是**会话级**状态。
//
// 用户反馈的 bug：一个会话切模型，其他所有会话跟着变——因为前端模型菜单走的是
// role.set（全局 default 角色绑定）。这条测试钉住修复后的语义：
//   - chat.model 只改目标会话，其他会话的模型纹丝不动（作用域隔离）；
//   - 新会话仍继承全局 default 角色（设置页的「默认模型」语义不变）；
//   - 改完广播 chat.modelChanged（多端同步），且不是 model.changed（不重拉注册表）；
//   - 落库：按 id 重建运行时（模拟重启/重新打开）读回来还是改后的模型。
package server

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// addModel 注册一个模型（测试里少写几行）。
func addModel(t *testing.T, client *wsTestClient, id string) {
	t.Helper()
	resp := client.call(protocol.MethodModelAdd, config.ModelConfig{
		ID: id, BaseURL: "http://127.0.0.1:9/v1", Model: id, Enabled: true,
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("model.add(%s) 失败: %+v", id, resp)
	}
}

func TestChatModelIsSessionScoped(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	addModel(t, client, "m2")

	// 两条顶层会话：都从 default 角色（m1）开始
	a := createTestSession(t, client, "")
	b := createTestSession(t, client, "")
	if got := readTestHistory(t, client, a).Model; got != "m1" {
		t.Fatalf("新会话应继承 default 角色: %q", got)
	}

	// A 切到 m2：只改 A
	resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: a, ModelID: "m2"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.model 失败: %+v", resp)
	}
	var got protocol.ChatModelResult
	decodeServerResult(t, resp.Result, &got)
	if got.ModelID != "m2" || got.SessionID != a {
		t.Fatalf("chat.model 应回规范化后的模型 id 与会话 id: %+v", got)
	}

	// 作用域隔离：A = m2，B 仍是 m1（bug 的验收点——原先 B 也会变成 m2）
	if m := readTestHistory(t, client, a).Model; m != "m2" {
		t.Fatalf("A 应是 m2: %q", m)
	}
	if m := readTestHistory(t, client, b).Model; m != "m1" {
		t.Fatalf("B 不该跟着变（模型选择是会话级）: %q", m)
	}

	// 全局 default 角色没被改动：之后新建的会话仍继承 m1
	c := createTestSession(t, client, "")
	if m := readTestHistory(t, client, c).Model; m != "m1" {
		t.Fatalf("chat.model 不该改全局 default 角色（新会话应仍是 m1）: %q", m)
	}

	// 落库往返在 TestChatModelPersistsAcrossRuntimeRebuild 里验（摘掉运行时重建）
}

// TestChatModelPersistsAcrossRuntimeRebuild：改完模型把运行时从缓存里摘掉，
// 下一次 chat.history 会 newRuntime → AttachTo 从库里读回——模拟重启/重新打开会话。
func TestChatModelPersistsAcrossRuntimeRebuild(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	addModel(t, client, "m2")

	id := createTestSession(t, client, "")
	if resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: id, ModelID: "m2"}); resp == nil || resp.Error != nil {
		t.Fatalf("chat.model 失败: %+v", resp)
	}
	// 摘掉内存里的运行时（等价于进程重启后第一次打开这条会话）
	srv.sessionsMu.Lock()
	delete(srv.sessions, id)
	srv.sessionsMu.Unlock()

	if m := readTestHistory(t, client, id).Model; m != "m2" {
		t.Fatalf("重建运行时后应读回落库的模型: %q", m)
	}
}

// TestChatModelValidation：模型不存在/已停用/空 id 都不静默回落，如实报错。
func TestChatModelValidation(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	id := createTestSession(t, client, "")

	if resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: id, ModelID: ""}); resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("空 model_id 应回 CodeInvalidParams: %+v", resp)
	}
	if resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: id, ModelID: "没有这个模型"}); resp == nil || resp.Error == nil {
		t.Fatalf("不存在的模型应报错（不静默回落）: %+v", resp)
	}
	// 失败的调用不该改动会话状态
	if m := readTestHistory(t, client, id).Model; m != "m1" {
		t.Fatalf("失败的 chat.model 不该改动会话模型: %q", m)
	}

	// 停用的模型同样拒绝
	addModel(t, client, "m3")
	if resp := client.call(protocol.MethodModelEnable, protocol.ModelEnableParams{ID: "m3", Enabled: false}); resp == nil || resp.Error != nil {
		t.Fatalf("model.enable 失败: %+v", resp)
	}
	resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: id, ModelID: "m3"})
	if resp == nil || resp.Error == nil || resp.Error.Code != protocol.CodeModelDisabled {
		t.Fatalf("停用的模型应回 CodeModelDisabled: %+v", resp)
	}
}

// TestChatModelBroadcast：改模型要广播 chat.modelChanged（多端同步），
// 且**不能**广播 model.changed（那是全局注册表变更，客户端会重拉整个列表）。
func TestChatModelBroadcast(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	addModel(t, client, "m2")
	id := createTestSession(t, client, "")

	if resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: id, ModelID: "m2"}); resp == nil || resp.Error != nil {
		t.Fatalf("chat.model 失败: %+v", resp)
	}

	ev := client.waitEvent(protocol.EventModelChanged)
	if ev == nil {
		t.Fatal("chat.model 应广播 chat.modelChanged（多端同步）")
	}
	b, _ := json.Marshal(ev.Params)
	var p protocol.ModelChangedParams
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("载荷解析失败: %v (%s)", err, b)
	}
	if p.SessionID != id || p.ModelID != "m2" {
		t.Fatalf("广播载荷应带会话 id 与模型 id: %+v", p)
	}
	// 事件队列是同一份：非阻塞地把剩余事件抽干净，确认里面没有 model.changed
	//（那是全局注册表变更——客户端收到会重拉整个列表，语义不同）。
	for drained := false; !drained; {
		select {
		case e, ok := <-client.events:
			if !ok {
				drained = true
				continue
			}
			if e.Method == protocol.EventModels {
				t.Fatal("chat.model 不该广播 model.changed（那是全局注册表变更）")
			}
		default:
			drained = true
		}
	}
}
