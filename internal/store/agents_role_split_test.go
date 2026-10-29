// 职责分工钉子（2026-12 用户拍板）：**代码 Agent 只写代码，不跑测试**——
// 测试与验证归测试 Agent。
//
// 为什么值得钉住：coder 的 Desc 会**逐字进主 Agent 的可委派名单**（compose.go ④），
// 写着「跑构建测试」就等于告诉主 Agent「验证任务也派给 coder」；而 Prompt 是
// coder 自己的行为约束，写着「构建测试通过才算完成」它就会去跑测试。两处漏一处，
// 分工都会当场破功。所以两处都要断言，并反向钉住「测试 Agent 接手验收」这一半
// （只删不改 = 测试没人负责）。
package store

import (
	"strings"
	"testing"
)

// seedDescPrompt 取种子 Agent 的描述与提示词。
func seedDescPrompt(t *testing.T, id string) (desc, prompt string) {
	t.Helper()
	a := seedAgentOf(id)
	if a.ID == "" {
		t.Fatalf("种子里没有 %s", id)
	}
	return a.Desc, a.Prompt
}

// TestCoderSeedDoesNotOwnTesting：代码 Agent 的描述与提示词都不把测试写成自己的活。
func TestCoderSeedDoesNotOwnTesting(t *testing.T) {
	desc, prompt := seedDescPrompt(t, "coder")
	// Desc 是主 Agent 的选人信号：不能出现「跑构建测试」这类职责声明
	for _, banned := range []string{"跑构建测试", "构建测试通过才算完成", "跑构建与测试"} {
		if strings.Contains(desc, banned) {
			t.Fatalf("代码 Agent 的描述不该声明测试职责（含 %q）: %s", banned, desc)
		}
	}
	// 提示词里「跑构建与测试」只允许出现在「是测试 Agent 的职责」这层归属说明里
	// （它是分工说明，不是 coder 自己的职责声明）
	if strings.Contains(prompt, "构建测试通过才算完成") {
		t.Fatalf("代码 Agent 的提示词不该以「构建测试通过」为完成标准: %s", prompt)
	}
	// 「跑测试」只允许出现在否定句里（「不跑测试」）；肯定句 = 又把测试写回自己的活
	for _, text := range []string{desc, prompt} {
		for i := 0; ; {
			j := strings.Index(text[i:], "跑测试")
			if j < 0 {
				break
			}
			pos := i + j
			prefix := text[:pos]
			if !strings.HasSuffix(strings.TrimRight(prefix, "*"), "不") {
				t.Fatalf("「跑测试」只该以否定形式出现（不跑测试）: %s", text)
			}
			i = pos + len("跑测试")
		}
	}
	// 正面：明确不跑测试 + 保留编译底线
	if !strings.Contains(prompt, "不跑测试") {
		t.Fatalf("代码 Agent 的提示词应明说「不跑测试」: %s", prompt)
	}
	if !strings.Contains(prompt, "编译") {
		t.Fatalf("代码 Agent 的提示词应保留编译自检底线: %s", prompt)
	}
	if !strings.Contains(desc, "测试交给测试 Agent") {
		t.Fatalf("代码 Agent 的描述应把测试指向测试 Agent（选人信号）: %s", desc)
	}
	// 推送纪律不能被这次改写吃掉
	if !strings.Contains(prompt, "git push") {
		t.Fatalf("代码 Agent 的提示词应保留推送纪律: %s", prompt)
	}
}

// TestTesterSeedOwnsVerification：测试 Agent 明确接手「代码 Agent 交回的改动」的验收。
func TestTesterSeedOwnsVerification(t *testing.T) {
	desc, prompt := seedDescPrompt(t, "tester")
	if !strings.Contains(desc, "验收") {
		t.Fatalf("测试 Agent 的描述应点明验收职责: %s", desc)
	}
	if !strings.Contains(prompt, "验收") || !strings.Contains(prompt, "代码 Agent") {
		t.Fatalf("测试 Agent 的提示词应写明「代码 Agent 交回的改动由你验收」: %s", prompt)
	}
	// 原有纪律不能丢：不许为绿灯弱化断言
	if !strings.Contains(prompt, "弱化断言") {
		t.Fatalf("测试 Agent 的提示词应保留「不许为绿灯弱化断言」: %s", prompt)
	}
}
