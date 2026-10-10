// 图片批次 A 的 server 层测试：chat.send 的校验与落盘、vision 能力门、
// HTTP 附件只读端点。
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// pngB64 是固定 1x1 PNG 的 base64。
const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// textStream 是最简单的假流：回一段文字就收尾。
func textStream() testStream {
	return func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventText, TextDelta: "好"}
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", Content: "好"}, FinishReason: llm.FinishStop,
		}}
		close(ch)
		return ch, nil
	}
}

// addVisionModel 注册一个声明了 vision 的模型并设为 default（newTestServer 的
// m1 没声明 vision——正好用来测拒绝路径）。
func addVisionModel(t *testing.T, reg *config.Registry) {
	t.Helper()
	if err := reg.Add(config.ModelConfig{
		ID: "v1", BaseURL: "http://127.0.0.1:1/v1", Model: "v", Enabled: true,
		Capabilities: config.Capabilities{Vision: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "v1"); err != nil {
		t.Fatal(err)
	}
}

func waitDone(t *testing.T, client *wsTestClient, sessionID string) {
	t.Helper()
	for {
		ev := waitSessionEvent(t, client, protocol.EventDone, sessionID)
		if ev.Method == protocol.EventDone {
			return
		}
	}
}

// TestChatSendImagesVisionRejected：模型没声明 vision → 人话错误，附件不落盘、
// 消息不入历史。
func TestChatSendImagesVisionRejected(t *testing.T) {
	srv, client, _ := newTestServer(t, textStream())
	createResp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{})
	if createResp.Error != nil {
		t.Fatalf("session.new: %+v", createResp.Error)
	}
	var sess protocol.SessionResult
	decodeServerResult(t, createResp.Result, &sess)

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sess.SessionID, Text: "看图",
		Images: []protocol.ChatSendImage{{Mime: "image/png", Data: pngB64}},
	})
	if resp == nil || resp.Error == nil {
		t.Fatalf("应拒绝无 vision 能力的模型: %+v", resp)
	}
	if !strings.Contains(resp.Error.Message, "不支持视觉") {
		t.Fatalf("错误文案应说明模型不支持视觉: %s", resp.Error.Message)
	}
	// 附件没落盘
	entries, err := os.ReadDir(srv.st.AttachmentsRoot())
	if err == nil && len(entries) > 0 {
		t.Fatalf("拒绝后不应有附件目录: %v", entries)
	}
	// 历史没入消息
	hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sess.SessionID})
	var h protocol.ChatHistoryResult
	decodeServerResult(t, hist.Result, &h)
	if len(h.Messages) != 0 {
		t.Fatalf("拒绝后历史应为空: %+v", h.Messages)
	}
}

// TestChatSendImagesFullLoop：vision 模型 + 带图 chat.send → 落盘 + 历史回放
// 带引用（不含 base64）。
func TestChatSendImagesFullLoop(t *testing.T) {
	srv, client, reg := newTestServer(t, textStream())
	addVisionModel(t, reg)
	createResp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{})
	var sess protocol.SessionResult
	decodeServerResult(t, createResp.Result, &sess)

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sess.SessionID, Text: "看图",
		Images: []protocol.ChatSendImage{
			{Mime: "image/png", Data: pngB64},
			{Mime: "image/jpeg", Data: pngB64},
		},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("chat.send 失败: %+v", resp)
	}
	waitDone(t, client, sess.SessionID)

	// 附件落盘（两张）
	entries, err := os.ReadDir(filepath.Join(srv.st.AttachmentsRoot(), sess.SessionID))
	if err != nil {
		t.Fatalf("附件目录应存在: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应落盘 2 张附件: %d", len(entries))
	}

	// 历史回放带引用、不含 base64
	hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sess.SessionID})
	var h protocol.ChatHistoryResult
	decodeServerResult(t, hist.Result, &h)
	if len(h.Messages) == 0 || len(h.Messages[0].Images) != 2 {
		t.Fatalf("历史首条应带 2 个图片引用: %+v", h.Messages)
	}
	for _, im := range h.Messages[0].Images {
		if im.Path == "" || im.Mime == "" {
			t.Fatalf("引用不完整: %+v", im)
		}
	}
	b, err := json.Marshal(h.Messages)
	if strings.Contains(string(b), "iVBORw0KGgo") {
		t.Fatalf("历史回放不应含 base64: %s", b)
	}
	// 引用指向的文件确实存在（HTTP 端点可取）
	for _, im := range h.Messages[0].Images {
		p := filepath.Join(srv.st.AttachmentsRoot(), filepath.FromSlash(im.Path))
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("附件应存在: %v", err)
		}
	}
}

// TestChatSendImagesValidation：张数/mime/base64/大小四类参数错误 →
// CodeInvalidParams + 人话文案，且不开轮。
func TestChatSendImagesValidation(t *testing.T) {
	srv, client, reg := newTestServer(t, textStream())
	addVisionModel(t, reg)
	createResp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{})
	var sess protocol.SessionResult
	decodeServerResult(t, createResp.Result, &sess)

	oversize := base64.StdEncoding.EncodeToString(make([]byte, llm.MaxImageBytes+1))
	cases := []struct {
		name   string
		images []protocol.ChatSendImage
		want   string
	}{
		{"五张拒绝", []protocol.ChatSendImage{
			{Mime: "image/png", Data: pngB64}, {Mime: "image/png", Data: pngB64},
			{Mime: "image/png", Data: pngB64}, {Mime: "image/png", Data: pngB64},
			{Mime: "image/png", Data: pngB64},
		}, "最多带 4 张"},
		{"mime 白名单外", []protocol.ChatSendImage{{Mime: "image/bmp", Data: pngB64}}, "不支持"},
		{"base64 坏", []protocol.ChatSendImage{{Mime: "image/png", Data: "!!不是base64!!"}}, "base64"},
		{"超 5MB", []protocol.ChatSendImage{{Mime: "image/png", Data: oversize}}, "大小上限"},
	}
	for _, tc := range cases {
		resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
			SessionID: sess.SessionID, Text: "看图", Images: tc.images,
		})
		if resp == nil || resp.Error == nil {
			t.Fatalf("%s: 应拒绝", tc.name)
		}
		if resp.Error.Code != protocol.CodeInvalidParams {
			t.Fatalf("%s: 应是 CodeInvalidParams: %d", tc.name, resp.Error.Code)
		}
		if !strings.Contains(resp.Error.Message, tc.want) {
			t.Fatalf("%s: 文案不符: %s", tc.name, resp.Error.Message)
		}
	}
	// 校验失败不开轮（忙闲不变）
	hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sess.SessionID})
	var h protocol.ChatHistoryResult
	decodeServerResult(t, hist.Result, &h)
	if h.Busy {
		t.Fatal("校验失败不应开轮")
	}
	_ = srv
}

// jsonMarshal 已内联：历史回放断言直接用 encoding/json。

// TestAttachmentsHTTP：GET 回原字节 + 正确 Content-Type；穿越/不存在/非法 id
// 一律 404。
func TestAttachmentsHTTP(t *testing.T) {
	srv, _, _ := newTestServer(t, textStream())
	data, err := base64.StdEncoding.DecodeString(pngB64)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := srv.st.SaveAttachment("sess-a", "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	// 正常取回
	resp, err := http.Get(ts.URL + "/attachments/" + rel)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("应 200: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type 应是 image/png: %q", ct)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(data) {
		t.Fatalf("字节不一致: %d vs %d", len(got), len(data))
	}

	// 穿越形态（Mux 会 Clean/重定向，最终都到不了文件）
	for _, p := range []string{
		"/attachments/sess-a/../../secret.png",
		"/attachments/sess-a/%2e%2e%2fsecret.png",
		"/attachments/../secret.png",
		"/attachments/sess-a/..%5Csecret.png",
	} {
		r, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode == http.StatusOK {
			t.Fatalf("穿越路径应 404: %s → %d", p, r.StatusCode)
		}
	}

	// 不存在（形态合法但文件没有）
	missing := "/attachments/sess-a/" + strings.Repeat("a", 32) + ".png"
	r, err := http.Get(ts.URL + missing)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在的附件应 404: %d", r.StatusCode)
	}

	// 非法会话 id
	r, err = http.Get(ts.URL + "/attachments/A_BAD/" + strings.Repeat("a", 32) + ".png")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("非法会话 id 应 404: %d", r.StatusCode)
	}

	// 其它方法拒绝
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/attachments/"+rel, nil)
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST 应 405: %d", r.StatusCode)
	}
}
