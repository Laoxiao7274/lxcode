// share_file 的测试：参数校验、穿越拒绝、复制登记（经注入的 save 口）、
// 收集器元数据随结果流动。工具层不知道磁盘布局——save 是假实现（内存登记），
// 真落盘的路径在 store（SaveSharedAttachment 的用例在 store 包）。
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// shareFixture 建一个带工作目录、假 save 口与收集器的执行环境：
// 返回 ctx（工作目录 = dir）、收集器与工作目录本身（测试往里放被共享的文件）。
// save 是假实现：按名字给分类（.png → 图片），登记进 saved 供断言。
func shareFixture(t *testing.T) (context.Context, *AttachmentCollector, string) {
	t.Helper()
	dir := t.TempDir()
	collector := &AttachmentCollector{}
	ctx := WithWorkDir(context.Background(), dir)
	ctx = WithAttachmentCollector(ctx, collector)
	ctx = WithAttachmentSave(ctx, func(name string, data []byte) (llm.AttachmentRef, error) {
		ref := llm.AttachmentRef{Path: "sess/" + name + ".bin", Name: name, Size: int64(len(data))}
		if strings.HasSuffix(name, ".png") {
			ref.Kind, ref.Mime = llm.AttachmentKindImage, "image/png"
		} else {
			ref.Kind = llm.AttachmentKindFile
		}
		return ref, nil
	})
	return ctx, collector, dir
}

func runShare(t *testing.T, ctx context.Context, args string) string {
	t.Helper()
	r := New()
	return r.Execute(ctx, call("share_file", args))
}

func TestShareFileCopiesAndCollects(t *testing.T) {
	ctx, collector, dir := shareFixture(t)
	src := filepath.Join(dir, "chart.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runShare(t, ctx, `{"path":"chart.png","caption":"本月数据图"}`)
	if !strings.Contains(got, "chart.png") || !strings.Contains(got, "本月数据图") {
		t.Fatalf("结果应含文件名与说明: %q", got)
	}
	refs := collector.Take()
	if len(refs) != 1 {
		t.Fatalf("应登记一个附件: %v", refs)
	}
	ref := refs[0]
	if ref.Kind != llm.AttachmentKindImage || ref.Mime != "image/png" || ref.Size != 9 {
		t.Fatalf("附件元数据不符: %+v", ref)
	}
	if ref.Caption != "本月数据图" {
		t.Fatalf("说明未随引用流动: %+v", ref)
	}
}

func TestShareFileRejectsTraversal(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, collector, _ := shareFixture(t)

	for _, args := range []string{
		`{"path":"../secret.txt"}`,
		`{"path":"..\\secret.txt"}`,
		`{"path":"sub/../../secret.txt"}`,
		`{"path":"` + strings.ReplaceAll(secret, `\`, `\\`) + `"}`, // 绝对路径越出工作目录
	} {
		got := runShare(t, ctx, args)
		if !strings.Contains(got, "拒绝") && !strings.Contains(got, "不合法") && !strings.Contains(got, "越出") {
			t.Fatalf("穿越路径应被拒绝: %q (args=%s)", got, args)
		}
	}
	if refs := collector.Take(); len(refs) != 0 {
		t.Fatalf("穿越路径不该登记任何附件: %v", refs)
	}
}

func TestShareFileWorkDirInsideAbsoluteOK(t *testing.T) {
	ctx, collector, dir := shareFixture(t)
	src := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 工作目录之内的绝对路径合法（agent 常拿绝对路径说话）
	got := runShare(t, ctx, `{"path":`+quote(src)+`}`)
	if !strings.Contains(got, "report.txt") {
		t.Fatalf("工作目录内的绝对路径应共享成功: %q", got)
	}
	if refs := collector.Take(); len(refs) != 1 {
		t.Fatalf("应登记一个附件: %v", refs)
	}
}

func TestShareFileRejectsMissingAndDir(t *testing.T) {
	ctx, collector, dir := shareFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := runShare(t, ctx, `{"path":"nope.txt"}`); !strings.Contains(got, "读取文件信息失败") {
		t.Fatalf("不存在的文件应报人话错误: %q", got)
	}
	if got := runShare(t, ctx, `{"path":"sub"}`); !strings.Contains(got, "目录") {
		t.Fatalf("目录应被拒绝: %q", got)
	}
	if got := runShare(t, ctx, `{}`); !strings.Contains(got, "path") {
		t.Fatalf("缺 path 应报人话错误: %q", got)
	}
	if refs := collector.Take(); len(refs) != 0 {
		t.Fatalf("失败路径不该登记附件: %v", refs)
	}
}

func TestShareFileRequiresSaveAndWorkDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := New()
	// 有工作目录、没有 save 口（无存储模式）：报「会话存储未启用」，不猜临时目录
	got := r.Execute(WithWorkDir(context.Background(), dir), call("share_file", `{"path":"a.txt"}`))
	if !strings.Contains(got, "会话存储未启用") {
		t.Fatalf("无 save 口应报人话错误: %q", got)
	}
	// 没有工作目录：拒绝（没有基准就没有穿越判定）
	got = r.Execute(context.Background(), call("share_file", `{"path":"a.txt"}`))
	if !strings.Contains(got, "工作目录") {
		t.Fatalf("无工作目录应报人话错误: %q", got)
	}
}

func TestShareFileCollectorNotInjected(t *testing.T) {
	_, collector, dir := shareFixture(t)
	src := filepath.Join(dir, "a.png")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 收集器未注入（单测直调形态）：执行照常成功，只是没有元数据流动
	ctx := WithWorkDir(WithAttachmentSave(context.Background(),
		func(name string, data []byte) (llm.AttachmentRef, error) {
			return llm.AttachmentRef{Name: name, Kind: llm.AttachmentKindImage}, nil
		}), dir)
	got := runShare(t, ctx, `{"path":"a.png"}`)
	if !strings.Contains(got, "a.png") {
		t.Fatalf("未注入收集器不应影响执行: %q", got)
	}
	if refs := collector.Take(); len(refs) != 0 {
		t.Fatalf("未注入收集器的 ctx 不该登记到别的收集器: %v", refs)
	}
}
