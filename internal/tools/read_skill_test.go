// read_skill 的执行测试（渐进披露的取数路径）：按 id 取正文、
// 清单外拒绝并列出可用、未挂目录的兜底。
package tools

import (
	"context"
	"strings"
	"testing"
)

func TestReadSkillExec(t *testing.T) {
	r := New()
	r.SetSkillSource(func(context.Context) []SkillEntry {
		return []SkillEntry{
			{ID: "gsap", Desc: "GSAP 动效", Body: "# GSAP\n\n入场收尾 clearProps。"},
			{ID: "shadcn", Desc: "组件库工程", Body: "# shadcn/ui\n\n按 registry 分发。"},
		}
	})
	ctx := context.Background()

	// 按 id 取正文
	out := r.Execute(ctx, call("read_skill", `{"id":"gsap"}`))
	if !strings.Contains(out, "clearProps") {
		t.Fatalf("应返回技能正文: %q", out)
	}

	// 清单外拒绝并列出可用（自解释——模型可立刻纠正）
	out = r.Execute(ctx, call("read_skill", `{"id":"nobody"}`))
	if !strings.Contains(out, "不在可用列表") || !strings.Contains(out, "gsap") {
		t.Fatalf("应列出可用技能: %q", out)
	}
}

func TestReadSkillNoSource(t *testing.T) {
	r := New()
	// 未挂目录（Agent 没配技能）：兜底说明，不炸（清掉同包其它测试
	// 可能注入的目录——全局注入态是包级共享的）
	r.SetSkillSource(nil)
	out := r.Execute(context.Background(), call("read_skill", `{"id":"gsap"}`))
	if !strings.Contains(out, "没有技能目录") {
		t.Fatalf("未挂目录应兜底说明: %q", out)
	}
}
