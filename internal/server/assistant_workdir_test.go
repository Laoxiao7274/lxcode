// 助手专属工作目录（<sessions>/../assistant，runServe 装配 → newAssistantRuntime
// 在 AttachTo 之后注入）的验收测试：
//   - 注入后助手会话的 ctx 工作目录 = 专属目录（工具路径解析以它为基准）；
//   - 未装配（空）保持旧行为（进程目录）——内存测试模式零回归；
//   - 主域会话不受影响（AttachTo 按库值解析项目根）；
//   - run_task 子会话仍落项目根（RunProjectTask 显式 SetWorkDir 覆盖）。
package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)



// workdirProbe 把服务端注册表里的 read_file 换成探针实现：回
// [workdir]<目录>（tools.WorkDir(ctx)——runTurn 每轮快照注入的同一份，
// 断言「工具实际看到的工作目录」而不是内存字段）。选 read_file 而不是
// bash_readonly：主域/助手/执行 Agent 三种白名单都有它（执行 Agent 的
// 内置名单不含 bash_readonly）。内置名不可被目录条目覆盖，测试直接改
// 注册表条目的 Exec（单测桩特权——注册表是测试里刚建的）。
func workdirProbe(t *testing.T, srv *Server) {
	t.Helper()
	d, ok := srv.treg.Get("read_file")
	if !ok {
		t.Fatal("内置 read_file 应在注册表里")
	}
	d.Exec = func(ctx context.Context, _ json.RawMessage) (string, error) {
		return "[workdir]" + tools.WorkDir(ctx), nil
	}
}

// assistantWorkdirStack 起一个带探针注册表的服务端（助手 + 主域共用），
// 返回 srv / ws 客户端 / 已装配的库句柄（测试自行 AddProject）。
func assistantWorkdirStack(t *testing.T) (*Server, *wsTestClient, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "models.json")
	reg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		ContextWindow: 100000, MaxOutputTokens: 1024, Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(reg)
	t.Cleanup(srv.CloseSessions)
	workdirProbe(t, srv)
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := srv.AttachSessionStore(st); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	client := dialTest(t, "ws"+strings.TrimPrefix(ts.URL, "http")+protocol.Path)
	t.Cleanup(func() { client.conn.Close() })
	return srv, client, st
}

// collectedDirs 是探针收集器（probeStream 的收尾轮从历史里取工具结果）。
type collectedDirs struct {
	mu   sync.Mutex
	dirs []string
}

func (c *collectedDirs) add(dir string) {
	c.mu.Lock()
	c.dirs = append(c.dirs, dir)
	c.mu.Unlock()
}

func (c *collectedDirs) last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.dirs) == 0 {
		return ""
	}
	return c.dirs[len(c.dirs)-1]
}

// probeStream 造探针假流：助手语境（个人智能体助手）与子会话语境（coder
// 执行协议）各发一次 bash_readonly 探针；工具结果回填后的收尾轮把目录记进
// collected。main/sub 收尾轮的区分靠系统提示词（子会话不是助手协议、主域
// 会话在这里不存在——本套假流只服务助手与它的子会话）。
func probeStream(collected *collectedDirs, mainRound *int, projID string) testStream {
	var mu sync.Mutex
	return func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		sys := msgs[0].Content
		// 收尾轮（工具结果已回填：msgs = [system user assistant tool]）——
		// 必须先于语境判定：助手收尾轮的系统提示词同样是助手协议文本，
		// 落在 main case 里会被当成「下一轮」吞掉。
		if len(msgs) >= 4 && msgs[3].Role == "tool" {
			toolDir := ""
			for _, m := range msgs {
				if m.Role == "tool" && strings.Contains(m.Content, "[workdir]") {
					toolDir = strings.TrimPrefix(m.Content, "[workdir]")
				}
			}
			// 只有探针结果才记（run_task 场景助手收尾轮的工具结果不带
			// [workdir]——记空串会覆盖子会话先记的项目根）。
			if toolDir != "" {
				collected.add(toolDir)
			}
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "done"}, FinishReason: llm.FinishStop}}
			}()
			return ch, nil
		}
		switch {
		case strings.Contains(sys, "调度中枢"):
			// 主域主 Agent：派发给 coder（主 Agent 不亲自执行——探针由子会话发）
			tc := llm.ToolCall{ID: "call-d1"}
			tc.Function.Name = "agent_dispatch"
			tc.Function.Arguments = `{"agent":"coder","task":"看看目录"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		case strings.Contains(sys, "个人智能体助手"):
			mu.Lock()
			*mainRound++
			r := *mainRound
			mu.Unlock()
			if projID != "" && r == 1 {
				// run_task 场景：第一轮发 run_task 调用
				tc := llm.ToolCall{ID: "call-rt1"}
				tc.Function.Name = "run_task"
				tc.Function.Arguments = `{"project_id":"` + projID + `","task":"看一眼"}`
				go func() {
					defer close(ch)
					ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
					ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
						Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
				}()
				return ch, nil
			}
			if r == 1 {
				// 探针场景：第一轮发 bash_readonly 探针
				tc := llm.ToolCall{ID: "call-pw"}
				tc.Function.Name = "read_file"
				tc.Function.Arguments = `{"path":"."}`
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
					Message: llm.Message{Role: "assistant", Content: "已转述。"}, FinishReason: llm.FinishStop}}
			}()
			return ch, nil
		case strings.Contains(sys, "执行 Agent"):
			// 子会话语境：发探针
			tc := llm.ToolCall{ID: "call-pw"}
			tc.Function.Name = "read_file"
			tc.Function.Arguments = `{"path":"."}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 兜底（理论不可达——主域会话不跑这套假流）：静默收尾
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "done"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// assistantProbeRound 建助手会话并跑一轮探针，返回收集到的目录。
func assistantProbeRound(t *testing.T, srv *Server, client *wsTestClient) string {
	t.Helper()
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
	resp = client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID, Text: "看看你的工作目录",
	})
	if resp.Error != nil {
		t.Fatalf("assistant.send: %v", resp.Error)
	}
	waitIdle(t, func() bool {
		sess := srv.peekAssistantSession(created.SessionID)
		return sess != nil && !sess.Busy()
	}, "助手会话")
	return created.SessionID
}

// TestAssistantWorkdirInjected（验收主断言）：装配专属目录后，助手会话里
// 工具看到的 ctx 工作目录 = 专属目录（不是进程 cwd）。
func TestAssistantWorkdirInjected(t *testing.T) {
	srv, client, _ := assistantWorkdirStack(t)
	collected := &collectedDirs{}
	mainRound := 0
	srv.SetStream(probeStream(collected, &mainRound, ""))

	dir := t.TempDir()
	srv.SetAssistantWorkDir(dir)
	assistantProbeRound(t, srv, client)

	if got := collected.last(); got != dir {
		t.Fatalf("助手工具应看到专属目录: got %q want %q", got, dir)
	}
}

// TestAssistantWorkdirUnsetKeepsLegacy：未装配（空）= 旧行为——工具看到的
// 目录为空串（WithWorkDir 不注入，OS 按进程目录解析）。内存测试模式零回归。
func TestAssistantWorkdirUnsetKeepsLegacy(t *testing.T) {
	srv, client, _ := assistantWorkdirStack(t)
	collected := &collectedDirs{}
	mainRound := 0
	srv.SetStream(probeStream(collected, &mainRound, ""))
	// 刻意不 SetAssistantWorkDir
	assistantProbeRound(t, srv, client)

	if got := collected.last(); got != "" {
		t.Fatalf("未装配时应保持旧行为（空 = 进程目录解析）: got %q", got)
	}
}

// TestMainDomainWorkdirUnaffected：主域项目会话的 AttachTo 解析项目根，
// 助手目录注入不得串进主域（两个域各有各的运行时装配路径）。
func TestMainDomainWorkdirUnaffected(t *testing.T) {
	srv, client, st := assistantWorkdirStack(t)
	collected := &collectedDirs{}
	srv.SetStream(probeStream(collected, new(int), ""))
	srv.SetAssistantWorkDir(t.TempDir()) // 装配了专属目录，但主域不该用它

	projDir := t.TempDir()
	proj, err := st.AddProject("demo-proj", projDir)
	if err != nil {
		t.Fatal(err)
	}
	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{Workspace: proj.ID})
	if resp.Error != nil {
		t.Fatalf("session.new: %v", resp.Error)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	resp = client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: created.SessionID, Text: "看看项目",
	})
	if resp.Error != nil {
		t.Fatalf("chat.send: %v", resp.Error)
	}
	waitIdle(t, func() bool {
		sess := srv.peekSession(created.SessionID)
		return sess != nil && !sess.Busy()
	}, "主域会话")

	if got := collected.last(); got != projDir {
		t.Fatalf("主域项目会话工具应看到项目根: got %q want %q", got, projDir)
	}
}

// TestRunTaskChildWorkdirStaysProjectRoot：run_task 子会话的工具目录 = 项目根
//（RunProjectTask 在 openChildSession 的 SetWorkDir 之后显式覆盖），专属目录
// 不漏进子会话。
func TestRunTaskChildWorkdirStaysProjectRoot(t *testing.T) {
	srv, client, st := assistantWorkdirStack(t)
	collected := &collectedDirs{}
	mainRound := 0

	projDir := t.TempDir()
	proj, err := st.AddProject("demo-proj", projDir)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetAssistantWorkDir(t.TempDir()) // 专属目录：子会话不该看到它
	srv.SetStream(probeStream(collected, &mainRound, proj.ID))

	assistantProbeRound(t, srv, client)

	if got := collected.last(); got != projDir {
		t.Fatalf("run_task 子会话工具应看到项目根: got %q want %q", got, projDir)
	}
}

// waitIdle 轮询等待条件成立（会话收尾有落库/事件窗口，idle 后给 50ms 缓冲）。
func waitIdle(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if cond() {
			time.Sleep(50 * time.Millisecond)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s 10s 未空闲", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
