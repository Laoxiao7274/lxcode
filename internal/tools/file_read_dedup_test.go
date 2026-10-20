// read_file 会话级重复读去重（2026-12）：
//
// 同一会话内、同一文件、同一 offset/limit、且文件 mtime+size 未变 → 回摘要
// 而不是再灌一遍全文。四条纪律：
//   - 只对完全相同分页参数生效（换区间 = 在读另一部分，必须照常返回）；
//   - 文件被改过（mtime 或 size 变）→ 照常返回全文；
//   - 作用域是会话级（ctx 注入），父子会话各算各的；
//   - 未注入时（纯内存模式 / 单测直调）不去重，行为与改动前一致。
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeReadFixture(t *testing.T, name string, lines int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	var sb strings.Builder
	for i := 1; i <= lines; i++ {
		sb.WriteString("line-")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readWithState(t *testing.T, r *Registry, st *ReadState, args string) string {
	t.Helper()
	ctx := WithReadState(context.Background(), st)
	return r.Execute(ctx, call("read_file", args))
}

func TestReadDedupSameRangeReturnsSummary(t *testing.T) {
	r := New()
	path := writeReadFixture(t, "dup.txt", 10)
	st := NewReadState()
	args := `{"path":` + quote(path) + `}`

	first := readWithState(t, r, st, args)
	if !strings.Contains(first, "1→line-1") || !strings.Contains(first, "10→line-10") {
		t.Fatalf("首次读取应返回全文: %q", first)
	}
	second := readWithState(t, r, st, args)
	if strings.Contains(second, "line-1") {
		t.Fatalf("同文件同区间重复读不该再返回全文: %q", second)
	}
	if !strings.Contains(second, "内容未变") {
		t.Fatalf("重复读应回摘要说明: %q", second)
	}
	// 摘要要点名「上次第几步 / 共几行 / 本区间」，模型才知道该怎么换区间
	if !strings.Contains(second, "第 1 步") || !strings.Contains(second, "共 10 行") ||
		!strings.Contains(second, "offset=1") {
		t.Fatalf("摘要缺少步号/总行数/区间: %q", second)
	}
}

func TestReadDedupDifferentRangeStillReturnsContent(t *testing.T) {
	r := New()
	path := writeReadFixture(t, "range.txt", 10)
	st := NewReadState()

	_ = readWithState(t, r, st, `{"path":`+quote(path)+`}`)
	// 换区间 = 模型在读另一部分，必须照常返回（它可能在拼接长文件）
	got := readWithState(t, r, st, `{"path":`+quote(path)+`,"offset":3,"limit":4}`)
	if !strings.Contains(got, "3→line-3") || !strings.Contains(got, "6→line-6") {
		t.Fatalf("不同区间应照常返回内容: %q", got)
	}
	if strings.Contains(got, "内容未变") {
		t.Fatalf("不同区间不该回摘要: %q", got)
	}
}

func TestReadDedupResetsAfterFileChange(t *testing.T) {
	r := New()
	path := writeReadFixture(t, "mod.txt", 5)
	st := NewReadState()
	args := `{"path":` + quote(path) + `}`

	_ = readWithState(t, r, st, args)
	// 改内容并推进 mtime（显式设时间戳，避免同毫秒内写入导致判据失效）
	if err := os.WriteFile(path, []byte("changed-1\nchanged-2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	got := readWithState(t, r, st, args)
	if !strings.Contains(got, "changed-1") {
		t.Fatalf("文件被改过后应返回新内容: %q", got)
	}
	if strings.Contains(got, "内容未变") {
		t.Fatalf("文件已变，不该回摘要: %q", got)
	}
}

func TestReadDedupIsSessionScoped(t *testing.T) {
	r := New()
	path := writeReadFixture(t, "scope.txt", 3)
	args := `{"path":` + quote(path) + `}`

	parent := NewReadState()
	_ = readWithState(t, r, parent, args)

	// 另一个会话（子会话）没读过这个文件 → 必须拿到全文
	child := NewReadState()
	got := readWithState(t, r, child, args)
	if !strings.Contains(got, "1→line-1") {
		t.Fatalf("另一个会话的首读不该被父会话的观测去重: %q", got)
	}
}

func TestReadWithoutStateKeepsLegacyBehavior(t *testing.T) {
	r := New()
	path := writeReadFixture(t, "nostate.txt", 3)
	args := `{"path":` + quote(path) + `}`

	// 未注入 ctx（纯内存模式 / 单测直调）：两次都返回全文，行为与改动前一致
	for i := 0; i < 2; i++ {
		got := r.Execute(context.Background(), call("read_file", args))
		if !strings.Contains(got, "1→line-1") {
			t.Fatalf("第 %d 次读取（未注入 state）应返回全文: %q", i+1, got)
		}
	}
}

// 摘要那次不推进「上次读取于第 N 步」——它没交付任何内容，记它会让模型
// 看到「上次读取于第 3 步」却找不到第 3 步读到了什么。
func TestReadDedupSummaryDoesNotAdvanceObservedStep(t *testing.T) {
	r := New()
	path := writeReadFixture(t, "step.txt", 4)
	st := NewReadState()
	args := `{"path":` + quote(path) + `}`

	_ = readWithState(t, r, st, args) // 第 1 步：全文
	_ = readWithState(t, r, st, args) // 第 2 步：摘要（不记）
	third := readWithState(t, r, st, args)

	if !strings.Contains(third, "第 1 步") {
		t.Fatalf("摘要不应改写「上次读取于第 N 步」（仍是交付全文的那一步）: %q", third)
	}
}

// force=true 是模型显式要求的重读出口：去重只是默认省上下文的行为，
// 不能变成「模型要不到原文」的死路。
func TestReadDedupForceOverrides(t *testing.T) {
	r := New()
	path := writeReadFixture(t, "force.txt", 3)
	st := NewReadState()
	args := `{"path":` + quote(path) + `}`

	_ = readWithState(t, r, st, args)
	if got := readWithState(t, r, st, args); !strings.Contains(got, "内容未变") {
		t.Fatalf("基线：同区间重读应回摘要: %q", got)
	}
	got := readWithState(t, r, st, `{"path":`+quote(path)+`,"force":true}`)
	if !strings.Contains(got, "1→line-1") {
		t.Fatalf("force=true 应返回全文: %q", got)
	}
}
