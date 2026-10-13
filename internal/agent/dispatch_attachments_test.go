// 子会话附件回传主会话的内核测试：子会话 share_file 共享的文件 →
// ① 主会话的调度结果消息带附件元数据（主时间线渲染附件卡——实时与回放同一份）；
// ② 调度结果文本带 [附件] 行（主 Agent 下一轮拿得到路径）；
// ③ agent_dispatch 的 ToolResultEvent 带同一份元数据（实时路径）。
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

func TestDispatchAttachmentsReturnToMain(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "m1", BaseURL: "http://127.0.0.1:1/v1", Model: "m1", Enabled: true,
		Capabilities: config.Capabilities{Tools: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "m1"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var dispatchResultEvt *ToolResultEvent
	env := &dispatchEnv{}
	s := New(reg, tools.New(), func(ev Event) {
		env.mu.Lock()
		env.events = append(env.events, ev)
		env.mu.Unlock()
		if e, ok := ev.(ToolResultEvent); ok && e.Name == "agent_dispatch" {
			mu.Lock()
			dispatchResultEvt = &e
			mu.Unlock()
		}
	})
	s.SetStream((&fakeStream{script: [][]llm.StreamEvent{
		// 主轮：派发
		toolCallResult("agent_dispatch", `{"agent":"coder","task":"把图共享给用户"}`),
		// 子轮：子会话 share_file
		toolCallResult("share_file", `{"path":"chart.png","caption":"本月数据"}`),
		// 主轮续：子会话结论回来后的收尾
		textResult("子 Agent 已把图共享给你"),
	}}).stream)
	t.Cleanup(s.Close)
	s.SetAgentResolver(&stubResolver{entries: map[string]*sessiondata.AgentContext{
		"main": {
			Def: sessiondata.AgentDef{ID: "main", Name: "主 Agent", IsMain: true, Enabled: true,
				Tools: []string{"agent_dispatch"}, Delegates: []string{"coder"}},
			Delegates: []sessiondata.AgentDef{
				{ID: "coder", Name: "代码 Agent", Desc: "写代码", Enabled: true},
			},
		},
		"coder": {
			Def: sessiondata.AgentDef{ID: "coder", Name: "代码 Agent", Enabled: true, Color: "#3b82f6",
				Tools: []string{"read_file", "share_file"}, Approval: "confirm"},
		},
	}})
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := s.SetWorkDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chart.png"), []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Send("派个 Agent 把图发给用户"); err != nil {
		t.Fatal(err)
	}
	waitDispatchIdle(t, s)

	// ③ 实时路径：agent_dispatch 的 ToolResultEvent 带附件元数据
	mu.Lock()
	evt := dispatchResultEvt
	mu.Unlock()
	if evt == nil || len(evt.Attachments) != 1 || evt.Attachments[0].Name != "chart.png" {
		t.Fatalf("agent_dispatch 的结果事件应带子会话附件: %+v", evt)
	}
	// ② 主 Agent 上下文：调度结果文本带 [附件] 行（路径指向子会话附件目录）
	var dispatchToolMsg *llm.Message
	for i := range s.history {
		if s.history[i].Role == "tool" && strings.Contains(s.history[i].Content, "[子会话 id:") {
			dispatchToolMsg = &s.history[i]
		}
	}
	if dispatchToolMsg == nil {
		t.Fatal("主历史应有调度结果消息")
	}
	childID := evt.Attachments[0].Path[:strings.Index(evt.Attachments[0].Path, "/")]
	if !strings.Contains(dispatchToolMsg.Content, "[附件] chart.png → attachments/"+evt.Attachments[0].Path) {
		t.Fatalf("调度结果文本应带 [附件] 行（主 Agent 拿得到路径）: %s", dispatchToolMsg.Content)
	}
	// ① 回放路径：调度结果消息带结构化附件元数据（主时间线渲染附件卡）
	if len(dispatchToolMsg.Attachments) != 1 || dispatchToolMsg.Attachments[0].Name != "chart.png" {
		t.Fatalf("调度结果消息应带附件元数据: %+v", dispatchToolMsg.Attachments)
	}
	if !strings.HasPrefix(dispatchToolMsg.Attachments[0].Path, childID+"/") {
		t.Fatalf("附件应落在子会话的附件目录: %+v", dispatchToolMsg.Attachments[0])
	}
	// 文件确实落在子会话自己的附件目录
	entries, err := os.ReadDir(filepath.Join(st.AttachmentsRoot(), childID))
	if err != nil || len(entries) != 1 {
		t.Fatalf("子会话附件目录应有 1 个文件: %v err=%v", entries, err)
	}
}
