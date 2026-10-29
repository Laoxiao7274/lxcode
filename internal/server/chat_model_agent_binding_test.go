// 子会话切模型 = **更新 Agent 绑定**（2026-12 用户反馈「子 Agent 模型切换能选
// 但不生效」）的 WS 集成测试。
//
// 语义：chat.model 带子会话 id 时，除落库 sessions.model 外，同步更新该子会话
// agent_id 对应的 agents.model 绑定——否则主 Agent 下次**新**派发（新子会话 id）
// 不继承旧子会话的选择（CreateChild 不复制 model 列，新会话的解析回落 Agent
// 绑定），用户的预期「给这个子 Agent 换模型」落空。
//
// 钉住三条：
//   - 子会话切模型 → agents.model 更新 + sessions.model 更新（双写）；
//   - 主会话切模型 → agents 表纹丝不动（会话级语义零变化）；
//   - 绑定行不存在（Agent 被删的 ghost 子会话）→ 显式报错，不静默。
package server

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// TestChatModelChildUpdatesAgentBinding：子会话切模型双写（sessions.model + agents.model）。
func TestChatModelChildUpdatesAgentBinding(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	addModel(t, client, "m2")

	// 主会话 + 一条子会话（归属种子名单里的 coder）
	mainID := createTestSession(t, client, "")
	childID, err := srv.st.CreateChild(mainID, "coder", "d1", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	// 子会话切到 m2
	resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: childID, ModelID: "m2"})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.model(child) 失败: %+v", resp)
	}

	// sessions.model 落库
	if m, err := srv.st.SessionModelOf(childID); err != nil || m != "m2" {
		t.Fatalf("子会话 sessions.model 应落库为 m2: %q err=%v", m, err)
	}
	// agents.model 绑定同步更新（coder 的绑定从空变成 m2）
	agents, err := srv.st.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range agents {
		if a.ID != "coder" {
			continue
		}
		if a.Model != "m2" {
			t.Fatalf("子会话切模型应更新 coder 的 Agent 绑定: model=%q", a.Model)
		}
	}

	// 下一次新派发的子会话（新 id）解析模型时能拿到新绑定——按 id 重建运行时
	//（模拟重启/重开）后 chat.history 的 model 键 = Agent 绑定（解析优先级：
	// 会话选择 > Agent 绑定 > default 角色；新会话没有会话选择，落绑定 m2）。
	srv.sessionsMu.Lock()
	delete(srv.sessions, mainID)
	srv.sessionsMu.Unlock()
	newChild, err := srv.st.CreateChild(mainID, "coder", "d2", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if m := readTestHistory(t, client, newChild).Model; m != "m2" {
		t.Fatalf("新派发的子会话应用新绑定的模型（用户报的「不生效」验收点）: %q", m)
	}
}

// TestChatModelMainDoesNotTouchAgents：主会话切模型不改 agents 表（行为零变化）。
func TestChatModelMainDoesNotTouchAgents(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	addModel(t, client, "m2")

	id := createTestSession(t, client, "")
	if resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: id, ModelID: "m2"}); resp == nil || resp.Error != nil {
		t.Fatalf("chat.model 失败: %+v", resp)
	}
	if m, err := srv.st.SessionModelOf(id); err != nil || m != "m2" {
		t.Fatalf("主会话 sessions.model 应落库为 m2: %q err=%v", m, err)
	}
	agents, err := srv.st.ListAgents()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range agents {
		if a.Model == "m2" {
			t.Fatalf("主会话切模型不得改任何 Agent 绑定: %s model=%q", a.ID, a.Model)
		}
	}
}

// TestChatModelChildGhostAgentErrors：子会话归属的 Agent 已不在名单 → 绑定更新
// 失败必须显式报错（会话模型已切，但用户要的「换 Agent 模型」没达成，不静默）。
func TestChatModelChildGhostAgentErrors(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	addModel(t, client, "m2")

	mainID := createTestSession(t, client, "")
	childID, err := srv.st.CreateChild(mainID, "ghost-agent", "d1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	resp := client.call(protocol.MethodChatModel, protocol.ChatModelParams{SessionID: childID, ModelID: "m2"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("ghost Agent 的绑定更新失败应报错: %+v", resp)
	}
	// 报错要说明「改了一半」：会话模型已切换、绑定没改成
	b, _ := json.Marshal(resp.Error)
	var e struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(b, &e); err != nil || e.Message == "" {
		t.Fatalf("错误信息解析失败: %v (%s)", err, b)
	}
}
