// read_image 的内核集成测试：模型发起 read_image → 工具执行（图片落进会话附件
// 目录 + 引用挂到 tool 结果消息）→ **下一轮请求里确实带上了图**（fakeStream
// 收到的消息序列断言）；非视觉模型得到人话错误。
package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/store"
)

// bindVision 注册并绑定一个声明了视觉能力的 default 模型。
func bindVision(t *testing.T, reg *config.Registry) {
	t.Helper()
	m := config.ModelConfig{
		ID: "v1", BaseURL: "http://localhost:1", Model: "vision-model",
		Format: config.FormatOpenAI, Enabled: true,
		Capabilities: config.Capabilities{Tools: true, Vision: true},
	}
	if err := reg.Add(m); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "v1"); err != nil {
		t.Fatal(err)
	}
}

const agentTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func TestReadImageFeedsNextRequest(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s, reg := newTestSession(t)
	bindVision(t, reg)
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := s.SetWorkDir(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(agentTestPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_image", `{"path":"shot.png"}`),
		textResult("看到了：一张 1x1 的透明 PNG"),
	}}
	s.stream = fake.stream
	if err := s.Send("看看这张截图"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	// 第二次请求（工具结果回填后的续轮）里，tool 消息带图片引用——视觉通道接通
	fake.mu.Lock()
	msgs := fake.lastMsgs
	fake.mu.Unlock()
	var toolMsg *llm.Message
	for i := range msgs {
		if msgs[i].Role == "tool" {
			toolMsg = &msgs[i]
		}
	}
	if toolMsg == nil {
		t.Fatalf("续轮请求应含 tool 消息: %+v", msgs)
	}
	if len(toolMsg.Images) != 1 {
		t.Fatalf("tool 结果消息应带 1 张图（视觉通道）: %+v", toolMsg)
	}
	if toolMsg.Images[0].Mime != "image/png" {
		t.Fatalf("图片 mime 不符: %+v", toolMsg.Images[0])
	}
	// 引用指向本会话的附件目录（llm 层按 attachments 根读回编码）
	if !strings.HasPrefix(toolMsg.Images[0].Path, s.SessionID()+"/") {
		t.Fatalf("图片引用应落在本会话附件目录下: %+v", toolMsg.Images[0])
	}
	// 文件确实落盘
	entries, err := os.ReadDir(filepath.Join(st.AttachmentsRoot(), s.SessionID()))
	if err != nil || len(entries) != 1 {
		t.Fatalf("附件目录应有 1 个文件: %v err=%v", entries, err)
	}
}

func TestReadImageWithoutVisionErrors(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s, reg := newTestSession(t)
	bindDefault(t, reg) // 未声明 Vision
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	fake := &fakeStream{script: [][]llm.StreamEvent{
		toolCallResult("read_image", `{"path":"shot.png"}`),
		textResult("好的，我换个方式"),
	}}
	s.stream = fake.stream
	if err := s.Send("看看这张截图"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	// 工具结果是人话错误（不假装读图）
	var toolMsg *llm.Message
	for i := range s.history {
		if s.history[i].Role == "tool" {
			toolMsg = &s.history[i]
		}
	}
	if toolMsg == nil {
		t.Fatal("历史应有 tool 结果消息")
	}
	if !strings.Contains(toolMsg.Content, "不支持视觉") {
		t.Fatalf("非视觉模型应得人话错误: %s", toolMsg.Content)
	}
	if len(toolMsg.Images) != 0 {
		t.Fatalf("不应有图片注入: %+v", toolMsg.Images)
	}
}
