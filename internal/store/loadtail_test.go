// LoadTail（会话切换优化批 4）的钉子：尾部/分页窗口与全量裁剪的一致性。
//
// 窗口化装载的正确性口径只有一条：**窗口内容必须是全量 Load → 内存裁剪能得到的
// 同一段历史**（含工具配对回退的余量、影子区间过滤、检查点锚点插入、分支路径）。
// 等价断言都拿「全量 Load 的 surface 序列」当参照物——它就是回放事实源。
package store

import (
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// tailSeqs 取窗口消息的 seq 序列（断言辅助）。
func tailSeqs(msgs []llm.Message) []int64 {
	out := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Seq)
	}
	return out
}

func TestLoadTailMatchesFullTail(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	// 12 条链式历史
	base := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant",
		"user", "assistant", "user", "assistant", "user", "assistant")
	full, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	// 三种窗口：尾部 4 / 尾部 7 / 尾部 100（超长 = 全量）
	for _, n := range []int{4, 7, 100} {
		msgs, atRoot, err := s.LoadTail(id, 0, n)
		if err != nil {
			t.Fatalf("n=%d: LoadTail: %v", n, err)
		}
		trimmed := full
		if len(full) > n {
			trimmed = full[len(full)-n:]
		}
		if got := tailSeqs(msgs); len(got) != len(tailSeqs(trimmed)) {
			t.Fatalf("n=%d: 窗口 seq %v 与全量裁剪 %v 不一致", n, got, tailSeqs(trimmed))
		}
		for i := range trimmed {
			if msgs[i].Seq != trimmed[i].Seq || msgs[i].Content != trimmed[i].Content {
				t.Fatalf("n=%d: 第 %d 条不一致：窗口 %v 全量裁剪 %v", n, i, msgs[i], trimmed[i])
			}
		}
		// atRoot 语义：窗口没到头（还剩更早的）→ false；到头（含全部）→ true
		if want := len(base) <= n; atRoot != want {
			t.Fatalf("n=%d: atRoot=%v，期望 %v", n, atRoot, want)
		}
	}
	// 空会话：空窗口 + atRoot
	empty, _ := s.Create()
	msgs, atRoot, err := s.LoadTail(empty, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 || !atRoot {
		t.Fatalf("空会话应为空窗口 + atRoot，得到 %d 条 atRoot=%v", len(msgs), atRoot)
	}
}

func TestLoadTailPagingBeforeAnchor(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	base := appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant")
	full, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	// anchor = 第 4 条（seq 4）：之前最多 2 条 = seq 2..3（不含锚点自身）
	msgs, atRoot, err := s.LoadTail(id, base[3], 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tailSeqs(msgs), tailSeqs(full[1:3]); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("分页窗口 seq %v，期望 %v（anchor 之前 2 条）", got, tailSeqs(full[1:3]))
	}
	if atRoot {
		t.Fatal("anchor 之前还有 seq 1，atRoot 应为 false")
	}
	// 锚点是第 1 条：之前没有内容
	msgs, atRoot, err = s.LoadTail(id, base[0], 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 || !atRoot {
		t.Fatalf("根锚点应为空窗口 + atRoot，得到 %v", tailSeqs(msgs))
	}
}

func TestLoadTailOnBranch(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant")
	// 从第 2 条分叉，长出 3 条
	if _, _, err := s.SetLeaf(id, 2); err != nil {
		t.Fatal(err)
	}
	branch := appendRoles(t, s, id, "user", "assistant", "user")
	// 当前 leaf 在分支上：尾部窗口必须来自**分支路径**，不含主链的 seq 4..6
	msgs, _, err := s.LoadTail(id, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{2, branch[0], branch[1], branch[2]}
	got := tailSeqs(msgs)
	if len(got) != len(want) {
		t.Fatalf("分支窗口 seq %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("分支窗口 seq %v，期望 %v", got, want)
		}
	}
}

func TestLoadTailSkipsShadowedRows(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	// 10 条 + 检查点影子前 6 条 → surface = [检查点, #7..#10]
	appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant",
		"user", "assistant", "user", "assistant")
	appendCheckpointForTest(t, s, id, 0, 6)
	full, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 5 {
		t.Fatalf("前置：surface 应为 检查点+4 条 = 5，得到 %d", len(full))
	}
	// 尾部窗口（行预算 6）必须与全量裁剪一致：影子行不出现在窗口里
	msgs, atRoot, err := s.LoadTail(id, 0, 6)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tailSeqs(msgs), tailSeqs(full); len(got) != len(want) {
		t.Fatalf("影子区间的尾部窗口 seq %v 与全量 %v 不一致", got, want)
	}
	for i := range full {
		if msgs[i].Seq != full[i].Seq || msgs[i].Content != full[i].Content {
			t.Fatalf("第 %d 条不一致：窗口 %v 全量 %v", i, msgs[i], full[i])
		}
	}
	// 窗口里第一个是检查点：atRoot=true（窗口已到根）
	if !atRoot {
		t.Fatal("窗口覆盖到根（检查点开头），atRoot 应为 true")
	}
}

func TestLoadTailCheckpointAnchorWithinWindow(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	// 6 条 + 尾部检查点（count=0 退化检查点 = 尾部追加，不影子任何行）
	appendRoles(t, s, id, "user", "assistant", "user", "assistant", "user", "assistant")
	appendCheckpointForTest(t, s, id, 6, 0)
	full, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	// 退化检查点落尾（锚点 = 自身 seq）：surface = 6 条 + 检查点
	msgs, _, err := s.LoadTail(id, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := tailSeqs(msgs), tailSeqs(full)[len(full)-4:]; len(got) != 4 || got[0] != want[0] {
		t.Fatalf("尾部 4 行窗口 seq %v，期望（全量裁剪）%v", got, want)
	}
}
