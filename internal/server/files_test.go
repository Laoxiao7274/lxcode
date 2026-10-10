// 图片批次 B 的 server 层测试：chat.send 的文件附件通道（校验 / 落盘 /
// 消息文本追加 / 原名净化 / 与图片混发）。文件不进视觉通道——非 vision 模型
// 发文件必须照常成功。
package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/store"
)

// newSessionForTest 开一个测试会话并返回 id。
func newSessionForTest(t *testing.T, client *wsTestClient) string {
	t.Helper()
	resp := client.call(protocol.MethodSessionNew, protocol.SessionNewParams{})
	if resp.Error != nil {
		t.Fatalf("session.new: %+v", resp.Error)
	}
	var sess protocol.SessionResult
	decodeServerResult(t, resp.Result, &sess)
	return sess.SessionID
}

// attachmentLines 从消息文本里抽出「[附件]」行。
func attachmentLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "[附件] ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestChatSendFilesSavesAndAppends：带文件 chat.send → 落盘到附件目录 +
// 消息文本追加「[附件] 名字 → attachments/<sid>/<文件>」行；非 vision 模型
//（m1 默认）也成功——文件不走视觉通道。
func TestChatSendFilesSavesAndAppends(t *testing.T) {
	srv, client, _ := newTestServer(t, textStream())
	sid := newSessionForTest(t, client)

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid,
		Text:      "帮我看看这个文件",
		Files: []protocol.ChatSendFile{
			{Name: "报告 v2.txt", Data: base64.StdEncoding.EncodeToString([]byte("hello file"))},
		},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("文件发送应成功: %+v", resp)
	}
	waitDone(t, client, sid)

	// 落盘：附件目录里恰有一个文件，内容一致
	entries, err := os.ReadDir(filepath.Join(srv.st.AttachmentsRoot(), sid))
	if err != nil || len(entries) != 1 {
		t.Fatalf("附件目录应有 1 个文件: %v err=%v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(srv.st.AttachmentsRoot(), sid, entries[0].Name()))
	if err != nil || string(data) != "hello file" {
		t.Fatalf("落盘内容不一致: %q err=%v", data, err)
	}
	// 扩展名保留（原名 .txt）
	if filepath.Ext(entries[0].Name()) != ".txt" {
		t.Fatalf("落盘名应保留原扩展名: %s", entries[0].Name())
	}

	// 历史里的用户消息带「[附件]」行，且指向落盘文件
	hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sid})
	var h protocol.ChatHistoryResult
	decodeServerResult(t, hist.Result, &h)
	var userText string
	for _, m := range h.Messages {
		if m.Role == "user" {
			userText = m.Content
			break
		}
	}
	lines := attachmentLines(userText)
	if len(lines) != 1 {
		t.Fatalf("消息文本应有 1 行附件说明: %q", userText)
	}
	if !strings.Contains(lines[0], "[附件] 报告 v2.txt → attachments/"+sid+"/") {
		t.Fatalf("附件行格式不符: %q", lines[0])
	}
	// 指向的文件确实存在
	idx := strings.LastIndex(lines[0], "attachments/")
	rel := lines[0][idx+len("attachments/"):]
	if _, err := os.Stat(filepath.Join(srv.st.AttachmentsRoot(), filepath.FromSlash(rel))); err != nil {
		t.Fatalf("附件行指向的文件不存在: %s err=%v", rel, err)
	}
}

// TestChatSendFilesNameSanitized：路径分隔符与控制字符被净化——原名里的
// 「../../」与换行都不会到达消息文本（换行会伪造出第二条附件行）。
func TestChatSendFilesNameSanitized(t *testing.T) {
	srv, client, _ := newTestServer(t, textStream())
	sid := newSessionForTest(t, client)

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid,
		Text:      "看文件",
		Files: []protocol.ChatSendFile{
			{Name: `..\..\evil\notes.txt`, Data: base64.StdEncoding.EncodeToString([]byte("x"))},
			{Name: "weird:name?.md", Data: base64.StdEncoding.EncodeToString([]byte("y"))},
		},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("带杂质名字的文件应被净化而不是拒绝: %+v", resp)
	}
	waitDone(t, client, sid)

	// 落盘名都是随机 hex（原名永不成为路径成分）；扩展名只留字母数字
	entries, err := os.ReadDir(filepath.Join(srv.st.AttachmentsRoot(), sid))
	if err != nil || len(entries) != 2 {
		t.Fatalf("附件目录应有 2 个文件: %v err=%v", entries, err)
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if ext != ".txt" && ext != ".md" {
			t.Fatalf("扩展名应净化为 .txt/.md: %s", e.Name())
		}
	}

	// 消息文本：每行一个附件说明，名字无路径分隔符、无控制字符
	hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sid})
	var h protocol.ChatHistoryResult
	decodeServerResult(t, hist.Result, &h)
	var userText string
	for _, m := range h.Messages {
		if m.Role == "user" {
			userText = m.Content
			break
		}
	}
	lines := attachmentLines(userText)
	if len(lines) != 2 {
		t.Fatalf("应有 2 行附件说明（换行未伪造新行）: %q", userText)
	}
	for _, line := range lines {
		if strings.ContainsAny(line, "\n\r/\\") && !strings.Contains(line, "attachments/") {
			t.Fatalf("附件行含未净化字符: %q", line)
		}
		if strings.Contains(line, "..") {
			t.Fatalf("附件行含路径穿越片段: %q", line)
		}
	}
	if !strings.Contains(lines[0], "notes.txt") {
		t.Fatalf("净化后应保留 base 名（穿越片段被切掉）: %q", lines[0])
	}
}

// TestChatSendFilesRejects：>20MB、>4 个、base64 坏——都是 CodeInvalidParams
// 人话错误，且不落盘、不入历史。
func TestChatSendFilesRejects(t *testing.T) {
	cases := []struct {
		name  string
		files []protocol.ChatSendFile
		want  string
	}{
		{
			name: "超 20MB",
			files: []protocol.ChatSendFile{
				{Name: "big.bin", Data: base64.StdEncoding.EncodeToString(make([]byte, store.MaxFileAttachmentBytes+1))},
			},
			want: "超过大小上限",
		},
		{
			name: "超过 4 个",
			files: []protocol.ChatSendFile{
				{Name: "a.txt", Data: "eA=="}, {Name: "b.txt", Data: "eA=="},
				{Name: "c.txt", Data: "eA=="}, {Name: "d.txt", Data: "eA=="},
				{Name: "e.txt", Data: "eA=="},
			},
			want: "最多带 4 个文件",
		},
		{
			name:  "base64 坏",
			files: []protocol.ChatSendFile{{Name: "x.txt", Data: "!!!not-base64!!!"}},
			want:  "base64 解码失败",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, client, _ := newTestServer(t, textStream())
			sid := newSessionForTest(t, client)
			resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
				SessionID: sid, Text: "看文件", Files: tc.files,
			})
			if resp == nil || resp.Error == nil {
				t.Fatalf("应拒绝: %+v", resp)
			}
			if !strings.Contains(resp.Error.Message, tc.want) {
				t.Fatalf("错误文案应含 %q: %s", tc.want, resp.Error.Message)
			}
			// 没落盘（拒绝发生在会话行之前——附件目录不该存在）
			if _, err := os.Stat(srv.st.AttachmentsRoot()); !os.IsNotExist(err) {
				entries, _ := os.ReadDir(srv.st.AttachmentsRoot())
				if len(entries) > 0 {
					t.Fatalf("拒绝后不应有附件: %v", entries)
				}
			}
			// 没入历史
			hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sid})
			var h protocol.ChatHistoryResult
			decodeServerResult(t, hist.Result, &h)
			if len(h.Messages) != 0 {
				t.Fatalf("拒绝后历史应为空: %+v", h.Messages)
			}
		})
	}
}

// TestChatSendFilesMixedWithImages：图片与文件同条消息混发——vision 模型上
// 图片走视觉通道（落盘 + WithImages 引用），文件走文本行，两条通道互不干扰。
func TestChatSendFilesMixedWithImages(t *testing.T) {
	srv, client, reg := newTestServer(t, textStream())
	addVisionModel(t, reg)
	sid := newSessionForTest(t, client)

	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid,
		Text:      "图和文件都看看",
		Images:    []protocol.ChatSendImage{{Mime: "image/png", Data: pngB64}},
		Files:     []protocol.ChatSendFile{{Name: "数据.csv", Data: base64.StdEncoding.EncodeToString([]byte("a,b\n1,2"))}},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("混发应成功: %+v", resp)
	}
	waitDone(t, client, sid)

	dir := filepath.Join(srv.st.AttachmentsRoot(), sid)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("附件目录应有 2 个文件（一图一文件）: %v err=%v", entries, err)
	}
	var hasPNG, hasCSV bool
	for _, e := range entries {
		switch filepath.Ext(e.Name()) {
		case ".png":
			hasPNG = true
		case ".csv":
			hasCSV = true
		}
	}
	if !hasPNG || !hasCSV {
		t.Fatalf("应各落盘一张 png 与一个 csv: %v", entries)
	}

	// 历史用户消息：带图片引用 + 附件文本行
	hist := client.call(protocol.MethodChatHistory, protocol.ChatHistoryParams{SessionID: sid})
	var h protocol.ChatHistoryResult
	decodeServerResult(t, hist.Result, &h)
	var user *struct {
		text   string
		images int
	}
	for i := range h.Messages {
		m := h.Messages[i]
		if m.Role != "user" {
			continue
		}
		user = &struct {
			text   string
			images int
		}{text: m.Content, images: len(m.Images)}
		break
	}
	if user == nil {
		t.Fatal("历史里应有用户消息")
	}
	if user.images != 1 {
		t.Fatalf("用户消息应带 1 个图片引用: %d", user.images)
	}
	if lines := attachmentLines(user.text); len(lines) != 1 || !strings.Contains(lines[0], "数据.csv") {
		t.Fatalf("消息文本应恰有一行 csv 附件说明: %q", user.text)
	}
}

// TestChatSendFilesOnlyNoVision：纯文件（无图）发给**未声明 vision** 的模型
// 必须成功——vision 门只看图片，不看文件。
func TestChatSendFilesOnlyNoVision(t *testing.T) {
	_, client, _ := newTestServer(t, textStream()) // m1 未声明 vision
	sid := newSessionForTest(t, client)
	resp := client.call(protocol.MethodChatSend, protocol.ChatSendParams{
		SessionID: sid,
		Text:      "读这个",
		Files:     []protocol.ChatSendFile{{Name: "a.txt", Data: "eA=="}},
	})
	if resp == nil || resp.Error != nil {
		t.Fatalf("纯文件不应触发 vision 门: %+v", resp)
	}
	waitDone(t, client, sid)
}
