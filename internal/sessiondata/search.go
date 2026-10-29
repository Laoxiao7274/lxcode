package sessiondata

import (
	"fmt"
	"strings"
)

// FormatSearchHits 将业务命中转换成工具文本，不依赖数据库格式。
//
// 两种形态：**无上下文**时保持一行一条的紧凑形态（[会话 #序号 角色] 内容）；
// **有上下文**时命中单独起一行（>>> 标记），前后文按时间顺序缩进列出——
// 模型据此一眼看出「问题在哪、答案在哪」。
func FormatSearchHits(hits []SearchHit, total int) string {
	if len(hits) == 0 {
		return "没有匹配的历史消息。"
	}
	var b strings.Builder
	for _, h := range hits {
		if len(h.Context) == 0 {
			fmt.Fprintf(&b, "[%s #%d %s] %s\n", hitHeader(h), h.Index, h.Role, h.Content)
			continue
		}
		fmt.Fprintf(&b, "[%s]\n", hitHeader(h))
		for _, c := range h.Context {
			mark := "    "
			if c.Index == h.Index && c.Role == h.Role {
				mark = ">>> " // 命中自身
			}
			fmt.Fprintf(&b, "%s#%d %s %s\n", mark, c.Index, c.Role, c.Content)
		}
	}
	if total > len(hits) {
		fmt.Fprintf(&b, "\n…（共 %d 条命中，显示前 %d 条：缩小 pattern 或提高 max）", total, len(hits))
	}
	return b.String()
}

// hitHeader 渲染命中的会话标识：id · 标题 · 更新时间。
// 标题为空（老库或极短会话）时省略该段，不留一个空的分隔符。
func hitHeader(h SearchHit) string {
	parts := []string{h.SessionID}
	if strings.TrimSpace(h.SessionTitle) != "" {
		parts = append(parts, strings.TrimSpace(h.SessionTitle))
	}
	if strings.TrimSpace(h.UpdatedAt) != "" {
		parts = append(parts, strings.TrimSpace(h.UpdatedAt))
	}
	return strings.Join(parts, " · ")
}
