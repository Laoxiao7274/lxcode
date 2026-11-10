// 会话树骨架（chat.tree 的数据源）：只回 user 节点 + 父子关系 + 当前路径标记。
//
// **只展示用户消息**（2026-12 用户拍板，改自「全部节点」版本）：
//   - 节点只有 user（assistant / tool / notice / checkpoint 都不出现在树上）；
//   - parent = **树上最近前驱 user**（沿原始 parent 链往回找，跨过 assistant /
//     tool / notice / checkpoint）——原始 parent_seq 指向的可能是 assistant，直接
//     用会把中间节点折成树的一层。现在树上每条边都是「这条 user 的前一条 user」，
//     某个问题有 N 个不同答案 = N 个孩子，分支语义直观；
//   - 分支结构完整保留（非路径上的 user 照常出现，on_path=false）；
//   - total / 截断都按 **user 消息数**计。
//
// 三条纪律（docs/session-tree.md §A.6/§A.7）：
//   - **不走 readRows**：那是全列读（含 KB 级正文）。树只需要骨架，sessiondata.TreeRows
//     用 substr(content,1,120) 的轻量投影（大会话 53k 行时 readRows ~195ms）。
//   - **畸形数据不炸**：孤儿（parent 链断掉/成环且找不到前驱 user）标 orphan，
//     挂 root，不丢节点、不抛异常——丢节点等于藏数据，抛异常等于整条会话的树
//     打不开。
//   - **截断不静默**：max_nodes 生效时按 seq 从大到小取最近 N 个 user 再补齐祖先，
//     并且 truncated=true（前端显示「更早的 N 个节点未加载」）。
package agent

import (
	"slices"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// TreeNode 是树里的一个节点（wire 形态由 protocol.TreeNode 承载）。
//
// 树上只有 user 节点：一轮对话（user + 它背后的 assistant/tool）在树上是一行。
type TreeNode struct {
	Seq       int64
	ParentSeq int64
	// Kind 恒为 "user"（协议字段保留：前端按 kind 渲染，历史载荷兼容）。
	Kind string
	// Turn 是轮次序号（与 SessionStats.turns、右栏 turnGroups 同口径：真实 user
	// 消息从 1 起）。
	Turn int
	// Summary 是单行摘要（≤120 字符，不含正文全文）。
	Summary string
	// Children 是直接子节点数（>1 = 这个问题有多个分支）。
	Children int
	// OnPath = 是否在当前 leaf 路径上（废弃分支 = false）。
	OnPath bool
	// Orphan = parent 链断掉/成环、找不到前驱 user（畸形数据的兜底）。
	Orphan bool
	// Files = 该轮可还原的文件改动数，**含子会话**；ChildFiles = 其中来自子会话
	// 的改动数。快照的 owner_seq 挂在 assistant 消息上，而树节点是 user——按
	// 「≤ owner_seq 的最大 user seq」投影归轮。都是 0 = 未知/无（wire 上整键缺席，
	// 前端不渲染徽标——不显示 0）。
	Files      int
	ChildFiles int
	// External = 该轮无法快照的写（bash/MCP/自定义工具，含子会话里的 bash）。
	// 旧模型由 turnCounters 按 assistant 锚点在内存里记、树上从不填充（重启即失）；
	// user-only 模型下该字段恒 0（保留协议字段做载荷兼容）。
	External int
}

// Tree 是会话树骨架。
type Tree struct {
	LeafSeq   int64
	Nodes     []TreeNode
	Total     int
	Truncated bool
}

// Tree 返回会话树骨架（按 seq 升序，只含 user 节点）。
//
// maxNodes <= 0 = 全量；> 0 = 只回最近 maxNodes 个 user 节点（再补齐它们的祖先）。
// 无存储时返回空树（**不编一棵假树**——前端据此禁用树面板）。
func (s *Session) Tree(maxNodes int) (Tree, error) {
	s.mu.Lock()
	st, id := s.st, s.id
	s.mu.Unlock()
	if st == nil || id == "" {
		return Tree{}, nil
	}
	rows, err := st.TreeRows(id)
	if err != nil {
		return Tree{}, err
	}
	leaf, err := st.LeafSeq(id)
	if err != nil {
		return Tree{}, err
	}
	tree := buildTree(rows, leaf, maxNodes)
	// 文件徽标（批次 3）：快照归属的 owner_seq（assistant 的 seq）投影到
	// 「包含它的那个用户轮」上——用户看到的是「这一轮回滚能还原几个文件」，含子
	// Agent 的改动。owner = 顶层会话 id（子会话的树与它的文件改动都算在父会话账上）。
	//
	// 失败只忽略（徽标是展示信息，不该让整棵树打不开）。
	ownerID, err := st.OwnerSessionID(id)
	if err != nil {
		return tree, nil
	}
	if counts, err := st.SnapshotCountsByOwnerSeq(ownerID); err == nil {
		projectCounts(tree.Nodes, counts)
	}
	return tree, nil
}

// buildTree 是纯函数（输入骨架行 + leaf，输出树）——树形状的全部规则都在这里，
// 单测直接喂数据，不需要库。
func buildTree(rows []sessiondata.TreeRow, leaf int64, maxNodes int) Tree {
	bySeq := make(map[int64]int, len(rows)) // seq → rows 下标
	for i, r := range rows {
		bySeq[int64(r.Seq)] = i
	}
	isUser := func(r sessiondata.TreeRow) bool {
		return !r.Checkpoint && !r.Notice && r.Role == "user"
	}

	// ① 每条 user 的「树上最近前驱 user」（沿原始 parent 链往回找）。
	//    parent 链断掉 → prev=0（挂 root）并标 orphan；成环（转回已见过的行）同样
	//    prev=0 + orphan。
	type userRow struct {
		row    sessiondata.TreeRow
		prev   int64 // 树上最近前驱 user 的 seq（0 = root）
		orphan bool
		onPath bool
	}
	users := make([]userRow, 0, len(rows))
	prevOf := make(map[int64]int64, len(rows)) // user seq → 前驱 user seq
	for _, r := range rows {
		if !isUser(r) {
			continue
		}
		u := userRow{row: r}
		seen := map[int64]bool{int64(r.Seq): true}
		for cur, guard := int64(r.ParentSeq), 0; cur > 0 && guard <= len(rows); guard++ {
			j, ok := bySeq[cur]
			if !ok {
				u.orphan = true // 断链：父指针指向不存在的行
				break
			}
			if seen[cur] {
				u.orphan = true // 环：父链转回了走过的行
				break
			}
			seen[cur] = true
			if isUser(rows[j]) {
				u.prev = int64(rows[j].Seq)
				break
			}
			cur = int64(rows[j].ParentSeq)
		}
		prevOf[int64(r.Seq)] = u.prev
		users = append(users, u)
	}
	// 树上的 prev 链本身成环（互为前驱）→ 链上的全部标 orphan 并钳到 root：
	// 结构上前驱循环意味着父子关系已经损坏，沿它回溯路径会死循环（guard 只是
	// 兜底，不该成为合法路径）。先判定全部成员、再统一清除（边判边清会让后判定
	// 的成员看到被截断的链，漏标）。
	cyclic := make(map[int64]bool, 4)
	for ui := range users {
		seq := int64(users[ui].row.Seq)
		seen := map[int64]bool{}
		for cur, guard := seq, 0; cur > 0 && guard <= len(users); guard++ {
			if seen[cur] {
				for s := range seen {
					cyclic[s] = true
				}
				break
			}
			seen[cur] = true
			cur = prevOf[cur]
		}
	}
	for seq := range cyclic {
		for ui := range users {
			if int64(users[ui].row.Seq) == seq {
				users[ui].orphan = true
				users[ui].prev = 0
				prevOf[seq] = 0
			}
		}
	}

	// ② 任意 seq 的「最近 user 祖先」（含自己）：leaf 常是 assistant（历史末尾是
	//    回复）——先把叶折到最近的 user，再沿 prev 链回溯路径。
	nearestUser := func(seq int64) int64 {
		for cur, guard := seq, 0; cur > 0 && guard <= len(rows); guard++ {
			j, ok := bySeq[cur]
			if !ok {
				return 0 // 断链
			}
			if isUser(rows[j]) {
				return cur
			}
			cur = int64(rows[j].ParentSeq)
		}
		return 0
	}
	leafUser := int64(0)
	if leaf > 0 {
		leafUser = nearestUser(leaf)
	}
	for cur, guard := leafUser, 0; cur > 0 && guard <= len(users); guard++ {
		for ui := range users {
			if int64(users[ui].row.Seq) == cur {
				users[ui].onPath = true
				break
			}
		}
		cur = prevOf[cur]
	}

	// ③ 子节点数（树上：prev 相同的 user 互为兄弟；>1 = 分支点）。
	children := make(map[int64]int, len(users))
	for ui := range users {
		if p := users[ui].prev; p > 0 {
			children[p]++
		}
	}

	// ④ 截断：按 seq 从大到小取最近 N 个 user。maxNodes<=0 = 全量（kept 全 true）。
	//    截断后树上前驱不在结果里的节点钳到 root（parent=0）——前端按 parent 重建
	//    父子，断父节点会挂错位置；「还有更早的」由 truncated 提示承担。
	total := len(users)
	kept := make([]bool, len(users))
	truncated := false
	if maxNodes > 0 && total > maxNodes {
		truncated = true
		for ui := total - 1; ui >= 0 && total-1-ui < maxNodes; ui-- {
			kept[ui] = true
		}
	} else {
		for ui := range kept {
			kept[ui] = true
		}
	}

	// ⑤ 组装：截断后树上前驱不在结果里的节点钳到 root（parent=0，前端把它画成
	//    树顶的孤立行——「还有更早的」由 truncated 提示承担）。轮次编号（真实
	//    user 从 1 起，与 SessionStats.turns 同口径）只数保留的节点。
	turn := 0
	keptSeq := make(map[int64]bool, len(users))
	for ui := range users {
		if kept[ui] {
			keptSeq[int64(users[ui].row.Seq)] = true
		}
	}
	nodes := make([]TreeNode, 0, len(users))
	for ui := range users {
		u := &users[ui]
		if !kept[ui] {
			continue
		}
		turn++
		parent := u.prev
		if parent > 0 && !keptSeq[parent] {
			parent = 0 // 前驱被截断：钳到 root，不指一个不存在的节点
		}
		nodes = append(nodes, TreeNode{
			Seq: int64(u.row.Seq), ParentSeq: parent, Kind: "user",
			Turn: turn, Summary: u.row.Summary, Children: children[int64(u.row.Seq)],
			OnPath: u.onPath, Orphan: u.orphan,
		})
	}
	return Tree{LeafSeq: leafUser, Nodes: nodes, Total: total, Truncated: truncated}
}

// projectCounts 把快照计数从 owner_seq（assistant 的 seq）投影到树节点（user 的
// seq）：owner_seq 归「≤ owner_seq 的最大 user seq」那一轮——本轮 assistant 的
// seq 恒大于本轮 user、小于下一轮 user（下一轮的 parent 链必经本轮 assistant），
// 所以这条投影在主链与分支上都成立。owner_seq 在第一条 user 之前 = 无处归属，跳过。
func projectCounts(nodes []TreeNode, counts map[int64][2]int) {
	if len(counts) == 0 || len(nodes) == 0 {
		return
	}
	for seq, c := range counts {
		if seq < nodes[0].Seq {
			continue // 第一条 user 之前：无处归属（不编一个孤儿轮）
		}
		// nodes 按 seq 升序：线性找「≤ seq 的最大 user」的下一个边界即可。
		i := 0
		for i+1 < len(nodes) && nodes[i+1].Seq <= seq {
			i++
		}
		nodes[i].Files += c[0]
		nodes[i].ChildFiles += c[1]
	}
}

// sortedSeqs 返回升序的 seq 列表（测试与调试用；生产路径按 rows 原序）。
func sortedSeqs(nodes []TreeNode) []int64 {
	out := make([]int64, len(nodes))
	for i, n := range nodes {
		out[i] = n.Seq
	}
	slices.Sort(out)
	return out
}
