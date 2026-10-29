// 控制台输出编码归一的测试（见 encoding.go 的头注：真库取证 354 条 bash 结果是
// 非法 UTF-8，57 条含 U+FFFD 且 56/57 条紧接着又发了一条 bash 调用）。
//
// 三条纪律：
//  1. 合法 UTF-8 **逐字节原样**返回（零回归面——git/node/go 的输出不能被动）；
//  2. 非法字节段按本机 ANSI 代码页解码（Windows 中文系统 = GBK），
//     解出来必须是合法 UTF-8 且可读；
//  3. 混合输出逐行处理：UTF-8 行不动，只有坏行才解码。
package tools

import (
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeConsoleOutputKeepsValidUTF8(t *testing.T) {
	in := "中文输出 ok\n第二个 line\r\n"
	if got := normalizeConsoleOutput([]byte(in)); got != in {
		t.Fatalf("合法 UTF-8 必须逐字节原样返回: %q → %q", in, got)
	}
	if got := normalizeConsoleOutput(nil); got != "" {
		t.Fatalf("空输入应返回空串: %q", got)
	}
}

func TestNormalizeConsoleOutputAlwaysReturnsValidUTF8(t *testing.T) {
	// 任意坏字节都不能流进 JSON（序列化会变成一串 \ufffd）
	cases := [][]byte{
		{0xb2, 0xe2, 0xca, 0xd4},                 // 「测试」的 GBK 字节
		{0xff, 0xfe, 0x00, 0x41},                 // UTF-16LE BOM + 'A'
		{0x80},                                   // 孤立续字节
		{'o', 'k', '\n', 0xb2, 0xe2, 0xca, 0xd4}, // ASCII 行 + GBK 行
		append([]byte("中文\n"), 0xd6, 0xd0, 0xce), // UTF-8 行 + 截断的 GBK 行
	}
	for i, in := range cases {
		got := normalizeConsoleOutput(in)
		if !utf8.ValidString(got) {
			t.Fatalf("用例 %d：输出必须是合法 UTF-8，实际 %q", i, got)
		}
	}
}

func TestNormalizeConsoleOutputDecodesGBKOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("代码页解码只在 Windows 上有意义")
	}
	// 「测试」的 GBK 字节 → 应解出「测试」；具体字形取决于本机 ANSI 代码页，
	// 中文机器上是 GBK（936）。非中文机器上解出的字符不同，但**必须**是
	// 合法 UTF-8 且不再是 4 个替换符——所以只钉「不是坏字节」这一层。
	got := normalizeConsoleOutput([]byte{0xb2, 0xe2, 0xca, 0xd4})
	if !utf8.ValidString(got) {
		t.Fatalf("GBK 字节解码后必须是合法 UTF-8: %q", got)
	}
	if strings.Contains(got, "\uFFFD") {
		t.Fatalf("CP_ACP 解码不该产出替换符（说明解码路径没生效）: %q", got)
	}
}

// 混合输出逐行处理：好行不能被坏行连累重解码。
func TestNormalizeConsoleOutputPerLine(t *testing.T) {
	in := append([]byte("first line ok\n"), 0xb2, 0xe2, 0xca, 0xd4)
	in = append(in, '\n')
	got := normalizeConsoleOutput(in)
	if !strings.HasPrefix(got, "first line ok\n") {
		t.Fatalf("合法行应原样保留: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("整段必须是合法 UTF-8: %q", got)
	}
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("行结构应保持不变: %q", got)
	}
}

// 截断必须退到 rune 边界：直接 s[:n] 会把多字节字符劈成两半，
// 产出非法 UTF-8（与刚修好的编码问题同一后果）。
func TestTruncateUTF8StaysOnRuneBoundary(t *testing.T) {
	s := strings.Repeat("中", 10) // 每个 3 字节，共 30 字节
	// 上限落在第二个字中间（3+1）：必须退到 3
	got := truncateUTF8(s, 4)
	if !utf8.ValidString(got) {
		t.Fatalf("截断后必须是合法 UTF-8: %q", got)
	}
	if got != "中" {
		t.Fatalf("应退到最近的 rune 边界: %q（%d 字节）", got, len(got))
	}
	// 不足上限时原样返回（不产生拷贝语义变化）
	if got := truncateUTF8("abc", 10); got != "abc" {
		t.Fatalf("未超限应原样返回: %q", got)
	}
}
