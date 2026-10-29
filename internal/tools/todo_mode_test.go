// todo 工具描述随 todo-first 档位变化：off 档不得含任何指令性/催促性措辞，
// strict 档逐字节保留原描述（有钉住断言），工具本身在 off 档仍注册、仍可写回。
package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/config"
)

// strictTodoDescriptionPinned 是 strict 档的工具描述原文——逐字节钉住，
// 改措辞必须是有意为之（同步改这里）。
const strictTodoDescriptionPinned = "维护当前任务清单（全量替换式写入）。每轮开始先审视清单：新任务先用本工具出" +
	"步骤清单（3~7 条、动词开头、可独立验收）再执行——只有两三步也要先建；已有清单先" +
	"对照现状更新，不要另起炉灶。" +
	"每完成一步立即更新状态：把当前项标 done、下一项标 active。清单帮你自己不漏步骤，" +
	"也让用户看到进度。同一时刻最多一项 active。"

// todoDescriptionBannedInOff 是 off 档描述里绝不能出现的指令性/催促性措辞。
var todoDescriptionBannedInOff = []string{
	"每轮开始先审视",
	"只有两三步也要先建",
	"每完成一步立即更新",
	"不要另起炉灶",
	"不漏步骤",
	"再执行",
}

func TestTodoDescriptionPinned(t *testing.T) {
	// strict（含空串缺省）逐字节不变
	for _, mode := range []string{"", config.TodoPromptStrict, config.TodoPromptNormal} {
		if got := todoDescriptionFor(mode); got != strictTodoDescriptionPinned {
			t.Fatalf("档位 %q 的 todo 描述必须与 strict 逐字节一致，got %q", mode, got)
		}
	}
	// 注册表里的 Def 也是 strict 原文（Execute/其他读 Def 的路径不受档位影响）
	d, ok := New().Get("todo")
	if !ok {
		t.Fatal("todo 未注册")
	}
	if d.Description != strictTodoDescriptionPinned {
		t.Fatalf("Def.Description 应为 strict 原文，got %q", d.Description)
	}
}

func TestTodoDescriptionOffIsNeutral(t *testing.T) {
	off := todoDescriptionFor(config.TodoPromptOff)
	if !strings.Contains(off, "维护当前任务清单（全量替换式写入）") {
		t.Fatalf("off 档描述应保留中性能力说明，got %q", off)
	}
	for _, ban := range todoDescriptionBannedInOff {
		if strings.Contains(off, ban) {
			t.Fatalf("off 档描述不得含指令性措辞 %q：%q", ban, off)
		}
	}
	// 参数语义仍在（模型要知道怎么调）
	for _, keep := range []string{"pending", "active", "done"} {
		if !strings.Contains(off, keep) {
			t.Fatalf("off 档描述应保留参数语义 %q：%q", keep, off)
		}
	}
}

// wire 声明：LLMToolsForMode 按档位出描述；空档 = 与 LLMTools 逐字节一致。
func TestLLMToolsForModeTodoDescription(t *testing.T) {
	r := New()
	plain := r.LLMTools()
	strict := r.LLMToolsForMode(config.TodoPromptStrict)
	off := r.LLMToolsForMode(config.TodoPromptOff)
	if len(plain) != len(strict) || len(plain) != len(off) {
		t.Fatalf("三份声明的工具数应一致：%d / %d / %d", len(plain), len(strict), len(off))
	}
	// 非 todo 工具在所有档位下描述一致；todo 按档位变
	for i := range plain {
		if plain[i].Name == "todo" {
			if strict[i].Description != strictTodoDescriptionPinned {
				t.Fatalf("strict 档 wire 描述应为原文，got %q", strict[i].Description)
			}
			if off[i].Description != todoDescriptionOff {
				t.Fatalf("off 档 wire 描述应为中性版，got %q", off[i].Description)
			}
			if off[i].Name != "todo" || string(off[i].Parameters) == "" {
				t.Fatal("off 档 todo 仍须注册且参数 schema 不变")
			}
			continue
		}
		if plain[i].Description != off[i].Description {
			t.Fatalf("非 todo 工具 %s 的描述不应随档位变化", plain[i].Name)
		}
	}
	// LLMTools() 与空档逐字节一致（旧调用方零改动）
	empty := r.LLMToolsForMode("")
	for i := range plain {
		if plain[i].Name != empty[i].Name || plain[i].Description != empty[i].Description {
			t.Fatalf("LLMTools 应与空档位一致（%s）", plain[i].Name)
		}
	}
}

// off 档下工具仍可正常写回（保留「模型自行决定是否使用 todo」的语义）。
func TestTodoStillExecutableInOffMode(t *testing.T) {
	r := New()
	var got []TodoItem
	ctx := WithTodoSink(context.Background(), func(items []TodoItem) { got = items })
	out := r.Execute(ctx, call("todo", `{"items":[{"content":"看代码","status":"active"}]}`))
	if !strings.Contains(out, "看代码") || len(got) != 1 {
		t.Fatalf("off 档 todo 仍应可写回: out=%q sink=%+v", out, got)
	}
}
