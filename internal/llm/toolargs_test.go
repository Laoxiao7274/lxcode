// 工具调用参数的**读侧兜底**：历史里的坏参数不能让请求发不出去。
//
// 事故（2026-09-23）：模型输出被 max_tokens 截断，半截 JSON 的参数进了历史；
// anthropic 适配器组装请求时硬校验参数，之后**每一次**请求都失败——用户连发三条
// 消息全部无响应，只能新开会话。这些测试钉住"读侧不再有这条路"。
package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// badCall 造一个参数为（可能非法的）原样的工具调用。
func badCall(name, args string) ToolCall {
	var tc ToolCall
	tc.ID, tc.Type = "call-bad", "function"
	tc.Function.Name, tc.Function.Arguments = name, args
	return tc
}

// TestRepairToolArgsForWire：合法原样（零改动）、可修则修、修不动发空对象。
func TestRepairToolArgsForWire(t *testing.T) {
	valid := `{"path":"a.txt"}`
	if got := repairToolArgsForWire(valid); got != valid {
		t.Fatalf("合法参数应原样返回: %q → %q", valid, got)
	}
	if got := repairToolArgsForWire(""); got != "" {
		t.Fatalf("空参数应原样返回: %q", got)
	}
	bad := `{"path":"a.txt"` // 对象没闭合
	if got := repairToolArgsForWire(bad); !json.Valid([]byte(got)) || !strings.Contains(got, "a.txt") {
		t.Fatalf("可修复的参数应被保守补全: %q → %q", bad, got)
	}
	// 非法转义（Windows 路径写了单反斜杠）+ 未闭合：保守修复修不动 → 空对象
	if got := repairToolArgsForWire(`{"path":"C:\Users\xzy`); got != "{}" {
		t.Fatalf("修不动时应发空对象: %q", got)
	}
}

// TestAnthropicRequestSurvivesBadHistoryArgs：历史里的坏参数不能让**组装**失败
// （旧实现在这里返回 `工具 X 的 arguments 不是合法 JSON`，会话永久锁死）。
func TestAnthropicRequestSurvivesBadHistoryArgs(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "派活"},
		// 真实事故形态：截断 + 非法转义（C:\Users 的 \U）
		{Role: "assistant", ToolCalls: []ToolCall{badCall("agent.dispatch", `{"agent":"coder","task":"在 C:\Users\xzy 的「主机"`)}},
		{Role: "tool", ToolCallID: "call-bad", Content: "本轮生成失败，该调用未执行。"},
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatalf("坏参数不该让请求组装失败: %v", err)
	}
	blocks := 0
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type != "tool_use" {
				continue
			}
			blocks++
			if !json.Valid(b.Input) {
				t.Fatalf("tool_use 的 input 必须是合法 JSON，得到 %q", string(b.Input))
			}
		}
	}
	if blocks == 0 {
		t.Fatal("应保留 tool_use 块（不能因为参数坏就把调用整个丢掉——那样 tool 结果会变孤儿）")
	}
}

// TestAnthropicRequestRepairsRepairableHistoryArgs：可修的坏参数应尽量保留内容。
func TestAnthropicRequestRepairsRepairableHistoryArgs(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{badCall("bash", `{"command":"echo repaired"`)}},
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == "tool_use" {
				found = string(b.Input)
			}
		}
	}
	if !strings.Contains(found, "echo repaired") {
		t.Fatalf("可修复的参数应保留内容，得到 %q", found)
	}
}

// TestOpenAIRequestSanitizesWithoutMutatingHistory：请求体里的参数被修好，而调用方
// 持有的历史切片**不被就地改写**（内存历史是会话的共享状态）。
func TestOpenAIRequestSanitizesWithoutMutatingHistory(t *testing.T) {
	bad := `{"command":"echo hi"`
	msgs := []Message{{Role: "assistant", ToolCalls: []ToolCall{badCall("bash", bad)}}}
	req := buildOpenAIRequest("m", msgs, requestOpts{}, false)

	got := req.Messages[0].ToolCalls[0].Function.Arguments
	if !json.Valid([]byte(got)) || !strings.Contains(got, "echo hi") {
		t.Fatalf("请求体里的参数应被修好: %q", got)
	}
	if msgs[0].ToolCalls[0].Function.Arguments != bad {
		t.Fatalf("不该就地改写调用方持有的历史: %q", msgs[0].ToolCalls[0].Function.Arguments)
	}
}
