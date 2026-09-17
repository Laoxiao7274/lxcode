package sessiondata

import (
	"fmt"
	"strings"
)

// FormatSearchHits 将业务命中转换成工具文本，不依赖数据库格式。
func FormatSearchHits(hits []SearchHit, total int) string {
	if len(hits) == 0 {
		return "没有匹配的历史消息。"
	}
	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "[%s #%d %s] %s\n", h.SessionID, h.Index, h.Role, h.Content)
	}
	if total > len(hits) {
		fmt.Fprintf(&b, "\n…（共 %d 条命中，显示前 %d 条：缩小 pattern 或提高 max）", total, len(hits))
	}
	return b.String()
}
