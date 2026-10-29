// ask_user 工具层的单元契约：参数校验、注入缺失时的自解释报错、
// 跳过（ok=false）与回答（ok=true）的回填措辞。
package tools

import (
	"context"
	"strings"
	"testing"
)

// TestAskUserExec 注入假提问通道，验证三条回填路径。
func TestAskUserExec(t *testing.T) {
	def := askUserDef()
	if def.Name != AskUserToolName || def.Risk != RiskLow || def.Mutates {
		t.Fatalf("ask_user 应是低危非变更工具: %+v", def)
	}

	// 用户回答 → 「用户回答：<text>」
	out, err := def.Exec(WithAsker(context.Background(), func(ctx context.Context, question string, options []string) (string, bool) {
		if question != "保哪边?" || len(options) != 2 {
			t.Fatalf("提问参数应原样传给会话侧: %q %v", question, options)
		}
		return "选 A", true
	}), []byte(`{"question":"保哪边?","options":["选 A","选 B"]}`))
	if err != nil || !strings.Contains(out, "用户回答：选 A") {
		t.Fatalf("回答应回填给模型: %q %v", out, err)
	}

	// 用户跳过 → 「用户没有回答」语义，且不是错误（模型该自己拿主意）
	out, err = def.Exec(WithAsker(context.Background(), func(context.Context, string, []string) (string, bool) {
		return "", false
	}), []byte(`{"question":"保哪边?"}`))
	if err != nil || !strings.Contains(out, "用户没有回答") {
		t.Fatalf("跳过应回填「用户没有回答」语义且不报错: %q %v", out, err)
	}

	// 未注入提问通道 → 自解释报错（模型可换做法，而不是静默失败）
	if _, err := def.Exec(context.Background(), []byte(`{"question":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "提问通道") {
		t.Fatalf("未接线时应自解释报错: %v", err)
	}

	// 参数硬校验：空问题 / 超量选项
	if _, err := def.Exec(context.Background(), []byte(`{"question":"  "}`)); err == nil {
		t.Fatal("空问题应报错")
	}
	tooMany := `{"question":"x","options":[`
	for i := 0; i < 7; i++ {
		if i > 0 {
			tooMany += ","
		}
		tooMany += `"o"`
	}
	tooMany += `]}`
	if _, err := def.Exec(context.Background(), []byte(tooMany)); err == nil ||
		!strings.Contains(err.Error(), "最多 6 个") {
		t.Fatalf("超过 6 个选项应报错: %v", err)
	}
}
