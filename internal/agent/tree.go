// 会话树骨架（chat.tree 的数据源）：节点 + 父子关系 + 当前路径标记 + 分支摘要。
//
// 三条纪律（docs/session-tree.md §A.6/§A.7）：
//   - **不走 readRows**：那是全列读（含 KB 级正文）。树只需要骨架，sessiondata.TreeRows
//     用 substr(content,1,120) 的轻量投影（大会话 53k 行时 readRows ~195ms）。
//   - **畸形数据不炸**：孤儿（父指针指向不存在的行）与环都标 orphan，不丢节点、
//     不抛异常——丢节点等于藏数据，抛异常等于整条会话的树打不开。
//   - **截断不静默**：max_nodes 生效时按 seq 从大到小取最近 N 个再补齐祖先，
//     并且 truncated=true（前端显示「更早的 N 个节点未加载」）。
package agent

import (
	"slices"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// TreeNode 是树里的一个节点（wire 形态由 protocol.TreeNode 承载）。
type TreeNode struct {
	Seq       int64
	ParentSeq int64
	// Kind = user | assistant | tool | checkpoint | notice（notice 是注入的提示条，
	// 前端在树上与 user 区分显示）。
	Kind string
	// Turn 是轮次序号（user / notice 才有；与 SessionStats.turns、右栏 turnGroups
	// 同口径：真实 user 消息从 1 起，notice 与 checkpoint 不算）。
	Turn int
	// Summary 是单行摘要（≤120 字符，不含正文全文）。
	Summary string
	// Children 是直接子节点数（>1 = 分支点）。
	Children int
	// OnPath = 是否在当前 leaf 路径上（废弃分支 = false）。
	OnPath bool
	// Orphan = 父节点缺失或成环（畸形数据的兜底）。
	Orphan bool
	// Shadowed 是检查点影子掉的条数（checkpoint 才有）。
	Shadowed int
	// Files = 该轮（owner_seq 落在这一轮）可还原的文件改动数，**含子会话**。
	// ChildFiles = 其中来自子会话的改动数（Files 的子集）。
	// External = 该轮无法快照的写（bash/MCP/自定义工具，含子会话里的 bash）。
	// 三者都是 0 = 未知/无（wire 上整键缺席，前端不渲染徽标——不显示 0）。
	Files      int
	ChildFiles int
	External   int
}

// Tree 是会话树骨架。
type Tree struct {
	LeafSeq   int64
	Nodes     []TreeNode
	Total     int
	Truncated bool
}

// Tree 返回会话树骨架（按 seq 升序）。
//
// maxNodes <= 0 = 全量；> 0 = 只回最近 maxNodes 个节点（再补齐它们的祖先）。
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
	// 文件徽标（批次 3）：按 **owner_seq** 归到轮节点上——用户看到的是「这一轮
	// 回滚能还原几个文件」，含子 Agent 的改动。owner = 顶层会话 id（子会话的树
	// 与它的文件改动都算在父会话账上）。
	//
	// 失败只记日志（徽标是展示信息，不该让整棵树打不开）。
	ownerID, err := st.OwnerSessionID(id)
	if err != nil {
		return tree, nil
	}
	if counts, err := st.SnapshotCountsByOwnerSeq(ownerID); err == nil {
		for i := range tree.Nodes {
			if c, ok := counts[tree.Nodes[i].Seq]; ok {
				tree.Nodes[i].Files, tree.Nodes[i].ChildFiles = c[0], c[1]
			}
		}
	}
	return tree, nil
}

// buildTree 是纯函数（输入骨架行 + leaf，输出树）——树形状的全部规则都在这里，
// 单测直接喂数据，不需要库。
func buildTree(rows []sessiondata.TreeRow, leaf int64, maxNodes int) Tree {
	total := len(rows)
	bySeq := make(map[int64]int, len(rows)) // seq → 下标
	for i, r := range rows {
		bySeq[int64(r.Seq)] = i
	}
	// 当前路径：沿 parent 链从 leaf 回溯（与 store.pathRows 同一段逻辑，但这里
	// 只需要集合，不需要顺序）。环与断链都钳住（guard 上界 = 行数）。
	onPath := make(map[int64]bool, 64)
	for cur, guard := leaf, 0; cur > 0 && guard <= len(rows); guard++ {
		if _, ok := bySeq[cur]; !ok {
			break
		}
		if onPath[cur] {
			break
		}
		onPath[cur] = true
		cur = int64(rows[bySeq[cur]].ParentSeq)
	}

	// 截断：按 seq 从大到小取最近 N 个，再补齐它们的祖先（否则是一堆断父的碎片）。
	keep := make([]bool, len(rows))
	truncated := false
	if maxNodes > 0 && len(rows) > maxNodes {
		truncated = true
		for i := len(rows) - 1; i >= 0 && len(rows)-1-i < maxNodes; i-- {
			keep[i] = true
		}
		// 补齐祖先（沿 parent 链；环钳住）。
		for i := range keep {
			if !keep[i] {
				continue
			}
			for cur, guard := int64(rows[i].ParentSeq), 0; cur > 0 && guard <= len(rows); guard++ {
				j, ok := bySeq[cur]
				if !ok || keep[j] {
					break
				}
				keep[j] = true
				cur = int64(rows[j].ParentSeq)
			}
		}
	}

	// 子节点数（只数被保留的节点之间的边；截断时祖先补齐保证边完整）。
	children := make(map[int64]int, len(rows))
	for i, r := range rows {
		if !keep[i] && truncated {
			continue
		}
		if r.ParentSeq > 0 {
			if _, ok := bySeq[int64(r.ParentSeq)]; ok {
				children[int64(r.ParentSeq)]++
			}
		}
	}

	turn := 0
	nodes := make([]TreeNode, 0, len(rows))
	for i, r := range rows {
		if truncated && !keep[i] {
			continue
		}
		kind := r.Role
		if r.Checkpoint {
			kind = "checkpoint"
		} else if r.Notice {
			kind = "notice"
		}
		// 轮次编号：真实 user 消息从 1 起（notice / checkpoint 不算——与
		// SessionStats.turns、右栏 turnGroups 同一条口径）。
		nodeTurn := 0
		if kind == "user" {
			turn++
			nodeTurn = turn
		} else if kind == "notice" {
			nodeTurn = turn // 提示条归属"当前轮"（它插在某一轮里）
		}
		orphan := false
		if r.ParentSeq > 0 {
			if _, ok := bySeq[int64(r.ParentSeq)]; !ok {
				orphan = true // 父节点缺失
			}
		}
		if onPath[int64(r.Seq)] && !pathReaches(rows, bySeq, int64(r.Seq)) {
			orphan = true // 环上的节点（回溯走不到 root）
		}
		nodes = append(nodes, TreeNode{
			Seq: int64(r.Seq), ParentSeq: int64(r.ParentSeq), Kind: kind,
			Turn: nodeTurn, Summary: r.Summary, Children: children[int64(r.Seq)],
			OnPath: onPath[int64(r.Seq)], Orphan: orphan,
		})
	}
	return Tree{LeafSeq: leaf, Nodes: nodes, Total: total, Truncated: truncated}
}

// pathReaches 报告从 seq 沿 parent 链能否走到 root（无环）。树面板用它把环上的
// 节点标成 orphan（DFS 已访问即停，与 buildTree 的回溯同一套钳制）。
func pathReaches(rows []sessiondata.TreeRow, bySeq map[int64]int, seq int64) bool {
	seen := make(map[int64]bool, 16)
	for cur, guard := seq, 0; cur > 0 && guard <= len(rows)+1; guard++ {
		if seen[cur] {
			return false // 环
		}
		seen[cur] = true
		i, ok := bySeq[cur]
		if !ok {
			return false // 断链
		}
		cur = int64(rows[i].ParentSeq)
	}
	return true
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
