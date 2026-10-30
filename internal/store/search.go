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

// SearchQuery 是历史检索的查询参数。
type SearchQuery = sessiondata.SearchQuery

// SearchLine 是命中上下文里的一条相邻消息。
type SearchLine = sessiondata.SearchLine

// searchClip 是命中内容的展示截断长度。
const searchClip = 120

// Search 在全部会话的消息内容里按正则搜索，按会话更新时间从近到远。
// 保持 Go 正则语义（与文件版行为一致）；FTS5 分词匹配留给语义记忆层。
//
// 返回 (命中, 总命中数, 错误)。**总命中数要单独数**：只回 len(命中) 的话
// 「共 N 条命中，显示前 M 条」那句提示永远不会出现（total 恒等于 len）——
// 模型就不知道「还有更多，该缩小 pattern」。
//
// role 过滤只作用于**命中判定**，不作用于上下文：过滤掉的行仍然会作为
// 相邻消息出现在上下文里（否则 role=user 时上下文里只有用户自己的话，
// 恰好丢掉最该看的助手答复）。
//
// 扫描成本护栏（全扫是 O(全部消息)，629MB 真库平均 4s+）：
//   - **限时**：超过 deadline（默认 2500ms）即停止扫描，已命中的正常返回，
//     timedOut=true 告知调用方「扫描未完整」——数「还有多少」的成本
//     不能无限搭上这一次调用；
//   - **总数封顶后限时**：超过 max 后仍数总数（「还有更多」是缩小 pattern
//     的依据），但**服从同一 deadline**——deadline 到了就停，total 是
//     「已扫到 N 条命中（可能更多）」，timedOut 让调用方如实转告。
//   - Session/Days/Workspace 过滤在 SQL 层裁剪扫描集（JOIN 子查询 WHERE）。
//
// **timedOut 是独立返回值而不是命中上的标记**：零命中 + 超时的场景只标
// 命中会丢（没有命中可标），而那恰恰是最误导的情况——「没有匹配」会被
// 模型当成全集，从此不再搜。
func (s *Store) Search(q SearchQuery) ([]SearchHit, int, bool, error) {
	re, err := regexp.Compile(q.Pattern)
	if err != nil {
		return nil, 0, false, fmt.Errorf("模式不是合法正则: %w", err)
	}
	max := q.Max
	if max <= 0 {
		max = 30
	}
	if max > 100 {
		max = 100
	}
	ctxN := q.Context
	if ctxN < 0 {
		ctxN = 0
	}
	if ctxN > maxSearchContext {
		ctxN = maxSearchContext
	}
	// 扫描时限：0 = 默认 2500ms（低于实测均值 4.07s，砍掉最慢尾部；
	// 负数视为 0 也走默认）。超时后停止扫描、已命中的正常返回。
	deadline := defaultSearchDeadline
	if q.DeadlineMS > 0 {
		deadline = time.Duration(q.DeadlineMS) * time.Millisecond
	}
	cutoff := time.Now().Add(deadline)

	// SQL 层过滤：session 精确匹配；days 限定最近 N 天的会话
	// （按 sessions.updated_at——消息没有 created_at 列，且会话级裁剪
	// 与既有 idx_sessions_updated 索引对得上）。
	scan := `SELECT m.session_id, m.role, m.content, s.title, s.updated_at
		 FROM messages m
		 JOIN (SELECT id, rowid AS srow, title, updated_at, workspace FROM sessions WHERE archived = 0
		       ORDER BY updated_at DESC, rowid DESC) s ON m.session_id = s.id`
	args := []any{}
	if q.Session != "" {
		scan += ` WHERE m.session_id = ?`
		args = append(args, q.Session)
	}
	if q.Days > 0 {
		cut := time.Now().AddDate(0, 0, -q.Days).Format(time.RFC3339Nano)
		if q.Session != "" {
			scan += ` AND s.updated_at >= ?`
		} else {
			scan += ` WHERE s.updated_at >= ?`
		}
		args = append(args, cut)
	}
	if q.Workspace != "" {
		// workspace 过滤（非空时）：与 Days 同款的三元拼接——已有 WHERE 就 AND。
		// 子会话在 CreateChild 时拷贝父的 workspace，按它过滤天然涵盖子会话。
		if q.Session != "" || q.Days > 0 {
			scan += ` AND s.workspace = ?`
		} else {
			scan += ` WHERE s.workspace = ?`
		}
		args = append(args, q.Workspace)
	}
	scan += ` ORDER BY s.srow DESC, m.seq` // 会话从近到远（srow 大 = 近），会话内按 seq
	rows, err := s.db.Query(scan, args...)
	if err != nil {
		return nil, 0, false, fmt.Errorf("搜索查询失败: %w", err)
	}
	defer rows.Close()

	// 流式扫描，同时维护两样东西来拼上下文（都不需要把全部消息读进内存）：
	//   prev[sid]  该会话最近 ctxN 条（供命中的「之前」用）
	//   fills[sid] 正在等「之后」上下文的命中（need 递减到 0 即摘掉）
	type fillTarget struct{ hitIdx, need int }
	prev := map[string][]SearchLine{}
	fills := map[string][]*fillTarget{}
	seqIn := map[string]int{}
	var hits []SearchHit
	total := 0
	timedOut := false
	for rows.Next() {
		if time.Now().After(cutoff) {
			timedOut = true
			break
		}
		var sid, role, content, title, updated string
		if err := rows.Scan(&sid, &role, &content, &title, &updated); err != nil {
			return nil, 0, false, fmt.Errorf("读搜索行失败: %w", err)
		}
		seqIn[sid]++
		line := SearchLine{Index: seqIn[sid], Role: role, Content: clipOneLine(content, searchClip)}
		matched := re.MatchString(content) && (q.Role == "" || q.Role == role)

		switch {
		case matched:
			total++
			// 超过上限后继续数总数（不早退）——「还有更多」是模型缩小
			// pattern 的依据，数不出来那句提示就是死的。但总数服从同一
			// deadline（见函数注释），超时后 total 是「已扫到 N 条+」。
			if len(hits) < max {
				h := SearchHit{
					SessionID: sid, SessionTitle: title, UpdatedAt: fmtTime(updated),
					Index: line.Index, Role: role, Content: line.Content,
				}
				if ctxN > 0 {
					// 前文 + 命中自身（复制一份，别与 prev 共享底层数组）
					h.Context = append(append([]SearchLine{}, prev[sid]...), line)
					fills[sid] = append(fills[sid], &fillTarget{hitIdx: len(hits), need: ctxN})
				}
				hits = append(hits, h)
			}
		case ctxN > 0:
			// 非命中行补进正在等「之后」上下文的命中。
			// 命中行不补（它自己会作为命中出现），避免同一行重复两遍。
			kept := fills[sid][:0]
			for _, t := range fills[sid] {
				hits[t.hitIdx].Context = append(hits[t.hitIdx].Context, line)
				t.need--
				if t.need > 0 {
					kept = append(kept, t)
				}
			}
			fills[sid] = kept
		}

		if ctxN > 0 {
			buf := append(prev[sid], line)
			if len(buf) > ctxN {
				buf = buf[len(buf)-ctxN:]
			}
			prev[sid] = buf
		}
	}
	if err := rows.Err(); err != nil && !timedOut {
		return nil, 0, false, fmt.Errorf("扫描搜索行失败: %w", err)
	}
	return hits, total, timedOut, nil
}

// defaultSearchDeadline 是会话搜索的默认扫描时限。
// 629MB 真库实测平均 4.07s、最长 12.3s——2500ms 砍掉最慢尾部，
// 保留大部分完整扫描（小库远不到这个数，恒不受影响）。
const defaultSearchDeadline = 2500 * time.Millisecond

// maxSearchContext 是每条命中上下文条数的上限（前后各 N 条）。
// 上下文成倍放大输出（每条命中多 2N 行），不设上限时模型传个 100 就能
// 把整段历史灌进上下文。
const maxSearchContext = 5

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
// FormatSearchHits 把命中渲染成给模型的文本（session_search 工具的输出）。
// scope 是范围说明（空 = 无），见 sessiondata.FormatSearchHits。
func FormatSearchHits(hits []SearchHit, total int, timedOut bool, scope string) string {
	return sessiondata.FormatSearchHits(hits, total, timedOut, scope)
}
