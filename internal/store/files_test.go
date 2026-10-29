// 文件附件通道（图片批次 B）的 store 层测试：SaveFileAttachment 落盘 /
// 大小上限 / 会话 id 白名单；SanitizeFileName 名字净化（路径穿越 / 控制字符 /
// 空名 / 超长）。
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveFileAttachmentRoundTrip(t *testing.T) {
	st := newTestStore(t)
	rel, err := st.SaveFileAttachment("s-1", "报告 v2.txt", []byte("hello file"))
	if err != nil {
		t.Fatal(err)
	}
	abs, err := ResolveAttachmentPath(st.AttachmentsRoot(), rel)
	if err != nil {
		t.Fatalf("返回的相对路径应能解析回根内: %v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil || string(data) != "hello file" {
		t.Fatalf("读回内容不一致: %q err=%v", data, err)
	}
	// 扩展名保留（净化的字母数字），磁盘名是随机 hex——原名不是路径成分
	if filepath.Ext(abs) != ".txt" {
		t.Fatalf("应保留原扩展名: %s", abs)
	}
	base := filepath.Base(abs)
	if len(base) != 32+len(".txt") || strings.Contains(base, "报告") {
		t.Fatalf("磁盘名应为 32 hex + 扩展名（不含原名）: %s", base)
	}
}

func TestSaveFileAttachmentRejects(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.SaveFileAttachment("s-1", "big.bin", make([]byte, MaxFileAttachmentBytes+1)); err == nil {
		t.Fatal(">20MB 应拒绝")
	}
	if _, err := st.SaveFileAttachment("BAD_ID", "a.txt", []byte{1}); err == nil {
		t.Fatal("会话 id 白名单外应拒绝")
	}
	// 无扩展名的名字照常保存（只是磁盘名无后缀）
	rel, err := st.SaveFileAttachment("s-1", "noext", []byte{1})
	if err != nil {
		t.Fatalf("无扩展名应可保存: %v", err)
	}
	if filepath.Ext(rel) != "" {
		t.Fatalf("无扩展名原名不应推导出后缀: %s", rel)
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"报告.txt", "报告.txt"},
		{`C:\Users\x\报告.txt`, "报告.txt"},  // Windows 全路径：只留 base 名
		{"/etc/passwd", "passwd"},          // POSIX 全路径
		{`..\..\evil\notes.txt`, "notes.txt"}, // 穿越片段被 base 名切掉
		{"a\nb.txt", "ab.txt"},             // 控制字符剔除（换行不伪造新附件行）
		{"  spaced name.md  ", "spaced name.md"}, // 首尾空白
		{"", "file"},                       // 空名兜底
		{`\\`, "file"},                     // 全是分隔符兜底
		{"x:name?.md", "x:name?.md"},       // 非分隔符/控制字符的杂质留给扩展名净化（原名可读）
	}
	for _, tc := range cases {
		if got := SanitizeFileName(tc.in); got != tc.want {
			t.Errorf("SanitizeFileName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// 超长钳到 120 rune
	long := strings.Repeat("长", 200)
	if got := SanitizeFileName(long); len([]rune(got)) != 120 {
		t.Errorf("超长名应钳到 120 rune，got %d", len([]rune(got)))
	}
}

func TestAttachmentExt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"报告 v2.txt", ".txt"},
		{"archive.tar.gz", ".gz"},   // 只取最后一段
		{"weird:name?.md", ".md"},   // 杂质段丢弃
		{"noext", ""},               // 无扩展名
		{"trailing.", ""},           // 点结尾不算扩展名
		{"a.中文", ""},               // 非字母数字扩展名丢弃
		{"a." + strings.Repeat("x", 30), ".xxxxxxxxxxxxxxxx"}, // 钳 16 字符
	}
	for _, tc := range cases {
		if got := attachmentExt(tc.in); got != tc.want {
			t.Errorf("attachmentExt(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
