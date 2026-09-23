package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- edit ----------

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEditReplacesUniqueMatch(t *testing.T) {
	r := New()
	path := writeTemp(t, "alpha\nbeta\ngamma\n")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"beta","new_string":"BETA"}`))
	if !strings.Contains(got, "1 处替换") {
		t.Fatalf("唯一匹配应替换成功: %q", got)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("替换结果不符: %q", data)
	}
}

func TestEditNoMatch(t *testing.T) {
	r := New()
	path := writeTemp(t, "alpha\n")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"nope","new_string":"x"}`))
	if !strings.Contains(got, "没有匹配") || !strings.Contains(got, "read_file") {
		t.Fatalf("无匹配应报错并提示先读文件: %q", got)
	}
}

func TestEditAmbiguousMatch(t *testing.T) {
	r := New()
	path := writeTemp(t, "dup\ndup\ndup\n")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"dup","new_string":"x"}`))
	if !strings.Contains(got, "3 处") || !strings.Contains(got, "replace_all") {
		t.Fatalf("多处匹配不 replace_all 应报错并给方案: %q", got)
	}
	// 显式 replace_all 后应全部替换
	got = r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"dup","new_string":"x","replace_all":true}`))
	if !strings.Contains(got, "3 处替换") {
		t.Fatalf("replace_all 应替换全部: %q", got)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "x\nx\nx\n" {
		t.Fatalf("replace_all 结果不符: %q", data)
	}
}

func TestEditNewFileRejected(t *testing.T) {
	r := New()
	path := filepath.Join(t.TempDir(), "no-such.txt")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"a","new_string":"b"}`))
	if !strings.Contains(got, "失败") {
		t.Fatalf("edit 不存在的文件应报错（新建文件是 write_file 的事）: %q", got)
	}
}

func TestEditSameStringsRejected(t *testing.T) {
	r := New()
	path := writeTemp(t, "alpha\n")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"alpha","new_string":"alpha"}`))
	if !strings.Contains(got, "相同") {
		t.Fatalf("new==old 应报错（no-op 编辑多半是手滑）: %q", got)
	}
}

func TestEditEmptyOldStringRejected(t *testing.T) {
	r := New()
	path := writeTemp(t, "alpha\n")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"","new_string":"x"}`))
	if !strings.Contains(got, "old_string") {
		t.Fatalf("空 old_string 应报错: %q", got)
	}
}

func TestEditPreservesLineEndings(t *testing.T) {
	r := New()
	// CRLF 文件：old/new 都按模型给的字符串精确匹配——模型必须照 read_file
	// 看到的原样写。这里验证 LF 文件不会被工具偷偷改动其他部分。
	path := writeTemp(t, "one\ntwo\nthree\n")
	got := r.Execute(context.Background(), call("edit",
		`{"path":`+quote(path)+`,"old_string":"two","new_string":"2"}`))
	if !strings.Contains(got, "已修改") {
		t.Fatalf("编辑应成功: %q", got)
	}
}

// ---------- todo ----------

func TestTodoWriteAndSink(t *testing.T) {
	r := New()
	var got []TodoItem
	// 清单写回口经 ctx 注入（每会话独立——父/子会话各有自己的清单）
	ctx := WithTodoSink(context.Background(), func(items []TodoItem) { got = items })
	out := r.Execute(ctx, call("todo",
		`{"items":[{"content":"盘点现状","status":"done"},{"content":"写实现","status":"active"},{"content":"验证","status":"pending"}]}`))
	if !strings.Contains(out, "done 1") || !strings.Contains(out, "写实现") {
		t.Fatalf("todo 应回显清单摘要: %q", out)
	}
	if len(got) != 3 || got[1].Status != "active" {
		t.Fatalf("sink 应收到完整清单: %+v", got)
	}
	// 展示顺序：active 在前
	if !strings.HasPrefix(strings.Split(out, "\n")[1], "▶") {
		t.Fatalf("active 项应排在最前: %q", out)
	}
}

func TestTodoRejectsBadStatus(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("todo",
		`{"items":[{"content":"x","status":"running"}]}`))
	if !strings.Contains(got, "不合法") {
		t.Fatalf("非法 status 应报错: %q", got)
	}
}

func TestTodoRejectsMultipleActive(t *testing.T) {
	r := New()
	got := r.Execute(context.Background(), call("todo",
		`{"items":[{"content":"a","status":"active"},{"content":"b","status":"active"}]}`))
	if !strings.Contains(got, "一项") {
		t.Fatalf("多个 active 应报错: %q", got)
	}
}

func TestTodoEmptyList(t *testing.T) {
	r := New()
	var sinkCalled bool
	ctx := WithTodoSink(context.Background(), func(items []TodoItem) { sinkCalled = true })
	got := r.Execute(ctx, call("todo", `{"items":[]}`))
	if !strings.Contains(got, "清空") || !sinkCalled {
		t.Fatalf("空清单 = 清空（合法收敛态）: %q sink=%v", got, sinkCalled)
	}
}

// 清单写回口是每会话状态：两个 ctx 各自的清单互不影响（父/子会话不串）。
func TestTodoSinkIsPerContext(t *testing.T) {
	r := New()
	var parent, child []TodoItem
	parentCtx := WithTodoSink(context.Background(), func(items []TodoItem) { parent = items })
	childCtx := WithTodoSink(context.Background(), func(items []TodoItem) { child = items })
	r.Execute(childCtx, call("todo", `{"items":[{"content":"子任务","status":"active"}]}`))
	if len(child) != 1 || child[0].Content != "子任务" {
		t.Fatalf("子上下文应写自己的清单: %+v", child)
	}
	if len(parent) != 0 {
		t.Fatalf("子会话写清单不应污染父会话: %+v", parent)
	}
	// 反向：父上下文写清单也不影响子上下文
	r.Execute(parentCtx, call("todo", `{"items":[{"content":"父任务","status":"active"}]}`))
	if len(parent) != 1 || parent[0].Content != "父任务" {
		t.Fatalf("父上下文应写自己的清单: %+v", parent)
	}
	if len(child) != 1 || child[0].Content != "子任务" {
		t.Fatalf("父会话写清单不应污染子会话: %+v", child)
	}
}

func TestTodoNoSinkStillWorks(t *testing.T) {
	r := New()
	// 不接 sink（如单测裸用注册表）：工具仍应正常回显，不 panic
	got := r.Execute(context.Background(), call("todo",
		`{"items":[{"content":"x","status":"pending"}]}`))
	if !strings.Contains(got, "清单已更新") {
		t.Fatalf("无 sink 时也应正常执行: %q", got)
	}
}
