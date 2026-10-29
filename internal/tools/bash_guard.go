package tools

import (
	"strings"
)

// bashGuardPatterns 是高危命令模式表：模式名 → 命令词序列（与段内首命令展开后逐词前缀匹配）。
// 匹配发生在小写化与按空白切分之后。
var bashGuardPatterns = []struct {
	name string
	args []string
}{
	{name: "git push", args: []string{"git", "push"}},
	// 前两条按首词前缀匹配，会连带拦下「rm -rf ~backup」这类以 ~ 或 / 开头的
	// 变体——已知取舍（宁可多拦）。
	{name: "rm -rf（家目录/系统根/通配）", args: []string{"rm", "-rf", "~"}},
	{name: "rm -rf（系统根/通配）", args: []string{"rm", "-rf", "/"}},
	{name: "rm -rf（家目录/系统根/通配）", args: []string{"rm", "-rf", "/root"}},
	{name: "rm -rf（家目录/系统根/通配）", args: []string{"rm", "-rf", "/home"}},
	{name: "reg add", args: []string{"reg", "add"}},
	{name: "reg delete", args: []string{"reg", "delete"}},
	{name: "mkfs", args: []string{"mkfs"}},
	{name: "format", args: []string{"format"}},
	{name: "diskpart", args: []string{"diskpart"}},
	{name: "remove-item -recurse -force", args: []string{"remove-item", "-recurse", "-force"}},
	{name: "remove-item -recurse -force", args: []string{"remove-item", "-force", "-recurse"}},
	{name: "npm uninstall -g", args: []string{"npm", "uninstall", "-g"}},
	{name: "pip uninstall", args: []string{"pip", "uninstall"}},
}

// HighRiskBashCommand 检查一条 bash 命令串是否命中高危模式。
// 命中返回（模式名, true）；否则返回 ("", false)。
// 命令串按 &&、;、|、换行拆段，逐段检查首命令词（跳过前导空格与环境变量赋值）。
// echo/printf 的参数不算命中——它们的正文按字面输出，不执行。
func HighRiskBashCommand(cmd string) (string, bool) {
	for _, seg := range splitBashSegments(cmd) {
		if name, ok := matchSegment(seg); ok {
			return name, true
		}
	}
	return "", false
}

// splitBashSegments 把命令串按 &&、;、|、换行拆段（不做引号内拆分——引号里的
// && ; | 是参数内容，不该改变分段；引号配对不全时按原样从断点切）。
func splitBashSegments(cmd string) []string {
	segs := make([]string, 0, 4)
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case '\n':
			segs = append(segs, cur.String())
			cur.Reset()
		case ';', '|':
			segs = append(segs, cur.String())
			cur.Reset()
		case '&':
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				segs = append(segs, cur.String())
				cur.Reset()
				i++ // 跳过第二个 &
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	segs = append(segs, cur.String())
	return segs
}

// matchSegment 检查单个段：取首命令词序列（小写化），匹配模式表。
func matchSegment(seg string) (string, bool) {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return "", false
	}
	fields := strings.Fields(seg)
	if len(fields) == 0 {
		return "", false
	}
	// 小写化每个词
	lowered := make([]string, len(fields))
	for i, f := range fields {
		lowered[i] = strings.ToLower(f)
	}
	// 跳过前导环境变量赋值（VAR=value 形式）
	start := 0
	for start < len(lowered) && isEnvAssignment(lowered[start]) {
		start++
	}
	if start >= len(lowered) {
		return "", false
	}
	// echo/printf 的参数不算命中：如果首命令是 echo/printf，整段跳过
	if lowered[start] == "echo" || lowered[start] == "printf" {
		return "", false
	}
	for _, p := range bashGuardPatterns {
		if len(p.args) > len(lowered)-start {
			continue
		}
		match := true
		for i, a := range p.args {
			got := lowered[start+i]
			if i == len(p.args)-1 {
				// 末词前缀匹配：容忍参数后缀（rm -rf ~/ 与 ~/path、mkfs 与 mkfs.ext4）
				if !strings.HasPrefix(got, a) {
					match = false
					break
				}
			} else if got != a {
				match = false
				break
			}
		}
		if match {
			return p.name, true
		}
	}
	return "", false
}

// isEnvAssignment 判定一个词是否是 VAR=value 形式的环境变量赋值（首命令前的前缀）。
func isEnvAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	// = 之前必须是合法变量名字符（字母/数字/下划线），且不以数字开头
	if !isIdentByte(word[0]) {
		return false
	}
	for i := 0; i < eq; i++ {
		if !isIdentByte(word[i]) {
			return false
		}
	}
	return true
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
