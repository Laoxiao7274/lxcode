// chat.history 的 model 键：会话**实际使用**的模型（子会话 = 它自己 Agent 绑定的模型）。
//
// 为什么要在 WS 这一层钉：会话页是**按 id 重建运行时**的（刷新后 newRuntime →
// AttachTo），而子会话的归属 Agent 只记在库里（sessions.agent_id）——不读回来就只能
// 回落主 Agent 的模型，于是子会话页显示一个它没用过的模型（编数据）。这是"live 与
// replay 分叉"的又一处，必须在协议载荷上钉住，而不是只在 agent 单测里。
package server

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/protocol"
)

func TestHistoryModelOverWS(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)

	// 再注册一个模型并绑给 coder：子会话的模型与 default 不同，才能区分
	// "用了自己 Agent 的模型"与"回落了 default"（两者相同时这个测试什么也证明不了）。
	if resp := client.call(protocol.MethodModelAdd, config.ModelConfig{
		ID: "m2", BaseURL: "http://127.0.0.1:2/v1", Model: "m2", Enabled: true,
	}); resp == nil || resp.Error != nil {
		t.Fatalf("model.add 失败: %+v", resp)
	}
	resp := client.call(protocol.MethodAgentList, nil)
	if resp == nil || resp.Error != nil {
		t.Fatalf("agent.list 失败: %+v", resp)
	}
	var list []protocol.AgentEntry
	decodeServerResult(t, resp.Result, &list)
	bound := false
	for i := range list {
		if list[i].ID != "coder" {
			continue
		}
		list[i].Model = "m2"
		if r := client.call(protocol.MethodAgentUpdate, protocol.AgentAddParams{Agent: list[i]}); r == nil || r.Error != nil {
			t.Fatalf("agent.update 失败: %+v", r)
		}
		bound = true
	}
	if !bound {
		t.Fatal("种子名单里应有 coder")
	}

	// 主会话：主 Agent 没绑模型 → default 角色（m1）
	mainID := createTestSession(t, client, "")
	if got := readTestHistory(t, client, mainID).Model; got != "m1" {
		t.Fatalf("主会话的 model 应是 default 角色: %q", got)
	}

	// 子会话：用它自己 Agent 绑定的模型（m2）——按 id 重建运行时也必须答对
	childID, err := srv.st.CreateChild(mainID, "coder", "d1")
	if err != nil {
		t.Fatal(err)
	}
	if got := readTestHistory(t, client, childID).Model; got != "m2" {
		t.Fatalf("子会话应用自己 Agent 的模型: %q", got)
	}

	// 归属 Agent 已不在名单（被删了）→ 未知：整键缺席，**不**回落成主 Agent 的模型
	ghostID, err := srv.st.CreateChild(mainID, "ghost", "d2")
	if err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: ghostID})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.history(ghost) 失败: %+v", resp)
	}
	b, _ := json.Marshal(resp.Result)
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if v, hit := raw["model"]; hit {
		t.Fatalf("未知模型应整键缺席（不编一个模型名）: %v (%s)", v, b)
	}
}
