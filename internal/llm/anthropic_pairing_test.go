// Anthropic wire 的两条配对硬约束（2026-09-23 由真实 400 换来）：
//  1. 一条 assistant 里的**全部** tool_use 必须在**紧接着的下一条消息**里拿到
//     tool_result——并行工具调用（主 Agent 一次派两个子任务）的轮次必踩这条；
//  2. 结果确实缺了也要补齐合成结果，不能让整轮请求（乃至整个会话）发不出去。
package llm

import (
	"strings"
	"testing"
)

// twoCalls 造一条"一次声明两个工具调用"的 assistant 消息（并行派发的形态）。
func twoCalls() Message {
	var a, b ToolCall
	a.ID, a.Type, a.Function.Name, a.Function.Arguments = "call-a", "function", "agent_dispatch", `{"agent":"researcher"}`
	b.ID, b.Type, b.Function.Name, b.Function.Arguments = "call-b", "function", "agent_dispatch", `{"agent":"coder"}`
	return Message{Role: "assistant", Content: "我并行派两个子任务", ToolCalls: []ToolCall{a, b}}
}

// TestAnthropicMergesParallelToolResults：并行调用的两条结果必须合进**同一条**
// user 消息（分开两条 → 严格端点报 `tool_use ids were found without tool_result
// blocks immediately after`，用户会话因此整轮失败）。
func TestAnthropicMergesParallelToolResults(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "看一下项目"},
		twoCalls(),
		{Role: "tool", ToolCallID: "call-a", Content: "报告 A"},
		{Role: "tool", ToolCallID: "call-b", Content: "报告 B"},
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("应是 user / assistant / user(两条结果) 三条，得到 %d 条: %+v", len(req.Messages), req.Messages)
	}
	got := req.Messages[2]
	if got.Role != "user" || len(got.Content) != 2 {
		t.Fatalf("两条并行结果应合进同一条 user 消息的两个块: %+v", got)
	}
	for i, wantID := range []string{"call-a", "call-b"} {
		if got.Content[i].Type != "tool_result" || got.Content[i].ToolUseID != wantID {
			t.Fatalf("第 %d 个块应是 %s 的结果，得到 %+v", i, wantID, got.Content[i])
		}
	}
}

// TestAnthropicSingleToolResultUnchanged：单调用单结果（最常见形态）仍是一条
// user 消息一个块——合并逻辑不该改变它的形状。
func TestAnthropicSingleToolResultUnchanged(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{badCall("bash", `{"command":"ls"}`)}},
		{Role: "tool", ToolCallID: "call-bad", Content: "输出"},
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 || len(req.Messages[1].Content) != 1 {
		t.Fatalf("单结果形态不该变: %+v", req.Messages)
	}
}

// TestAnthropicBackfillsMissingToolResult：历史里少了一条结果时补合成结果
// （否则整轮 400，会话此后再也发不出请求——今天的真实事故）。
func TestAnthropicBackfillsMissingToolResult(t *testing.T) {
	msgs := []Message{
		twoCalls(),
		{Role: "tool", ToolCallID: "call-a", Content: "报告 A"}, // call-b 的结果丢了
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
		t.Fatalf("两条 tool_use 应各有一条 tool_result（缺的补合成），得到 %d 条: %+v", len(results), results)
	}
	// 原有的那条不许被改写，补的那条要自解释
	if results[0].ToolUseID != "call-a" || results[0].Content != "报告 A" {
		t.Fatalf("原有结果被改动了: %+v", results[0])
	}
	if results[1].ToolUseID != "call-b" || !strings.Contains(results[1].Content, "残缺") {
		t.Fatalf("补的合成结果应指向缺失的调用并自解释: %+v", results[1])
	}
}

// TestAnthropicDoesNotInventOnLooseOrder：结果与调用不相邻（历史顺序已乱）时
// 不凭空插消息——只让端点报错，不猜语义。
func TestAnthropicDoesNotInventOnLooseOrder(t *testing.T) {
	msgs := []Message{
		twoCalls(),
		{Role: "user", Content: "接着聊别的"}, // 下一条不是工具结果
	}
	req, err := convertToAnthropic("m", msgs, requestOpts{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("不该插入新消息: %+v", req.Messages)
	}
	if len(req.Messages[1].Content) != 1 || req.Messages[1].Content[0].Type != "text" {
		t.Fatalf("不该往用户正文里塞工具结果: %+v", req.Messages[1])
	}
}
