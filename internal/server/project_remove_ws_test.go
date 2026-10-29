// project.remove 的验收用例（2026-12 用户报「没有删除项目的地方」）：
//
//   - 移除注册成功：项目从 project.list 消失，**磁盘目录与会话一条不动**；
//   - 广播 project.changed：多端（桌面 + 手机）同时看项目列表时同步；
//   - 边界：项目下有会话正在跑 → 拒绝（先停再删）；
//   - 边界：项目不存在 → 明确报错（不是静默成功）。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// listProjectIDs 拉一次 project.list 并返回 id 集合。
func listProjectIDs(t *testing.T, client *wsTestClient) map[string]bool {
	t.Helper()
	resp := client.call(protocol.MethodProjectList, struct{}{})
	if resp == nil || resp.Error != nil {
		t.Fatalf("project.list 失败: %+v", resp)
	}
	var list []protocol.ProjectMeta
	decodeServerResult(t, resp.Result, &list)
	out := map[string]bool{}
	for _, p := range list {
		out[p.ID] = true
	}
	return out
}

// TestProjectRemoveKeepsDiskAndSessions：移除注册后——列表里没有了、磁盘目录还在、
// 该项目的会话仍留在库里（一条不删）、并广播 project.changed 让其它客户端刷新。
func TestProjectRemoveKeepsDiskAndSessions(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	dir := initGitProject(t)
	meta := addGitProject(t, client, dir)

	// 建一条归属该项目的会话（project.remove 的语义是「不删会话」，得有会话可验）
	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{Workspace: meta.ID})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.new(workspace) 失败: %+v", resp)
	}
	var created protocol.SessionResult
	decodeServerResult(t, resp.Result, &created)
	client.waitEvent(protocol.EventSessionChanged)

	if !listProjectIDs(t, client)[meta.ID] {
		t.Fatal("移除前项目应在列表里")
	}

	rmResp := client.call(protocol.MethodProjectRemove, protocol.ProjectRemoveParams{ProjectID: meta.ID})
	if rmResp == nil || rmResp.Error != nil {
		t.Fatalf("project.remove 失败: %+v", rmResp)
	}
	var rm protocol.ProjectRemoveResult
	decodeServerResult(t, rmResp.Result, &rm)
	if !rm.Removed {
		t.Fatal("removed 应为 true")
	}
	if rm.Sessions != 1 {
		t.Fatalf("保留会话数应为 1（会话不随项目删除）: %d", rm.Sessions)
	}

	// ① 广播：其它客户端据此重拉 project.list
	if ev := client.waitEvent(protocol.EventProjectChanged); ev == nil {
		t.Fatal("project.remove 应广播 project.changed（多端同步）")
	}
	// ② 列表里没有了
	if listProjectIDs(t, client)[meta.ID] {
		t.Fatal("移除后项目不应再出现在 project.list")
	}
	// ③ 磁盘目录还在（只解绑，不删文件）
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("移除项目不应删除磁盘目录: %v", err)
	}
	// ④ 会话仍在库里（一行没删），归属已清空 → 落进「未分组」（侧栏有入口，
	//    不会变成找不到的孤儿）
	ws, err := srv.st.WorkspaceOf(created.SessionID)
	if err != nil {
		t.Fatalf("项目移除后会话必须还在库里（不删会话）: %v", err)
	}
	if ws != "" {
		t.Fatalf("项目移除后会话归属应清空（落进未分组）: %q", ws)
	}
	sessResp := client.call(protocol.MethodSessionList, struct{}{})
	if sessResp == nil || sessResp.Error != nil {
		t.Fatalf("session.list 失败: %+v", sessResp)
	}
	var sessions []protocol.SessionMeta
	decodeServerResult(t, sessResp.Result, &sessions)
	for _, s := range sessions {
		if s.ID == created.SessionID && s.Workspace != "" {
			t.Fatalf("侧栏列表里该会话应显示为未分组: %+v", s)
		}
	}
}

// TestProjectRemoveRejectsRunningSession：项目下有会话正在跑 → 拒绝（先停再删）。
func TestProjectRemoveRejectsRunningSession(t *testing.T) {
	// 卡在流里的 stream：会话保持 busy 直到测试放行
	gate := make(chan struct{})
	stream := func(_ context.Context, _ config.ModelConfig, _ []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		go func() {
			<-gate
			ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "ok"}
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "ok"}, FinishReason: llm.FinishStop,
			}}
			close(ch)
		}()
		return ch, nil
	}
	srv, client, _ := newTestServer(t, stream)
	dir := initGitProject(t)
	meta := addGitProject(t, client, dir)

	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{Workspace: meta.ID})
	if resp == nil || resp.Error != nil {
		t.Fatalf("session.new(workspace) 失败: %+v", resp)
	}
	var created protocol.SessionResult
	decodeServerResult(t, resp.Result, &created)
	client.waitEvent(protocol.EventSessionChanged)

	// 发一条消息让它跑起来（流被 gate 卡住 → busy）
	sendResp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{SessionID: created.SessionID, Text: "跑起来"})
	if sendResp == nil || sendResp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", sendResp)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !srv.peekSession(created.SessionID).Busy() {
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.peekSession(created.SessionID).Busy() {
		t.Fatal("会话应处于运行中（假流被 gate 卡住）")
	}

	rmResp := client.call(protocol.MethodProjectRemove, protocol.ProjectRemoveParams{ProjectID: meta.ID})
	if rmResp == nil || rmResp.Error == nil {
		close(gate)
		t.Fatalf("有会话在跑时应拒绝移除: %+v", rmResp)
	}
	if !strings.Contains(rmResp.Error.Message, "正在运行") {
		close(gate)
		t.Fatalf("拒绝文案应说明原因（有会话在运行）: %q", rmResp.Error.Message)
	}
	// 拒绝之后项目仍在列表里（没有半途改状态）
	if !listProjectIDs(t, client)[meta.ID] {
		close(gate)
		t.Fatal("被拒绝的移除不应改变项目列表")
	}
	close(gate)
	waitSessionIdle(t, srv, created.SessionID)
}

// TestProjectRemoveUnknownProject：项目不存在 → 明确报错。
func TestProjectRemoveUnknownProject(t *testing.T) {
	_, client, _ := newTestServer(t, nil)
	resp := client.call(protocol.MethodProjectRemove, protocol.ProjectRemoveParams{ProjectID: "no-such-project"})
	if resp == nil || resp.Error == nil {
		t.Fatalf("移除不存在的项目应报错: %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "不存在") {
		t.Fatalf("错误文案应说明项目不存在: %q", resp.Error.Message)
	}
}
