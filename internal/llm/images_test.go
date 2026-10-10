// 图片附件的 wire 编码测试：带图消息的双格式形态 + 无图消息逐字节不变 +
// 读取失败 fail-open。样本用固定 1x1 PNG（base64），断言可复现。
package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// fixedPNG 是固定 1x1 透明 PNG 的 base64（解码 70 字节，远小于 5MB 上限）。
const fixedPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// bindTestLoader 装一个返回固定 PNG 的 loader，测完恢复原状（loader 是包级
// 单例——串行测试共享，各自保存/恢复避免互相污染）。
func bindTestLoader(t *testing.T) {
	t.Helper()
	old := imageLoader
	raw, err := base64.StdEncoding.DecodeString(fixedPNG)
	if err != nil {
		t.Fatalf("固定 PNG 样本解码失败（样本写坏了）: %v", err)
	}
	imageLoader = func(ImageRef) (string, []byte, error) {
		return "image/png", raw, nil
	}
	t.Cleanup(func() { imageLoader = old })
}

// TestOpenAIImageMessageContentArray：带图 user 消息的 content 变数组，
// 形态 = text + image_url（data: 前缀 + base64）。
func TestOpenAIImageMessageContentArray(t *testing.T) {
	bindTestLoader(t)
	msgs := []Message{{Role: "user", Content: "看这张图", Images: []ImageRef{{Path: "s1/a.png", Mime: "image/png"}}}}
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
	if len(body.Messages) != 1 {
		t.Fatalf("消息数不符: %s", b)
	}
	var parts []map[string]any
	if err := json.Unmarshal(body.Messages[0].Content, &parts); err != nil {
		t.Fatalf("content 应是分片数组: %s", body.Messages[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("应有 text + image_url 两个分片: %s", body.Messages[0].Content)
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "看这张图" {
		t.Fatalf("text 分片不符: %v", parts[0])
	}
	if parts[1]["type"] != "image_url" {
		t.Fatalf("第二分片应是 image_url: %v", parts[1])
	}
	iu, ok := parts[1]["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("image_url 应是对象: %v", parts[1])
	}
	url, _ := iu["url"].(string)
	want := "data:image/png;base64," + fixedPNG
	if url != want {
		t.Fatalf("image_url 不符:\n got %q\nwant %q", url, want)
	}
}

// TestOpenAIPlainMessagesByteIdentical：无图消息的 wire 形态必须与旧实现
// （Message 直接序列化）逐字节一致——换 wire 类型不允许动存量请求体。
func TestOpenAIPlainMessagesByteIdentical(t *testing.T) {
	tc := ToolCall{ID: "c1", Type: "function"}
	tc.Function.Name = "bash"
	tc.Function.Arguments = `{"command":"ls"}`
	msgs := []Message{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "在", ReasoningContent: "想了一下"},
		{Role: "tool", Content: "结果", ToolCallID: "t1"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{tc}},
	}
	got, err := json.Marshal(buildOpenAIRequest("m", msgs, requestOpts{}, false).Messages)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("无图消息 wire 形态变了:\n got %s\nwant %s", got, want)
	}
}

// TestAnthropicImageBlocks：带图 user 消息 → text 块 + image 块
// （source.type=base64 + media_type + data）。
func TestAnthropicImageBlocks(t *testing.T) {
	bindTestLoader(t)
	msgs := []Message{{Role: "user", Content: "看这张图", Images: []ImageRef{{Path: "s1/a.png", Mime: "image/png"}}}}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("应有 text + image 两个块: %+v", req.Messages)
	}
	text := req.Messages[0].Content[0]
	if text.Type != "text" || text.Text != "看这张图" {
		t.Fatalf("text 块不符: %+v", text)
	}
	img := req.Messages[0].Content[1]
	if img.Type != "image" || img.Source == nil {
		t.Fatalf("第二块应是 image: %+v", img)
	}
	if img.Source.Type != "base64" || img.Source.MediaType != "image/png" || img.Source.Data != fixedPNG {
		t.Fatalf("image source 不符: %+v", img.Source)
	}
}

// TestImageLoadFailureFailOpen：读取失败不中断请求——跳过该图，text 尾部
// 追加人话提示（openai 与 anthropic 两条路径同行为）。
func TestImageLoadFailureFailOpen(t *testing.T) {
	old := imageLoader
	imageLoader = func(ref ImageRef) (string, []byte, error) {
		return "", nil, errTestLoad
	}
	t.Cleanup(func() { imageLoader = old })

	msgs := []Message{{Role: "user", Content: "看这张图",
		Images: []ImageRef{{Path: "s1/gone.png", Mime: "image/png"}}}}

	// openai：只有 text 分片，尾部带提示
	req := buildOpenAIRequest("m", msgs, requestOpts{}, false)
	b, _ := json.Marshal(req)
	if strings.Contains(string(b), "base64") {
		t.Fatalf("失败的图不应出现在请求里: %s", b)
	}
	if !strings.Contains(string(b), `[图片未能加载: s1/gone.png]`) {
		t.Fatalf("openai 请求应带 fail-open 提示: %s", b)
	}
	// 调用方的消息不被就地改写（fail-open 只作用于请求副本）
	if msgs[0].Content != "看这张图" {
		t.Fatalf("不该改写调用方历史: %q", msgs[0].Content)
	}

	// anthropic：同款
	ar, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ar.Messages) != 1 || len(ar.Messages[0].Content) != 1 {
		t.Fatalf("失败的图不应产生 image 块: %+v", ar.Messages)
	}
	if !strings.Contains(ar.Messages[0].Content[0].Text, "[图片未能加载: s1/gone.png]") {
		t.Fatalf("anthropic 应带 fail-open 提示: %+v", ar.Messages[0].Content[0])
	}
}

// TestNoImageMessagesUntouchedByLoader：无图消息即使 loader 已注入也零开销、
// 零改动（回归钉子：无图路径不许碰 loader）。
func TestNoImageMessagesUntouchedByLoader(t *testing.T) {
	called := false
	old := imageLoader
	imageLoader = func(ImageRef) (string, []byte, error) { called = true; return "", nil, nil }
	t.Cleanup(func() { imageLoader = old })

	msgs := []Message{{Role: "user", Content: "纯文本"}}
	req := buildOpenAIRequest("m", msgs, requestOpts{}, false)
	if called {
		t.Fatal("无图消息不该调用 loader")
	}
	if req.Messages[0].Content != "纯文本" {
		t.Fatalf("无图消息 content 应保持 string: %v", req.Messages[0].Content)
	}
}

var errTestLoad = &testLoadError{}

type testLoadError struct{}

func (*testLoadError) Error() string { return "附件读取失败（测试）" }
