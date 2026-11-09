// 助手附件（2026-12 用户拍板：assistant.send 也带 images/files）的端到端验收：
// assistant.new → assistant.send（带图）→ 图片落盘、历史回放带引用（不含 base64）、
// 模型收到的消息带图（假流侧验证视觉通道）；vision 门拒绝路径与 chat.send 同款。
package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// assistantAttachStream 是「捕获收到的消息 + 回一句固定话」的假流：断言视觉
// 通道（最后一条 user 消息的 Images 引用）在 LLM 请求里真实到达。
func assistantAttachStream(t *testing.T, got *[]llm.Message) testStream {
	t.Helper()
	return func(_ context.Context, _ config.ModelConfig, msgs []llm.Message, _ []llm.Option) (<-chan llm.StreamEvent, error) {
		*got = append([]llm.Message(nil), msgs...)
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "看到了"}
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: "看到了"}, FinishReason: llm.FinishStop,
		}}
		close(ch)
		return ch, nil
	}
}

// TestAssistantSendImagesFullLoop：vision 模型 + 带图 assistant.send →
// 落盘 + 历史回放带引用（不含 base64）+ LLM 请求真实带图（视觉通道贯通）。
func TestAssistantSendImagesFullLoop(t *testing.T) {
	var got []llm.Message
	srv, client, reg := newTestServer(t, assistantAttachStream(t, &got))
	addVisionModel(t, reg)
	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created protocol.SessionResult
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}

	send := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID, Text: "看图",
		Images: []protocol.ChatSendImage{
			{Mime: "image/png", Data: pngB64},
			{Mime: "image/jpeg", Data: pngB64},
		},
	})
	if send == nil || send.Error != nil {
		t.Fatalf("assistant.send(带图) 失败: %+v", send)
	}
	// 事件域照常：assistant.userMessage / assistant.done（换名路径不受附件影响）
	if ev := client.waitEvent(protocol.EventAssistantUserMsg); ev == nil {
		t.Fatal("应收到 assistant.userMessage")
	}
	if ev := client.waitEvent(protocol.EventAssistantDone); ev == nil {
		t.Fatal("应收到 assistant.done（事件域映射）")
	}
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}

	// 附件落盘（两张）
	entries, err := os.ReadDir(filepath.Join(srv.st.AttachmentsRoot(), created.SessionID))
	if err != nil {
		t.Fatalf("附件目录应存在: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应落盘 2 张附件: %d", len(entries))
	}

	// 历史回放带引用、不含 base64（assistant.history 与 chat.history 同构）
	hist := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: created.SessionID})
	if hist.Error != nil {
		t.Fatalf("assistant.history: %v", hist.Error)
	}
	var h protocol.ChatHistoryResult
	if err := unmarshalResult(hist, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Messages) == 0 || len(h.Messages[0].Images) != 2 {
		t.Fatalf("历史首条应带 2 个图片引用: %+v", h.Messages)
	}
	for _, im := range h.Messages[0].Images {
		if im.Path == "" || im.Mime == "" {
			t.Fatalf("引用不完整: %+v", im)
		}
		p := filepath.Join(srv.st.AttachmentsRoot(), filepath.FromSlash(im.Path))
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("附件应存在: %v", err)
		}
	}
	b, _ := json.Marshal(h.Messages)
	if strings.Contains(string(b), "iVBORw0KGgo") {
		t.Fatalf("历史回放不应含 base64: %s", b)
	}
	// 展示元数据（附件卡）也随 user 消息落库（kind=image）
	if len(h.Messages[0].Attachments) != 2 {
		t.Fatalf("历史首条应带 2 个附件元数据: %+v", h.Messages[0].Attachments)
	}

	// LLM 请求真实带图（视觉通道）：最后一条 user 消息是这次发送
	var lastUser *llm.Message
	for i := range got {
		if got[i].Role == "user" {
			lastUser = &got[i]
		}
	}
	if lastUser == nil {
		t.Fatal("假流应收到 user 消息")
	}
	if len(lastUser.Images) != 2 {
		t.Fatalf("模型收到的 user 消息应带 2 个图片引用: %+v", lastUser.Images)
	}
}

// TestAssistantSendImagesVisionRejected：助手域同样受 vision 能力门——模型没声明
// vision → 人话错误，附件不落盘、消息不入历史。
func TestAssistantSendImagesVisionRejected(t *testing.T) {
	// newTestServer 的默认 m1 没声明 vision（与 attachments_test 的拒绝路径同款）
	srv, client, _ := newTestServer(t, textStream())
	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created protocol.SessionResult
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	send := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID, Text: "看图",
		Images: []protocol.ChatSendImage{{Mime: "image/png", Data: pngB64}},
	})
	if send == nil || send.Error == nil {
		t.Fatalf("应拒绝无 vision 能力的模型: %+v", send)
	}
	if !strings.Contains(send.Error.Message, "不支持视觉") {
		t.Fatalf("错误文案应说明模型不支持视觉: %s", send.Error.Message)
	}
	// 附件没落盘、消息没入历史
	entries, err := os.ReadDir(srv.st.AttachmentsRoot())
	if err == nil && len(entries) > 0 {
		t.Fatalf("拒绝后不应有附件目录: %v", entries)
	}
	hist := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: created.SessionID})
	var h protocol.ChatHistoryResult
	if err := unmarshalResult(hist, &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Messages) != 0 {
		t.Fatalf("拒绝后历史应为空: %+v", h.Messages)
	}
}

// TestAssistantSendImagesValidation：张数/mime 等参数错误在助手域同样
// CodeInvalidParams + 人话文案（与 chat.send 同一道闸）。
func TestAssistantSendImagesValidation(t *testing.T) {
	srv, client, reg := newTestServer(t, textStream())
	addVisionModel(t, reg)
	resp := client.call(protocol.MethodAssistantNew, map[string]any{})
	if resp.Error != nil {
		t.Fatalf("assistant.new: %v", resp.Error)
	}
	var created protocol.SessionResult
	if err := unmarshalResult(resp, &created); err != nil {
		t.Fatal(err)
	}
	send := client.call(protocol.MethodAssistantSend, protocol.ChatSendParams{
		SessionID: created.SessionID, Text: "看图",
		Images: []protocol.ChatSendImage{{Mime: "image/bmp", Data: pngB64}},
	})
	if send == nil || send.Error == nil {
		t.Fatalf("mime 白名单外应拒绝: %+v", send)
	}
	if send.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("应是 CodeInvalidParams: %d", send.Error.Code)
	}
	if !strings.Contains(send.Error.Message, "不支持") {
		t.Fatalf("文案不符: %s", send.Error.Message)
	}
	// 校验失败不开轮
	if err := waitAssistantIdle(srv, created.SessionID); err != nil {
		t.Fatal(err)
	}
	hist := client.call(protocol.MethodAssistantHistory, protocol.ChatHistoryParams{SessionID: created.SessionID})
	var h protocol.ChatHistoryResult
	if err := unmarshalResult(hist, &h); err != nil {
		t.Fatal(err)
	}
	if h.Busy {
		t.Fatal("校验失败不应开轮")
	}
}
