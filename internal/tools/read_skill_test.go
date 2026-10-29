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
	// 技能目录经 ctx 注入（每会话独立——父/子会话各有自己的白名单）
	ctx := WithSkillSource(context.Background(), func(context.Context) []SkillEntry {
		return []SkillEntry{
			{ID: "gsap", Desc: "GSAP 动效", Body: "# GSAP\n\n入场收尾 clearProps。"},
			{ID: "shadcn", Desc: "组件库工程", Body: "# shadcn/ui\n\n按 registry 分发。"},
		}
	})

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
	// 未注入目录（Agent 没配技能）：兜底说明，不炸
	out := r.Execute(context.Background(), call("read_skill", `{"id":"gsap"}`))
	if !strings.Contains(out, "没有技能目录") {
		t.Fatalf("未挂目录应兜底说明: %q", out)
	}
}

// 技能目录是每会话状态：两个 ctx 各自的目录互不影响（父/子会话不串）。
func TestReadSkillSourceIsPerContext(t *testing.T) {
	r := New()
	parentCtx := WithSkillSource(context.Background(), func(context.Context) []SkillEntry {
		return []SkillEntry{{ID: "parent-skill", Desc: "父", Body: "父会话的技能正文"}}
	})
	childCtx := WithSkillSource(context.Background(), func(context.Context) []SkillEntry {
		return []SkillEntry{{ID: "child-skill", Desc: "子", Body: "子会话的技能正文"}}
	})
	if out := r.Execute(childCtx, call("read_skill", `{"id":"child-skill"}`)); !strings.Contains(out, "子会话的技能正文") {
		t.Fatalf("子上下文应能读自己的技能: %q", out)
	}
	if out := r.Execute(parentCtx, call("read_skill", `{"id":"parent-skill"}`)); !strings.Contains(out, "父会话的技能正文") {
		t.Fatalf("父上下文应能读自己的技能: %q", out)
	}
	// 交叉：子上下文读不到父会话的技能（白名单隔离）
	if out := r.Execute(childCtx, call("read_skill", `{"id":"parent-skill"}`)); !strings.Contains(out, "不在可用列表") {
		t.Fatalf("子上下文不应看到父会话的技能: %q", out)
	}
}
