// 工具调用参数的写边界不变量（2026-09-23 线上事故）：模型输出被 max_tokens 截断时
// arguments 会是半截 JSON；这种消息一旦进历史，之后**每一次**请求都会在端点适配器
// 组装时被拒绝（anthropic 侧是硬校验），整个会话再也发不出请求。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
	"github.com/moyunteng/lxcode/internal/llm"
)

// TestSanitizeToolCallArgs 清洗表：合法原样、可修则修（保留字段）、修不动降级空对象。
func TestSanitizeToolCallArgs(t *testing.T) {
	cases := []struct {
		name, in, wantKey, wantVal string
	}{
		{"合法 JSON 原样", `{"path":"a.txt"}`, "path", "a.txt"},
		{"对象未闭合", `{"path":"a.txt"`, "path", "a.txt"},
		{"字符串未闭合", `{"path":"a.txt`, "path", "a.txt"},
		{"修不动降级空对象", `完全不是 JSON`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := []llm.ToolCall{{ID: "c1"}}
			calls[0].Function.Name = "read_file"
			calls[0].Function.Arguments = tc.in
			sanitizeToolCallArgs(calls, "单测")
			got := calls[0].Function.Arguments
			if !json.Valid([]byte(got)) {
				t.Fatalf("清洗后必须是合法 JSON: %q → %q", tc.in, got)
			}
			if tc.wantKey == "" {
				if got != "{}" {
					t.Fatalf("修不动时应降级成空对象，得到 %q", got)
				}
				return
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatal(err)
			}
			if m[tc.wantKey] != tc.wantVal {
				t.Fatalf("修复应保留原字段: %q → %q (%v)", tc.in, got, m)
			}
		})
	}
}

// TestSanitizeToolCallArgsLeavesEmptyAlone：没写参数的调用是合法的（有无参工具）。
func TestSanitizeToolCallArgsLeavesEmptyAlone(t *testing.T) {
	calls := []llm.ToolCall{{ID: "c1"}}
	calls[0].Function.Name = "todo"
	sanitizeToolCallArgs(calls, "单测")
	if calls[0].Function.Arguments != "" {
		t.Fatalf("空参数不该被改写: %q", calls[0].Function.Arguments)
	}
}

// TestStreamFailureNeverLetsBadArgsIntoHistory：流式失败时保留的那条 partial
// assistant 消息（已声明的工具调用）在进历史前必须把半截 JSON 参数修好。
func TestStreamFailureNeverLetsBadArgsIntoHistory(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)

	var tc llm.ToolCall
	tc.ID, tc.Function.Name = "call-bad", "read_file"
	tc.Function.Arguments = `{"path":"a.txt"` // 半截 JSON（对象没闭合）
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		ch := make(chan llm.StreamEvent, 2)
		ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
		ch <- llm.StreamEvent{Type: llm.EventError, Err: errors.New("端点 500"),
			Result: &llm.ChatResult{Message: llm.Message{
				Role: "assistant", Content: "我先看一下", ToolCalls: []llm.ToolCall{tc},
			}}}
		close(ch)
		return ch, nil
	}
	if err := s.Send("读文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msgs := s.History().Messages
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if !json.Valid([]byte(c.Function.Arguments)) {
				t.Fatalf("非法 JSON 参数进了历史（会话会永久发不出请求）: %q", c.Function.Arguments)
			}
		}
	}
	if p := AnalyzeToolPairing(msgs); len(p.Unpaired) != 0 {
		t.Fatalf("失败路径仍要补齐配对: %v", p.Unpaired)
	}
	if got := toolResultsByID(msgs); !strings.Contains(got["call-bad"], "失败") {
		t.Fatalf("失败路径的合成结果应说明生成失败: %+v", got)
	}
}

// TestNormalTurnRepairsArgsBeforeExecute：正常轮次里参数被截断时，**执行用的**与
// **落历史的**必须是同一份修好的参数（执行 / 落库 / 回放三者一致）。
func TestNormalTurnRepairsArgsBeforeExecute(t *testing.T) {
	s, reg := newTestSession(t)
	bindDefault(t, reg)

	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("你好，世界\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Windows 的反斜杠会造出 `\U` 这类非法转义，这里用正斜杠（Go 在 Windows 上也认）
	slashPath := filepath.ToSlash(path)

	var round int
	s.stream = func(ctx context.Context, m config.ModelConfig, msgs []llm.Message, opts []llm.Option) (<-chan llm.StreamEvent, error) {
		round++
		ch := make(chan llm.StreamEvent, 2)
		defer close(ch)
		if round > 1 {
			ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
				Message: llm.Message{Role: "assistant", Content: "读完了"}, FinishReason: llm.FinishStop}}
			return ch, nil
		}
		var tc llm.ToolCall
		tc.ID, tc.Function.Name = "call-1", "read_file"
		tc.Function.Arguments = `{"path":"` + slashPath + `"` // 少右括号 = 非法 JSON
		ch <- llm.StreamEvent{Type: llm.EventToolCall, ToolCall: tc}
		ch <- llm.StreamEvent{Type: llm.EventDone, Result: &llm.ChatResult{
			Message: llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}}, FinishReason: llm.FinishToolCalls}}
		return ch, nil
	}
	if err := s.Send("读文件"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !s.Busy() })

	msgs := s.History().Messages
	if got := toolResultsByID(msgs); !strings.Contains(got["call-1"], "你好，世界") {
		t.Fatalf("应使用修好的参数真正执行（读到文件内容）: %q", got["call-1"])
	}
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if !json.Valid([]byte(c.Function.Arguments)) {
				t.Fatalf("落历史的参数必须合法: %q", c.Function.Arguments)
			}
			if !strings.Contains(c.Function.Arguments, slashPath) {
				t.Fatalf("落历史的参数应是修好的那一份: %q", c.Function.Arguments)
			}
		}
	}
}
