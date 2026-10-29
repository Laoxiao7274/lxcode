// 流式请求的受限重试（2026-10-10）：网关超时/5xx 不再一碰就断。
// 安全边界只有一条——已向调用方交付过任何事件就不能重试（会重复输出）；
// 交付 0 事件时失败（连接期非 2xx / 首事件前断流）可安全重试。
// 429 读 Retry-After（秒数或 HTTP 日期，钳 10s）；ctx 取消与 4xx 不重试。
package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 缩短退避：流式与非流式共用 retryBackoff，测试改小避免真等 3 秒。
func shrinkBackoff(t *testing.T) {
	t.Helper()
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryBackoff = old })
}

// 流式连接期 503 → 前两次 503、第三次成功：后端收到 3 次请求，
// 调用方收到完整流（重试对上层透明）。
func TestChatStreamRetriesOnConnect5xx(t *testing.T) {
	shrinkBackoff(t)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("gateway timeout"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"完整"}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, srv.URL, FormatOpenAI)

	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("连接期 503 应被重试吸收: %v", err)
	}
	var text strings.Builder
	var done *ChatResult
	for ev := range ch {
		switch ev.Type {
		case EventText:
			text.WriteString(ev.TextDelta)
		case EventDone:
			done = ev.Result
		case EventError:
			t.Fatalf("重试后不应再有错误: %v", ev.Err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("后端应收到 3 次请求, got %d", calls.Load())
	}
	if text.String() != "完整" || done == nil || done.FinishReason != "stop" {
		t.Fatalf("重试后应收到完整流: %q %+v", text.String(), done)
	}
}

// 已吐 2 个 delta 后断流 → 不重试、错误原样冒泡（partial 语义不变——
// Result 携带已生成的部分内容）。
func TestChatStreamNoRetryAfterDelivery(t *testing.T) {
	shrinkBackoff(t)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"第一"}}]}`)
		fl.Flush()
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"段"}}]}`)
		fl.Flush()
		// 已交付两个 delta 后掐断连接（不写 [DONE] 直接挂断）
		conn, ok := w.(http.Hijacker)
		if ok && conn != nil {
			c, buf, _ := conn.Hijack()
			_ = buf.Flush()
			c.Close()
		}
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, srv.URL, FormatOpenAI)

	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var sawErr bool
	var partial *ChatResult
	for ev := range ch {
		switch ev.Type {
		case EventText:
			text.WriteString(ev.TextDelta)
		case EventError:
			sawErr = true
			partial = ev.Result
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("已交付事件后不能重试（会重复输出）, 后端收到 %d 次", calls.Load())
	}
	if !sawErr {
		t.Fatal("断流应以 error 事件收尾")
	}
	if text.String() != "第一段" || partial == nil || partial.Message.Content != "第一段" {
		t.Fatalf("partial 语义不变（已生成的部分保留）: %q %+v", text.String(), partial)
	}
}

// 首事件前断流（0 delta）→ 可重试：第一次掐断、第二次完整。
func TestChatStreamRetriesBeforeFirstEvent(t *testing.T) {
	shrinkBackoff(t)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			// 首事件前掐断
			if conn, ok := w.(http.Hijacker); ok && conn != nil {
				c, buf, _ := conn.Hijack()
				_ = buf.Flush()
				c.Close()
			}
			return
		}
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"重试成功"}}]}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, srv.URL, FormatOpenAI)

	ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for ev := range ch {
		if ev.Type == EventText {
			text.WriteString(ev.TextDelta)
		}
		if ev.Type == EventError {
			t.Fatalf("零交付断流应重试成功: %v", ev.Err)
		}
	}
	if calls.Load() != 2 || text.String() != "重试成功" {
		t.Fatalf("应重试到成功: calls=%d text=%q", calls.Load(), text.String())
	}
}

// ctx 取消不重试：取消后请求失败直接返回（后端一次都收不到）。
func TestChatStreamNoRetryOnCanceledCtx(t *testing.T) {
	shrinkBackoff(t)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, srv.URL, FormatOpenAI)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消
	_, err := c.ChatStream(ctx, []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("已取消的 ctx 应直接报错")
	}
	if calls.Load() != 0 {
		t.Fatalf("取消不重试（后端不应收到请求）, got %d", calls.Load())
	}
	if ctx.Err() == nil {
		t.Fatal("应保留 ctx 取消语义")
	}
}

// 429 带 Retry-After → 非流式也重试（等待用 Retry-After，钳 10s）。
// 测试用 Retry-After: 0（可解析但为 0 → 回落默认退避，配合缩短的退避表快速跑完）。
func TestChatRetriesOn429(t *testing.T) {
	shrinkBackoff(t)
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)
	c := mustClient(t, srv.URL, FormatOpenAI)

	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "x"}})
	if err != nil || res.Message.Content != "ok" {
		t.Fatalf("429 应重试: %v %+v", err, res)
	}
	if calls.Load() != 2 {
		t.Fatalf("应重试一次, got %d", calls.Load())
	}
}

// Retry-After 解析：秒数 / HTTP 日期 / 垃圾值 / 负数；钳制上限 10s。
func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("3"); d != 3*time.Second {
		t.Fatalf("秒数解析不符: %v", d)
	}
	if d := parseRetryAfter(" 5 "); d != 5*time.Second {
		t.Fatalf("带空格的秒数应容忍: %v", d)
	}
	future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(future); d <= 0 || d > 3*time.Second {
		t.Fatalf("HTTP 日期解析不符: %v", d)
	}
	if d := parseRetryAfter("garbage"); d != 0 {
		t.Fatalf("解析失败应回落 0: %v", d)
	}
	if d := parseRetryAfter("-1"); d != 0 {
		t.Fatalf("负数应回落 0: %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("缺席应回落 0: %v", d)
	}
	// 钳制：超过 10s 一律 10s
	api := newAPIError(429, "busy", http.Header{"Retry-After": []string{"3600"}})
	if d := retryDelay(api, 0); d != maxRetryAfter {
		t.Fatalf("Retry-After 应钳到 10s: %v", d)
	}
	if api.RetryAfter != time.Hour {
		t.Fatalf("APIError 上保留原始解析值（钳制在 retryDelay 做）: %v", api.RetryAfter)
	}
	// 无 Retry-After 的 429：回落默认退避表（重试仍发生）
	api = newAPIError(429, "busy", nil)
	if d := retryDelay(api, 1); d != retryBackoff[1] {
		t.Fatalf("无 Retry-After 应回落默认退避: %v", d)
	}
}
