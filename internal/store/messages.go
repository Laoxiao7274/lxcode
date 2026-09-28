// 消息读写与压缩检查点：写入、回放（Load/Latest）、影子区间与检查点落库。
// 回放只认 shadowed_seqs（影子掉的 seq 集合）——检查点按「落在它影子段原本
// 占据的位置上」重建，见 AGENTS.md §2.2。

package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/moyunteng/lxcode/internal/llm"
)

// AppendMsg 把一条消息追加到会话（事务：INSERT 消息 + UPDATE 会话时间戳）。
func (s *Store) AppendMsg(id string, m llm.Message) error {
	toolCalls, err := json.Marshal(m.ToolCalls)
	if err != nil {
		return fmt.Errorf("序列化工具调用失败: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op
	// seq = 当前会话最大 seq + 1（单会话写入串行，无竞态窗口）
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?`, id).Scan(&seq); err != nil {
		return fmt.Errorf("取序号失败: %w", err)
	}
	// 标题懒维护：首条 user 消息截断（写入时算好，List 零计算）
	if m.Role == "user" {
		var title string
		if err := tx.QueryRow(`SELECT title FROM sessions WHERE id = ?`, id).Scan(&title); err != nil {
			return fmt.Errorf("读标题失败: %w", err)
		}
		if title == "" {
			if _, err := tx.Exec(`UPDATE sessions SET title = ? WHERE id = ?`, clipTitle(m.Content), id); err != nil {
				return fmt.Errorf("写标题失败: %w", err)
			}
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, seq, m.Role, m.Content, m.ReasoningContent, m.ReasoningSignature, string(toolCalls), m.ToolCallID,
	); err != nil {
		return fmt.Errorf("写消息失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, nowNano(), id); err != nil {
		return fmt.Errorf("更新会话时间失败: %w", err)
	}
	return tx.Commit()
}

// Load 读出会话的全部消息（按 seq 升序）。会话不存在时报错
// （调用方 SwitchTo 依赖此语义区分「空会话」与「不存在」）。
func (s *Store) Load(id string) ([]llm.Message, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("会话 %s 不存在", id)
		}
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	return s.loadSurface(id)
}

// loadSurface 读会话的**当前历史**：跳过被压缩检查点影子覆盖的行，并把历史
// 顺序还原成 surface 顺序（见 surfaceRows）。
//
// 与 surfaceRows 共用同一份实现：这两条路径（回放 / 落库时算存活集）必须
// 逐字一致——判定漂移的代价是「写进去的影子区间与读出来的历史对不上」。
func (s *Store) loadSurface(id string) ([]llm.Message, error) {
	rows, err := s.readRows(id)
	if err != nil {
		return nil, err
	}
	surface := surfaceRows(rows)
	msgs := make([]llm.Message, 0, len(surface))
	for _, r := range surface {
		msgs = append(msgs, r.msg)
	}
	return msgs, nil
}

// rowData 是一行消息（含压缩检查点的影子信息）。
type rowData struct {
	seq        int
	msg        llm.Message
	checkpoint bool
	shadowed   []int // 检查点影子掉的 seq 集合（权威；空 = 不是检查点）
}

// readRows 读会话全部消息行（按 seq 升序）。
func (s *Store) readRows(id string) ([]rowData, error) {
	rows, err := s.db.Query(
		`SELECT seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		        checkpoint, shadowed_seqs
		 FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	defer rows.Close()
	var out []rowData
	for rows.Next() {
		var r rowData
		var toolCalls, shadowedSeqs string
		var cp int
		if err := rows.Scan(&r.seq, &r.msg.Role, &r.msg.Content, &r.msg.ReasoningContent,
			&r.msg.ReasoningSignature, &toolCalls, &r.msg.ToolCallID, &cp, &shadowedSeqs); err != nil {
			return nil, fmt.Errorf("读消息行失败: %w", err)
		}
		if toolCalls != "" && toolCalls != "[]" {
			if err := json.Unmarshal([]byte(toolCalls), &r.msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("解析工具调用失败: %w", err)
			}
		}
		r.checkpoint = cp == 1
		if r.checkpoint && shadowedSeqs != "" && shadowedSeqs != "[]" {
			if err := json.Unmarshal([]byte(shadowedSeqs), &r.shadowed); err != nil {
				return nil, fmt.Errorf("解析影子区间失败: %w", err)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// shadowSet 把所有检查点的影子区间取并集（权威判定：seq 在集合里 = 已被压缩掉）。
func shadowSet(rows []rowData) map[int]struct{} {
	set := make(map[int]struct{})
	for _, r := range rows {
		if !r.checkpoint {
			continue
		}
		for _, seq := range r.shadowed {
			set[seq] = struct{}{}
		}
	}
	return set
}

// surfaceRows 返回当前历史（存活行）**按历史顺序**：检查点落在它影子段原本占据
// 的位置上，其余按 seq 升序。
//
// 为什么检查点不是"一律排最前"：检查点行是**追加在末尾**的（seq 最大），但它顶替
// 的是被影子那一段在历史里的位置（DSH 的 surface position）。插入锚点 = 影子集合
// 里的最小 seq（被影子段的第一条）；无影子集合（count=0 的退化检查点）时回落自身
// seq，即落尾。压缩区间是 [skip, skip+count)——主会话恒为前缀（skip=0）所以锚点落
// 在库内最小 seq 上、检查点仍在第一位（与"一律排最前"的老行为逐字节一致）；子会话
// 保护了头部的任务说明书（skip=1）时，检查点就落在任务消息之后。
func surfaceRows(rows []rowData) []rowData {
	shadowed := shadowSet(rows)
	var checkpoints, others []rowData
	for _, r := range rows {
		if _, hit := shadowed[r.seq]; hit {
			continue
		}
		if r.checkpoint {
			checkpoints = append(checkpoints, r)
		} else {
			others = append(others, r)
		}
	}
	if len(checkpoints) == 0 {
		return others
	}
	// 按**锚点**排序（不是自身 seq）：检查点行总是追加在末尾，而它顶替的是被影子
	// 段的位置——两次压缩可以"后来的替换更靠前的一段"（先压中间、再回头压头部），
	// 那时按 seq 排会把两份摘要的先后搞反。同锚点（影子同一段起点的多次压缩）按
	// 自身 seq 升序，即更晚写的那份排后。
	slices.SortStableFunc(checkpoints, func(a, b rowData) int {
		if d := checkpointAnchor(a) - checkpointAnchor(b); d != 0 {
			return d
		}
		return a.seq - b.seq
	})
	out := make([]rowData, 0, len(checkpoints)+len(others))
	ci := 0
	for _, r := range others {
		for ci < len(checkpoints) && checkpointAnchor(checkpoints[ci]) < r.seq {
			out = append(out, checkpoints[ci])
			ci++
		}
		out = append(out, r)
	}
	return append(out, checkpoints[ci:]...)
}

// checkpointAnchor 是检查点在历史里的插入锚点：它影子段的第一条 seq。被影子的行
// 都在它之后（或就是它本身），所以"插在第一个 seq 更大的存活行之前"就是把检查点
// 放回被替换段原本的位置。无影子集合时回落自身 seq（落尾）。
func checkpointAnchor(r rowData) int {
	if len(r.shadowed) == 0 {
		return r.seq
	}
	anchor := r.shadowed[0]
	for _, seq := range r.shadowed[1:] {
		if seq < anchor {
			anchor = seq
		}
	}
	return anchor
}

// AppendCheckpoint 追加一条压缩检查点：它替换（影子）当前历史里从第 skip 条起的
// count 条。区间越界时报错——那说明落盘落后于内存（某次写入失败过），此时写一个
// 错的影子区间会让回放丢掉不该丢的历史；压缩必须失败而不是写错数据。
//
// 为什么按"存活集里的位置"说话（而不是 seq 区间）：agent 层不见 seq，它只知道
// 自己的历史下标；skip/count 就是它选出的可压区间 [skip, skip+count)。主会话恒为
// skip=0（压缩区间是前缀，见 selectCompactRange）；子会话保护了头部的任务说明书，
// 于是 skip=1（那条任务消息留在历史里，摘要从它之后开始）。
func (s *Store) AppendCheckpoint(id string, m llm.Message, skip, count int) error {
	toolCalls, err := json.Marshal(m.ToolCalls)
	if err != nil {
		return fmt.Errorf("序列化工具调用失败: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op

	rows, err := s.readRowsTx(tx, id)
	if err != nil {
		return err
	}
	surface := surfaceRows(rows)
	var shadowedSeqs []int
	var shadStart, shadEnd int
	if count > 0 {
		if skip < 0 || skip+count > len(surface) {
			return fmt.Errorf("落盘落后于内存：当前历史只有 %d 条，需要影子 [%d, %d)（拒绝写错的影子区间）",
				len(surface), skip, skip+count)
		}
		shadowedSeqs = make([]int, 0, count)
		for _, r := range surface[skip : skip+count] {
			shadowedSeqs = append(shadowedSeqs, r.seq)
		}
		// 区间边界按"历史位置"记：起点 = 被替换段的第一条，终点 = 被替换段的最后一条
		shadStart, shadEnd = surface[skip].seq, surface[skip+count-1].seq
	}

	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?`, id).Scan(&seq); err != nil {
		return fmt.Errorf("取序号失败: %w", err)
	}
	seqJSON, err := json.Marshal(shadowedSeqs)
	if err != nil {
		return fmt.Errorf("序列化影子区间失败: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		                       checkpoint, shadow_start_seq, shadow_end_seq, shadowed_seqs)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', 1, ?, ?, ?)`,
		id, seq, m.Role, m.Content, m.ReasoningContent, m.ReasoningSignature, string(toolCalls),
		shadStart, shadEnd, string(seqJSON),
	); err != nil {
		return fmt.Errorf("写检查点失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, nowNano(), id); err != nil {
		return fmt.Errorf("更新会话时间失败: %w", err)
	}
	return tx.Commit()
}

// readRowsTx 是 readRows 的事务版（落库要在同一事务里读存活集）。
func (s *Store) readRowsTx(tx *sql.Tx, id string) ([]rowData, error) {
	rows, err := tx.Query(
		`SELECT seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		        checkpoint, shadowed_seqs
		 FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	defer rows.Close()
	var out []rowData
	for rows.Next() {
		var r rowData
		var toolCalls, shadowedSeqs string
		var cp int
		if err := rows.Scan(&r.seq, &r.msg.Role, &r.msg.Content, &r.msg.ReasoningContent,
			&r.msg.ReasoningSignature, &toolCalls, &r.msg.ToolCallID, &cp, &shadowedSeqs); err != nil {
			return nil, fmt.Errorf("读消息行失败: %w", err)
		}
		if toolCalls != "" && toolCalls != "[]" {
			if err := json.Unmarshal([]byte(toolCalls), &r.msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("解析工具调用失败: %w", err)
			}
		}
		r.checkpoint = cp == 1
		if r.checkpoint && shadowedSeqs != "" && shadowedSeqs != "[]" {
			if err := json.Unmarshal([]byte(shadowedSeqs), &r.shadowed); err != nil {
				return nil, fmt.Errorf("解析影子区间失败: %w", err)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
