// 控制台输出编码归一（任务 C 的低风险修复，2026-12）。
//
// 问题（真库取证，见 AGENTS.md §5 坑 14）：Windows 上 console 程序的输出编码
// 跟着**控制台代码页**走，中文系统默认是 936（GBK）。我们的后端是 GUI/服务形态
// 进程，子进程拿到的是 CREATE_NO_WINDOW 新建的隐藏控制台，代码页同样是 936——
// 于是 `ipconfig` / `tasklist` / `powershell -Command "..."` 里的中文全是 GBK
// 字节。`string(out)` 把它们当成 UTF-8 直接拼进工具结果，再经 JSON 序列化变成
// 一串 `����`：模型看不懂输出，只能盲目重试（真库统计：354 条 bash 工具结果是
// 非法 UTF-8，其中 57 条含 U+FFFD，56/57 条紧接着又发了一条 bash 调用）。
//
// 修复边界（刻意窄）：
//   - **只在输出不是合法 UTF-8 时**动手；合法 UTF-8 逐字节原样返回（git/node/go
//     这些本来就输出 UTF-8 的程序完全不受影响，零回归面）；
//   - **按行判定**：混合输出（前半 ASCII/UTF-8、后半 GBK）逐行处理，不整段重解码；
//   - **不改变执行的命令**：不做 `chcp 65001` 前缀（那会让确认卡展示的命令与
//     实际执行的命令不一致，且对「无视代码页直接写 GBK」的程序无效）。
package tools

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// normalizeConsoleOutput 把子进程输出转成合法 UTF-8 文本。
//
// 合法 UTF-8 直接返回（最常见路径，零拷贝判断）；非法段落按平台解码：
// Windows 按 GBK（CP936）解码，其他平台用 U+FFFD 替换坏字节。
func normalizeConsoleOutput(out []byte) string {
	if len(out) == 0 {
		return ""
	}
	if utf8.Valid(out) {
		return string(out)
	}
	// 逐行处理（保留行尾）：一行是纯 ASCII/UTF-8 就原样过，只有坏行才解码——
	// 整段重解码会把「UTF-8 正文 + 一行 GBK 报错」这种混合输出一起搅坏。
	var b strings.Builder
	b.Grow(len(out))
	for len(out) > 0 {
		i := bytes.IndexByte(out, '\n')
		var line []byte
		if i < 0 {
			line, out = out, nil
		} else {
			line, out = out[:i+1], out[i+1:]
		}
		if utf8.Valid(line) {
			b.Write(line)
			continue
		}
		b.WriteString(decodeLegacyText(line))
	}
	return b.String()
}

// truncateUTF8 按字节上限截断，但**退到 rune 边界**——直接 s[:n] 会把一个多字节
// 字符劈成两半，产出的又是非法 UTF-8（与本文件要修的编码问题同一后果：JSON 序列化
// 成 U+FFFD、模型读到乱码）。截断只发生在超长输出上，代价是一次 ≤3 字节的回退。
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
