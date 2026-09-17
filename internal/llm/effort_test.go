// effort_test.go —— 推理强度档位在两种 wire 格式请求体上的真实落形断言。
// effort 是协议新字段，映射错误只会静默无效（端点忽略未知参数），所以
// 必须钉住请求体形状。
package llm

import (
	"encoding/json"
	"testing"
)

// TestOpenAIEffortPayload：reasoning_effort 的存在性与收敛规则。
func TestOpenAIEffortPayload(t *testing.T) {
	t.Run("不带 effort 不产生字段（非推理模型零影响）", func(t *testing.T) {
		req := buildOpenAIRequest("m", []Message{{Role: "user", Content: "hi"}}, requestOpts{}, false)
		b, _ := json.Marshal(req)
		if string(b) == "" || json.Unmarshal(b, &map[string]any{}) != nil {
			t.Fatal("序列化失败")
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if _, has := m["reasoning_effort"]; has {
			t.Fatalf("未指定 effort 时不应产生 reasoning_effort: %s", b)
		}
	})
	t.Run("四档直传 + 超集值收敛到 high", func(t *testing.T) {
		cases := map[string]string{
			"minimal": "minimal", "low": "low", "medium": "medium", "high": "high",
			"xhigh": "high", "max": "high", // 历史遗留超集值防御性收敛
		}
		for in, want := range cases {
			req := buildOpenAIRequest("m", nil, requestOpts{effort: in}, false)
			if req.ReasoningEffort != want {
				t.Fatalf("effort %q → %q, want %q", in, req.ReasoningEffort, want)
			}
		}
	})
}

// TestAnthropicEffortPayload：thinking budget 映射与 max_tokens 抬高规则。
func TestAnthropicEffortPayload(t *testing.T) {
	t.Run("不带 effort 不产生 thinking 字段", func(t *testing.T) {
		req, err := convertToAnthropic("m", []Message{{Role: "user", Content: "hi"}}, requestOpts{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if req.Thinking != nil {
			t.Fatalf("未指定 effort 时不应产生 thinking: %+v", req.Thinking)
		}
	})
	t.Run("四档 budget 映射", func(t *testing.T) {
		cases := map[string]int{"minimal": 1024, "low": 4096, "medium": 16384, "high": 32768}
		for effort, want := range cases {
			req, err := convertToAnthropic("m", nil, requestOpts{effort: effort}, false)
			if err != nil {
				t.Fatal(err)
			}
			if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens != want {
				t.Fatalf("effort %q → %+v, want budget %d enabled", effort, req.Thinking, want)
			}
			if req.MaxTokens <= req.Thinking.BudgetTokens {
				t.Fatalf("max_tokens(%d) 必须大于 budget(%d)——API 硬约束", req.MaxTokens, req.Thinking.BudgetTokens)
			}
		}
	})
	t.Run("budget 盖过 max_tokens 时自动抬高", func(t *testing.T) {
		// 默认 max_tokens=4096，medium=16384 > 4096 → 应抬高到 16384+4096
		req, err := convertToAnthropic("m", nil, requestOpts{effort: "medium"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if req.MaxTokens != 16384+4096 {
			t.Fatalf("max_tokens 应抬高到 budget+4096: got %d", req.MaxTokens)
		}
		// 用户显式 max_tokens 已高于 budget → 不动
		req2, err := convertToAnthropic("m", nil, requestOpts{effort: "low", maxTokens: 32000}, false)
		if err != nil {
			t.Fatal(err)
		}
		if req2.MaxTokens != 32000 {
			t.Fatalf("显式 max_tokens 高于 budget 时不应改动: got %d", req2.MaxTokens)
		}
	})
}
