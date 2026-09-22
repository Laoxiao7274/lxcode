package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadInstructionsStates：守则读取的三种态必须分清——
// 没有（正常，不注入）／读到（正文）／读不了（Note 说明，不报错）。
// 关键契约：**绝不返回 error**，守则读不到不该打断生成。
func TestLoadInstructionsStates(t *testing.T) {
	t.Run("文件不存在", func(t *testing.T) {
		dir := t.TempDir()
		got := LoadInstructions(dir)
		if got.Exists {
			t.Fatalf("不存在应 Exists=false: %+v", got)
		}
		if got.Content != "" || got.Note != "" {
			t.Fatalf("不存在时不该有内容或说明: %+v", got)
		}
		if got.Path != filepath.Join(dir, InstructionFileName) {
			t.Fatalf("路径应为项目根下的 %s: %s", InstructionFileName, got.Path)
		}
	})

	t.Run("读到正文", func(t *testing.T) {
		dir := t.TempDir()
		body := "# 项目守则\n\n提交信息用中文。\n"
		if err := os.WriteFile(filepath.Join(dir, InstructionFileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got := LoadInstructions(dir)
		if !got.Exists || got.Content != body || got.Note != "" {
			t.Fatalf("应原样读到正文: %+v", got)
		}
	})

	t.Run("空文件", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, InstructionFileName), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		got := LoadInstructions(dir)
		if !got.Exists || got.Content != "" || got.Note != "" {
			t.Fatalf("空文件应 Exists=true 且无内容无说明: %+v", got)
		}
	})

	t.Run("同名目录", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, InstructionFileName), 0o755); err != nil {
			t.Fatal(err)
		}
		got := LoadInstructions(dir)
		if !got.Exists || !strings.Contains(got.Note, "目录") {
			t.Fatalf("同名目录应给出说明: %+v", got)
		}
	})

	t.Run("超大文件跳过", func(t *testing.T) {
		dir := t.TempDir()
		big := make([]byte, maxInstructionSourceBytes+1)
		for i := range big {
			big[i] = 'a'
		}
		if err := os.WriteFile(filepath.Join(dir, InstructionFileName), big, 0o644); err != nil {
			t.Fatal(err)
		}
		got := LoadInstructions(dir)
		if !got.Exists || got.Content != "" || !strings.Contains(got.Note, "上限") {
			t.Fatalf("超大文件应跳过并说明: exists=%v note=%q", got.Exists, got.Note)
		}
	})

	t.Run("空项目根", func(t *testing.T) {
		if got := LoadInstructions(""); got.Exists || got.Path != "" {
			t.Fatalf("未分组会话（无项目根）不该读任何文件: %+v", got)
		}
	})
}
