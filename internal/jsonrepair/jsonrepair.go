// Package jsonrepair 保守修复大模型给出的坏 JSON 参数。
//
// 为什么单独成包：这份修复有三个使用方，分属不同层——tools（执行前修参数）、
// agent（写历史前保证参数合法）、llm（组装请求前兜底，anthropic 适配器对此是
// 硬校验，一条坏参数会让**整个会话永久发不出请求**）。tools 已经依赖 llm，所以
// 实现不能放在 tools 里被 llm 反向引用，抽成只依赖标准库的叶子包最干净。
//
// 只做"几乎确定模型本意"的变换，不做语义猜测：裸换行/制表符、尾逗号、单引号包
// key、被 max_tokens 截断（补右侧引号与括号）。非法转义（如 Windows 路径写成
// 单个反斜杠产生 `\U`）修不了——那类只能由调用方兜底。
package jsonrepair

import (
	"encoding/json"
	"strings"
)

// Repair 尝试把坏 JSON 修成合法 JSON，成功返回修复后的文本。
//
// 候选顺序对应真实失败形态：裸换行/裸制表符（多行命令最常见）、单引号包字符串、
// 被 max_tokens 截断（补右侧括号），以及它们的组合。
func Repair(raw string) (string, bool) {
	candidates := []string{
		fixRawWhitespace(raw), // 字符串内裸换行/制表符 → \n \t，并去掉尾逗号
		fixSingleQuotes(raw),  // 单引号字符串 → 双引号
		closeTruncated(raw),   // 截断的 JSON：补右括号/右引号
		closeTruncated(fixRawWhitespace(raw)),
		closeTruncated(fixSingleQuotes(raw)),
	}
	for _, c := range candidates {
		if c != "" && json.Valid([]byte(c)) {
			return c, true
		}
	}
	return "", false
}

// fixRawWhitespace 把字符串字面量内部的裸换行/制表符转义，并删掉对象/数组
// 的尾逗号。扫描式实现：跟踪是否处于字符串内与是否被反斜杠转义。
func fixRawWhitespace(s string) string {
	var b strings.Builder
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
			b.WriteByte(c)
		case c == '\\' && inStr:
			esc = true
			b.WriteByte(c)
		case c == '"':
			inStr = !inStr
			b.WriteByte(c)
		case inStr && c == '\n':
			b.WriteString(`\n`)
		case inStr && c == '\r':
			// 归一：CRLF 视作一个换行
			if i+1 < len(s) && s[i+1] == '\n' {
				continue
			}
			b.WriteString(`\n`)
		case inStr && c == '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	out := stripTrailingCommas(b.String())
	return out
}

// stripTrailingCommas 删除 `,` 后紧跟 `}` 或 `]` 的尾逗号（JSON 规范不允许）。
func stripTrailingCommas(s string) string {
	var b strings.Builder
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
			b.WriteByte(c)
		case c == '\\' && inStr:
			esc = true
			b.WriteByte(c)
		case c == '"':
			inStr = !inStr
			b.WriteByte(c)
		case !inStr && c == ',':
			// 向前看下一个非空白字符
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\n' || s[j] == '\t' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue // 丢弃这个逗号
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// fixSingleQuotes 把单引号包起来的字符串改成双引号（模型偶尔用 JS/Python 习惯）。
// 只在整体不含双引号时启用，避免误伤正常 JSON 里的撇号。
func fixSingleQuotes(s string) string {
	if strings.Contains(s, `"`) || !strings.Contains(s, "'") {
		return ""
	}
	return strings.ReplaceAll(s, "'", `"`)
}

// closeTruncated 处理被 max_tokens 截断的 JSON：先补未闭合的字符串引号，
// 再按栈补右括号。返回空串表示没有可补的东西（例如整体不是 JSON 开头）。
func closeTruncated(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || (s[0] != '{' && s[0] != '[') {
		return ""
	}
	// 先去掉可能残留的半个键值（截断点常落在 `"command": "ls` 这类中间）
	inStr := false
	esc := false
	stack := []byte{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case !inStr && (c == '{' || c == '['):
			stack = append(stack, c)
		case !inStr && (c == '}' || c == ']'):
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	out := s
	if inStr {
		out += `"` // 补上被截断的字符串引号
	}
	// 截断常留一个没有值的 key 或悬空逗号，补个占位值让结构合法
	trimmed := strings.TrimRight(out, " \t\r\n")
	if strings.HasSuffix(trimmed, ",") {
		out = trimmed
	} else if strings.HasSuffix(trimmed, ":") {
		out = trimmed + `""`
	} else if n := len(stack); n > 0 && strings.HasSuffix(trimmed, "{") {
		out = trimmed
	}
	out = stripTrailingCommas(out)
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			out += "}"
		} else {
			out += "]"
		}
	}
	return out
}
