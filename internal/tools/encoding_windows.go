//go:build windows

package tools

import (
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// cpACP 是 Windows 的「本机 ANSI 代码页」（中文系统 = 936/GBK）。
// x/sys/windows 没有导出这个常量（它只导出 MultiByteToWideChar 本身），
// 值按 Win32 文档固定为 0。
const cpACP = 0

// decodeLegacyText 把一段非 UTF-8 字节按**本机 ANSI 代码页**（中文系统 = 936/GBK）
// 解码成 UTF-8 文本。
//
// 为什么用 MultiByteToWideChar + CP_ACP 而不是自己写 GBK 表：
//   - 零新增依赖（`golang.org/x/sys/windows` 已是直接依赖），也不用把 GB18030
//     的映射表（几百 KB）抄进仓库；
//   - CP_ACP 就是「这个进程实际用的代码页」，非中文系统（西欧 1252、俄语 866）
//     自动跟着对——写死 GBK 只在中文机器上对；
//   - 失败时回落 U+FFFD 替换（`strings.ToValidUTF8`），绝不 panic、绝不返回坏字节。
func decodeLegacyText(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	n, err := windows.MultiByteToWideChar(cpACP, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return strings.ToValidUTF8(string(b), "\uFFFD")
	}
	buf := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cpACP, 0, &b[0], int32(len(b)), &buf[0], n); err != nil {
		return strings.ToValidUTF8(string(b), "\uFFFD")
	}
	return string(utf16.Decode(buf))
}
