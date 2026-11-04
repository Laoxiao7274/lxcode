// edit 的「改前先读」**提示**（不是硬拦）——2026-12 用户拍板的保守方案。
//
// 取舍记录（写在测试里，因为它是一条会被反复讨论的设计决定）：
// DSH 的 fs-observation-policy 用 FS_NOT_OBSERVED 硬拒绝「没读过就 edit」。
// lxcode 不照抄，理由有二：
//  1. edit 的 old_string 唯一匹配语义已经自带「必须先看到真实内容」的约束
//     ——凭记忆写的 old_string 几乎必然不匹配；
//  2. 硬拦会打断合法流程：刚 write_file 写完的文件（内容就在上下文里）、
//     本会话之前读过但进程重启过的会话——那些场景里 read_file 是纯仪式。
//
// 于是只做一件事：**失败信息里补上关键事实**（本会话读过没有 / 读的是不是
// 同一版本 / 读到第几步），让模型自己判断该去读还是该换 old_string。
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEditNoMatchHintSaysNotReadYet(t *testing.T) {
	r := New()
	path := writeTemp(t, "alpha\n")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"nope","new_string":"x"}`))
	if !strings.Contains(got, "还没有读过这个文件") {
		t.Fatalf("没读过时失败信息应点名「还没读过」: %q", got)
	}
}

func TestEditNoMatchHintReportsObservedStep(t *testing.T) {
	r := New()
	path := writeTemp(t, "alpha\n")
	st := NewReadState()
	ctx := WithReadState(context.Background(), st)

	// 先经 read_file 记一次观测（第 1 步）
	_ = r.Execute(ctx, call("read_file", `{"path":`+quote(path)+`}`))

	got := r.Execute(ctx, call("edit",
		`{"path":`+quote(path)+`,"old_string":"nope","new_string":"x"}`))
	if !strings.Contains(got, "第 1 步读过它") {
		t.Fatalf("读过时失败信息应给出步号: %q", got)
	}
	if !strings.Contains(got, "内容此后未变") {
		t.Fatalf("文件未变时应说清「内容此后未变」: %q", got)
	}
}

func TestEditNoMatchHintDetectsChangedFile(t *testing.T) {
	r := New()
	path := writeTemp(t, "alpha\n")
	st := NewReadState()
	ctx := WithReadState(context.Background(), st)
	_ = r.Execute(ctx, call("read_file", `{"path":`+quote(path)+`}`))

	// 外部改动（模拟用户在编辑器里改了）：内容与 mtime 都变
	if err := os.WriteFile(path, []byte("rewritten-by-user\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	got := r.Execute(ctx, call("edit",
		`{"path":`+quote(path)+`,"old_string":"nope","new_string":"x"}`))
	if !strings.Contains(got, "此后被改过") {
		t.Fatalf("文件被外部改过时应提示重新读取: %q", got)
	}
}

// 硬拦的**反证**：没读过也可以 edit 成功——这是保守方案的核心
// （刚 write_file 写完、或内容就在上下文里的场景不该被拦）。
func TestEditDoesNotRequirePriorRead(t *testing.T) {
	r := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.txt")
	// 不经 read_file 直接改：这是「刚写完的文件」的真实形态
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"x","new_string":"y"}`))
	// 文件不存在 → 读失败（另一条错误路径），这里换一个已存在的文件
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"x","new_string":"y"}`))
	if !strings.Contains(got, "1 处替换") {
		t.Fatalf("没读过也应允许 edit 成功（保守方案：只提示不硬拦）: %q", got)
	}
}

// TestEditNoMatchAttachesNearestLines（2026-12 P6）：未匹配错误回带文件中
// 最接近的行段（含行号）——568 次「读过文件没变还是抄错」多数是缩进/全半角
// 的细微出入，把最像的原文行摆在眼前，模型照抄即修，不必再付一次 read_file。
func TestEditNoMatchAttachesNearestLines(t *testing.T) {
	r := New()
	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	path := writeTemp(t, content)
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"func main(){","new_string":"x"}`))
	if !strings.Contains(got, "最接近的几行") {
		t.Fatalf("未匹配错误应附最接近的行段: %q", got)
	}
	// 带行号；最像的 func main() { 行应排在最前
	if !strings.Contains(got, "[行 5] func main() {") {
		t.Fatalf("应带行号且最像的行在列: %q", got)
	}
	// 批量尾注：多处失败时先核对
	if !strings.Contains(got, "若本批多处修改都失败，先 read_file") {
		t.Fatalf("未匹配错误应带批量核对尾注: %q", got)
	}
}

// TestEditNoMatchNoSimilarLines：文件里完全没有相似的行时不硬凑——
// 如实说「可能来自记忆或别的文件」，引导去读而不是猜。
func TestEditNoMatchNoSimilarLines(t *testing.T) {
	r := New()
	path := writeTemp(t, "alpha\nbeta\n")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"zzz-完全无关的内容","new_string":"x"}`))
	if !strings.Contains(got, "没有与 old_string 首行相似的行") {
		t.Fatalf("无相似行时应如实说明: %q", got)
	}
}

// TestEditExistingBranchesUnregressed（2026-12 P6 回归钉）：空 old_string、
// new==old、多处匹配三条既有分支的错误口径不变（hint 增强只动 n==0 分支）。
func TestEditExistingBranchesUnregressed(t *testing.T) {
	r := New()
	path := writeTemp(t, "dup\ndup\n")

	if got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"","new_string":"x"}`)); !strings.Contains(got, "old_string 不能为空") {
		t.Fatalf("空 old_string 分支口径变了: %q", got)
	}
	if got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"dup","new_string":"dup"}`)); !strings.Contains(got, "不会产生任何改动") {
		t.Fatalf("new==old 分支口径变了: %q", got)
	}
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"dup","new_string":"x"}`))
	if !strings.Contains(got, "匹配了 2 处") {
		t.Fatalf("多处匹配分支口径变了: %q", got)
	}
	if strings.Contains(got, "最接近的几行") {
		t.Fatalf("多处匹配分支不该带行段（它不是「没匹配到」）: %q", got)
	}
}
