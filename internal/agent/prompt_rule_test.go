// 全局守则的职责分工钉子（2026-12 用户拍板）：写代码的那条守则把**编译**当底线，
// 不再要求「跑测试」——测试与验证归测试 Agent。
//
// 为什么在守则层而不是只改 coder 的 Prompt：守则是所有 Agent 共用的，写着
// 「改完能验证就验证（跑测试/编译）」就等于要求每个 Agent 都去跑测试，与
// 「coder 只写代码」的分工直接冲突（子 Agent 自己读到会自相矛盾）。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
)

func TestEditRuleKeepsCompileAsFloor(t *testing.T) {
	footer := systemPromptFooter(config.TodoPromptStrict)
	var rule string
	for _, line := range strings.Split(footer, "\n") {
		if strings.Contains(line, "修改代码") {
			rule = line
		}
	}
	if rule == "" {
		t.Fatal("守则里应有「修改代码」那一条")
	}
	if strings.Contains(rule, "跑测试/编译") {
		t.Fatalf("守则不该再要求「跑测试」（验证按职责分工交给测试 Agent）: %s", rule)
	}
	if !strings.Contains(rule, "编译") {
		t.Fatalf("守则应保留编译底线（连编译都过不了的代码不该流到下游）: %s", rule)
	}
	// 也不能反过来变成「不用管验证」：编辑纪律（read_file 再 edit）必须还在
	if !strings.Contains(rule, "read_file") || !strings.Contains(rule, "edit") {
		t.Fatalf("编辑纪律不该被这次改写丢掉: %s", rule)
	}
}
