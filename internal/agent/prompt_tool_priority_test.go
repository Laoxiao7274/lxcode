// 工具优先级纪律的钉子（2026-12 工具选择引导修复）。
//
// 为什么钉在守则层与工具摘要层：模型有 ripgrep 却用 bash grep、有专用工具却走
// bash 的根因是三层模型可见信号全缺席/反向（工具描述、systemPromptTools 摘要、
// 全局守则）——修复把优先级写进这三层，本文件钉住守则层与摘要层不在场就报错。
// 描述层（tools/bash.go 与 store 种子）的断言在各自包里。
package agent

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/tools"
)

// TestToolPriorityRuleInGlobalFooter：全局守则必须有「专用工具优先」条目，
// 且三档 todo 模式下都在场（它插在 todo 占位之后，off 档编号顺延不影响在场）。
func TestToolPriorityRuleInGlobalFooter(t *testing.T) {
	for _, footer := range []string{
		systemPromptFooter("strict"), systemPromptFooter("normal"), systemPromptFooter("off"),
	} {
		if !strings.Contains(footer, "有专用工具的场景必须用专用工具") {
			t.Fatalf("守则缺少工具优先级纪律条目: %q", footer)
		}
		if !strings.Contains(footer, "bash 只做终端操作与专用工具覆盖不了的组合场景") {
			t.Fatalf("守则的工具优先级条目应给 bash 划定兜底定位: %q", footer)
		}
	}
}

// TestToolPriorityAcrossVisibleLayers：内核注册表里的两处模型可见文本同源——
// bash 的定位收窄到「终端操作」，bash_readonly 带反干扰句（点名专用工具）；
// ripgrep 是 binary 工具（Desc 来自目录种子，经 CustomDef 同步），它的优先级句
// 断言在 store 层（TestSeedToolDescriptionsGuideToolChoice）。
func TestToolPriorityAcrossVisibleLayers(t *testing.T) {
	r := tools.New()
	br, ok := r.Get("bash_readonly")
	if !ok {
		t.Fatal("注册表缺 bash_readonly")
	}
	for _, want := range []string{"read_file", "ripgrep", "db_query"} {
		if !strings.Contains(br.Description, want) {
			t.Errorf("bash_readonly 的 Description 应点名专用工具 %s（反干扰句）: %q", want, br.Description)
		}
	}
	if !strings.Contains(br.Description, "写操作会被拒绝") {
		t.Errorf("bash_readonly 的 Description 应保留写操作拒绝边界: %q", br.Description)
	}
	bash, _ := r.Get("bash")
	if !strings.Contains(bash.Description, "终端操作专用") {
		t.Errorf("bash 的 Description 应收窄到终端操作定位: %q", bash.Description)
	}
	if line := systemPromptTools["bash_readonly"]; !strings.Contains(line, "只读") {
		t.Errorf("systemPromptTools 的 bash_readonly 摘要应保留只读语义: %q", line)
	}
	if line := systemPromptTools["bash_readonly"]; strings.Contains(line, "grep") {
		t.Errorf("systemPromptTools 的 bash_readonly 摘要不应再把 grep 列为用途（反向信号）: %q", line)
	}
}
