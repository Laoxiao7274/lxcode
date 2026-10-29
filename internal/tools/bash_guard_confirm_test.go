package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// TestBashConfirmForced：完整 bash 的强制确认面（**机制保留**——runTools 不再
// 消费 ConfirmForced，auto = 完全无确认，2026-10 拍板；本测试钉的是机制本身，
// 供将来新档位接回）。命中高危命令模式返回带模式名与命令原文的文案；
// 未命中返回空串；bash_readonly 不触发强制确认（恒空串）。
func TestBashConfirmForced(t *testing.T) {
	r := New()
	ctx := context.Background()

	cases := []struct {
		name    string
		tool    string
		command string
		wantHit bool // 期望触发强制确认
	}{
		{name: "git push 命中", tool: "bash", command: "git push origin main", wantHit: true},
		{name: "rm -rf 根命中", tool: "bash", command: "rm -rf /", wantHit: true},
		{name: "go build 不命中", tool: "bash", command: "go build ./..."},
		{name: "echo hi 不命中", tool: "bash", command: "echo hi"},
		{name: "只读面 git push 不触发", tool: bashReadOnlyName, command: "git push"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"command": tc.command})
			call := toolCallOf(tc.tool, string(args))
			got := r.ConfirmForced(ctx, call)
			if tc.wantHit && got == "" {
				t.Fatalf("期望触发强制确认，实际为空")
			}
			if !tc.wantHit && got != "" {
				t.Fatalf("期望空串，实际: %s", got)
			}
			if tc.wantHit {
				if !strings.Contains(got, "高危命令模式命中") || !strings.Contains(got, tc.command) {
					t.Fatalf("确认文案应含模式名与命令原文: %s", got)
				}
			}
		})
	}
}

// toolCallOf 构造一个 llm.ToolCall（测试便捷封装）。
func toolCallOf(name, args string) llm.ToolCall {
	var c llm.ToolCall
	c.Function.Name = name
	c.Function.Arguments = args
	return c
}

// TestBashConfirmForcedRegistryAlignment：Registry.ConfirmForced 对未知工具
// 与 nil 回调返回空串（与 Confirm 同款兜底纪律）。
func TestBashConfirmForcedRegistryAlignment(t *testing.T) {
	r := New()
	ctx := context.Background()
	if got := r.ConfirmForced(ctx, toolCallOf("no-such-tool", `{}`)); got != "" {
		t.Fatalf("未知工具应返回空串: %s", got)
	}
	// read_file 无 ConfirmForced 字段
	if got := r.ConfirmForced(ctx, toolCallOf("read_file", `{}`)); got != "" {
		t.Fatalf("无强制确认面的工具应返回空串: %s", got)
	}
}
