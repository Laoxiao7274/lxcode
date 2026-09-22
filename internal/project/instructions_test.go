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

// TestWriteInstructionsRoundtrip：写入 → 读回一致；覆盖已有文件（Windows 的
// rename 不覆盖语义走的是备份回退，必须真的换掉内容）；目录不存在时显式报错。
func TestWriteInstructionsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteInstructions(dir, "第一版\n")
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if path != filepath.Join(dir, InstructionFileName) {
		t.Fatalf("路径应为项目根下的 %s: %s", InstructionFileName, path)
	}
	if got := LoadInstructions(dir); got.Content != "第一版\n" {
		t.Fatalf("读回不一致: %q", got.Content)
	}
	// 覆盖写（已存在文件）——这是 Windows rename 语义的关键路径
	if _, err := WriteInstructions(dir, "第二版\n"); err != nil {
		t.Fatalf("覆盖写失败: %v", err)
	}
	if got := LoadInstructions(dir); got.Content != "第二版\n" {
		t.Fatalf("覆盖后读回不一致: %q", got.Content)
	}
	// 临时文件不留残骸
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != InstructionFileName {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("目录里应只有守则文件，实际: %v", names)
	}
	// 失效项目目录：显式报错，不凭空造目录
	if _, err := WriteInstructions(filepath.Join(dir, "not-exist"), "x"); err == nil {
		t.Fatal("目录不存在时应报错")
	}
	// 超大内容拒绝（与读取上限一致）
	big := strings.Repeat("a", maxInstructionWriteBytes+1)
	if _, err := WriteInstructions(dir, big); err == nil {
		t.Fatal("超过上限的内容应被拒绝")
	}
}
