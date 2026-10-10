// 图片批次 A 的内核集成测试：
//   - 带图 Send → 假 LLM 收到**引用**（base64 只在 llm 适配器内出现）→ 历史与
//     落库都是引用；
//   - 真 llm 适配器 + httptest 假 OpenAI 端点：请求构造瞬间才出现 base64，
//     Load 恢复后的历史再次请求仍能从引用编码。
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/store"
	"github.com/moyunteng/lxcode/internal/tools"
)

// pngB64 是固定 1x1 PNG 的 base64（样本写死，断言可复现）。
const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// TestSendWithImagesPersistsRefs：带图 Send → 假 LLM 收到的 user 消息含引用；
// 内存历史与落库（Load 读回）都是引用，序列化历史不含 base64。
func TestSendWithImagesPersistsRefs(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	fs := &fakeStream{script: [][]llm.StreamEvent{textResult("收到")}}
	s.stream = fs.stream

	refs := []llm.ImageRef{{Path: "s1/a.png", Mime: "image/png"}}
	if err := s.Send("看图", WithImages(refs)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	// 假 LLM 收到的消息带引用（base64 编码发生在 llm 适配器内，不在这条链路上）
	fs.mu.Lock()
	msgs := fs.lastMsgs
	fs.mu.Unlock()
	found := false
	for _, m := range msgs {
		if m.Role == "user" && len(m.Images) == 1 && m.Images[0].Path == "s1/a.png" {
			found = true
		}
	}
	if !found {
		t.Fatalf("假 LLM 应收到带图片引用的 user 消息: %+v", msgs)
	}

	// 内存历史是引用
	h := s.History()
	if len(h.Messages[0].Images) != 1 || h.Messages[0].Images[0].Mime != "image/png" {
		t.Fatalf("内存历史应带图片引用: %+v", h.Messages[0])
	}
	// 落库读回一致
	loaded, err := st.Load(s.SessionID())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded[0].Images) != 1 || loaded[0].Images[0].Path != "s1/a.png" {
		t.Fatalf("落库应带图片引用: %+v", loaded[0])
	}
	// 序列化历史不应出现 base64（本批的硬约束）
	b, _ := json.Marshal(h.Messages)
	if strings.Contains(string(b), "iVBORw0KGgo") {
		t.Fatalf("历史里不应出现 base64: %s", b)
	}
}

// TestSendRejectsTooManyImages：一条消息 >4 张在内核入历史前拒绝（快失败，
// 不开轮、不留半截状态）。
func TestSendRejectsTooManyImages(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)
	s.stream = (&fakeStream{script: [][]llm.StreamEvent{textResult("x")}}).stream
	var refs []llm.ImageRef
	for i := 0; i < llm.MaxImagesPerMessage+1; i++ {
		refs = append(refs, llm.ImageRef{Path: fmt.Sprintf("s1/%d.png", i), Mime: "image/png"})
	}
	if err := s.Send("看图", WithImages(refs)); err == nil {
		t.Fatal("超限图片应在入历史前拒绝")
	}
	if s.Busy() {
		t.Fatal("被拒绝的发送不应开轮")
	}
}

// fakeOpenAIEndpoint 起一个记录请求体的假 OpenAI 端点：按请求形态回包——
// 带 "stream":true 回 SSE（无工具的 ChatAuto 走流式），否则回 JSON completion
// （agent 默认带工具声明，ChatAuto 对 openai+工具走非流式回放）。
func fakeOpenAIEndpoint(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if strings.Contains(string(b), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"收到\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],"+
				"\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"收到"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

// TestSendWithImagesEncodesBase64AtRequestTime：真 llm 适配器链路——带图消息
// 到端点时是 content 数组 + data: base64；历史回放仍是引用；模拟重启（同一库
// 上重新附着）后的再次请求仍能从引用编码出 base64。
func TestSendWithImagesEncodesBase64AtRequestTime(t *testing.T) {
	ts, bodies := fakeOpenAIEndpoint(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// 注入图片读取函数（server 在 AttachSessionStore 里做同一件事）：测试桩
	// 直接回固定 PNG 字节，可精确断言。loader 是包级单例，测完清掉。
	llm.SetImageLoader(func(ref llm.ImageRef) (string, []byte, error) {
		raw, err := base64.StdEncoding.DecodeString(pngB64)
		if err != nil {
			return "", nil, err
		}
		return ref.Mime, raw, nil
	})
	t.Cleanup(func() { llm.SetImageLoader(nil) })

	reg, err := config.Load(filepath.Join(t.TempDir(), "config", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Add(config.ModelConfig{
		ID: "v1", BaseURL: ts.URL, Model: "vision-model", Enabled: true,
		Capabilities: config.Capabilities{Vision: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetRole(config.RoleDefault, "v1"); err != nil {
		t.Fatal(err)
	}
	s := New(reg, tools.New(), nil)
	t.Cleanup(s.Close)
	if err := s.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}

	// 先建会话行（历史为空），再按真实会话 id 落一张附件（走 store 落盘路径）
	if err := s.Send("先打个招呼"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })
	rel, err := st.SaveAttachment(s.SessionID(), "image/png", mustDecodePNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send("看图", WithImages([]llm.ImageRef{{Path: rel, Mime: "image/png"}})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	bs := bodies()
	if len(bs) < 2 {
		t.Fatalf("端点应收到两次请求: %d", len(bs))
	}
	if !strings.Contains(bs[1], `"type":"image_url"`) ||
		!strings.Contains(bs[1], "data:image/png;base64,"+pngB64) {
		t.Fatalf("请求应含 image_url 分片与 base64: %s", bs[1])
	}
	// 图片是 content 数组内的标准字段，但会话簿记纪律不变（多一个未知字段
	// 严格端点就 400——AGENTS.md §5 坑 13）
	for _, forbidden := range []string{"\"seq\":", "\"usage_tokens\":", "\"first_token_ms\":"} {
		if strings.Contains(bs[1], forbidden) {
			t.Fatalf("请求不应带会话簿记 %s: %s", forbidden, bs[1])
		}
	}
	// 内存历史恒存引用
	h := s.History()
	for _, m := range h.Messages {
		for _, im := range m.Images {
			if im.Path != rel {
				t.Fatalf("历史应存落盘引用 %q: %+v", rel, im)
			}
		}
	}

	// 模拟重启：新 Session 附着同一个库（Latest 恢复同一会话），历史里的引用
	// 再次被编码成 base64 发给端点。
	s2 := New(reg, tools.New(), nil)
	t.Cleanup(s2.Close)
	if err := s2.EnablePersistence(st); err != nil {
		t.Fatal(err)
	}
	if s2.SessionID() != s.SessionID() {
		t.Fatalf("应恢复同一会话: %s vs %s", s2.SessionID(), s.SessionID())
	}
	if err := s2.Send("再看看"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s2.Busy() })
	bs = bodies()
	if len(bs) < 3 {
		t.Fatalf("第三次请求应到达端点: %d", len(bs))
	}
	if !strings.Contains(bs[2], "data:image/png;base64,"+pngB64) {
		t.Fatalf("Load 后的请求仍应从引用编码出 base64: %s", bs[2])
	}
}

// mustDecodePNG 解码固定样本（失败 = 样本写坏了，直接 Fatal）。
func mustDecodePNG(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(pngB64)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// 防止 context 导入被误删（fakeStream 签名需要）。
var _ = context.Background
