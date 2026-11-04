// edit_hint.go：edit 失败治理（2026-12 P6）——old_string 未匹配时回带
// 「文件中最接近的几行」。
//
// 为什么只带行段而不是把整份文件塞回去：失败后模型最需要的是「我的 old_string
// 差在哪」——多数失败是缩进/空行/全半角的细微出入（568 次「读过文件没变还是
// 抄错」），把最像的原文行摆在眼前，模型直接照抄就能修好，不需要再付一次
// read_file 往返（失败后 46% 转去 bash 而不是 read_file，就是绕这个往返）。
//
// 相似度为什么用「公共前缀 + 公共子串」而不是编辑距离：old_string 与候选行段
// 的差异通常是一两个字符（缩进数量、引号全半角），最长公共前缀对这类差异最
// 敏感（Levenshtein 对「首字符就差一位缩进」不敏感——差 1 与差 5 只差一位）；
// 公共子串兜住「中段抄错、首尾对」的形态。两个分数简单相加归一即可——这里
// 不做语义相似度，够把「最像的那一行」排到第一位就行。
package tools

import (
	"fmt"
	"strings"
)

// editNearestLines 是回带行段的数量上限：3 段——太多会稀释「最像的那行」的
// 信号，太少会在 old_string 跨多行时给出不足的对照。
const editNearestLines = 3

// nearestLinesForEdit 从文件内容里找出与 old 最像的至多 3 个行段（含行号）。
// 候选是文件的每一行（含前导空白原样保留——缩进差异正是要暴露的）；
// 完全无相似（全 0 分）时不硬凑，返回「（没有相似的行——old_string 可能
// 来自记忆或别的文件）」。空文件/空 old 不会走到这里（edit 已先行报错）。
func nearestLinesForEdit(content, old string) string {
	// 候选窗口：old 是多行时，以它的首行内容为锚在文件里找行段——
	// 跨行 old_string 的失败几乎总是首行先错（缩进/可见差异都暴露在那里）。
	oldFirst := firstLine(old)
	lines := strings.Split(content, "\n")

	type cand struct {
		no    int // 1 起的行号
		text  string
		score int
	}
	cands := make([]cand, 0, len(lines))
	for i, ln := range lines {
		score := similarityScore(ln, oldFirst)
		if score <= 0 {
			continue
		}
		cands = append(cands, cand{no: i + 1, text: ln, score: score})
	}
	if len(cands) == 0 {
		return "（文件里没有与 old_string 首行相似的行——它可能来自记忆或别的文件，务必先 read_file）"
	}
	// 稳定排序（同分按行号）：分数高的在前，截 editNearestLines 段
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0; j-- {
			if cands[j].score > cands[j-1].score ||
				(cands[j].score == cands[j-1].score && cands[j].no < cands[j-1].no) {
				cands[j], cands[j-1] = cands[j-1], cands[j]
			} else {
				break
			}
		}
	}
	if len(cands) > editNearestLines {
		cands = cands[:editNearestLines]
	}
	parts := make([]string, len(cands))
	for i, c := range cands {
		parts[i] = fmt.Sprintf("[行 %d] %s", c.no, c.text)
	}
	return strings.Join(parts, " / ")
}

// firstLine 取 s 的第一行（去换行符）。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSuffix(s[:i], "\r")
	}
	return strings.TrimSuffix(s, "\r")
}

// similarityScore 给「候选行」与「old 首行」打分：最长公共前缀 ×2 +
// 最长公共子串。两者都按字节计——代码文件的缩进/关键字差异都在 ASCII 区，
// 中文的逐字节比较对「差一个字」同样给正分，够排序用。
func similarityScore(candidate, old string) int {
	if old == "" || candidate == "" {
		return 0
	}
	// 最长公共前缀
	prefix := 0
	for prefix < len(candidate) && prefix < len(old) && candidate[prefix] == old[prefix] {
		prefix++
	}
	return prefix*2 + longestCommonSubstring(candidate, old)
}

// longestCommonSubstring 是 O(len×len) 的经典 DP（行级输入很短，不值得
// 上后缀结构）。
func longestCommonSubstring(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	prev := make([]int, len(b)+1)
	best := 0
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			}
		}
		prev = cur
	}
	return best
}
