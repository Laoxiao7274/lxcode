// chat.send 附件 path 形态（2026-12 用户拍板：本地路径优先、base64 兜底）的
// server 层测试：按路径读盘落盘 / mime 扩展名推导 / 超限与不存在的带路径错误 /
// path+data 互斥 / 一条消息 path 与 base64 混发。wire 兼容（老客户端纯 base64）
// 由 attachments_test.go / files_test.go 的既有用例钉住——那些用例全走 base64，
// 本文件不改它们。
package server

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// writeTempFile 落一个测试源文件（path 形态的读取源）。
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestChatSendPathFileSavesAndAppends：path 形态的文件 → 服务端按路径读盘落盘 +
// 「[附件]」行 + 展示元数据（名字回落路径 basename）。WS 帧里只有路径没有内容
// ——这正是这次改造的目的，wire 层不再出现 base64。
func TestChatSendPathFileSavesAndAppends(t *testing.T) {
	srv, client, _ := newTestServer(t, textStream())
	sid := newSessionForTest(t, client)

	src := writeTempFile(t, t.TempDir(), "报告 v2.txt", "hello from disk")
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid,
		Text:      "帮我看看这个文件",
		Files:     []protocol.ChatSendFile{{Path: src}},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("path 形态发送应成功: %+v", resp)
	}
	waitDone(t, client, sid)

	entries, err := os.ReadDir(filepath.Join(srv.st.AttachmentsRoot(), sid))
	if err != nil || len(entries) != 1 {
		t.Fatalf("附件目录应有 1 个文件: %v err=%v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(srv.st.AttachmentsRoot(), sid, entries[0].Name()))
	if err != nil || string(data) != "hello from disk" {
		t.Fatalf("落盘内容应与源文件一致: %q err=%v", data, err)
	}
	if filepath.Ext(entries[0].Name()) != ".txt" {
		t.Fatalf("落盘名应保留原扩展名: %s", entries[0].Name())
	}

	hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sid})
	var h protocol.ChatHistoryResult
	decodeServerResult(t, hist.Result, &h)
	var user *struct {
		text string
		atts int
	}
	for i := range h.Messages {
		m := &h.Messages[i]
		if m.Role != "user" {
			continue
		}
		user = &struct {
			text string
			atts int
		}{m.Content, len(m.Attachments)}
	}
	if user == nil {
		t.Fatal("历史里应有用户消息")
	}
	if !strings.Contains(user.text, "[附件] 报告 v2.txt → attachments/") {
		t.Fatalf("应追加带 basename 的附件行: %q", user.text)
	}
	if user.atts != 1 {
		t.Fatalf("用户消息应带 1 条附件元数据: %d", user.atts)
	}
	// wire 上绝不该出现源文件内容（path 形态的意义所在）
	b, _ := json.Marshal(h.Messages)
	if strings.Contains(string(b), "hello from disk") {
		t.Fatalf("历史回放不应含源文件内容: %s", b)
	}
}

// TestChatSendPathImageMimeDerivation：path 形态的图片 mime 可省——服务端按
// 扩展名推导（.png → image/png）；推导不出（未知扩展名）报参数错误。
func TestChatSendPathImageMimeDerivation(t *testing.T) {
	srv, client, reg := newTestServer(t, textStream())
	addVisionModel(t, reg)
	sid := newSessionForTest(t, client)

	dir := t.TempDir()
	src := writeTempFile(t, dir, "shot.png", "PNGDATA")
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid,
		Text:      "看图",
		Images:    []protocol.ChatSendImage{{Path: src}},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("mime 省略 + .png 应推导成功: %+v", resp)
	}
	waitDone(t, client, sid)
	entries, err := os.ReadDir(filepath.Join(srv.st.AttachmentsRoot(), sid))
	if err != nil || len(entries) != 1 {
		t.Fatalf("图片应落盘: %v err=%v", entries, err)
	}

	// 未知扩展名 → 参数错误（mime 推导不出）
	sid2 := newSessionForTest(t, client)
	bad := writeTempFile(t, dir, "weird.xyz", "x")
	resp = client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid2,
		Text:      "看图",
		Images:    []protocol.ChatSendImage{{Path: bad}},
	})
	if resp == nil || resp.Error == nil {
		t.Fatal("未知扩展名应拒绝")
	}
	if resp.Error.Code != protocol.CodeInvalidParams {
		t.Fatalf("应是 CodeInvalidParams: %d", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "不支持") {
		t.Fatalf("文案应说明类型不支持: %s", resp.Error.Message)
	}
}

// TestChatSendPathValidation：文件不存在 / 超限 / path+data 互斥三类参数错误。
// 不存在的错误必须带路径——前端以「本地文件读取失败」特征串识别并降级 base64 重发。
func TestChatSendPathValidation(t *testing.T) {
	_, client, reg := newTestServer(t, textStream())
	addVisionModel(t, reg)
	sid := newSessionForTest(t, client)

	// 超限的真实文件（>20MB——稀疏写不必真占盘，直接 WriteFile 21MB 字节）
	bigPath := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(bigPath, make([]byte, store.MaxFileAttachmentBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		files []protocol.ChatSendFile
		want  string
	}{
		{"文件不存在", []protocol.ChatSendFile{{Path: filepath.Join(t.TempDir(), "gone.txt")}}, "本地文件读取失败"},
		{"超 20MB", []protocol.ChatSendFile{{Path: bigPath}}, "大小上限"},
		{"path+data 互斥", []protocol.ChatSendFile{{Path: "C:\\x.txt", Data: "aGk="}}, "不能同时携带 path 与 data"},
	}
	for _, tc := range cases {
		resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
			SessionID: sid, Text: "看文件", Files: tc.files,
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
	// 「文件不存在」的错误带路径（前端展示与排查都需要）
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid, Text: "看文件",
		Files: []protocol.ChatSendFile{{Path: "Z:\\不存在\\gone.txt"}},
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "gone.txt") {
		t.Fatalf("不存在的错误应带路径: %+v", resp.Error)
	}
}

// TestChatSendPathMixedWithBase64：一条消息里 path 与 base64 混发——两边都落盘
// （降级重发形态：同一个文件，一次 path 一次 base64，也走这条路径）。
func TestChatSendPathMixedWithBase64(t *testing.T) {
	srv, client, reg := newTestServer(t, textStream())
	addVisionModel(t, reg)
	sid := newSessionForTest(t, client)

	src := writeTempFile(t, t.TempDir(), "disk.txt", "from disk")
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid,
		Text:      "两个文件",
		Files: []protocol.ChatSendFile{
			{Path: src},                                                        // path 形态
			{Name: "inline.txt", Data: base64.StdEncoding.EncodeToString([]byte("from wire"))}, // base64 形态
		},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("混合形态发送应成功: %+v", resp)
	}
	waitDone(t, client, sid)

	entries, err := os.ReadDir(filepath.Join(srv.st.AttachmentsRoot(), sid))
	if err != nil || len(entries) != 2 {
		t.Fatalf("应落盘 2 个文件: %d err=%v", len(entries), err)
	}
	contents := map[string]bool{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(srv.st.AttachmentsRoot(), sid, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		contents[string(data)] = true
	}
	if !contents["from disk"] || !contents["from wire"] {
		t.Fatalf("两份内容都应落盘: %v", contents)
	}

	// 图片同批混发（vision 门只看图片，模型已声明 vision）
	sid2 := newSessionForTest(t, client)
	imgSrc := writeTempFile(t, t.TempDir(), "shot.png", "PNGDATA")
	resp = client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid2, Text: "图",
		Images: []protocol.ChatSendImage{
			{Path: imgSrc, Mime: "image/png"},
			{Mime: "image/png", Data: pngB64},
		},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("图片混合形态应成功: %+v", resp)
	}
	waitDone(t, client, sid2)
	hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sid2})
	var h protocol.ChatHistoryResult
	decodeServerResult(t, hist.Result, &h)
	if len(h.Messages) == 0 || len(h.Messages[0].Images) != 2 {
		t.Fatalf("应带 2 个图片引用: %+v", h.Messages)
	}
}

// TestChatSendPathImageOversize：path 形态的图片同样受 5MB（MaxImageBytes）约束。
func TestChatSendPathImageOversize(t *testing.T) {
	_, client, reg := newTestServer(t, textStream())
	addVisionModel(t, reg)
	sid := newSessionForTest(t, client)

	bigPath := filepath.Join(t.TempDir(), "big.png")
	if err := os.WriteFile(bigPath, make([]byte, llm.MaxImageBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid, Text: "看图",
		Images: []protocol.ChatSendImage{{Path: bigPath}},
	})
	if resp == nil || resp.Error == nil {
		t.Fatal("超限图片应拒绝")
	}
	if !strings.Contains(resp.Error.Message, "大小上限") {
		t.Fatalf("文案应说明大小上限: %s", resp.Error.Message)
	}
}
