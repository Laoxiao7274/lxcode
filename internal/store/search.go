// 历史会话全文检索：跨会话搜消息正文（会话搜索先行，语义记忆待做）。

package store

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// SearchHit 是一条会话搜索命中。
type SearchHit = sessiondata.SearchHit

// searchClip 是命中内容的展示截断长度。
const searchClip = 120

// Search 在全部会话的消息内容里按正则搜索，按会话更新时间从近到远。
// 保持 Go 正则语义（与文件版行为一致）；FTS5 分词匹配留给语义记忆层。
func (s *Store) Search(pattern string, max int) ([]SearchHit, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("模式不是合法正则: %w", err)
	}
	if max <= 0 {
		max = 30
	}
	if max > 100 {
		max = 100
	}
	rows, err := s.db.Query(
		`SELECT m.session_id, m.role, m.content
		 FROM messages m
		 JOIN (SELECT id, rowid AS srow FROM sessions WHERE archived = 0
		       ORDER BY updated_at DESC, rowid DESC) s ON m.session_id = s.id
		 ORDER BY s.srow DESC, m.seq`) // 会话从近到远（srow 大 = 近），会话内按 seq
	if err != nil {
		return nil, fmt.Errorf("搜索查询失败: %w", err)
	}
	defer rows.Close()
	var hits []SearchHit
	seqIn := map[string]int{} // 会话内消息序号（1 起）
	for rows.Next() {
		var sid, role, content string
		if err := rows.Scan(&sid, &role, &content); err != nil {
			return nil, fmt.Errorf("读搜索行失败: %w", err)
		}
		seqIn[sid]++
		if !re.MatchString(content) {
			continue
		}
		hits = append(hits, SearchHit{
			SessionID: sid, Index: seqIn[sid], Role: role,
			Content: clipOneLine(content, searchClip),
		})
		if len(hits) >= max {
			break
		}
	}
	return hits, rows.Err()
}

// clipTitle 截标题：一行以内、40 个字符封顶（列表预览用）。
func clipTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// clipOneLine 截断到 n 个字符（rune 安全），尾部加省略号。
func clipOneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// fmtTime 把库内时间戳（RFC3339Nano）格式化为列表展示形态。
func fmtTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts // 解析失败原样返回（新格式不该失败；旧数据无）
	}
	return t.Format("2006-01-02 15:04")
}

// FormatSearchHits 把命中渲染成给模型的文本（session_search 工具的输出）。
func FormatSearchHits(hits []SearchHit, total int) string {
	return sessiondata.FormatSearchHits(hits, total)
}
