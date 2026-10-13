// 工具结果带图（read_image 的视觉注入）的双 wire 编码测试：
// openai = content 分片数组（text + image_url data URI）；
// anthropic = tool_result 的 content 块数组（text + image base64 源）；
// 以及不带图的工具结果逐字节不变（string content / 空 content 缺席）。
package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// TestOpenAIToolMessageImageContent：带图 tool 消息的 content 变分片数组
//（text + image_url）——与带图 user 消息同一形态。
func TestOpenAIToolMessageImageContent(t *testing.T) {
	bindTestLoader(t)
	msgs := []Message{
		{Role: "user", Content: "看看截图"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1", Function: struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{Name: "read_image", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "c1", Content: "已读取图片", Images: []ImageRef{{Path: "s1/a.png", Mime: "image/png"}}},
	}
	req := buildOpenAIRequest("m", msgs, requestOpts{}, false)
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	var toolMsg *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	for i := range body.Messages {
		if body.Messages[i].Role == "tool" {
			toolMsg = &body.Messages[i]
		}
	}
	if toolMsg == nil {
		t.Fatalf("应有一条 tool 消息: %s", b)
	}
	var parts []map[string]any
	if err := json.Unmarshal(toolMsg.Content, &parts); err != nil {
		t.Fatalf("tool content 应是分片数组: %s", toolMsg.Content)
	}
	if len(parts) != 2 {
		t.Fatalf("应有 text + image_url 两个分片: %s", toolMsg.Content)
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "已读取图片" {
		t.Fatalf("text 分片不符: %v", parts[0])
	}
	if parts[1]["type"] != "image_url" {
		t.Fatalf("第二分片应是 image_url: %v", parts[1])
	}
	iu, _ := parts[1]["image_url"].(map[string]any)
	url, _ := iu["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("image_url 应是 data URI: %q", url)
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,")); err != nil {
		t.Fatalf("data URI 的 base64 应可解码: %v", err)
	}
}

// TestOpenAIToolMessageWithoutImageUnchanged：不带图的 tool 消息 content 保持
// string（旧形态逐字节不变）。
func TestOpenAIToolMessageWithoutImageUnchanged(t *testing.T) {
	msgs := []Message{{Role: "tool", ToolCallID: "c1", Content: "普通结果"}}
	req := buildOpenAIRequest("m", msgs, requestOpts{}, false)
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"content":"普通结果"`) {
		t.Fatalf("无图 tool 消息应保持 string content: %s", b)
	}
}

// TestAnthropicToolResultImageBlocks：带图工具结果的 tool_result content 变
// 块数组（text + image，base64 源）——Anthropic 官方形态。
func TestAnthropicToolResultImageBlocks(t *testing.T) {
	bindTestLoader(t)
	msgs := []Message{
		{Role: "user", Content: "看看截图"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1"}}},
		{Role: "tool", ToolCallID: "c1", Content: "已读取图片", Images: []ImageRef{{Path: "s1/a.png", Mime: "image/png"}}},
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	// user("看看截图") + assistant(tool_use) + user(tool_result 承接) = 3 条
	if len(req.Messages) != 3 {
		t.Fatalf("消息数不符: %+v", req.Messages)
	}
	var resultBlock *anthropicContentBlock
	for _, m := range req.Messages {
		for i := range m.Content {
			if m.Content[i].Type == "tool_result" {
				resultBlock = &m.Content[i]
			}
		}
	}
	if resultBlock == nil {
		t.Fatalf("应有 tool_result 块: %+v", req.Messages[1].Content)
	}
	parts, ok := resultBlock.Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("带图 tool_result 的 content 应是块数组: %T", resultBlock.Content)
	}
	if len(parts) != 2 {
		t.Fatalf("应有 text + image 两个块: %+v", parts)
	}
	if parts[0].Type != "text" || parts[0].Text != "已读取图片" {
		t.Fatalf("text 块不符: %+v", parts[0])
	}
	if parts[1].Type != "image" || parts[1].Source == nil ||
		parts[1].Source.MediaType != "image/png" || parts[1].Source.Type != "base64" {
		t.Fatalf("image 块不符: %+v", parts[1])
	}
	if _, err := base64.StdEncoding.DecodeString(parts[1].Source.Data); err != nil {
		t.Fatalf("image 源 base64 应可解码: %v", err)
	}
}

// TestAnthropicToolResultWithoutImageUnchanged：不带图的 tool_result content
// 保持 string；空结果不写 content（旧形态逐字节不变）。
func TestAnthropicToolResultWithoutImageUnchanged(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1"}, {ID: "c2"}}},
		{Role: "tool", ToolCallID: "c1", Content: "普通结果"},
		{Role: "tool", ToolCallID: "c2", Content: ""},
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var results []anthropicContentBlock
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == "tool_result" {
				results = append(results, b)
			}
		}
	}
	if len(results) != 2 {
		t.Fatalf("应有两个 tool_result 块: %+v", req.Messages[1].Content)
	}
	if s, ok := results[0].Content.(string); !ok || s != "普通结果" {
		t.Fatalf("无图 tool_result 应保持 string content: %T %v", results[0].Content, results[0].Content)
	}
	raw, err := json.Marshal(results[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"content"`) {
		t.Fatalf("空结果不该写 content 键: %s", raw)
	}
}
