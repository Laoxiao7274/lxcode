// read_image 的工具层测试：能力门控（fail-closed）、穿越拒绝、格式白名单、
// 内容嗅探、成功路径（附件落盘 + 视觉收集器登记 + 结果文本带基本信息）。
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// fixedPNG 是固定 1x1 透明 PNG 的 base64（与 llm/images_test.go 同一样本）。
const readImagePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// readImageEnv 是一次 read_image 执行的注入面：假附件保存口（记录收到的展示名）。
type readImageEnv struct {
	saved []string
}

func setupReadImage(t *testing.T, vision bool) (context.Context, *readImageEnv, string) {
	t.Helper()
	dir := t.TempDir()
	env := &readImageEnv{}
	ctx := WithWorkDir(context.Background(), dir)
	ctx = WithVisionCapable(ctx, vision)
	ctx = WithAttachmentSave(ctx, func(name string, data []byte) (llm.AttachmentRef, error) {
		env.saved = append(env.saved, name)
		return llm.AttachmentRef{Path: "sess-1/" + name, Name: name, Size: int64(len(data)), Kind: llm.AttachmentKindImage, Mime: "image/png"}, nil
	})
	col := &ImageCollector{}
	ctx = WithImageCollector(ctx, col)
	return ctx, env, dir
}

func TestReadImageRequiresVision(t *testing.T) {
	// 未注入门控（fail-closed）与显式 false 都要给人话错误
	ctx, _, dir := setupReadImage(t, false)
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := execReadImage(ctx, []byte(`{"path":"shot.png"}`))
	if err == nil || !strings.Contains(err.Error(), "不支持视觉") {
		t.Fatalf("非视觉模型应得人话错误: %v", err)
	}
	ctxNone, _, _ := setupReadImage(t, false)
	ctxNone = context.Background() // 完全不注入
	if _, err := execReadImage(ctxNone, []byte(`{"path":"shot.png"}`)); err == nil || !strings.Contains(err.Error(), "不支持视觉") {
		t.Fatalf("未注入门控应按不支持处理: %v", err)
	}
}

func TestReadImageRejectsTraversal(t *testing.T) {
	ctx, _, _ := setupReadImage(t, true)
	for _, p := range []string{"../outside.png", "..\\outside.png", "/etc/hosts"} {
		_, err := execReadImage(ctx, []byte(`{"path":`+quoteJSON(p)+`}`))
		if err == nil {
			t.Fatalf("路径 %q 应被拒绝", p)
		}
	}
}

func TestReadImageRejectsUnsupportedFormat(t *testing.T) {
	ctx, _, dir := setupReadImage(t, true)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := execReadImage(ctx, []byte(`{"path":"notes.txt"}`))
	if err == nil || !strings.Contains(err.Error(), "不支持的图片格式") {
		t.Fatalf("非图片扩展名应被拒绝: %v", err)
	}
}

func TestReadImageSniffsContent(t *testing.T) {
	ctx, _, dir := setupReadImage(t, true)
	// 扩展名是 png、内容是文本——嗅探拒绝（端点收到的是字节，扩展名骗不了）
	if err := os.WriteFile(filepath.Join(dir, "fake.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := execReadImage(ctx, []byte(`{"path":"fake.png"}`))
	if err == nil || !strings.Contains(err.Error(), "不是有效的") {
		t.Fatalf("扩展名与内容不符应被拒绝: %v", err)
	}
}

func TestReadImageSuccess(t *testing.T) {
	ctx, env, dir := setupReadImage(t, true)
	raw, err := base64.StdEncoding.DecodeString(readImagePNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shot.png"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := execReadImage(ctx, []byte(`{"path":"shot.png"}`))
	if err != nil {
		t.Fatalf("读取应成功: %v", err)
	}
	// 结果文本带基本信息（路径 / 格式 / 尺寸 / 大小）——模型要知道它「看到了什么文件」
	for _, want := range []string{"shot.png", "image/png", "1×1", "70 B"} {
		if !strings.Contains(out, want) {
			t.Fatalf("结果文本应含 %q: %s", want, out)
		}
	}
	if !strings.Contains(out, "share_file") {
		t.Fatalf("结果文本应提示展示用 share_file: %s", out)
	}
	// 附件落盘（假保存口收到原始字节）
	if len(env.saved) != 1 || env.saved[0] != "shot.png" {
		t.Fatalf("应把原图交给保存口: %v", env.saved)
	}
	// 视觉收集器登记引用（内核把它挂到 tool 结果消息上）
	col := ImageCollectorFrom(ctx)
	refs := col.Take()
	if len(refs) != 1 || refs[0].Path != "sess-1/shot.png" || refs[0].Mime != "image/png" {
		t.Fatalf("视觉收集器应有 1 个引用: %+v", refs)
	}
	// 磁盘上的源文件没被改动（只读语义）
	got, _ := os.ReadFile(filepath.Join(dir, "shot.png"))
	if string(got) != string(raw) {
		t.Fatal("源文件不应被修改")
	}
}

// quoteJSON 把字符串包成 JSON 字符串字面量（测试参数拼接用）。
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
