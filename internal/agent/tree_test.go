// 会话树骨架（user-only 模型）的单元测试（纯函数，不需要库/网络）。
package agent

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// treeRow 构造一个骨架行。
func treeRow(seq, parent int, role string, cp, notice bool, summary string) sessiondata.TreeRow {
	return sessiondata.TreeRow{
		Seq: seq, ParentSeq: parent, Role: role, Checkpoint: cp, Notice: notice,
		ContentLen: len(summary), Summary: summary,
	}
}

// TestBuildTreeUserOnly：只回 user 节点；parent = 树上最近前驱 user（跨过
// assistant/tool）；轮次按 user 编号。
func TestBuildTreeUserOnly(t *testing.T) {
	rows := []sessiondata.TreeRow{
		treeRow(1, 0, "user", false, false, "第一问"),
		treeRow(2, 1, "assistant", false, false, "答一"),
		treeRow(3, 2, "tool", false, false, "结果"),
		treeRow(4, 2, "assistant", false, false, "答一补"),
		treeRow(5, 4, "user", false, false, "第二问"),
		treeRow(6, 5, "assistant", false, false, "答二"),
	}
	tree := buildTree(rows, 6, 0)
	if tree.Total != 2 || tree.Truncated {
		t.Fatalf("total 应按 user 计 = 2，实际 %d (truncated=%v)", tree.Total, tree.Truncated)
	}
	if len(tree.Nodes) != 2 {
		t.Fatalf("只应有 2 个 user 节点，实际 %d", len(tree.Nodes))
	}
	first, second := tree.Nodes[0], tree.Nodes[1]
	if first.Kind != "user" || first.Turn != 1 || first.ParentSeq != 0 {
		t.Fatalf("首节点不符: %+v", first)
	}
	if second.ParentSeq != 1 {
		t.Fatalf("第二问的前驱应是第一问（跨过 assistant/tool）: %+v", second)
	}
	if second.Turn != 2 {
		t.Fatalf("轮次应按 user 编: %+v", second)
	}
	// leaf=6 是 assistant：树的 LeafSeq 应折到最近的 user（seq 5）
	if tree.LeafSeq != 5 {
		t.Fatalf("LeafSeq 应折到最近的 user（5），实际 %d", tree.LeafSeq)
	}
	if !first.OnPath || !second.OnPath {
		t.Fatal("主链上的 user 都应 on_path=true")
	}
}

// TestBuildTreeBranch：分支结构保留——废弃分支的 user 在树上、on_path=false，
// 分叉点的 children>1。
func TestBuildTreeBranch(t *testing.T) {
	rows := []sessiondata.TreeRow{
		treeRow(1, 0, "user", false, false, "问"),
		treeRow(2, 1, "assistant", false, false, "答"),
		// 旧分支：第二问（将被废弃）
		treeRow(3, 2, "user", false, false, "旧第二问"),
		treeRow(4, 3, "assistant", false, false, "旧答"),
		// 新分支：rewind 到 2 之后从同一个前驱长出
		treeRow(5, 2, "user", false, false, "新第二问"),
		treeRow(6, 5, "assistant", false, false, "新答"),
	}
	tree := buildTree(rows, 6, 0)
	bySeq := map[int64]TreeNode{}
	for _, n := range tree.Nodes {
		bySeq[n.Seq] = n
	}
	if len(tree.Nodes) != 3 {
		t.Fatalf("应有 3 个 user 节点（含废弃分支），实际 %d", len(tree.Nodes))
	}
	if bySeq[1].Children != 2 {
		t.Fatalf("分叉点应 children=2，实际 %d", bySeq[1].Children)
	}
	if bySeq[3].OnPath {
		t.Fatal("废弃分支的 user 应 on_path=false")
	}
	if !bySeq[5].OnPath {
		t.Fatal("当前分支的 user 应 on_path=true")
	}
	if bySeq[3].ParentSeq != 1 || bySeq[5].ParentSeq != 1 {
		t.Fatalf("两条分支的 user 都应挂在分叉点上: %+v %+v", bySeq[3], bySeq[5])
	}
}

// TestBuildTreeNoticeAndCheckpointNotOnTree：notice 与 checkpoint 不出现在树上，
// 但不挡前驱查找（中间夹了它们，user 链照样连贯）。
func TestBuildTreeNoticeAndCheckpointNotOnTree(t *testing.T) {
	rows := []sessiondata.TreeRow{
		treeRow(1, 0, "user", false, false, "问"),
		treeRow(2, 1, "user", false, true, "提示条"),
		treeRow(3, 2, "user", true, false, "摘要"),
		treeRow(4, 3, "user", false, false, "第二问"),
	}
	tree := buildTree(rows, 4, 0)
	if len(tree.Nodes) != 2 {
		t.Fatalf("notice/checkpoint 不该上树: %d 节点", len(tree.Nodes))
	}
	if tree.Nodes[1].Seq != 4 || tree.Nodes[1].ParentSeq != 1 {
		t.Fatalf("第二问应跨过 notice/checkpoint 挂到第一问: %+v", tree.Nodes[1])
	}
	if tree.Nodes[1].Turn != 2 {
		t.Fatalf("notice/checkpoint 不占轮次: %+v", tree.Nodes[1])
	}
}

// TestBuildTreeOrphanAndCycle：畸形数据不炸——找不到前驱 user 的挂 root 并标
// orphan，节点一个不丢。
func TestBuildTreeOrphanAndCycle(t *testing.T) {
	rows := []sessiondata.TreeRow{
		treeRow(1, 0, "user", false, false, "正常"),
		// 孤儿：父 99 不存在
		treeRow(2, 99, "user", false, false, "孤儿"),
		// 环：3.parent=4、4.parent=3
		treeRow(3, 4, "user", false, false, "环A"),
		treeRow(4, 3, "user", false, false, "环B"),
	}
	tree := buildTree(rows, 1, 0)
	if len(tree.Nodes) != 4 {
		t.Fatalf("畸形数据不该丢节点: %d", len(tree.Nodes))
	}
	bySeq := map[int64]TreeNode{}
	for _, n := range tree.Nodes {
		bySeq[n.Seq] = n
	}
	if !bySeq[2].Orphan {
		t.Fatal("父链断掉的应标 orphan")
	}
	if !bySeq[3].Orphan || !bySeq[4].Orphan {
		t.Fatal("环上找不到前驱的应标 orphan")
	}
	for _, seq := range []int64{2, 3, 4} {
		if bySeq[seq].ParentSeq != 0 {
			t.Fatalf("孤儿应挂 root（parent=0），seq %d = %d", seq, bySeq[seq].ParentSeq)
		}
	}
}

// TestBuildTreeTruncationKeepsAncestors：截断按 user 计数并补齐树上祖先。
func TestBuildTreeTruncationKeepsAncestors(t *testing.T) {
	var rows []sessiondata.TreeRow
	// 10 轮（每轮 user+assistant）
	for i := 1; i <= 10; i++ {
		rows = append(rows, treeRow(2*i-1, 2*i-2, "user", false, false, "问"))
		rows = append(rows, treeRow(2*i, 2*i-1, "assistant", false, false, "答"))
	}
	tree := buildTree(rows, 20, 3)
	if !tree.Truncated {
		t.Fatal("应置 truncated")
	}
	if tree.Total != 10 {
		t.Fatalf("total 应按 user 计 = 10，实际 %d", tree.Total)
	}
	if len(tree.Nodes) != 3 {
		t.Fatalf("应只回最近 3 个 user，实际 %d", len(tree.Nodes))
	}
	// 最近 3 个 user 的 seq = 15、17、19
	for _, seq := range []int64{15, 17, 19} {
		found := false
		for _, n := range tree.Nodes {
			if n.Seq == seq {
				found = true
			}
		}
		if !found {
			t.Fatalf("最近的 user %d 应被保留", seq)
		}
	}
	// 每个保留节点的树上前驱必须在结果里（或 0）
	have := map[int64]bool{}
	for _, n := range tree.Nodes {
		have[n.Seq] = true
	}
	for _, n := range tree.Nodes {
		if n.ParentSeq != 0 && !have[n.ParentSeq] {
			t.Fatalf("节点 %d 的前驱 %d 不在结果里（没补齐祖先）", n.Seq, n.ParentSeq)
		}
	}
}

// TestProjectCounts：快照计数从 owner_seq（assistant 的 seq）投影到所属 user 轮。
func TestProjectCounts(t *testing.T) {
	// 节点：user@1（轮1）、user@5（轮2）、user@9（轮3）
	nodes := []TreeNode{
		{Seq: 1}, {Seq: 5}, {Seq: 9},
	}
	counts := map[int64][2]int{
		2: {3, 1}, // 轮 1 的 assistant
		6: {2, 0}, // 轮 2 的 assistant
		7: {1, 1}, // 轮 2 的第二个 assistant（派发结论）
		9: {4, 2}, // 挂在 user 自己 seq 上（兜底）
	}
	projectCounts(nodes, counts)
	if nodes[0].Files != 3 || nodes[0].ChildFiles != 1 {
		t.Fatalf("轮 1 应拿到 3/1，实际 %d/%d", nodes[0].Files, nodes[0].ChildFiles)
	}
	if nodes[1].Files != 3 || nodes[1].ChildFiles != 1 {
		t.Fatalf("轮 2 应拿到 2+1/0+1，实际 %d/%d", nodes[1].Files, nodes[1].ChildFiles)
	}
	if nodes[2].Files != 4 || nodes[2].ChildFiles != 2 {
		t.Fatalf("轮 3 应拿到 4/2，实际 %d/%d", nodes[2].Files, nodes[2].ChildFiles)
	}
}

// TestProjectCountsEmpty：空计数 / 空节点不炸。
func TestProjectCountsEmpty(t *testing.T) {
	projectCounts(nil, nil)
	projectCounts([]TreeNode{{Seq: 1}}, nil)
	projectCounts(nil, map[int64][2]int{1: {1, 0}})
}

// TestCountExternal：口径单点定义（回滚确认文案共用）。
func TestCountExternal(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		mutates bool
		result  string
		want    int
	}{
		{"bash 改文件", "bash", true, "已执行", 1},
		{"MCP 工具", "mcp_write", true, "ok", 1},
		{"只读工具", "read_file", false, "内容", 0},
		{"edit 正常抓到快照", "edit", true, "已修改 a.go（1 处替换）", 0},
		{"edit 快照被跳过", "edit", true, "已修改 big.txt（1 处替换）" + snapshotSkipMarker, 1},
		{"write_file 正常", "write_file", true, "已写入 a.go（10 字节）", 0},
		{"write_file 被跳过", "write_file", true, "已写入 big.txt（10 字节）" + snapshotSkipMarker, 1},
	}
	for _, c := range cases {
		if got := countExternal(c.tool, c.mutates, c.result); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

// TestTurnCounters：按轮记账与累加。
func TestTurnCounters(t *testing.T) {
	c := newTurnCounters()
	c.add(10, 2)
	c.add(10, 1)
	c.add(20, 5)
	if got := c.countOf(10); got != 3 {
		t.Fatalf("第 10 轮应有 3 处，实际 %d", got)
	}
	if got := c.countOf(20); got != 5 {
		t.Fatalf("第 20 轮应有 5 处，实际 %d", got)
	}
	if got := c.countOf(0); got != 0 {
		t.Fatalf("0 不是合法轮次: %d", got)
	}
	c.add(0, 3) // 忽略
	if got := c.countOf(0); got != 0 {
		t.Fatalf("锚点 0 不该记账: %d", got)
	}
}
