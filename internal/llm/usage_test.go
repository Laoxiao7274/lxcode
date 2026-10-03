// 用量分桶的解析口径（2026-09-30，对齐 DSH token-meter 的四桶）。
//
// 为什么单独立一个文件：这套语义**跨两条 wire 格式、四条解析路径**（openai/anthropic ×
// 非流式/流式），而四处的坑各不相同——OpenAI 的 prompt_tokens **含**缓存，anthropic 的
// input_tokens **不含**。这些差异只有摆在一起才看得出，散在各自的功能测试里必然漏掉一条
// （漏掉的表现是"同一条端点在流式/非流式下给出不一样的数字"）。
package llm

import (
	"context"
	"testing"
)

// TestOpenAIUsageCacheBuckets：OpenAI 兼容端点的 prompt_tokens 含缓存命中部分，
// 未缓存输入要减掉它（deepseek 用扁平的 prompt_cache_hit_tokens，OpenAI 官方用嵌套的
// prompt_tokens_details.cached_tokens——两个都认）。
func TestOpenAIUsageCacheBuckets(t *testing.T) {
	cases := []struct {
		name                                     string
		resp                                     string
		in, cacheRead, cacheWrite, output, total int
	}{
		{
			name: "嵌套 cached_tokens",
			resp: `{"choices":[{"message":{"content":"好"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,
				"prompt_tokens_details":{"cached_tokens":800}}}`,
			in: 200, cacheRead: 800, cacheWrite: 0, output: 20, total: 1000,
		},
		{
			name: "deepseek 扁平字段",
			resp: `{"choices":[{"message":{"content":"好"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,
				"prompt_cache_hit_tokens":750}}`,
			in: 250, cacheRead: 750, cacheWrite: 0, output: 20, total: 1000,
		},
		{
			name: "端点不报缓存",
			resp: `{"choices":[{"message":{"content":"好"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020}}`,
			in: 1000, cacheRead: 0, cacheWrite: 0, output: 20, total: 1000,
		},
		{
			name: "脏数据 cached > prompt（钳到 0）",
			resp: `{"choices":[{"message":{"content":"好"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,
				"prompt_tokens_details":{"cached_tokens":300}}}`,
			in: 0, cacheRead: 100, cacheWrite: 0, output: 5, total: 100,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, FormatOpenAI, tc.resp, 200)
			res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "嗨"}})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if res.InputTokens != tc.in || res.CacheReadTokens != tc.cacheRead ||
				res.CacheWriteTokens != tc.cacheWrite || res.UsageTokens != tc.output {
				t.Fatalf("分桶不符: got in=%d read=%d write=%d out=%d, want in=%d read=%d write=%d out=%d",
					res.InputTokens, res.CacheReadTokens, res.CacheWriteTokens, res.UsageTokens,
					tc.in, tc.cacheRead, tc.cacheWrite, tc.output)
			}
			// prompt 侧总量 = 三桶之和（上下文压力用它，漏掉缓存两桶会把真实上下文报小）
			if res.PromptTokens != tc.total {
				t.Fatalf("prompt 总量不符: got %d, want %d", res.PromptTokens, tc.total)
			}
		})
	}
}

// TestAnthropicUsageCacheBuckets：anthropic 的 input_tokens **不含**缓存那两项，
// prompt 侧总量 = 三者之和。
func TestAnthropicUsageCacheBuckets(t *testing.T) {
	resp := `{"content":[{"type":"text","text":"好"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":200,"output_tokens":30,
		"cache_read_input_tokens":700,"cache_creation_input_tokens":100}}`
	c, _ := newTestClient(t, FormatAnthropic, resp, 200)
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "嗨"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.InputTokens != 200 || res.CacheReadTokens != 700 || res.CacheWriteTokens != 100 {
		t.Fatalf("分桶不符: %+v", res)
	}
	if res.UsageTokens != 30 {
		t.Fatalf("输出 token 不符: %d", res.UsageTokens)
	}
	if res.PromptTokens != 1000 { // 200 + 700 + 100
		t.Fatalf("prompt 总量应含缓存两桶: %d", res.PromptTokens)
	}
}

// TestUsageBucketsStreamPaths：流式路径必须与非流式给出**同一套**分桶
// （两条路径各写一遍是本仓库反复踩的坑：同一条端点在流式/非流式下数字不一样）。
func TestUsageBucketsStreamPaths(t *testing.T) {
	t.Run("openai 流式", func(t *testing.T) {
		lines := []string{
			`data: {"choices":[{"delta":{"content":"好"}}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,"prompt_tokens_details":{"cached_tokens":800}}}`,
			`data: [DONE]`,
		}
		srv := sseServer(t, lines)
		c := mustClient(t, srv.URL, FormatOpenAI)
		ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("ChatStream: %v", err)
		}
		var done *ChatResult
		for _, ev := range drain(ch) {
			if ev.Type == EventDone {
				done = ev.Result
			}
		}
		if done == nil {
			t.Fatal("缺少 done 事件")
		}
		if done.InputTokens != 200 || done.CacheReadTokens != 800 || done.UsageTokens != 20 || done.PromptTokens != 1000 {
			t.Fatalf("流式分桶与非流式不一致: %+v", done)
		}
	})

	t.Run("anthropic 流式", func(t *testing.T) {
		lines := []string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":200,"output_tokens":0,"cache_read_input_tokens":700,"cache_creation_input_tokens":100}}}`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"好"}}`,
			`data: {"type":"content_block_stop","index":0}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":30}}`,
			`data: {"type":"message_stop"}`,
		}
		srv := sseServer(t, lines)
		c := mustClient(t, srv.URL, FormatAnthropic)
		ch, err := c.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}})
		if err != nil {
			t.Fatalf("ChatStream: %v", err)
		}
		var done *ChatResult
		for _, ev := range drain(ch) {
			if ev.Type == EventDone {
				done = ev.Result
			}
		}
		if done == nil {
			t.Fatal("缺少 done 事件")
		}
		if done.InputTokens != 200 || done.CacheReadTokens != 700 || done.CacheWriteTokens != 100 {
			t.Fatalf("流式分桶不符: %+v", done)
		}
		if done.UsageTokens != 30 || done.PromptTokens != 1000 {
			t.Fatalf("流式输出/prompt 总量不符: %+v", done)
		}
	})
}

// TestStampUsageCarriesBuckets：用量簿记从结果盖到消息上（会话统计的折叠输入是消息上的
// 这份——落库与会话统计都读它，漏盖一桶就是"统计里那一栏永远是 0"）。
func TestStampUsageCarriesBuckets(t *testing.T) {
	res := &ChatResult{UsageTokens: 30, PromptTokens: 1000, InputTokens: 200, CacheReadTokens: 700, CacheWriteTokens: 100}
	var m Message
	StampUsage(&m, res)
	if m.UsageTokens != 30 || m.InputTokens != 200 || m.CacheReadTokens != 700 || m.CacheWriteTokens != 100 {
		t.Fatalf("簿记未盖全: %+v", m)
	}
}
