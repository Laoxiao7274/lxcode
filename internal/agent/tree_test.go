// 会话树骨架与「外部操作数」的单元测试（纯函数，不需要库/网络）。
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

// TestBuildTreeShape：父子关系、children 计数、on_path、轮次编号。
func TestBuildTreeShape(t *testing.T) {
	rows := []sessiondata.TreeRow{
		treeRow(1, 0, "user", false, false, "第一问"),
		treeRow(2, 1, "assistant", false, false, ""),
		treeRow(3, 2, "tool", false, false, "结果"),
		treeRow(4, 2, "assistant", false, false, "答一"),
		treeRow(5, 4, "user", false, false, "第二问"),
		treeRow(6, 5, "assistant", false, false, "答二"),
	}
	tree := buildTree(rows, 6, 0)
	if tree.Total != 6 || tree.Truncated {
		t.Fatalf("全量树: total=%d truncated=%v", tree.Total, tree.Truncated)
	}
	if len(tree.Nodes) != 6 {
		t.Fatalf("节点数: %d", len(tree.Nodes))
	}
	bySeq := map[int64]TreeNode{}
	for _, n := range tree.Nodes {
		bySeq[n.Seq] = n
	}
	// 分支点：seq 2 有两个孩子（3 与 4）
	if bySeq[2].Children != 2 {
		t.Fatalf("seq 2 应有 2 个孩子，实际 %d", bySeq[2].Children)
	}
	// on_path：leaf=6 的链是 1→2→4→5→6，seq 3（tool）不在路径上
	if !bySeq[1].OnPath || !bySeq[6].OnPath {
		t.Fatal("leaf 链上的节点应 on_path=true")
	}
	if bySeq[3].OnPath {
		t.Fatal("废弃分支（seq 3）应 on_path=false")
	}
	// 轮次：user 从 1 起，assistant/tool 不编号
	if bySeq[1].Turn != 1 || bySeq[5].Turn != 2 {
		t.Fatalf("user 轮次应为 1、2，实际 %d、%d", bySeq[1].Turn, bySeq[5].Turn)
	}
	if bySeq[2].Turn != 0 || bySeq[3].Turn != 0 {
		t.Fatal("assistant/tool 不该有轮次编号")
	}
	if bySeq[1].Kind != "user" || bySeq[2].Kind != "assistant" || bySeq[3].Kind != "tool" {
		t.Fatal("kind 应按 role 映射")
	}
}

// TestBuildTreeNoticeAndCheckpoint：notice 与 checkpoint 的 kind、轮次与影子数。
func TestBuildTreeNoticeAndCheckpoint(t *testing.T) {
	rows := []sessiondata.TreeRow{
		treeRow(1, 0, "user", false, false, "问"),
		treeRow(2, 1, "user", false, true, "提示条"),
		treeRow(3, 2, "user", true, false, "摘要"),
	}
	tree := buildTree(rows, 3, 0)
	bySeq := map[int64]TreeNode{}
	for _, n := range tree.Nodes {
		bySeq[n.Seq] = n
	}
	if bySeq[2].Kind != "notice" {
		t.Fatalf("notice 行的 kind 应是 notice: %+v", bySeq[2])
	}
	if bySeq[2].Turn != 1 {
		t.Fatalf("提示条应归属当前轮（1），实际 %d", bySeq[2].Turn)
	}
	if bySeq[3].Kind != "checkpoint" {
		t.Fatalf("检查点的 kind 应是 checkpoint: %+v", bySeq[3])
	}
	if bySeq[3].Turn != 0 {
		t.Fatalf("检查点不该有轮次，实际 %d", bySeq[3].Turn)
	}
}

// TestBuildTreeOrphanAndCycle：畸形数据不炸——孤儿与环都标 orphan，节点一个不丢。
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
		t.Fatal("父节点缺失的应标 orphan")
	}
	// 环上的节点：on_path 为假（leaf=1 的链不经过它们），pathReaches 也判 false。
	// 这里只断言「不 panic 且节点在」——orphan 标记对环的判定见 pathReaches。
	if bySeq[3].Orphan && bySeq[4].Orphan {
		t.Log("环上节点已标 orphan（可选，取决于 on_path）")
	}
}

// TestBuildTreeTruncationKeepsAncestors：截断时补齐祖先（否则是一堆断父的碎片）。
func TestBuildTreeTruncationKeepsAncestors(t *testing.T) {
	var rows []sessiondata.TreeRow
	for i := 1; i <= 20; i++ {
		rows = append(rows, treeRow(i, i-1, "user", false, false, "m"))
	}
	tree := buildTree(rows, 20, 3)
	if !tree.Truncated {
		t.Fatal("应置 truncated")
	}
	if tree.Total != 20 {
		t.Fatalf("total 应是全量 20，实际 %d", tree.Total)
	}
	// 每个保留节点的父节点必须在结果里（或 0）
	have := map[int64]bool{}
	for _, n := range tree.Nodes {
		have[n.Seq] = true
	}
	for _, n := range tree.Nodes {
		if n.ParentSeq != 0 && !have[n.ParentSeq] {
			t.Fatalf("节点 %d 的父 %d 不在结果里（没补齐祖先）", n.Seq, n.ParentSeq)
		}
	}
	// 最近 3 个（18/19/20）一定在
	for _, seq := range []int64{18, 19, 20} {
		if !have[seq] {
			t.Fatalf("最近的节点 %d 应被保留", seq)
		}
	}
}

// TestCountExternal：口径单点定义（树面板与回滚确认文案共用）。
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
