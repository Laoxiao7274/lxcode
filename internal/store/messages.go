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
	// 会话树（docs/session-tree.md §A.4）：**追加 = 沿当前叶前进**。
	//   parent = leaf_seq（0 = 新 root：用户在第一条消息之前「回到这里」再发消息，
	//            就是开一条与旧历史并列的新根分支——旧历史仍在，切回去就回来）
	//   然后 leaf_seq = 新 seq、leaf_len + 1（同一事务内）。
	// 注意**不要**写成 COALESCE(leaf, MAX(seq))：那会把新消息接回旧链尾，等于撤回失效。
	var leaf int
	if err := tx.QueryRow(`SELECT leaf_seq FROM sessions WHERE id = ?`, id).Scan(&leaf); err != nil {
		return 0, fmt.Errorf("读叶指针失败: %w", err)
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
	// images 是图片**文件引用**的 JSON（视觉请求）：恒存引用不存 base64（分层纪律
	// 见 llm/images.go）。空串 = 无图（绝大多数消息）。
	images := ""
	if len(m.Images) > 0 {
		b, err := json.Marshal(m.Images)
		if err != nil {
			return 0, fmt.Errorf("序列化图片引用失败: %w", err)
		}
		images = string(b)
	}
	// attachments 是附件**引用 + 展示元数据**的 JSON（前端渲染附件卡——实时与
	// 回放同一份）。恒存引用不存 base64（与 images 同一条分层纪律）。
	// 空串 = 无附件（绝大多数消息）。
	attachments := ""
	if len(m.Attachments) > 0 {
		b, err := json.Marshal(m.Attachments)
		if err != nil {
			return 0, fmt.Errorf("序列化附件引用失败: %w", err)
		}
		attachments = string(b)
	}
	// usage_split 一律写 1：写这一行的是**认识拆分口径**的二进制（usage_tokens 真的是
	// 输出、输入侧三桶另记）。旧二进制写的行走列默认值 0 = 老口径（那时 usage_tokens
	// 是 provider 的 total_tokens）——折叠统计按这一位把两种口径分开（见 store.go 的
	// ALTER 注释与 stats.go 的 legacyUsageRow）。
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, parent_seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		                       first_token_ms, duration_ms, model, usage_tokens, images, attachments,
		                       input_tokens, cache_read_tokens, cache_write_tokens, notice, usage_split)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		id, seq, leaf, m.Role, m.Content, m.ReasoningContent, m.ReasoningSignature, string(toolCalls), m.ToolCallID,
		m.FirstTokenMs, m.DurationMs, m.Model, m.UsageTokens, images, attachments,
		m.InputTokens, m.CacheReadTokens, m.CacheWriteTokens, boolInt(m.Notice),
	); err != nil {
		return 0, fmt.Errorf("写消息失败: %w", err)
	}
	// 叶指针前进 + 路径长度 +1（与 INSERT 同一事务：不允许「消息进去了但叶没动」）。
	if _, err := tx.Exec(`UPDATE sessions SET leaf_seq = ?, leaf_len = leaf_len + 1, updated_at = ? WHERE id = ?`,
		seq, nowNano(), id); err != nil {
		return 0, fmt.Errorf("更新叶指针失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(seq), nil
}

// Load 读出会话的**当前历史**（leaf 路径，按 surface 顺序）。会话不存在时报错
// （调用方 SwitchTo 依赖此语义区分「空会话」与「不存在」）。
//
// 会话树（docs/session-tree.md §A.3）：回放 = 「从 leaf 回溯到 root 的可达行」再套
// 现有的 surfaceRows（影子集合跳过 + 检查点锚点插入，一字不改）。单链会话
// （leaf = 最后一条）的结果与迁移前**逐字节一致**。
//
// 取数口用 readPathRows（SQL 递归回溯，只读路径上的行）而不是「全表 + Go 侧筛」：
// 分支化之后路径长度与表行数会越差越多，而大会话的行正文是 KB 级的。判定仍然只有
// surfaceRows 一份——SQL 只回答「哪些行可达」。
func (s *Store) Load(id string) ([]llm.Message, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("会话 %s 不存在", id)
		}
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	if !s.treeMigrated {
		// 未迁移的库（只在 Open 失败前的一瞬间可能存在）：旧语义 = 全表 surface。
		rows, err := s.readRows(id)
		if err != nil {
			return nil, err
		}
		return messagesOf(surfaceRows(rows)), nil
	}
	leaf, err := s.LeafSeq(id)
	if err != nil {
		return nil, err
	}
	if leaf <= 0 {
		// 空路径（用户回到了第一条消息之前 / 新会话还没发消息）。
		// **不是**「回落全表」——那样等于把刚离开路径的历史又装回来。
		return []llm.Message{}, nil
	}
	rows, err := s.readPathRows(id, int(leaf))
	if err != nil {
		return nil, err
	}
	return messagesOf(surfaceRows(rows)), nil
}

// messagesOf 把行集拷成消息切片（空行集也返回**非 nil** 的空切片：wire 上是 `[]`
// 而不是 `null`，与改动前的载荷形状一致）。
func messagesOf(rows []rowData) []llm.Message {
	msgs := make([]llm.Message, 0, len(rows))
	for _, r := range rows {
		msgs = append(msgs, r.msg)
	}
	return msgs
}

// rowData 是一行消息（含压缩检查点的影子信息与会话树的父指针）。
type rowData struct {
	seq        int
	parentSeq  int // 会话树：父消息的 seq（0 = root，见 tree.go）
	msg        llm.Message
	checkpoint bool
	shadowed   []int // 检查点影子掉的 seq 集合（权威；空 = 不是检查点）
	notice     bool  // 注入的提示条（不是用户说的话——会话统计的轮数按它排除）
	usageSplit bool  // 这行的用量是**拆分口径**（usage_tokens 是输出）——老行是 total_tokens
}

// messageCols 是 messages 行读出的**唯一**列清单（按此顺序 Scan）。
//
// 为什么抽成常量（2026-12 批次 1）：readRows 与 readRowsTx 原先各写一遍 SELECT，
// 而它们已经漂移过——readRowsTx 漏了 images / attachments 两列（注释却写着「必须
// 逐字一致」），只因当时的调用方只读 seq/checkpoint/shadowed_seqs 才没暴露。
// 一份清单 + 一个扫描函数，从结构上消灭这类漂移（「写进去的与读出来的对不上」
// 是本仓库反复踩的坑）。
const messageCols = `seq, parent_seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
	checkpoint, shadowed_seqs, first_token_ms, duration_ms, model, usage_tokens, images, attachments,
	input_tokens, cache_read_tokens, cache_write_tokens, notice, usage_split`

// querier 是 *sql.DB 与 *sql.Tx 的公共读接口（同一份查询在两条路径上跑）。
type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// scanRows 把消息行扫成 rowData（唯一一份扫描逻辑，与 messageCols 严格对应）。
func scanRows(rows *sql.Rows) ([]rowData, error) {
	defer rows.Close()
	var out []rowData
	for rows.Next() {
		var r rowData
		var toolCalls, shadowedSeqs, images, attachments string
		var cp, notice, usageSplit int
		if err := rows.Scan(&r.seq, &r.parentSeq, &r.msg.Role, &r.msg.Content, &r.msg.ReasoningContent,
			&r.msg.ReasoningSignature, &toolCalls, &r.msg.ToolCallID, &cp, &shadowedSeqs,
			&r.msg.FirstTokenMs, &r.msg.DurationMs, &r.msg.Model, &r.msg.UsageTokens, &images, &attachments,
			&r.msg.InputTokens, &r.msg.CacheReadTokens, &r.msg.CacheWriteTokens, &notice,
			&usageSplit); err != nil {
			return nil, fmt.Errorf("读消息行失败: %w", err)
		}
		// 序号随消息一起回给上层：前端拿它当撤回锚点（chat.rewind 的 seq），
		// 而「刷新后的历史」与「内存里的历史」必须给出同一个号（否则撤回会打偏）
		r.msg.Seq = int64(r.seq)
		// 父指针同理（会话树的边）：chat.history 直接回放 llm.Message，
		// 不回填的话前端拿不到父指针（批次 2 的 chat.tree 与分支视图要用）。
		r.msg.ParentSeq = int64(r.parentSeq)
		if toolCalls != "" && toolCalls != "[]" {
			if err := json.Unmarshal([]byte(toolCalls), &r.msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("解析工具调用失败: %w", err)
			}
		}
		// 图片引用读回（'' = 无图——老行/纯文本消息，零影响）
		if images != "" && images != "[]" {
			if err := json.Unmarshal([]byte(images), &r.msg.Images); err != nil {
				return nil, fmt.Errorf("解析图片引用失败: %w", err)
			}
		}
		// 附件引用读回（'' = 无附件——老行/纯文本消息，零影响）
		if attachments != "" && attachments != "[]" {
			if err := json.Unmarshal([]byte(attachments), &r.msg.Attachments); err != nil {
				return nil, fmt.Errorf("解析附件引用失败: %w", err)
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

// readRows 读会话**全部**消息行（按 seq 升序）——写路径（落库算存活集、撤回、
// 迁移、统计）要全表；回放走 readPathRows（只取 leaf 可达的行）。
func (s *Store) readRows(id string) ([]rowData, error) {
	return queryRows(s.db, id)
}

// queryRows 是全表读的实现（readRows / readRowsTx 共用）。
func queryRows(q querier, id string) ([]rowData, error) {
	rows, err := q.Query(`SELECT `+messageCols+` FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	return scanRows(rows)
}

// readPathRows 只读**当前 leaf 路径上**的消息行（从 leaf 沿 parent_seq 回溯到 root
// 的可达集合，按 seq 升序）——回放（Load/Latest）的取数口。
//
// 为什么在 SQL 里做回溯（而不是像 pathRows 那样把全表拉进 Go 再筛）：大会话
// 53k 行时全表读要扫 KB 级正文（与 stats.go 记录的覆盖索引优化同一个理由），
// 而分支化之后「路径长度」与「表行数」会越差越多。递归 CTE 只取路径上的行。
//
// 与 pathRows 的分工（**判定只有一份，别在这里加第二套**）：
//   - 这里只回答「哪些行可达」（parent 链的传递闭包），**不判影子、不排检查点**；
//   - 影子集合跳过与检查点锚点插入仍然只由 surfaceRows 做。
//
// 等价性由 TestSQLPathMatchesGoReach 钉住：对同一份库，
// readPathRows(leaf) 必须与 pathRows 眼里的可达集逐 seq 相同（含检查点、含影子行）。
//
// 防御与 pathRows 同一条纪律：环用 UNION（去重）自然终止，断链（父指针指向不存在
// 的行）就停在那里——畸形数据不炸加载路径。
func (s *Store) readPathRows(id string, leaf int) ([]rowData, error) {
	return queryPathRows(s.db, id, leaf)
}

func queryPathRows(q querier, id string, leaf int) ([]rowData, error) {
	if leaf <= 0 {
		return nil, nil // 空路径（见 leaf_seq 的注释：0 不是「未知」）
	}
	rows, err := q.Query(`WITH RECURSIVE reach(seq, parent_seq) AS (
			SELECT seq, parent_seq FROM messages WHERE session_id = ? AND seq = ?
			UNION
			SELECT m.seq, m.parent_seq FROM messages m JOIN reach r ON m.seq = r.parent_seq
			 WHERE m.session_id = ?
		)
		SELECT `+messageCols+` FROM messages
		 WHERE session_id = ? AND seq IN (SELECT seq FROM reach)
		 ORDER BY seq`, id, leaf, id, id)
	if err != nil {
		return nil, fmt.Errorf("查询会话 %s 的路径失败: %w", id, err)
	}
	return scanRows(rows)
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
	// 会话树（docs/session-tree.md §A.3 的三件事）：区间必须解析到**当前 leaf 路径**
	// 的 surface，而不是全表的 surface——否则「内存里的路径」与「库里 leaf 的路径」
	// 不一致时，会写出一个跨分支的无效影子集。
	leaf, err := leafOfTx(tx, id)
	if err != nil {
		return 0, err
	}
	surface := pathRows(rows, leaf, s.treeMigrated)
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
	// 检查点的父指针 = 被影子段**之前**的最后一条存活行（没有则 0 = root）。
	// 这一步让「检查点属于哪条分支」由 parent 链自己回答——pathRows 里因此**不需要**
	// 第二套「影子集合包含关系」的分支判定（两套判定迟早分叉）。
	//
	// count == 0 的退化检查点单独一条规则：它**不替换任何行**，旧语义（surfaceRows 的
	// 锚点回落自身 seq）把它排在历史**末尾**。树模型下对应「挂在当前叶之后、自己成为
	// 新叶」——写成 cpParent = surface[skip-1]（并重接 surface[skip]）会把整条历史
	// 挂到摘要底下，从 leaf 回溯出来的路径顺序就与旧回放不一致了。
	cpParent := 0
	switch {
	case count == 0:
		cpParent = leaf // 尾部追加：父 = 当前叶（leaf=0 时它就是新 root）
	case skip > 0:
		cpParent = surface[skip-1].seq
	}
	seqJSON, err := json.Marshal(shadowedSeqs)
	if err != nil {
		return 0, fmt.Errorf("序列化影子区间失败: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, parent_seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id,
		                       checkpoint, shadow_start_seq, shadow_end_seq, shadowed_seqs)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', 1, ?, ?, ?)`,
		id, seq, cpParent, m.Role, m.Content, m.ReasoningContent, m.ReasoningSignature, string(toolCalls),
		shadStart, shadEnd, string(seqJSON),
	); err != nil {
		return 0, fmt.Errorf("写检查点失败: %w", err)
	}
	// **重接父指针**：被影子段之后那条存活行（surface[skip+count]）改挂到检查点上。
	// 没有后继行（压缩发生在历史末尾）时不用改——检查点自己就是新的路径末尾。
	// count == 0 时**不重接**：退化检查点没有影子段，它只是尾部追加一行（见 cpParent
	// 的注释），把 surface[skip] 挂过去会把整条历史塞到摘要底下。
	if count > 0 && skip+count < len(surface) {
		if _, err := tx.Exec(
			`UPDATE messages SET parent_seq = ? WHERE session_id = ? AND seq = ?`,
			seq, id, surface[skip+count].seq); err != nil {
			return 0, fmt.Errorf("重接父指针失败: %w", err)
		}
	}
	// 叶指针：**只在它原本落在被影子段里时才改到检查点**。压缩区间在尾部保留段之前，
	// 所以 leaf 通常不受影响——无条件改成检查点会让模型看不到保留的尾部，而
	// chat.compact 返回的 after 占用仍按完整路径算（两个数字互相矛盾）。
	// count == 0（退化检查点）是唯一的例外：它追加在路径末尾、自己就是新叶（见上面
	// cpParent 的注释；旧语义里它也排在历史最后一条）。
	//
	// leaf_len **不能**用 len(surface)−count+1 硬算：那假设 leaf 在路径尾部。用户把叶
	// 切到中间节点后再压缩时，路径长度是「leaf 之前那段 + 检查点 + leaf 之后那段」，
	// 与整条 surface 的长度无关。所以统一改成**重算**（pathRows 走一遍，O(n) 可忽略）
	// ——三处维护点里只有这里是「形状变了且叶可能不在尾部」，硬算迟早漂移。
	newLeaf := leaf
	switch {
	case count == 0:
		newLeaf = seq // 退化检查点成为新叶（父 = 原 leaf）
	case leafInShadowed(leaf, shadowedSeqs):
		newLeaf = seq
	}
	// 重算 leaf_len 要用「压缩之后」的行集：把新检查点加进去，并且**同步父指针的
	// 两处改动**（检查点挂 cpParent、后继行改挂检查点）——否则算出来的还是旧树。
	afterRows := make([]rowData, 0, len(rows)+1)
	for _, r := range rows {
		if r.seq == seq {
			continue // 新检查点还没进 rows（下面单独追加）
		}
		if count > 0 && skip+count < len(surface) && r.seq == surface[skip+count].seq {
			r.parentSeq = seq // 后继行改挂检查点（与上面的 UPDATE 同步）
		}
		afterRows = append(afterRows, r)
	}
	afterRows = append(afterRows, rowData{
		seq: seq, parentSeq: cpParent, checkpoint: true, shadowed: shadowedSeqs, msg: m,
	})
	newLen := len(pathRows(afterRows, newLeaf, s.treeMigrated))
	if _, err := tx.Exec(`UPDATE sessions SET leaf_seq = ?, leaf_len = ?, updated_at = ? WHERE id = ?`,
		newLeaf, newLen, nowNano(), id); err != nil {
		return 0, fmt.Errorf("更新叶指针失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(seq), nil
}

// readRowsTx 是 readRows 的事务版（落库要在同一事务里读存活集）。
// 与 readRows **共用 messageCols + scanRows**（2026-12 批次 1 收敛）：原先两处各写
// 一遍 SELECT，而 readRowsTx 已经漏过 images / attachments 两列（注释却写着「必须
// 逐字一致」）。一份清单 + 一个扫描函数，从结构上消灭这类漂移。
func (s *Store) readRowsTx(tx *sql.Tx, id string) ([]rowData, error) {
	return queryRows(tx, id)
}
