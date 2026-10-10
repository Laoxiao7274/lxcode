// 图片批次 B 的协议往返测试：ChatSendParams.Files 解析 + omitempty 不破坏
// 老形态（与 images_test.go 同一套口径）。
package protocol

import (
	"encoding/json"
	"testing"
)

func TestChatSendParamsFilesRoundTrip(t *testing.T) {
	raw := []byte(`{"session_id":"s1","text":"看文件","files":[{"name":"报告 v2.txt","data":"aGVsbG8="}]}`)
	var p ChatSendParams
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 1 {
		t.Fatalf("应解析出 1 个文件: %+v", p.Files)
	}
	if p.Files[0].Name != "报告 v2.txt" || p.Files[0].Data != "aGVsbG8=" {
		t.Fatalf("文件字段不符: %+v", p.Files[0])
	}
	// 回写仍带 files 键
	back, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var p2 ChatSendParams
	if err := json.Unmarshal(back, &p2); err != nil {
		t.Fatal(err)
	}
	if len(p2.Files) != 1 || p2.Files[0] != p.Files[0] {
		t.Fatalf("往返不一致: %+v", p2.Files)
	}
}

func TestChatSendParamsOmitEmptyFiles(t *testing.T) {
	raw, err := json.Marshal(ChatSendParams{SessionID: "s1", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["files"]; ok {
		t.Fatalf("不带文件时 wire 上不应有 files 键: %s", raw)
	}
	if _, ok := m["images"]; ok {
		t.Fatalf("不带图片时 wire 上不应有 images 键: %s", raw)
	}
}
