// 会话树：消息级父指针（messages.parent_seq）+ 叶指针（sessions.leaf_seq）+
// 路径构造（从 leaf 回溯到 root 的行集）。设计见 docs/session-tree.md §A。
//
// 核心结论（文档 §A.3）：**树模型不需要新的回放算法**——把「输入行集」从
// 「全表」换成「从 leaf 可达的行」，其余交给现有的 surfaceRows（影子集合跳过 +
// 检查点锚点插入）一字不改。于是单链会话逐字节等价，存量零风险。
//
// 与 sessions.parent_id 的分工（两个 parent 不许混）：
//
//	sessions.parent_id  = **会话级**父子（子 Agent = 独立会话，AGENTS §2.3）
//	messages.parent_seq = **消息级**父指针（会话树的一条边，同一会话内）
package store

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// treeSchemaVersion 是会话树迁移的版本号（写进 PRAGMA user_version）。
//
// 为什么用 user_version 而不是「WHERE parent_seq = 0 兜底」：每条会话的**第一条**
// 消息的 parent_seq 天生就是 0（root），拿它当幂等守卫会让那一条在每次 Open 都
// 重算一遍；更糟的是它把「合法的 root」与「还没迁移」混为一谈。
const treeSchemaVersion = 1

// migrateSessionTree 把存量库升级到会话树模型（幂等）：
//
//  1. 按 **surface 顺序**（surfaceRows 的结果，含检查点锚点插入）重写整条 parent 链；
//  2. 定 leaf_seq = 该链的最后一条、leaf_len = 链长；
//  3. 同一个事务里写 PRAGMA user_version = treeSchemaVersion（数据与版本号原子提交）。
//
// 为什么必须按 surface 顺序而不是按 seq 链（文档 §A.2 的例子）：
//
//	库内：A1..A5（被 CK 影子）、CK（seq=11，影子 A1..A5）、A6..A10
//	按 seq 链：A6.parent = A5  ← 错！从 leaf=A10 回溯会走到 A1..A5 那堆原文，
//	                            检查点根本不在路径上
//	按 surface：CK 落在被影子段的位置（首位）→ CK.parent = 0，A6.parent = CK
//	            回溯 A10 → … → A6 → CK → 0：检查点在路径上 ✅
//
// 失败处理（fail-closed）：整个迁移在一个事务里，任何一步失败都回滚并让 Open 报错。
// 「跳过迁移继续跑」会把库留在「一半 parent 是 0、一半是链」的状态，pathRows 会切出
// 半截历史——那比打不开更坏（用户会以为历史丢了）。
func (s *Store) migrateSessionTree() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("读库版本失败: %w", err)
	}
	if v >= treeSchemaVersion {
		s.treeMigrated = true
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开会话树迁移事务失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op

	ids, err := sessionIDsTx(tx)
	if err != nil {
		return err
	}
	// 预编译两条 UPDATE：108k 行逐条 Exec 时，语句解析与编译的固定开销实测占迁移
	// 总耗时的绝大部分（本机 264MB / 109k 行库：不预编译 ~5.9s，预编译 ~1.2s）。
	// 迁移只在老库首次打开时跑一次，但 5.9s 的启动延迟用户是能感觉到的。
	setParent, err := tx.Prepare(`UPDATE messages SET parent_seq = ? WHERE session_id = ? AND seq = ?`)
	if err != nil {
		return fmt.Errorf("预编译父指针迁移语句失败: %w", err)
	}
	defer setParent.Close()
	setLeaf, err := tx.Prepare(`UPDATE sessions SET leaf_seq = ?, leaf_len = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("预编译叶指针迁移语句失败: %w", err)
	}
	defer setLeaf.Close()

	rewritten := 0
	for _, id := range ids {
		// 复用 readRowsTx（与回放**同一份** SELECT），不新写一份查询：
		// 两份 SELECT 各写一遍必然漂移，而迁移与回放的判定必须逐字一致。
		rows, err := s.readRowsTx(tx, id)
		if err != nil {
			return fmt.Errorf("迁移会话 %s 失败: %w", id, err)
		}
		// 同一份存活判定（影子集合 + 检查点锚点插入）——**不写第二套**。
		surface := surfaceRows(rows)
		prev := 0
		for _, r := range surface {
			if r.parentSeq != prev {
				if _, err := setParent.Exec(prev, id, r.seq); err != nil {
					return fmt.Errorf("迁移会话 %s 的父指针失败: %w", id, err)
				}
				rewritten++
			}
			prev = r.seq
		}
		// surface 为空 → leaf=0、leaf_len=0（空会话：0 就是合法的空路径）
		if _, err := setLeaf.Exec(prev, len(surface), id); err != nil {
			return fmt.Errorf("迁移会话 %s 的叶指针失败: %w", id, err)
		}
	}
	// 版本号与数据在**同一个事务**里提交（PRAGMA user_version 是库头字段，但它随事务
	// 一起回滚——实测 modernc.org/sqlite：事务内写 user_version 后 Rollback，版本号
	// 回到旧值）。这样「数据已迁移但版本号没写上」的中间态在结构上不存在，比
	// 「先提交数据、再单独写版本号」少一个崩溃窗口。
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, treeSchemaVersion)); err != nil {
		return fmt.Errorf("写库版本失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交会话树迁移失败: %w", err)
	}
	s.treeMigrated = true
	if len(ids) > 0 {
		log.Printf("会话树迁移完成：%d 条会话，重写 %d 个父指针（版本 → %d）",
			len(ids), rewritten, treeSchemaVersion)
	}
	return nil
}

// sessionIDsTx 列出全部会话 id（迁移要逐会话处理）。
func sessionIDsTx(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`SELECT id FROM sessions ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("列出会话失败: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读会话 id 失败: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// reachSet 返回从 leaf 沿 parent 链回溯到的 seq 集合（= 当前路径的节点）。
//
// 防御（畸形数据不炸加载路径，与 toolpair.go 同一条纪律）：环与断链都**钳住**而不是
// 抛异常——历史来自 SQLite，残缺记录是既成事实，加载路径抛异常会让老会话直接打不开。
// guard 上界是行数：任何环都会在 n+1 步内撞上已访问节点或耗尽。
func reachSet(rows []rowData, leaf int) map[int]struct{} {
	if leaf <= 0 || len(rows) == 0 {
		return nil
	}
	bySeq := make(map[int]struct{}, len(rows))
	parent := make(map[int]int, len(rows))
	for _, r := range rows {
		bySeq[r.seq] = struct{}{}
		parent[r.seq] = r.parentSeq
	}
	reach := make(map[int]struct{}, 64)
	for cur, guard := leaf, 0; cur > 0 && guard <= len(rows); guard++ {
		if _, ok := bySeq[cur]; !ok {
			break // 断链（父指针指向不存在的行）
		}
		if _, dup := reach[cur]; dup {
			break // 环
		}
		reach[cur] = struct{}{}
		cur = parent[cur]
	}
	return reach
}

// pathRows 返回**当前 leaf 路径**上的历史（surface 顺序）。
//
// 与 surfaceRows 的关系：pathRows 只负责「从全表里切出可达的那部分行」，
// 跳过影子行与检查点锚点插入仍由 surfaceRows 做——**判定只有一份**。
//
// migrated = false（未迁移的库）：回落旧的整表 surface，与今天逐字节一致。
// leaf <= 0：**空路径**（用户回到了第一条消息之前）。这**不是**「回落全表」——
// 那样等于把刚撤回的历史又装回来（见 store.go 的 leaf_seq 注释）。
func pathRows(rows []rowData, leaf int, migrated bool) []rowData {
	if len(rows) == 0 {
		return nil
	}
	if !migrated {
		return surfaceRows(rows)
	}
	if leaf <= 0 {
		return nil
	}
	reach := reachSet(rows, leaf)
	if len(reach) == 0 {
		return nil // leaf 不在库里（坏号）：空路径，不猜
	}
	// 按 seq 升序遍历（rows 本来就是）——**live 必须保持 seq 升序**：surfaceRows
	// 依赖 others 的既有顺序（它只给检查点排序），从 map 直接铺开会把历史顺序打乱。
	live := make([]rowData, 0, len(reach))
	for _, r := range rows {
		if _, ok := reach[r.seq]; ok {
			live = append(live, r)
		}
	}
	return surfaceRows(live)
}

// leafInShadowed 报告叶指针是否落在被影子段里（压缩时决定要不要把叶移到检查点）。
func leafInShadowed(leaf int, shadowed []int) bool {
	for _, sh := range shadowed {
		if sh == leaf {
			return true
		}
	}
	return false
}

// leafOf 读会话的叶指针（0 = 空路径）。
func leafOfTx(tx *sql.Tx, id string) (int, error) {
	var leaf int
	err := tx.QueryRow(`SELECT leaf_seq FROM sessions WHERE id = ?`, id).Scan(&leaf)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("会话 %s 不存在", id)
	}
	if err != nil {
		return 0, fmt.Errorf("读叶指针失败: %w", err)
	}
	return leaf, nil
}

// LeafSeq 读会话的叶指针（0 = 空路径，**不是「未知」**）。
func (s *Store) LeafSeq(id string) (int64, error) {
	var leaf int
	err := s.db.QueryRow(`SELECT leaf_seq FROM sessions WHERE id = ?`, id).Scan(&leaf)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("会话 %s 不存在", id)
	}
	if err != nil {
		return 0, fmt.Errorf("读叶指针失败: %w", err)
	}
	return int64(leaf), nil
}

// ParentSeq 读某条消息的父指针（0 = root / 行不存在）。树面板与「回到这里」的
// 落点计算要用它（rewind 的落点 = parent(anchor)）。
func (s *Store) ParentSeq(id string, seq int64) (int64, error) {
	var parent int
	err := s.db.QueryRow(`SELECT parent_seq FROM messages WHERE session_id = ? AND seq = ?`, id, seq).Scan(&parent)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("消息 %s/%d 不存在", id, seq)
	}
	if err != nil {
		return 0, fmt.Errorf("读父指针失败: %w", err)
	}
	return int64(parent), nil
}

// SetLeaf 把叶指针移到 seq（0 = 空路径），返回离开路径的条数与新叶。
//
// **只动指针，不删任何行**——这就是「撤回」在新模型下的全部实现（旧分支完整
// 保留在树里，切回去就回来）。
//
// removed = |path(旧叶)| − |path(新叶)|，两端都按 **surface 口径**数（含检查点
// 节点）。这与旧删除语义（len(surface) − idx，「从锚点起真删的条数」）逐例相同
// ——两条口径的定义本来就是一回事（见 docs/session-tree.md §B.1 的算例）。
// 负数钳到 0：切到更长的路径不是「撤回」，没有「离开」的条数。
//
// 目标必须在本会话里（0 = 空路径，合法）；坏号报错而**不静默当 0**——静默会把
// 用户送到一个他没点的位置。
func (s *Store) SetLeaf(id string, seq int64) (removed int, leaf int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback()

	rows, err := s.readRowsTx(tx, id)
	if err != nil {
		return 0, 0, err
	}
	oldLeaf, err := leafOfTx(tx, id)
	if err != nil {
		return 0, 0, err
	}
	// 目标必须在本会话里（0 = 空路径，合法）。
	if seq != 0 {
		found := false
		for _, r := range rows {
			if int64(r.seq) == seq {
				found = true
				break
			}
		}
		if !found {
			return 0, 0, fmt.Errorf("seq %d 不在会话 %s 里", seq, id)
		}
	}
	oldLen := len(pathRows(rows, oldLeaf, s.treeMigrated))
	newLen := len(pathRows(rows, int(seq), s.treeMigrated))
	removed = oldLen - newLen
	if removed < 0 {
		removed = 0 // 切到更长的路径：不是「撤回」，没有「离开」的条数
	}
	if _, err := tx.Exec(`UPDATE sessions SET leaf_seq = ?, leaf_len = ?, updated_at = ? WHERE id = ?`,
		seq, newLen, nowNano(), id); err != nil {
		return 0, 0, fmt.Errorf("写叶指针失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return removed, seq, nil
}

// RewindTo 是「回到这里」的存储实现：把叶移到 **anchor 的父节点**（anchor 自己
// 也离开路径），返回离开路径的条数与新叶。
//
// 为什么落点是 parent(anchor) 而不是 anchor：用户点「回到这里」的意图是「从这条
// 之前重来」——那条消息本身也要被重写（它的正文回到输入框）。这条语义与旧
// `chat.rewind`（DELETE seq >= anchor）的可见行为完全一致。
//
// 幂等：anchor 不在**当前 leaf 路径**上（重复撤回 / 废弃分支的节点 / 坏号）→
// 返回 (0, 当前叶, nil)，不报错——撤回是正常交互，重复不该变成错误码。
func (s *Store) RewindTo(id string, anchor int64) (removed int, leaf int64, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback()

	rows, err := s.readRowsTx(tx, id)
	if err != nil {
		return 0, 0, err
	}
	oldLeaf, err := leafOfTx(tx, id)
	if err != nil {
		return 0, 0, err
	}
	// 锚点必须在**当前历史**里：被影子掉的原文不是历史（前端看不到它，也就无从
	// 撤回它），废弃分支上的节点也不在当前路径上。
	surface := pathRows(rows, oldLeaf, s.treeMigrated)
	target := -1
	for i, r := range surface {
		if int64(r.seq) == anchor {
			target = i
			break
		}
	}
	if target < 0 {
		return 0, int64(oldLeaf), nil // 幂等空操作
	}
	newLeaf := 0
	if target > 0 {
		newLeaf = surface[target-1].seq
	}
	removed = len(surface) - target
	newLen := len(pathRows(rows, newLeaf, s.treeMigrated))
	if _, err := tx.Exec(`UPDATE sessions SET leaf_seq = ?, leaf_len = ?, updated_at = ? WHERE id = ?`,
		newLeaf, newLen, nowNano(), id); err != nil {
		return 0, 0, fmt.Errorf("写叶指针失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return removed, int64(newLeaf), nil
}

// TreeRows 读会话的**树骨架**（全部行，含废弃分支与被影子行——树要显示它们）。
//
// 只取骨架列：正文用 substr 截 120 字符（中文约 40 字）。这条查询必须走
// 覆盖索引之外的最小投影——大会话 53k 行时 readRows 要读 KB 级正文，实测 ~195ms。
func (s *Store) TreeRows(id string) ([]sessiondata.TreeRow, error) {
	rows, err := s.db.Query(
		`SELECT seq, parent_seq, role, checkpoint, notice, length(content), substr(content, 1, 120)
		 FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("查询会话 %s 的树骨架失败: %w", id, err)
	}
	defer rows.Close()
	var out []sessiondata.TreeRow
	for rows.Next() {
		var r sessiondata.TreeRow
		var cp, notice, contentLen int
		var summary string
		if err := rows.Scan(&r.Seq, &r.ParentSeq, &r.Role, &cp, &notice, &contentLen, &summary); err != nil {
			return nil, fmt.Errorf("读树骨架行失败: %w", err)
		}
		r.Checkpoint = cp == 1
		r.Notice = notice == 1
		r.ContentLen = contentLen
		r.Summary = oneLine(summary)
		out = append(out, r)
	}
	return out, rows.Err()
}

// oneLine 把摘要压成单行（换行/制表折成空格，连续空白收成一个，去首尾空白）。
// 树面板是窄栏，多行摘要会把整棵树撑得没法看。
func oneLine(s string) string {
	var b []rune
	space := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\r', '\n':
			space = true
			continue
		}
		if space && len(b) > 0 {
			b = append(b, ' ')
		}
		space = false
		b = append(b, r)
	}
	return string(b)
}
