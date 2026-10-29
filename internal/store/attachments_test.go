// 图片附件落盘测试：保存/读回一致、大小与 mime 白名单、路径穿越防护。
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestSaveAttachmentRoundTrip：保存 → 读回字节一致；返回相对路径能解析回
// 根内的绝对路径。
func TestSaveAttachmentRoundTrip(t *testing.T) {
	st := newTestStore(t)
	data := []byte("fake-png-bytes-\x00\x01")
	rel, err := st.SaveAttachment("20260101-000000-ab", "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, "20260101-000000-ab/") || !strings.HasSuffix(rel, ".png") {
		t.Fatalf("相对路径形态不对: %q", rel)
	}
	p, err := ResolveAttachmentPath(st.AttachmentsRoot(), rel)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("读回字节不一致: %d vs %d", len(got), len(data))
	}
	// 目录确实在 <sessions>/attachments 下（与 worktree 同层）
	if filepath.Dir(filepath.Dir(p)) != st.AttachmentsRoot() {
		t.Fatalf("附件不在附件根下: %s", p)
	}
}

// TestSaveAttachmentRejectsOversize：单张 >5MB（解码后）拒绝。
func TestSaveAttachmentRejectsOversize(t *testing.T) {
	st := newTestStore(t)
	big := make([]byte, llm.MaxImageBytes+1)
	if _, err := st.SaveAttachment("s-1", "image/png", big); err == nil {
		t.Fatal("超限图片应被拒绝")
	}
	// 恰好 5MB 允许
	ok := make([]byte, llm.MaxImageBytes)
	if _, err := st.SaveAttachment("s-1", "image/png", ok); err != nil {
		t.Fatalf("恰好 5MB 应允许: %v", err)
	}
}

// TestSaveAttachmentMimeWhitelist：白名单外 mime 拒绝；四种白名单 mime 全通过。
func TestSaveAttachmentMimeWhitelist(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.SaveAttachment("s-1", "image/bmp", []byte{1}); err == nil {
		t.Fatal("image/bmp 应被拒绝")
	}
	if _, err := st.SaveAttachment("s-1", "", []byte{1}); err == nil {
		t.Fatal("空 mime 应被拒绝")
	}
	for _, mime := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		if _, err := st.SaveAttachment("s-1", mime, []byte{1}); err != nil {
			t.Fatalf("%s 应允许: %v", mime, err)
		}
	}
}

// TestSaveAttachmentSessionIDWhitelist：会话 id 只允许 [a-z0-9-]——路径成分
// 不可信输入的第一道闸。
func TestSaveAttachmentSessionIDWhitelist(t *testing.T) {
	st := newTestStore(t)
	for _, id := range []string{"", "a/b", "..", "A-1", "a b", "a\\b", "s_1"} {
		if _, err := st.SaveAttachment(id, "image/png", []byte{1}); err == nil {
			t.Fatalf("会话 id %q 应被拒绝", id)
		}
	}
}

// TestResolveAttachmentPathTraversal：路径穿越输入一律拒绝，绝不返回根外路径。
func TestResolveAttachmentPathTraversal(t *testing.T) {
	st := newTestStore(t)
	root := st.AttachmentsRoot()
	for _, rel := range []string{
		"../../etc/passwd",
		`s-1\..\..\x.png`,
		"..\\x.png",
		"/abs/path.png",
		"",
		"a/../b/c.png",
	} {
		if p, err := ResolveAttachmentPath(root, rel); err == nil {
			t.Fatalf("穿越路径 %q 应被拒绝，却返回 %q", rel, p)
		}
	}
	// 合法相对路径解析成功
	if p, err := ResolveAttachmentPath(root, "s-1/a.png"); err != nil {
		t.Fatalf("合法路径应通过: %v", err)
	} else if !strings.HasPrefix(p, root+string(filepath.Separator)) {
		t.Fatalf("解析结果应仍在根内: %s", p)
	}
}
