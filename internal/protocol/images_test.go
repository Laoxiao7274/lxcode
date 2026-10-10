// 图片批次 A 的协议往返测试：ChatSendParams.Images 解析 + omitempty 不破坏
// 旧客户端（不带 images 的载荷 wire 上无该键）+ 历史消息的图片引用回放。
package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// TestChatSendParamsImagesRoundTrip：带图参数解析；Data = 不带前缀的 base64。
func TestChatSendParamsImagesRoundTrip(t *testing.T) {
	raw := `{"session_id":"s1","text":"看图","images":[
		{"mime":"image/png","data":"iVBORw0KGgo="},
		{"mime":"image/jpeg","data":"/9j/4AAQ"}]}`
	var p ChatSendParams
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Images) != 2 {
		t.Fatalf("应解析出 2 张图: %+v", p)
	}
	if p.Images[0].Mime != "image/png" || p.Images[0].Data != "iVBORw0KGgo=" {
		t.Fatalf("第一张不符: %+v", p.Images[0])
	}
	if p.Images[1].Mime != "image/jpeg" {
		t.Fatalf("第二张不符: %+v", p.Images[1])
	}
	// 再序列化回来字段名稳定
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back ChatSendParams
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Images) != 2 || back.Images[0] != p.Images[0] {
		t.Fatalf("往返不一致: %s", b)
	}
}

// TestChatSendParamsOmitEmptyImages：不带 images 的旧形态载荷 wire 上无该键
// （老客户端零影响，协议 Version 不变）。
func TestChatSendParamsOmitEmptyImages(t *testing.T) {
	b, err := json.Marshal(ChatSendParams{SessionID: "s1", Text: "你好"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "images") {
		t.Fatalf("无图载荷不应出现 images 键: %s", b)
	}
	// 旧客户端的载荷（无 images 键）照常解析
	var p ChatSendParams
	if err := json.Unmarshal([]byte(`{"session_id":"s1","text":"你好"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Images != nil {
		t.Fatalf("旧载荷不应产生图片: %+v", p)
	}
}

// TestHistoryMessageImagesWire：历史回放的消息带 images **引用**（path + mime），
// 不含 base64。
func TestHistoryMessageImagesWire(t *testing.T) {
	res := ChatHistoryResult{Messages: []llm.Message{
		{Role: "user", Content: "看图", Seq: 1,
			Images: []llm.ImageRef{{Path: "s1/abc.png", Mime: "image/png"}}},
	}}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "base64") {
		t.Fatalf("历史回放不含 base64: %s", b)
	}
	if !strings.Contains(string(b), `"path":"s1/abc.png"`) || !strings.Contains(string(b), `"mime":"image/png"`) {
		t.Fatalf("回放应带图片引用: %s", b)
	}
	// 无图消息整键缺席
	b, err = json.Marshal(ChatHistoryResult{Messages: []llm.Message{{Role: "user", Content: "纯文本"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "images") {
		t.Fatalf("无图消息不应出现 images 键: %s", b)
	}
}
