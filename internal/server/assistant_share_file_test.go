// 助手产物交付通道（2026-12 产物交付改造）的端到端验收：助手白名单加了
// share_file 后，助手会话里 share_file 专属目录内的文件 → 工具执行落盘、
// 实时事件与历史回放都带附件元数据（toolResult 带 attachments）。链路前提：
// 助手 write_file 的产物都在专属目录，share_file 的路径边界（会话工作目录内）
// 天然满足。
package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/chatmsg"
	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// assistantShareStream 是「助手语境第一轮发 share_file、收尾轮回一句」的假流。
// 与 assistantRoundStream 同款语境判定（助手协议文本），但走的是 share_file
// 而不是 run_task——专门验收助手的产物交付通道。
func assistantShareStream(t *testing.T, probe *assistantShareProbe) testStream {
	t.Helper()
	return func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 4)
		sys := msgs[0].Content
		// 收尾轮（share_file 结果已回填）——先于语境判定：助手收尾轮的系统
		// 提示词同样是助手协议文本。
		if len(msgs) >= 4 && msgs[3].Role == "tool" {
			for _, m := range msgs {
				if m.Role == "tool" && strings.Contains(m.Content, "已把文件共享到对话") {
					probe.record(m.Attachments)
				}
			}
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", Content: "已交付。"}, FinishReason: llm.FinishStop}}
			}()
			return ch, nil
		}
		if strings.Contains(sys, "个人智能体助手") {
			// 第一轮：发起 share_file（助手白名单现在有它——白名单外的调用
			// 会在 runTools 被拒，用例会失败在这里）。
			tc := llm.ToolCall{ID: "call-sf1"}
			tc.Function.Name = "share_file"
			tc.Function.Arguments = `{"path":"chart.png","caption":"本月数据"}`
			go func() {
				defer close(ch)
				ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
				ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
					Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
			}()
			return ch, nil
		}
		// 兜底（理论不可达）：静默收尾
		go func() {
			defer close(ch)
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "done"}, FinishReason: llm.FinishStop}}
		}()
		return ch, nil
	}
}

// assistantShareProbe 收集两路附件：实时事件（assistant.toolResult 的
// attachments）与历史回放（assistant.history 的 tool 消息）。
type assistantShareProbe struct {
	mu    sync.Mutex
	event []llm.AttachmentRef
}

func (p *assistantShareProbe) record(atts []llm.AttachmentRef) {
	p.mu.Lock()
	p.event = append(p.event, atts...)
	p.mu.Unlock()
}

func (p *assistantShareProbe) snapshot() []llm.AttachmentRef {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llm.AttachmentRef(nil), p.event...)
}

// TestAssistantShareFileFullLoop：装配专属目录 → 助手会话 share_file 专属
// 目录里的文件 → ① 实时 assistant.toolResult 事件带附件元数据；② 历史 tool
// 消息带同一份；③ 文件落进该助手会话的附件目录（不是主域/别的会话）。
func TestAssistantShareFileFullLoop(t *testing.T) {
	srv, client, _ := newTestServer(t, nil)
	probe := &assistantShareProbe{}
	srv.SetStream(assistantShareStream(t, probe))

	// 专属目录 + 产物文件（助手 write_file 的等价物——直接放一个文件）
	dir := t.TempDir()
	srv.SetAssistantWorkDir(dir)
	chart := filepath.Join(dir, "chart.png")
	if err := os.WriteFile(chart, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

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
		SessionID: created.SessionID, Text: "把图表发给用户",
	})
	if resp.Error != nil {
		t.Fatalf("assistant.send: %v", resp.Error)
	}
	if ev := client.waitEvent(protocol.EventAssistantDone); ev == nil {
		t.Fatal("应收到 assistant.done")
	}
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}

	// ① 实时事件路径：assistant.toolResult 带 attachments
	if got := probe.snapshot(); len(got) != 1 || got[0].Name != "chart.png" {
		t.Fatalf("实时事件应带 1 个附件（chart.png）: %+v", got)
	}

	// ② 历史回放路径：tool 消息带同一份元数据
	hresp := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: created.SessionID})
	if hresp.Error != nil {
		t.Fatalf("assistant.history: %v", hresp.Error)
	}
	var hist protocol.ChatHistoryResult
	if err := unmarshalResult(hresp, &hist); err != nil {
		t.Fatal(err)
	}
	var toolMsg *chatmsg.Message
	for i := range hist.Messages {
		if hist.Messages[i].Role == "tool" {
			toolMsg = &hist.Messages[i]
		}
	}
	if toolMsg == nil || len(toolMsg.Attachments) != 1 || toolMsg.Attachments[0].Name != "chart.png" {
		t.Fatalf("历史 tool 消息应带附件元数据: %+v", toolMsg)
	}

	// ③ 落盘归属：文件在**本会话**的附件目录下
	entries, err := os.ReadDir(filepath.Join(srv.st.AttachmentsRoot(), created.SessionID))
	if err != nil || len(entries) != 1 {
		t.Fatalf("附件目录应有 1 个文件: %v err=%v", entries, err)
	}
	if !strings.HasSuffix(entries[0].Name(), ".png") {
		t.Fatalf("落盘名应保留图片扩展名: %s", entries[0].Name())
	}
}
