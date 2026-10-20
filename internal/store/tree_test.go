// 会话树的存储层契约（docs/session-tree.md 批次 1）：
//
//  1. 存量迁移按 **surface 顺序**回填父链（检查点接入树），单链会话回放逐字节等价；
//  2. 迁移幂等（重复跑不损坏）、失败整体回滚（不留半迁移状态）；
//  3. 路径构造 = leaf 回溯的可达行 + 现有 surfaceRows —— 树模型不需要新回放算法；
//  4. 压缩必须重接父指针（否则摘要在树里成孤立分支）；
//  5. leaf_seq = 0 是**空路径**（合法状态），不是「未知」；
//  6. List 的可见性不能看 leaf_len（否则撤回第一条后会话从侧栏消失）。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// ---- 测试辅助 ----

// appendRoles 按给定角色序列追加消息（内容带序号，便于断言顺序）。
func appendRoles(t *testing.T, s *Store, id string, roles ...string) []int64 {
	t.Helper()
	seqs := make([]int64, 0, len(roles))
	for i, role := range roles {
		seq, err := s.AppendMsg(id, llm.Message{Role: role, Content: fmt.Sprintf("%s#%d", role, i+1)})
		if err != nil {
			t.Fatalf("追加第 %d 条失败: %v", i, err)
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

// appendCheckpointForTest 追加一个检查点（skip/count 是**当前路径 surface** 的下标）。
func appendCheckpointForTest(t *testing.T, s *Store, id string, skip, count int) int64 {
	t.Helper()
	seq, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "摘要"}, skip, count)
	if err != nil {
		t.Fatalf("追加检查点失败: %v", err)
	}
	return seq
}

// parentOf 读某条消息的父指针。
func parentOf(t *testing.T, s *Store, id string, seq int64) int {
	t.Helper()
	var p int
	if err := s.db.QueryRow(`SELECT parent_seq FROM messages WHERE session_id = ? AND seq = ?`, id, seq).Scan(&p); err != nil {
		t.Fatalf("读 parent_seq 失败: %v", err)
	}
	return p
}

// leafAndLen 读叶指针与路径长度。
func leafAndLen(t *testing.T, s *Store, id string) (int, int) {
	t.Helper()
	var leaf, n int
	if err := s.db.QueryRow(`SELECT leaf_seq, leaf_len FROM sessions WHERE id = ?`, id).Scan(&leaf, &n); err != nil {
		t.Fatalf("读 leaf 失败: %v", err)
	}
	return leaf, n
}

// userVersion 读 PRAGMA user_version。
func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("读 user_version 失败: %v", err)
	}
	return v
}

// ---- 迁移 ----

// TestMigrationBackfillsChain：新库（已迁移）的普通消息按 seq 链式回填父指针，
// leaf 指向最后一条，leaf_len 等于条数。
func TestMigrationBackfillsChain(t *testing.T) {
	s := openTestStore(t)
	if !s.treeMigrated {
		t.Fatal("新库应当已跑过会话树迁移（treeMigrated=true）")
	}
	if v := userVersion(t, s); v != treeSchemaVersion {
		t.Fatalf("user_version 应为 %d，实际 %d", treeSchemaVersion, v)
	}
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user")

	want := []int{0, int(seqs[0]), int(seqs[1])}
	for i, seq := range seqs {
		if got := parentOf(t, s, id, seq); got != want[i] {
			t.Fatalf("seq %d 的父指针应为 %d，实际 %d", seq, want[i], got)
		}
	}
	leaf, n := leafAndLen(t, s, id)
	if leaf != int(seqs[2]) || n != 3 {
		t.Fatalf("leaf/leaf_len 应为 %d/3，实际 %d/%d", seqs[2], leaf, n)
	}
}

// TestMigrationIsIdempotent：迁移重复跑不改变任何东西（幂等由 user_version 守卫）。
func TestMigrationIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendRoles(t, s, id, "user", "assistant", "user", "assistant")

	snapshot := func() string {
		rows, err := s.readRows(id)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(rows)
		leaf, n := leafAndLen(t, s, id)
		return string(b) + fmt.Sprintf("|leaf=%d|len=%d", leaf, n)
	}
	before := snapshot()

	// 强制重跑：把版本号拨回去（模拟「上次迁移后崩溃在写版本号之前」）。
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	s.treeMigrated = false
	if err := s.migrateSessionTree(); err != nil {
		t.Fatalf("重跑迁移失败: %v", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("迁移不幂等：\n前 %s\n后 %s", before, after)
	}
	if v := userVersion(t, s); v != treeSchemaVersion {
		t.Fatalf("重跑后 user_version 应为 %d，实际 %d", treeSchemaVersion, v)
	}
}

// TestMigrationConnectsCheckpointToTree：存量库里带前缀压缩时，父链按 **surface 顺序**
// 重写——检查点接在它影子段的位置上，后继行挂到检查点之下（而不是挂在被影子原文后面）。
func TestMigrationConnectsCheckpointToTree(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user")
	// 压缩前 3 条（前缀）→ 检查点影子 seqs[0..2]，后继是 seqs[3]
	cp := appendCheckpointForTest(t, s, id, 0, 3)

	// 模拟老库：清掉全部父指针与叶指针，然后重跑迁移。
	if _, err := s.db.Exec(`UPDATE messages SET parent_seq = 0 WHERE session_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET leaf_seq = 0, leaf_len = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	s.treeMigrated = false
	if err := s.migrateSessionTree(); err != nil {
		t.Fatal(err)
	}

	// surface 顺序 = [CK, A4, A5]（CK 落在被影子段的位置）
	if got := parentOf(t, s, id, cp); got != 0 {
		t.Fatalf("检查点应是 root（父指针 0），实际 %d", got)
	}
	if got := parentOf(t, s, id, seqs[3]); got != int(cp) {
		t.Fatalf("被影子段之后那条应挂在检查点 %d 下，实际父指针 %d", cp, got)
	}
	if got := parentOf(t, s, id, seqs[4]); got != int(seqs[3]) {
		t.Fatalf("尾部最后一条应挂在 %d 下，实际 %d", seqs[3], got)
	}
	leaf, n := leafAndLen(t, s, id)
	if leaf != int(seqs[4]) || n != 3 {
		t.Fatalf("leaf/leaf_len 应为 %d/3，实际 %d/%d", seqs[4], leaf, n)
	}
}

// TestMigrationFailureRollsBack：迁移中途失败必须整体回滚（不留半迁移状态）。
//
// 用「把 messages 表换成只读触发器」制造失败：迁移里的 UPDATE 报错 → 事务回滚 →
// user_version 保持 0、parent_seq 一个都没写。
func TestMigrationFailureRollsBack(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user")

	// 清成「未迁移」状态。
	if _, err := s.db.Exec(`UPDATE messages SET parent_seq = 0 WHERE session_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET leaf_seq = 0, leaf_len = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	// 让第二条消息的 UPDATE 失败（第一条成功后失败，验证「已做的部分也回滚」）。
	if _, err := s.db.Exec(fmt.Sprintf(
		`CREATE TRIGGER fail_tree_migration BEFORE UPDATE ON messages
		 WHEN OLD.seq = %d BEGIN SELECT RAISE(ABORT, 'boom'); END`, seqs[1])); err != nil {
		t.Fatal(err)
	}

	s.treeMigrated = false
	err := s.migrateSessionTree()
	if err == nil {
		t.Fatal("迁移应当失败（触发器制造的错误）")
	}
	// 回滚验证：一条父指针都没写进去、版本号仍是 0。
	for _, seq := range seqs {
		if got := parentOf(t, s, id, seq); got != 0 {
			t.Fatalf("失败迁移留下了半成品：seq %d 的父指针被写成 %d", seq, got)
		}
	}
	if v := userVersion(t, s); v != 0 {
		t.Fatalf("失败迁移不该推进版本号，实际 %d", v)
	}
	if s.treeMigrated {
		t.Fatal("失败迁移不该把 treeMigrated 置真")
	}
}

// ---- 单链等价性（硬要求）----

// TestMigrationLinearEquivalence：单链会话迁移前后 Load 结果**逐字节相同**。
//
// 这是批次 1 的核心钉子：树模型不改变单链会话的回放结果。做法是把同一段历史
// 造两次——一次在已迁移的新库（正常写入路径），一次在「模拟老库」（清掉父/叶指针
// 后重跑迁移）——然后逐字段比较。
func TestMigrationLinearEquivalence(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	// 混合形态：user / assistant+tool_calls / tool / 通知行 / 多轮压缩
	seq1, _ := s.AppendMsg(id, llm.Message{Role: "user", Content: "第一问"})
	seq2, _ := s.AppendMsg(id, llm.Message{Role: "assistant", Content: "",
		ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: "read_file", Arguments: `{"path":"a.go"}`}}}})
	_, _ = s.AppendMsg(id, llm.Message{Role: "tool", Content: "文件内容", ToolCallID: "c1", DurationMs: 42})
	_, _ = s.AppendMsg(id, llm.Message{Role: "assistant", Content: "答一"})
	_, _ = s.AppendMsg(id, llm.Message{Role: "user", Content: "提示条", Notice: true})
	_, _ = s.AppendMsg(id, llm.Message{Role: "user", Content: "第二问"})
	_, _ = s.AppendMsg(id, llm.Message{Role: "assistant", Content: "答二"})
	_ = seq1
	_ = seq2
	// 一次前缀压缩 + 一次中间段压缩（让 surface 里有检查点）
	appendCheckpointForTest(t, s, id, 0, 3)
	appendCheckpointForTest(t, s, id, 1, 2)

	want, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("样本历史不该为空")
	}
	// 单链下「未迁移语义」= 整表 surfaceRows，两者必须相同。
	rows, err := s.readRows(id)
	if err != nil {
		t.Fatal(err)
	}
	legacy := surfaceRows(rows)
	if len(legacy) != len(want) {
		t.Fatalf("迁移后 Load 条数 %d 与旧 surfaceRows %d 不一致", len(want), len(legacy))
	}
	for i := range want {
		if want[i].Seq != int64(legacy[i].seq) || want[i].Role != legacy[i].msg.Role ||
			want[i].Content != legacy[i].msg.Content || want[i].ToolCallID != legacy[i].msg.ToolCallID ||
			want[i].Notice != legacy[i].msg.Notice {
			t.Fatalf("第 %d 条与旧 surfaceRows 不一致：\n新 %+v\n旧 %+v", i, want[i], legacy[i].msg)
		}
	}

	// 再把库清成「未迁移」并重跑迁移，Load 结果必须逐字节不变。
	before, _ := json.Marshal(want)
	if _, err := s.db.Exec(`UPDATE messages SET parent_seq = 0 WHERE session_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET leaf_seq = 0, leaf_len = 0 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	s.treeMigrated = false
	if err := s.migrateSessionTree(); err != nil {
		t.Fatal(err)
	}
	after, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(before) != string(afterJSON) {
		t.Fatalf("迁移前后 Load 不逐字节相同：\n前 %s\n后 %s", before, afterJSON)
	}
}

// ---- 路径构造 ----

// TestPathRowsSkipsAbandonedCheckpoint：切回压缩之前的分支时，别的分支的检查点不生效
// （它的影子集合不在可达集里）——「压缩是分支局部的」这条的结构保证。
func TestPathRowsSkipsAbandonedCheckpoint(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user")
	// 先压缩前 3 条 → CK 影子 seqs[0..2]
	cp := appendCheckpointForTest(t, s, id, 0, 3)

	// 路径 = [CK, seqs[3], seqs[4]]；把 leaf 移回 seqs[1]（压缩之前的分支）
	if _, _, err := s.SetLeaf(id, seqs[1]); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	// 期望 [seqs[0], seqs[1]] —— 原文可见，检查点不生效
	if len(got) != 2 || got[0].Seq != seqs[0] || got[1].Seq != seqs[1] {
		t.Fatalf("切回压缩前的分支应看到原文 [%d,%d]，实际 %+v", seqs[0], seqs[1], seqSummary(got))
	}
	// 切回原 leaf：检查点又生效
	if _, _, err := s.SetLeaf(id, seqs[4]); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Seq != cp {
		t.Fatalf("切回原路径应看到 [CK,%d,%d]，实际 %+v", seqs[3], seqs[4], seqSummary(got))
	}
}

// TestPathRowsLeafOnCheckpoint：leaf 落在检查点上时路径只有它自己（压缩后立刻停住）。
func TestPathRowsLeafOnCheckpoint(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user")
	cp := appendCheckpointForTest(t, s, id, 0, 2)
	if _, _, err := s.SetLeaf(id, cp); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != cp {
		t.Fatalf("leaf 落在检查点上应只看到它自己，实际 %+v", seqSummary(got))
	}
	_ = seqs
}

// TestPathRowsEmptyLeaf：leaf = 0 是**空路径**（合法），不是「回落全表」。
func TestPathRowsEmptyLeaf(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendRoles(t, s, id, "user", "assistant")

	if _, _, err := s.SetLeaf(id, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("leaf=0 应得到空路径，实际 %+v（回落全表就是错的）", seqSummary(got))
	}
	// 库里行还在（一条不删）
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("空路径不该删行，库里应有 2 条，实际 %d", n)
	}
	// 在空路径上追加 → 新 root（与旧历史并列）
	seq, err := s.AppendMsg(id, llm.Message{Role: "user", Content: "重开一条"})
	if err != nil {
		t.Fatal(err)
	}
	if got := parentOf(t, s, id, seq); got != 0 {
		t.Fatalf("空路径上追加应是新 root（父指针 0），实际 %d", got)
	}
	got, err = s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != seq {
		t.Fatalf("新分支的路径应只有新消息，实际 %+v", seqSummary(got))
	}
}

// TestPathRowsMultiBranch：多分支（预留能力）——两条分支各自回放自己的路径。
func TestPathRowsMultiBranch(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	base := appendRoles(t, s, id, "user", "assistant") // seq 1,2
	// 分支 A
	a := appendRoles(t, s, id, "user", "assistant") // seq 3,4
	// 回到 base 末尾再长分支 B
	if _, _, err := s.SetLeaf(id, base[1]); err != nil {
		t.Fatal(err)
	}
	b := appendRoles(t, s, id, "user") // seq 5

	// 当前 leaf = b[0]：路径 = base + B
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	wantSeq := []int64{base[0], base[1], b[0]}
	if !sameSeqs(got, wantSeq) {
		t.Fatalf("B 分支路径应为 %v，实际 %v", wantSeq, seqOf(got))
	}
	// 切回 A 分支末尾：路径 = base + A（B 不在路径上）
	if _, _, err := s.SetLeaf(id, a[1]); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	wantSeq = []int64{base[0], base[1], a[0], a[1]}
	if !sameSeqs(got, wantSeq) {
		t.Fatalf("A 分支路径应为 %v，实际 %v", wantSeq, seqOf(got))
	}
	// 一条不删：库里 5 条
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("切分支不该删行，库里应有 5 条，实际 %d", n)
	}
}

// ---- 压缩重接父指针 ----

// TestCheckpointRewiresParent：压缩把「被压段之后那条存活行」改挂到检查点下，
// 且「路径回放」与「整表 surface 回放」逐字节一致（树结构换了、回放结果没换）。
func TestCheckpointRewiresParent(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user")

	// 压缩中间段 [1,3)（第 2、3 条）
	cp := appendCheckpointForTest(t, s, id, 1, 2)

	// 父指针：检查点挂在 seqs[0] 下；后继 seqs[3] 改挂检查点；leaf 不变
	if got := parentOf(t, s, id, cp); got != int(seqs[0]) {
		t.Fatalf("检查点的父指针应为 %d（被压段之前那条），实际 %d", seqs[0], got)
	}
	if got := parentOf(t, s, id, seqs[3]); got != int(cp) {
		t.Fatalf("被压段之后那条应改挂检查点 %d，实际 %d", cp, got)
	}
	leaf, n := leafAndLen(t, s, id)
	if leaf != int(seqs[4]) {
		t.Fatalf("压缩后 leaf 不该移动（尾部保留段仍在路径上）：期望 %d，实际 %d", seqs[4], leaf)
	}
	if n != 4 { // 5 条 → 影子 2 条 + 检查点 1 条 = 4
		t.Fatalf("leaf_len 应为 4，实际 %d", n)
	}

	// 树模型没有改变回放语义：pathRows(leaf) 必须与旧的整表 surfaceRows 逐字节一致。
	// （压缩本身会改变 surface 内容——那一段换成了摘要，这是既有语义；这里钉的是
	//  「树结构换了、回放结果没换」。）
	rows, err := s.readRows(id)
	if err != nil {
		t.Fatal(err)
	}
	gotPath := pathRows(rows, int(leaf), s.treeMigrated)
	wantSurface := surfaceRows(rows)
	if len(gotPath) != len(wantSurface) {
		t.Fatalf("路径回放 %d 条与整表 surface %d 条不一致", len(gotPath), len(wantSurface))
	}
	for i := range gotPath {
		if gotPath[i].seq != wantSurface[i].seq {
			t.Fatalf("第 %d 条 seq 不一致：路径 %d vs surface %d", i, gotPath[i].seq, wantSurface[i].seq)
		}
	}
	// 树结构变了：从 leaf 回溯会经过检查点（而不是经过被影子原文）
	reach := reachSet(rows, int(leaf))
	if _, ok := reach[int(cp)]; !ok {
		t.Fatal("检查点必须在 leaf 路径上（漏了重接父指针的话它是孤立分支）")
	}
	if _, ok := reach[int(seqs[1])]; ok {
		t.Fatal("被影子行不该在 leaf 路径上")
	}
}

// TestCheckpointLeafInsideShadowed：leaf 落在被压段里时，压缩把叶移到检查点。
func TestCheckpointLeafInsideShadowed(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user", "assistant")
	// 先把叶切到第 2 条（在被压段 [0,2) 里）
	if _, _, err := s.SetLeaf(id, seqs[1]); err != nil {
		t.Fatal(err)
	}
	// 现在路径 = [seqs[0], seqs[1]]，压缩这两条
	cp := appendCheckpointForTest(t, s, id, 0, 2)
	leaf, n := leafAndLen(t, s, id)
	if leaf != int(cp) {
		t.Fatalf("叶落在被压段里时应移到检查点 %d，实际 %d", cp, leaf)
	}
	if n != 1 {
		t.Fatalf("压缩后路径长度应为 1，实际 %d", n)
	}
}

// ---- leaf_len 与路径长度同源（防漂移总闸）----

// TestLeafLenMatchesPathLen：一串 append / 压缩 / 切叶之后，leaf_len 恒等于
// pathRows 的长度（三处增量维护点不许漂移）。
func TestLeafLenMatchesPathLen(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()

	assert := func(stage string) {
		t.Helper()
		rows, err := s.readRows(id)
		if err != nil {
			t.Fatal(err)
		}
		leaf, n := leafAndLen(t, s, id)
		if want := len(pathRows(rows, leaf, s.treeMigrated)); n != want {
			t.Fatalf("%s: leaf_len=%d 与路径长度 %d 不一致", stage, n, want)
		}
	}
	seqs := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant")
	assert("追加 6 条")
	appendCheckpointForTest(t, s, id, 0, 2)
	assert("前缀压缩")
	appendCheckpointForTest(t, s, id, 1, 2)
	assert("中间段压缩")
	if _, _, err := s.SetLeaf(id, seqs[1]); err != nil {
		t.Fatal(err)
	}
	assert("切到旧节点")
	appendRoles(t, s, id, "user")
	assert("旧节点上追加（新分支）")
	if _, _, err := s.SetLeaf(id, 0); err != nil {
		t.Fatal(err)
	}
	assert("空路径")
	appendRoles(t, s, id, "user")
	assert("空路径上追加")
}

// ---- List 可见性 ----

// TestListVisibilityIgnoresLeafLen：切到空路径后 leaf_len=0，但会话仍在侧栏
// （历史行还在库里）——可见性判据必须是「表里有任意行」。
func TestListVisibilityIgnoresLeafLen(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendRoles(t, s, id, "user", "assistant")

	// 切到第一条之前 → 空路径（一条不删）。
	// 注：批次 1 的 Rewind 仍是「真删行」的旧语义（批次 2 才改成移 leaf）——删光了就
	// 真的是空会话、从侧栏消失是对的。**空路径但有历史**这个状态由 SetLeaf(0) 产生，
	// 它正是批次 2「撤回第一条消息」之后的形态，所以现在就把它钉住。
	if _, _, err := s.SetLeaf(id, 0); err != nil {
		t.Fatal(err)
	}
	leaf, n := leafAndLen(t, s, id)
	if leaf != 0 || n != 0 {
		t.Fatalf("空路径应为 leaf=0/len=0，实际 %d/%d", leaf, n)
	}

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range list {
		if m.ID == id {
			found = true
			if m.Messages != 0 {
				t.Fatalf("侧栏条数应显示 0（空路径），实际 %d", m.Messages)
			}
		}
	}
	if !found {
		t.Fatal("空路径的会话不该从侧栏消失（数据在库里，入口必须还在）")
	}
}

// TestListMessagesUsesLeafLen：侧栏条数 = 当前路径长度（压缩后变小、切分支后跟着变）。
func TestListMessagesUsesLeafLen(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendRoles(t, s, id, "user", "assistant", "user", "assistant")
	appendCheckpointForTest(t, s, id, 0, 2) // 4 条 → 影子 2 + 检查点 1 = 3

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if m.ID == id && m.Messages != 3 {
			t.Fatalf("压缩后侧栏条数应为 3（当前路径长度），实际 %d", m.Messages)
		}
	}
}

// ---- 小工具 ----

// stripParents 复制一份消息并把 ParentSeq 清零——用于「回放内容等价」的比对
// （父指针的变化正是本次改动要的，不该混进内容等价性断言里）。
func stripParents(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		out[i].ParentSeq = 0
	}
	return out
}

func seqSummary(msgs []llm.Message) []int64 { return seqOf(msgs) }

func seqOf(msgs []llm.Message) []int64 {
	out := make([]int64, len(msgs))
	for i, m := range msgs {
		out[i] = m.Seq
	}
	return out
}

func sameSeqs(msgs []llm.Message, want []int64) bool {
	got := seqOf(msgs)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestCheckpointLeafMidPath：用户把叶切到中间节点后再压缩——leaf_len 必须按新路径重算
// （不是 len(surface)−count+1，那个公式假设叶在尾部）。
func TestCheckpointLeafMidPath(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant")
	// 叶切到第 4 条（中间），路径 = seqs[0..3]
	if _, _, err := s.SetLeaf(id, seqs[3]); err != nil {
		t.Fatal(err)
	}
	// 压缩路径的前 2 条 → 检查点替换 seqs[0..1]
	cp := appendCheckpointForTest(t, s, id, 0, 2)

	leaf, n := leafAndLen(t, s, id)
	if leaf != int(seqs[3]) {
		t.Fatalf("叶不在被压段里，压缩不该移动它：期望 %d，实际 %d", seqs[3], leaf)
	}
	// 新路径 = [CK, seqs[2], seqs[3]] → 3 条
	if n != 3 {
		t.Fatalf("leaf_len 应为 3（重算路径），实际 %d", n)
	}
	rows, err := s.readRows(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(pathRows(rows, leaf, s.treeMigrated)); got != n {
		t.Fatalf("leaf_len=%d 与真实路径长度 %d 不一致", n, got)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{cp, seqs[2], seqs[3]}
	if !sameSeqs(got, want) {
		t.Fatalf("路径应为 %v，实际 %v", want, seqOf(got))
	}
}

// ---- 真·老库迁移（手工建旧 schema，再用 Open 升级）----

// legacySchemaDDL 是**会话树之前**的库结构（照抄 store.go 的 schema + 当年全部 ALTER
// 出来的列，只少了 parent_seq / leaf_seq / leaf_len）。用它造一个真老库，验证升级路径。
const legacySchemaDDL = `
CREATE TABLE sessions (
  id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '', archived INTEGER NOT NULL DEFAULT 0,
  workspace TEXT NOT NULL DEFAULT '', parent_id TEXT NOT NULL DEFAULT '',
  agent_id TEXT NOT NULL DEFAULT '', dispatch_id TEXT NOT NULL DEFAULT '',
  worktree_path TEXT NOT NULL DEFAULT '', worktree_branch TEXT NOT NULL DEFAULT '',
  worktree_base TEXT NOT NULL DEFAULT '', context_usage TEXT NOT NULL DEFAULT '',
  approval TEXT NOT NULL DEFAULT '', todos TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT ''
);
CREATE TABLE messages (
  session_id TEXT NOT NULL, seq INTEGER NOT NULL,
  role TEXT NOT NULL, content TEXT NOT NULL DEFAULT '',
  reasoning TEXT NOT NULL DEFAULT '', reasoning_sig TEXT NOT NULL DEFAULT '',
  tool_calls TEXT NOT NULL DEFAULT '[]', tool_call_id TEXT NOT NULL DEFAULT '',
  checkpoint INTEGER NOT NULL DEFAULT 0, shadow_start_seq INTEGER NOT NULL DEFAULT 0,
  shadow_end_seq INTEGER NOT NULL DEFAULT 0, shadowed_seqs TEXT NOT NULL DEFAULT '[]',
  first_token_ms INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0,
  model TEXT NOT NULL DEFAULT '', usage_tokens INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0, cache_read_tokens INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0, notice INTEGER NOT NULL DEFAULT 0,
  usage_split INTEGER NOT NULL DEFAULT 0, images TEXT NOT NULL DEFAULT '',
  attachments TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (session_id, seq)
);`

// openLegacyDB 在 dir 里建一个**会话树之前**的库并返回连接（调用方写入数据后关闭）。
func openLegacyDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "sessions.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(legacySchemaDDL); err != nil {
		db.Close()
		t.Fatalf("建老 schema 失败: %v", err)
	}
	return db
}

// TestOpenMigratesRealLegacyDB：手工建的真老库（含前缀压缩 + 中间段压缩 + 通知行 +
// 工具调用）经 Open 升级后，Load 结果与「旧实现会给出的整表 surface」逐字节一致。
func TestOpenMigratesRealLegacyDB(t *testing.T) {
	dir := t.TempDir()
	db := openLegacyDB(t, dir)

	// 老库数据：A1..A8 + 一个前缀检查点（影子 1..3）+ 一个中间段检查点（影子 5..6）
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("写老库失败: %v", err)
		}
	}
	mustExec(`INSERT INTO sessions (id, created_at, updated_at, title) VALUES ('s1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '老会话')`)
	roles := []string{"user", "assistant", "tool", "assistant", "user", "assistant", "user", "assistant"}
	for i, role := range roles {
		seq := i + 1
		notice := 0
		if i == 4 {
			notice = 1 // 一条通知行（统计要排除它，但回放要包含它）
		}
		mustExec(`INSERT INTO messages (session_id, seq, role, content, notice, usage_split) VALUES ('s1', ?, ?, ?, ?, 1)`,
			seq, role, fmt.Sprintf("%s#%d", role, seq), notice)
	}
	// 前缀压缩：影子 seq 1..3
	mustExec(`INSERT INTO messages (session_id, seq, role, content, checkpoint, shadow_start_seq, shadow_end_seq, shadowed_seqs)
	          VALUES ('s1', 9, 'user', '<compacted-summary>前缀摘要</compacted-summary>', 1, 1, 3, '[1,2,3]')`)
	// 中间段压缩：影子 seq 5,6（检查点 seq 10）
	mustExec(`INSERT INTO messages (session_id, seq, role, content, checkpoint, shadow_start_seq, shadow_end_seq, shadowed_seqs)
	          VALUES ('s1', 10, 'user', '<compacted-summary>中段摘要</compacted-summary>', 1, 5, 6, '[5,6]')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// 升级
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("升级老库失败: %v", err)
	}
	defer s.Close()
	if !s.treeMigrated {
		t.Fatal("升级后 treeMigrated 应为真")
	}
	if v := userVersion(t, s); v != treeSchemaVersion {
		t.Fatalf("user_version 应为 %d，实际 %d", treeSchemaVersion, v)
	}

	// 期望 = 旧实现的整表 surface（用同一份 surfaceRows 算，输入是全表行）
	rows, err := s.readRows("s1")
	if err != nil {
		t.Fatal(err)
	}
	want := surfaceRows(rows)
	got, err := s.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("升级后 Load %d 条，旧 surface %d 条", len(got), len(want))
	}
	for i := range got {
		if got[i].Seq != int64(want[i].seq) || got[i].Role != want[i].msg.Role || got[i].Content != want[i].msg.Content {
			t.Fatalf("第 %d 条不一致：新 %+v 旧 %+v", i, got[i], want[i].msg)
		}
	}
	// 期望的 seq 序列：CK1(9), 4, CK2(10), 7, 8
	wantSeqs := []int64{9, 4, 10, 7, 8}
	if !sameSeqs(got, wantSeqs) {
		t.Fatalf("surface 顺序应为 %v，实际 %v", wantSeqs, seqOf(got))
	}
	// 树：检查点 9 是 root；检查点 10 挂在 4 下；7 挂在 10 下；8 挂在 7 下；leaf=8
	checks := map[int64]int{9: 0, 4: 9, 10: 4, 7: 10, 8: 7}
	for seq, wantParent := range checks {
		if got := parentOf(t, s, "s1", seq); got != wantParent {
			t.Fatalf("seq %d 的父指针应为 %d，实际 %d", seq, wantParent, got)
		}
	}
	leaf, n := leafAndLen(t, s, "s1")
	if leaf != 8 || n != 5 {
		t.Fatalf("leaf/leaf_len 应为 8/5，实际 %d/%d", leaf, n)
	}

	// 二次 Open（幂等）：结果不变、版本号不变
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("二次 Open 失败: %v", err)
	}
	defer s2.Close()
	again, err := s2.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	if !sameSeqs(again, wantSeqs) {
		t.Fatalf("二次 Open 后 surface 变了：%v", seqOf(again))
	}
}

// ---- 真·会话库的等价性（可选，需显式给库路径）----

// TestLoadEquivalenceOnRealDB 在**真实会话库的副本**上验证「迁移 + 路径回放」与
// 「旧的整表 surface 回放」逐条会话逐字节等价。
//
// 为什么单列出来并且默认跳过：它要一份几百 MB 的真库（本机数据，不进仓库）。
// 用法：
//
//	LXCODE_REAL_SESSIONS_DB=%APPDATA%\lxcode\sessions\sessions.db go test ./internal/store -run RealDB -v
//
// 这是批次 1 最硬的一条证据：合成样本能覆盖的形态（长会话、多次压缩、中间段影子、
// 子会话头部保护、撤回过的行）在真库里都有真实分布。
func TestLoadEquivalenceOnRealDB(t *testing.T) {
	src := os.Getenv("LXCODE_REAL_SESSIONS_DB")
	if src == "" {
		t.Skip("未设置 LXCODE_REAL_SESSIONS_DB（真实库副本的等价性验证，默认跳过）")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "sessions.db")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("复制真实库失败: %v", err)
	}
	// WAL 与主库必须成对复制，否则最近的事务会丢（-wal 不存在时忽略）。
	if err := copyFile(src+"-wal", dst+"-wal"); err != nil && !os.IsNotExist(err) {
		t.Fatalf("复制 WAL 失败: %v", err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("打开真实库副本失败: %v", err)
	}
	defer s.Close()
	if !s.treeMigrated {
		t.Fatal("打开真实库后应当已迁移")
	}

	ids, err := sessionIDsTx2(s)
	if err != nil {
		t.Fatal(err)
	}
	checked, totalMsgs, withCP := 0, 0, 0
	for _, id := range ids {
		// 旧语义（改动前的实现）：全表 surface。
		rows, err := s.readRows(id)
		if err != nil {
			t.Fatalf("会话 %s 读行失败: %v", id, err)
		}
		want := surfaceRows(rows)
		got, err := s.Load(id)
		if err != nil {
			t.Fatalf("会话 %s Load 失败: %v", id, err)
		}
		if len(got) != len(want) {
			t.Fatalf("会话 %s 条数不一致：新 %d 旧 %d", id, len(got), len(want))
		}
		for i := range want {
			if got[i].Seq != int64(want[i].seq) {
				t.Fatalf("会话 %s 第 %d 条 seq 不一致：新 %d 旧 %d", id, i, got[i].Seq, want[i].seq)
			}
			// 内容逐字段比（ParentSeq 是本批新增的簿记，不在旧语义里）。
			a, _ := json.Marshal(got[i])
			b, _ := json.Marshal(want[i].msg)
			if string(a) != string(b) {
				t.Fatalf("会话 %s 第 %d 条内容不一致：\n新 %s\n旧 %s", id, i, a, b)
			}
		}
		// leaf_len 与旧 surfaceCounts 的口径必须相同（批次 1 的对照钉子）。
		_, n := leafAndLen(t, s, id)
		if n != len(want) {
			t.Fatalf("会话 %s 的 leaf_len=%d 与旧 surface 条数 %d 不一致", id, n, len(want))
		}
		for _, r := range rows {
			if r.checkpoint {
				withCP++
				break
			}
		}
		checked++
		totalMsgs += len(got)
	}
	t.Logf("真实库等价性：%d 条会话 / %d 条路径消息 / %d 条含压缩检查点，全部逐字节一致",
		checked, totalMsgs, withCP)
	if checked == 0 {
		t.Fatal("真实库里一条会话都没读到——样本无效")
	}
	if withCP == 0 {
		t.Fatal("真实库里没有一条含压缩检查点的会话——压缩路径未被真数据覆盖")
	}
}

// sessionIDsTx2 列出全部会话 id（只读，测试用；不复用迁移里的会话事务版本）。
func sessionIDsTx2(s *Store) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// copyFile 逐字节复制文件（大库用 io.Copy，不整块读进内存）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---- SQL 路径查询与 Go 回溯的等价性 ----

// TestSQLPathMatchesGoReach：readPathRows（SQL 递归回溯）与 pathRows 眼里的可达集
// 必须逐 seq 相同。两条实现分工不同（SQL 只取可达行，Go 版还要套 surfaceRows），
// 但「哪些行可达」这一个判定不允许分叉——分叉的表现是「回放的历史」与
// 「写路径算的存活集」对不上。
func TestSQLPathMatchesGoReach(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	base := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant")
	// 前缀压缩（影子前 2 条）+ 中间段压缩（影子第 3、4 条）
	appendCheckpointForTest(t, s, id, 0, 2)
	appendCheckpointForTest(t, s, id, 1, 2)
	// 从中间节点分叉（新分支）
	if _, _, err := s.SetLeaf(id, base[1]); err != nil {
		t.Fatal(err)
	}
	appendRoles(t, s, id, "user", "assistant")

	assertSame := func(leaf int, stage string) {
		t.Helper()
		all, err := s.readRows(id)
		if err != nil {
			t.Fatal(err)
		}
		gotRows, err := s.readPathRows(id, leaf)
		if err != nil {
			t.Fatalf("%s: readPathRows: %v", stage, err)
		}
		reach := reachSet(all, leaf)
		gotSeqs := make(map[int]struct{}, len(gotRows))
		for _, r := range gotRows {
			gotSeqs[r.seq] = struct{}{}
		}
		if len(gotSeqs) != len(reach) {
			t.Fatalf("%s: 可达集大小不一致：SQL %d，Go %d（SQL=%v Go=%v）",
				stage, len(gotSeqs), len(reach), gotSeqs, reach)
		}
		for seq := range reach {
			if _, ok := gotSeqs[seq]; !ok {
				t.Fatalf("%s: SQL 漏了 seq %d（Go 认为可达）", stage, seq)
			}
		}
		// 顺带钉住：两条取数路径回放出的历史也必须相同。
		want := messagesOf(pathRows(all, leaf, s.treeMigrated))
		if got := messagesOf(surfaceRows(gotRows)); !sameSeqs(got, seqOf(want)) {
			t.Fatalf("%s: SQL 路径回放 %v 与 Go 路径 %v 不一致", stage, seqOf(got), seqOf(want))
		}
	}

	leaf, _ := leafAndLen(t, s, id)
	assertSame(leaf, "分叉后的当前叶")
	assertSame(int(base[1]), "压缩前的旧节点")
	assertSame(0, "空路径")
	// 叶落在检查点上
	rows, _ := s.readRows(id)
	var cp int
	for _, r := range rows {
		if r.checkpoint {
			cp = r.seq
			break
		}
	}
	assertSame(cp, "叶落在检查点上")
}

// TestSQLPathDefensive：断链与环不炸加载路径（与 pathRows 同一条纪律）。
func TestSQLPathDefensive(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user")

	// 断链：把第 2 条的父指针指到一个不存在的 seq。
	if _, err := s.db.Exec(`UPDATE messages SET parent_seq = 9999 WHERE session_id = ? AND seq = ?`,
		id, seqs[1]); err != nil {
		t.Fatal(err)
	}
	got, err := s.readPathRows(id, int(seqs[2]))
	if err != nil {
		t.Fatalf("断链不该报错: %v", err)
	}
	// 从 seqs[2] 回溯：seqs[2] → seqs[1] → 9999（不存在，停）→ 可达 {seqs[1], seqs[2]}
	if len(got) != 2 || got[0].seq != int(seqs[1]) || got[1].seq != int(seqs[2]) {
		t.Fatalf("断链应停在缺失的父节点，实际 %d 条", len(got))
	}

	// 环：seqs[1].parent = seqs[2]、seqs[2].parent = seqs[1]。
	if _, err := s.db.Exec(`UPDATE messages SET parent_seq = ? WHERE session_id = ? AND seq = ?`,
		seqs[2], id, seqs[1]); err != nil {
		t.Fatal(err)
	}
	got, err = s.readPathRows(id, int(seqs[2]))
	if err != nil {
		t.Fatalf("环不该报错: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("环应在去重处终止（2 条），实际 %d 条", len(got))
	}
}

// ---- 批次 1 验收清单里剩余的几个钉子 ----

// TestLeafLenMatchesSurfaceCounts：leaf_len 与**旧** surfaceCounts() 数值相同。
// 批次 1 里两个实现并存，这条对照测试是删掉旧实现的放行条件（文档 §F 批次 1）。
func TestLeafLenMatchesSurfaceCounts(t *testing.T) {
	s := openTestStore(t)
	// 普通消息 + 前缀压缩 + 中间段压缩 + 通知行 + 退化检查点 + 子会话
	a, _ := s.Create()
	appendRoles(t, s, a, "user", "assistant", "user", "assistant", "user")
	appendCheckpointForTest(t, s, a, 0, 2)
	appendCheckpointForTest(t, s, a, 1, 2)

	b, _ := s.Create()
	if _, err := s.AppendMsg(b, llm.Message{Role: "user", Content: " hi", Notice: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCheckpoint(b, llm.Message{Role: "user", Content: "空摘要"}, 0, 0); err != nil {
		t.Fatal(err)
	}

	child, err := s.CreateChild(a, "agent-x", "dispatch-1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	appendRoles(t, s, child, "user", "assistant")

	counts, err := s.surfaceCounts()
	if err != nil {
		t.Fatal(err)
	}
	ids, err := sessionIDsTx2(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		_, n := leafAndLen(t, s, id)
		if n != counts[id] {
			t.Fatalf("会话 %s: leaf_len=%d 与旧 surfaceCounts=%d 不一致", id, n, counts[id])
		}
	}
	// 对照测试必须在**有内容的**会话上跑过（否则恒真）
	if counts[a] == 0 || counts[child] == 0 {
		t.Fatalf("样本会话不该为空：a=%d child=%d", counts[a], counts[child])
	}
}

// TestPathSeqMonotonic：路径上**真实消息**的 seq 严格递增（§C.5 的切段前提：
// 回滚按 `owner_seq >= anchor` 切段，靠的就是「更靠后的行 seq 更大」）。
//
// ⚠️ 与文档 §F 的字面写法有一处偏差，值得记下来：文档写「pathRows 返回值 seq 严格
// 递增」，但**检查点行不满足**——它是追加在末尾的（seq 最大），却按锚点插在历史
// 中间（surface position）。所以这里把检查点排除在外：文件快照的锚点是携带
// tool_call 的 assistant 消息，检查点是合成行，永远不会挂快照——C.5 依赖的单调性
// 只对真实消息成立，对检查点既不成立也不需要成立。
func TestPathSeqMonotonic(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant")
	appendCheckpointForTest(t, s, id, 0, 2)
	appendCheckpointForTest(t, s, id, 1, 2)
	appendRoles(t, s, id, "user")
	if _, _, err := s.SetLeaf(id, seqs[1]); err != nil {
		t.Fatal(err)
	}
	appendRoles(t, s, id, "user", "assistant")

	check := func(stage string) {
		t.Helper()
		rows, err := s.readRows(id)
		if err != nil {
			t.Fatal(err)
		}
		leaf, _ := leafAndLen(t, s, id)
		surface := pathRows(rows, leaf, s.treeMigrated)
		prev := 0
		realRows := 0
		for _, r := range surface {
			if r.checkpoint {
				continue // 检查点按锚点插入，seq 不代表位置（见函数注释）
			}
			realRows++
			if prev >= r.seq {
				t.Fatalf("%s: 真实消息的 seq 不严格递增：前一条 %d，这一条 %d", stage, prev, r.seq)
			}
			prev = r.seq
		}
		if realRows == 0 {
			t.Fatalf("%s: 路径上没有真实消息——样本无效", stage)
		}
	}
	check("分叉后的当前路径")
	if _, _, err := s.SetLeaf(id, seqs[5]); err != nil {
		t.Fatal(err)
	}
	check("旧分支")
}

// TestBranchCompactionDoesNotLeak：A 分支压缩后切回 B 分支，B 的原文全部可见
// （别的分支的压缩**结构上**不影响本分支：检查点不在 B 的 parent 链上）。
func TestBranchCompactionDoesNotLeak(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	base := appendRoles(t, s, id, "user", "assistant") // seq 1,2 —— 两条分支的公共前缀
	// A 分支
	a := appendRoles(t, s, id, "user", "assistant", "user") // seq 3,4,5
	// 回到公共前缀末尾，长 B 分支
	if _, _, err := s.SetLeaf(id, base[1]); err != nil {
		t.Fatal(err)
	}
	b := appendRoles(t, s, id, "user", "assistant") // seq 6,7

	// 在 A 分支上压缩（把公共前缀 + A 的前两条都压掉）
	if _, _, err := s.SetLeaf(id, a[2]); err != nil {
		t.Fatal(err)
	}
	cpA := appendCheckpointForTest(t, s, id, 0, 4)
	if got, err := s.Load(id); err != nil || len(got) != 2 || got[0].Seq != cpA {
		t.Fatalf("A 分支压缩后应看到 [CK_A, %d]，实际 %v（err=%v）", a[2], seqOf(got), err)
	}

	// 切回 B 分支：公共前缀的原文与 B 的两条都可见，A 的检查点不生效
	if _, _, err := s.SetLeaf(id, b[1]); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{base[0], base[1], b[0], b[1]}
	if !sameSeqs(got, want) {
		t.Fatalf("B 分支路径应为 %v（A 的压缩不该污染它），实际 %v", want, seqOf(got))
	}
	// 叶子回到 A 分支末尾：检查点重新生效
	if _, _, err := s.SetLeaf(id, a[2]); err != nil {
		t.Fatal(err)
	}
	got, err = s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Seq != cpA {
		t.Fatalf("切回 A 分支应重新看到检查点，实际 %v", seqOf(got))
	}
}

// TestChildSessionStartsEmptyPath：子会话建好时 leaf=0/leaf_len=0（空路径是合法起点），
// 它的第一条消息是 root，父链与 leaf_len 与顶层会话同款维护。
func TestChildSessionStartsEmptyPath(t *testing.T) {
	s := openTestStore(t)
	parent, _ := s.Create()
	appendRoles(t, s, parent, "user", "assistant")

	child, err := s.CreateChild(parent, "agent-x", "dispatch-1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if leaf, n := leafAndLen(t, s, child); leaf != 0 || n != 0 {
		t.Fatalf("新子会话应是空路径（leaf=0/len=0），实际 %d/%d", leaf, n)
	}
	seqs := appendRoles(t, s, child, "user", "assistant")
	if got := parentOf(t, s, child, seqs[0]); got != 0 {
		t.Fatalf("子会话第一条消息应是 root（父指针 0），实际 %d", got)
	}
	if got := parentOf(t, s, child, seqs[1]); got != int(seqs[0]) {
		t.Fatalf("子会话第二条消息应挂第一条，实际 %d", got)
	}
	if leaf, n := leafAndLen(t, s, child); leaf != int(seqs[1]) || n != 2 {
		t.Fatalf("子会话叶指针应为 %d/2，实际 %d/%d", seqs[1], leaf, n)
	}
	// 子会话的路径回放独立于父会话
	got, err := s.Load(child)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSeqs(got, seqs) {
		t.Fatalf("子会话路径应为 %v，实际 %v", seqs, seqOf(got))
	}
	// 父会话的路径不受子会话影响
	got, err = s.Load(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("父会话历史应仍是 2 条，实际 %d", len(got))
	}
}

// TestRewindKeepsLeafConsistent：批次 1 的 Rewind 仍是「真删行」的旧语义，但叶指针
// 必须跟着回退——否则下次 Load 从一条已删的行回溯，历史会整个消失。
func TestRewindKeepsLeafConsistent(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user", "assistant")
	// 前缀压缩后撤回中间那条：叶要退到锚点之前那条存活行
	cp := appendCheckpointForTest(t, s, id, 0, 2)
	before, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	anchor := before[len(before)-1].Seq // 最后一条（seqs[3]）
	if _, _, err := s.RewindTo(id, anchor); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{cp, seqs[2]}
	if !sameSeqs(got, want) {
		t.Fatalf("撤回后路径应为 %v，实际 %v", want, seqOf(got))
	}
	leaf, n := leafAndLen(t, s, id)
	if leaf != int(seqs[2]) || n != 2 {
		t.Fatalf("撤回后叶指针应为 %d/2，实际 %d/%d", seqs[2], leaf, n)
	}
	// 叶上继续追加：挂在 seqs[2] 下（单链延续）
	next := appendRoles(t, s, id, "user")
	if got := parentOf(t, s, id, next[0]); got != int(seqs[2]) {
		t.Fatalf("撤回后追加应挂在 %d 下，实际 %d", seqs[2], got)
	}
}

// TestListCountsMatchLegacyCountsOnRealDB：真实库副本上，List 的 Messages（新口径
// leaf_len）与旧 surfaceCounts 逐会话相同、可见会话集合也相同——批次 1 的
// 「外部零变化」硬要求（侧栏条数是本批唯一可观察的差异，必须数值不变）。
func TestListCountsMatchLegacyCountsOnRealDB(t *testing.T) {
	src := os.Getenv("LXCODE_REAL_SESSIONS_DB")
	if src == "" {
		t.Skip("未设置 LXCODE_REAL_SESSIONS_DB（默认跳过）")
	}
	dir := t.TempDir()
	if err := copyFile(src, filepath.Join(dir, "sessions.db")); err != nil {
		t.Fatalf("复制真实库失败: %v", err)
	}
	if err := copyFile(src+"-wal", filepath.Join(dir, "sessions.db-wal")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("复制 WAL 失败: %v", err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.legacySurfaceCounts() // 旧口径（surfaceCounts 的对照实现）
	if err != nil {
		t.Fatal(err)
	}
	// 旧 List 的可见判据 = 顶层会话且 Messages > 0
	ids, err := sessionIDsTx2(s)
	if err != nil {
		t.Fatal(err)
	}
	wantVisible := 0
	for _, id := range ids {
		var parent string
		if err := s.db.QueryRow(`SELECT parent_id FROM sessions WHERE id = ?`, id).Scan(&parent); err != nil {
			t.Fatal(err)
		}
		if parent == "" && legacy[id] > 0 {
			wantVisible++
		}
	}
	if len(list) != wantVisible {
		t.Fatalf("可见会话数：新 %d 旧 %d", len(list), wantVisible)
	}
	for _, m := range list {
		if m.Messages != legacy[m.ID] {
			t.Fatalf("会话 %s 的 messages：新 %d 旧 %d", m.ID, m.Messages, legacy[m.ID])
		}
	}
	t.Logf("真实库：%d 条可见会话，条数逐条与旧口径一致", len(list))
}

// TestDegenerateCheckpointStaysAtTail：count=0 的退化检查点**不替换任何行**——
// 旧语义里它排在历史末尾（surfaceRows 的锚点回落自身 seq），树模型下必须
// 「挂在当前叶之后、自己成为新叶」，否则整条历史会被挂到摘要底下（回放顺序会变）。
//
// 这个形态由 surfacecounts_test.go 的「通知行 + 退化检查点」用例实际覆盖，而
// AppendCheckpoint 的契约明确允许 count=0，所以它必须正确。
func TestDegenerateCheckpointStaysAtTail(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	seqs := appendRoles(t, s, id, "user", "assistant", "user")

	cp := appendCheckpointForTest(t, s, id, 0, 0)

	// 父链：检查点挂在原叶（seqs[2]）下，自己成为新叶。
	if got := parentOf(t, s, id, cp); got != int(seqs[2]) {
		t.Fatalf("退化检查点的父指针应为原叶 %d，实际 %d", seqs[2], got)
	}
	if got := parentOf(t, s, id, seqs[0]); got != 0 {
		t.Fatalf("首条消息必须仍是 root，实际父指针 %d（挂到摘要下就是错的）", got)
	}
	leaf, n := leafAndLen(t, s, id)
	if leaf != int(cp) || n != 4 {
		t.Fatalf("叶应为检查点 %d、路径长 4，实际 %d/%d", cp, leaf, n)
	}
	// 回放顺序与旧语义一致：普通消息按序，检查点落尾。
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{seqs[0], seqs[1], seqs[2], cp}
	if !sameSeqs(got, want) {
		t.Fatalf("回放应为 %v，实际 %v", want, seqOf(got))
	}
	// 之后追加：挂在检查点下（沿当前叶前进）。
	next := appendRoles(t, s, id, "user")
	if got := parentOf(t, s, id, next[0]); got != int(cp) {
		t.Fatalf("退化检查点之后追加应挂在 %d 下，实际 %d", cp, got)
	}
	// 与旧口径对照：surfaceCounts 与 leaf_len 同数。
	counts, err := s.surfaceCounts()
	if err != nil {
		t.Fatal(err)
	}
	if _, n := leafAndLen(t, s, id); n != counts[id] {
		t.Fatalf("leaf_len=%d 与 surfaceCounts=%d 不一致", n, counts[id])
	}
}

// TestDegenerateCheckpointOnEmptyPath：空路径上写退化检查点 → 它自己就是新 root。
func TestDegenerateCheckpointOnEmptyPath(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendRoles(t, s, id, "user")
	if _, _, err := s.SetLeaf(id, 0); err != nil {
		t.Fatal(err)
	}
	cp := appendCheckpointForTest(t, s, id, 0, 0)
	if got := parentOf(t, s, id, cp); got != 0 {
		t.Fatalf("空路径上的检查点应是新 root（父指针 0），实际 %d", got)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSeqs(got, []int64{cp}) {
		t.Fatalf("空路径上写检查点后应只看到它自己，实际 %v", seqOf(got))
	}
}

// TestRandomSequenceInvariants：随机跑一串 append / compact / setLeaf，每步都断言
//
//	① leaf_len == len(pathRows(leaf))；
//	② Load(id) == pathRows(leaf) 的 seq 序列；
//	③ leaf_len == 旧口径 surfaceCounts（批次 1 两个实现并存的对照）。
//
// 这是防漂移的总闸：三处 leaf_len 维护点（AppendMsg +1、AppendCheckpoint 重算、
// SetLeaf 重算）与两条回放路径任何一处漂移都会在随机序列里被抓住。用固定种子
// （0..29）——可复现，不是随机抖动。
func TestRandomSequenceInvariants(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		r := rand.New(rand.NewSource(seed))
		s := openTestStore(t)
		id, _ := s.Create()
		for step := 0; step < 12; step++ {
			rows, err := s.readRows(id)
			if err != nil {
				t.Fatal(err)
			}
			leaf, _ := leafAndLen(t, s, id)
			surface := pathRows(rows, leaf, s.treeMigrated)
			switch op := r.Intn(10); {
			case op < 6: // 追加
				if _, err := s.AppendMsg(id, llm.Message{Role: "user", Content: fmt.Sprintf("m%d", step)}); err != nil {
					t.Fatal(err)
				}
			case op < 9 && len(surface) > 0: // 压缩一段随机区间（含 count=0 的退化形态）
				skip := r.Intn(len(surface))
				count := r.Intn(len(surface) - skip)
				if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "摘要"}, skip, count); err != nil {
					t.Fatalf("seed=%d step=%d skip=%d count=%d surface=%d: %v",
						seed, step, skip, count, len(surface), err)
				}
			case len(rows) > 0: // 切叶到随机已有行
				target := rows[r.Intn(len(rows))].seq
				if _, _, err := s.SetLeaf(id, int64(target)); err != nil {
					t.Fatal(err)
				}
			}

			rows2, err := s.readRows(id)
			if err != nil {
				t.Fatal(err)
			}
			leaf2, n2 := leafAndLen(t, s, id)
			path := pathRows(rows2, leaf2, s.treeMigrated)
			// ①
			if n2 != len(path) {
				t.Fatalf("seed=%d step=%d: leaf_len=%d 与路径长度 %d 不一致", seed, step, n2, len(path))
			}
			// ②
			msgs, err := s.Load(id)
			if err != nil {
				t.Fatal(err)
			}
			want := make([]int64, len(path))
			for i, rr := range path {
				want[i] = int64(rr.seq)
			}
			if !sameSeqs(msgs, want) {
				t.Fatalf("seed=%d step=%d: Load %v 与 pathRows %v 不一致", seed, step, seqOf(msgs), want)
			}
			// ③ 只在**单链**（当前路径覆盖全表行）时成立：surfaceCounts 是全表口径，
			// 分支化之后两者天然不同（废弃分支的行也在表里）——批次 1 的对照要求
			// 就是「单链会话数值不变」。
			if len(reachSet(rows2, leaf2)) == len(rows2) {
				counts, err := s.surfaceCounts()
				if err != nil {
					t.Fatal(err)
				}
				if n2 != counts[id] {
					t.Fatalf("seed=%d step=%d: 单链下 leaf_len=%d 与 surfaceCounts=%d 不一致", seed, step, n2, counts[id])
				}
			}
		}
	}
}
