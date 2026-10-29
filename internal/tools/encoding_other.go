//go:build !windows

package tools

import "strings"

// decodeLegacyText 在非 Windows 平台没有「控制台代码页」这回事（POSIX 终端的
// 输出按 locale 编码，实际几乎总是 UTF-8）。走到这里说明字节确实不是合法
// UTF-8（二进制混入、截断的多字节序列），用 U+FFFD 替换坏字节——绝不把坏
// 字节原样交给 JSON 序列化。
func decodeLegacyText(b []byte) string {
	return strings.ToValidUTF8(string(b), "\uFFFD")
}
