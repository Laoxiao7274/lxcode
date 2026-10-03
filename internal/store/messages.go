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

// AppendMsg 把一条消息追加到会话（事务：INSERT 消息 + UPDATE 会话时间戳），
// 返回 store 分配的序号（messages.seq，从 1 起）。
//
// 为什么要把序号回给调用方：前端要拿它当撤回锚点（chat.rewind 的 seq），而会话历史
// 有两条给前端的路径（chat.history 回放 / chat.userMessage 实时）——序号必须由**同一个
// 持有者**（这里）写进消息，两边各算一遍必然漂移（AGENTS.md §2.2 的同类教训：
// 同一屏两个数字互相矛盾）。
func (s *Store) AppendMsg(id string, m llm.Message) (int64, error) {
	toolCalls, err := json.Marshal(m.ToolCalls)
	if err != nil {
		return 0, fmt.Errorf("序列化工具调用失败: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op
	// seq = 当前会话最大 seq + 1（单会话写入串行，无竞态窗口）
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?`, id).Scan(&seq); err != nil {
		return 0, fmt.Errorf("取序号失败: %w", err)
	}
	// 标题懒维护：首条 user 消息截断（写入时算好，List 零计算）
	if m.Role == "user" {
		var title string
		if err := tx.QueryRow(`SELECT title FROM sessions WHERE id = ?`, id).Scan(&title); err != nil {
			return 0, fmt.Errorf("读标题失败: %w", err)
		}
		if title == "" {
			if _, err := tx.Exec(`UPDATE sessions SET title = ? WHERE id = ?`, clipTitle(m.Content), id); err != nil {
				return 0, fmt.Errorf("写标题失败: %w", err)
			}
		}
	}
	// 每轮生成的簿记（计时/用量/模型）随消息一起落库：刷新后 chat.history 回放的
	// 必须是同一份数字（只放内存的话用户一刷新就没了，而 live 与 replay 分叉正是
	// 本仓库反复踩过的坑）。
	// usage_split 一律写 1：写这一行的是**认识拆分口径**的二进制（usage_tokens 真的是
	// 输出、输入侧三桶另记）。旧二进制写的行走列默认值 0 = 老口径（那时 usage_tokens
	// 是 provider 的 total_tokens）——折叠统计按这一位把两种口径分开（见 store.go 的
	// ALTER 注释与 stats.go 的 legacyUsageRow）。
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		                       first_token_ms, duration_ms, model, usage_tokens,
		                       input_tokens, cache_read_tokens, cache_write_tokens, notice, usage_split)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		id, seq, m.Role, m.Content, m.ReasoningContent, m.ReasoningSignature, string(toolCalls), m.ToolCallID,
		m.FirstTokenMs, m.DurationMs, m.Model, m.UsageTokens,
		m.InputTokens, m.CacheReadTokens, m.CacheWriteTokens, boolInt(m.Notice),
	); err != nil {
		return 0, fmt.Errorf("写消息失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, nowNano(), id); err != nil {
		return 0, fmt.Errorf("更新会话时间失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(seq), nil
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
	notice     bool  // 注入的提示条（不是用户说的话——会话统计的轮数按它排除）
	usageSplit bool  // 这行的用量是**拆分口径**（usage_tokens 是输出）——老行是 total_tokens
}

// readRows 读会话全部消息行（按 seq 升序）。
//
// SELECT 列表与 readRowsTx 必须逐字一致：两条路径各写一遍是本仓库的老坑（写进去的
// 与读出来的对不上），计时字段同样在这两条路径上——漏一条就是"刷新后数字消失"。
func (s *Store) readRows(id string) ([]rowData, error) {
	rows, err := s.db.Query(
		`SELECT seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		        checkpoint, shadowed_seqs, first_token_ms, duration_ms, model, usage_tokens,
		        input_tokens, cache_read_tokens, cache_write_tokens, notice, usage_split
		 FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	defer rows.Close()
	var out []rowData
	for rows.Next() {
		var r rowData
		var toolCalls, shadowedSeqs string
		var cp, notice, usageSplit int
		if err := rows.Scan(&r.seq, &r.msg.Role, &r.msg.Content, &r.msg.ReasoningContent,
			&r.msg.ReasoningSignature, &toolCalls, &r.msg.ToolCallID, &cp, &shadowedSeqs,
			&r.msg.FirstTokenMs, &r.msg.DurationMs, &r.msg.Model, &r.msg.UsageTokens,
			&r.msg.InputTokens, &r.msg.CacheReadTokens, &r.msg.CacheWriteTokens, &notice,
			&usageSplit); err != nil {
			return nil, fmt.Errorf("读消息行失败: %w", err)
		}
		// 序号随消息一起回给上层：前端拿它当撤回锚点（chat.rewind 的 seq），
		// 而「刷新后的历史」与「内存里的历史」必须给出同一个号（否则撤回会打偏）
		r.msg.Seq = int64(r.seq)
		if toolCalls != "" && toolCalls != "[]" {
			if err := json.Unmarshal([]byte(toolCalls), &r.msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("解析工具调用失败: %w", err)
			}
		}
		r.checkpoint = cp == 1
		r.notice = notice == 1
		r.usageSplit = usageSplit == 1
		// 簿记位要**写回消息**（不只是留在 rowData 上）：Load 出来的历史必须与
		// 写进去的那条逐字段一致——本仓库的老坑就是"写进去的与读出来的对不上"
		//（漏这一行的表现是"内存里的历史少了这个位"，而库里其实有）
		r.msg.Notice = r.notice
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
// 与 AppendMsg 一样把 store 分配的序号回给调用方：检查点也要进内存历史
// （chat.history 直接回放内存），它在那份历史里的 seq 必须与库里一致——
// 否则同一条摘要「刷新后」与「不刷新」的序号不同，前端按序号做的任何定位都会错位。
func (s *Store) AppendCheckpoint(id string, m llm.Message, skip, count int) (int64, error) {
	toolCalls, err := json.Marshal(m.ToolCalls)
	if err != nil {
		return 0, fmt.Errorf("序列化工具调用失败: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op

	rows, err := s.readRowsTx(tx, id)
	if err != nil {
		return 0, err
	}
	surface := surfaceRows(rows)
	var shadowedSeqs []int
	var shadStart, shadEnd int
	if count > 0 {
		if skip < 0 || skip+count > len(surface) {
			return 0, fmt.Errorf("落盘落后于内存：当前历史只有 %d 条，需要影子 [%d, %d)（拒绝写错的影子区间）",
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
		return 0, fmt.Errorf("取序号失败: %w", err)
	}
	seqJSON, err := json.Marshal(shadowedSeqs)
	if err != nil {
		return 0, fmt.Errorf("序列化影子区间失败: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		                       checkpoint, shadow_start_seq, shadow_end_seq, shadowed_seqs)
		 VALUES (?, ?, ?, ?, ?, ?, ?, '', 1, ?, ?, ?)`,
		id, seq, m.Role, m.Content, m.ReasoningContent, m.ReasoningSignature, string(toolCalls),
		shadStart, shadEnd, string(seqJSON),
	); err != nil {
		return 0, fmt.Errorf("写检查点失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, nowNano(), id); err != nil {
		return 0, fmt.Errorf("更新会话时间失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(seq), nil
}

// readRowsTx 是 readRows 的事务版（落库要在同一事务里读存活集）。
// SELECT 列表与 readRows 逐字一致——两条路径各写一遍就会漂移（见 readRows 的注释）。
func (s *Store) readRowsTx(tx *sql.Tx, id string) ([]rowData, error) {
	rows, err := tx.Query(
		`SELECT seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		        checkpoint, shadowed_seqs, first_token_ms, duration_ms, model, usage_tokens,
		        input_tokens, cache_read_tokens, cache_write_tokens, notice, usage_split
		 FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	defer rows.Close()
	var out []rowData
	for rows.Next() {
		var r rowData
		var toolCalls, shadowedSeqs string
		var cp, notice, usageSplit int
		if err := rows.Scan(&r.seq, &r.msg.Role, &r.msg.Content, &r.msg.ReasoningContent,
			&r.msg.ReasoningSignature, &toolCalls, &r.msg.ToolCallID, &cp, &shadowedSeqs,
			&r.msg.FirstTokenMs, &r.msg.DurationMs, &r.msg.Model, &r.msg.UsageTokens,
			&r.msg.InputTokens, &r.msg.CacheReadTokens, &r.msg.CacheWriteTokens, &notice,
			&usageSplit); err != nil {
			return nil, fmt.Errorf("读消息行失败: %w", err)
		}
		// 序号随消息一起回给上层：前端拿它当撤回锚点（chat.rewind 的 seq），
		// 而「刷新后的历史」与「内存里的历史」必须给出同一个号（否则撤回会打偏）
		r.msg.Seq = int64(r.seq)
		if toolCalls != "" && toolCalls != "[]" {
			if err := json.Unmarshal([]byte(toolCalls), &r.msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("解析工具调用失败: %w", err)
			}
		}
		r.checkpoint = cp == 1
		r.notice = notice == 1
		r.usageSplit = usageSplit == 1
		r.msg.Notice = r.notice // 与 readRows 逐字一致（写进去的与读出来的必须对得上）
		if r.checkpoint && shadowedSeqs != "" && shadowedSeqs != "[]" {
			if err := json.Unmarshal([]byte(shadowedSeqs), &r.shadowed); err != nil {
				return nil, fmt.Errorf("解析影子区间失败: %w", err)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Rewind 撤回：把 seq 那条消息**及其之后的全部历史**从会话里删掉，返回从当前历史里
// 移除的条数。幂等：锚点已经不在当前历史里（重复撤回、序号不存在）返回 0，不报错——
// 重复撤回与两个客户端同时点撤回都是正常交互，不该变成错误码。
//
// 为什么「seq >= N」不能直接套在检查点上：压缩检查点行是**追加在末尾**的（它的 seq 比
// 它顶替的那段历史里任何一行都大），但它顶替的是那段历史在**历史顺序**里的位置。按行号
// 一刀切会把一个位置在锚点**之前**的摘要一起删掉，被它影子掉的原文随即「复活」——磁盘
// 回放（Load）就与内存历史分叉了（内存只做截断，不会让旧原文回来）。
//
// 所以检查点按**影子集合**判死活：引用已删行的项滤掉；滤空了说明它影子掉的整段都在锚点
// 之后（没有影子可替了），整条删掉。检查点只可能引用**比自己更早**的行（影子集合取自创建
// 那一刻的存活行），所以按 seq 降序遍历 + 扫到不动点即可覆盖级联（引用被删检查点）。
func (s *Store) Rewind(id string, seq int64) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op

	rows, err := s.readRowsTx(tx, id)
	if err != nil {
		return 0, err
	}
	// 锚点必须在**当前历史**里：被影子掉的原文不是历史（前端看不到它，也就无从撤回它）
	surface := surfaceRows(rows)
	idx := -1
	for i, r := range surface {
		if int64(r.seq) == seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, nil
	}
	removed := len(surface) - idx

	// 1) 锚点及其之后的**消息**行直接删（检查点不在此列——见上面的注释）
	if _, err := tx.Exec(
		`DELETE FROM messages WHERE session_id = ? AND seq >= ? AND checkpoint = 0`, id, seq); err != nil {
		return 0, fmt.Errorf("撤回消息失败: %w", err)
	}

	// 2) 活下来的检查点收缩影子集合；影子全没了就整条删掉
	deleted := make(map[int]struct{})
	for _, r := range rows {
		if !r.checkpoint && int64(r.seq) >= seq {
			deleted[r.seq] = struct{}{}
		}
	}
	// 反复扫到不动点：检查点 A 可能引用检查点 B，而 B 在这一趟里才被删掉（引用被删检查点
	// 的项同样不许留）——一趟过后 A 的影子集合会变，所以必须扫到没有变化为止。
	// 检查点数量极少（每次压缩一条），这个循环实际只跑一两趟。
	for changed := true; changed; {
		changed = false
		for i := len(rows) - 1; i >= 0; i-- {
			r := rows[i]
			if !r.checkpoint || len(r.shadowed) == 0 {
				continue // 非检查点行归上面那条 DELETE 管；无影子集合的退化检查点不受撤回影响
			}
			if _, gone := deleted[r.seq]; gone {
				continue // 上一趟已经删掉了它（不跳过会永远"删"同一条，循环停不下来）
			}
			kept := make([]int, 0, len(r.shadowed))
			for _, sh := range r.shadowed {
				if _, gone := deleted[sh]; !gone {
					kept = append(kept, sh)
				}
			}
			if len(kept) == 0 {
				if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ? AND seq = ?`, id, r.seq); err != nil {
					return 0, fmt.Errorf("撤回检查点失败: %w", err)
				}
				deleted[r.seq] = struct{}{}
				changed = true
				continue
			}
			if len(kept) == len(r.shadowed) {
				continue // 影子一个没少：不动它（省一次写，也免得把顺序写乱）
			}
			b, err := json.Marshal(kept)
			if err != nil {
				return 0, fmt.Errorf("序列化影子区间失败: %w", err)
			}
			if _, err := tx.Exec(`UPDATE messages SET shadowed_seqs = ? WHERE session_id = ? AND seq = ?`,
				string(b), id, r.seq); err != nil {
				return 0, fmt.Errorf("收缩影子区间失败: %w", err)
			}
			r.shadowed = kept
			changed = true
		}
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, nowNano(), id); err != nil {
		return 0, fmt.Errorf("更新会话时间失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return removed, nil
}
