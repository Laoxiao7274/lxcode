// share_file 的内核集成测试：模型发起 share_file → 工具执行（登记进会话附件
// 目录）→ 结果消息与 ToolResultEvent 都带附件元数据（前端实时与回放同一份）。
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/store"
)

func TestShareFileToolRoundTrip(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := s.SetWorkDir(dir); err != nil {
		t.Fatal(err)
	}
	// 被共享的文件（工作目录里的产物）
	if err := os.WriteFile(filepath.Join(dir, "chart.png"), []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("share_file", `{"path":"chart.png","caption":"本月数据"}`),
		textResult("已共享"),
	}}
	s.stream = fake.stream

	var mu sync.Mutex
	var gotAtts []llm.AttachmentRef
	s.emit = func(ev Event) {
		if e, ok := ev.(ToolResultEvent); ok && e.Name == "share_file" {
			mu.Lock()
			gotAtts = e.Attachments
			mu.Unlock()
		}
	}
	if err := s.Send("把图发给用户"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	mu.Lock()
	defer mu.Unlock()
	// 事件携带元数据（前端实时路径）
	if len(gotAtts) != 1 || gotAtts[0].Name != "chart.png" || gotAtts[0].Kind != llm.AttachmentKindImage {
		t.Fatalf("ToolResultEvent 应带附件元数据: %+v", gotAtts)
	}
	if gotAtts[0].Caption != "本月数据" {
		t.Fatalf("说明应随引用流动: %+v", gotAtts[0])
	}
	// 结果消息（回放路径）带同一份
	var toolMsg *llm.Message
	for i := range s.history {
		if s.history[i].Role == "tool" {
			toolMsg = &s.history[i]
		}
	}
	if toolMsg == nil || len(toolMsg.Attachments) != 1 || toolMsg.Attachments[0].Name != "chart.png" {
		t.Fatalf("历史 tool 消息应带附件元数据: %+v", toolMsg)
	}
	// 文件确实落在会话附件目录（本会话 id 名下）
	entries, err := os.ReadDir(filepath.Join(st.AttachmentsRoot(), s.SessionID()))
	if err != nil || len(entries) != 1 {
		t.Fatalf("附件目录应有 1 个文件: %v err=%v", entries, err)
	}
	if !strings.HasSuffix(entries[0].Name(), ".png") {
		t.Fatalf("落盘名应保留图片扩展名: %s", entries[0].Name())
	}
}
