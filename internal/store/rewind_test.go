// rewind_test.go —— 「回到这里」（rewind）在存储层的契约：**只移 leaf，一行不删**。
//
// 批次 2 起 Store.Rewind（真删行）已退役，语义由 RewindTo（leaf = parent(anchor)）
// 承载。本文件断言的是新语义下与旧语义**可观察行为完全一致**的那部分：
//
//	① 回放结果 == 撤回后应有的那段前缀（内存与磁盘同口径）；
//	② 被移出路径的行**仍在库里**（旧分支完整保留，切回去就回来）；
//	③ removed 数值与旧删除语义逐例相同（口径定义相同，见 §B.1）；
//	④ 幂等：锚点不在当前路径上 → removed=0、leaf 不动、不报错。
package store

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// checkpointShadows 读出某会话全部检查点的影子集合（按 seq 升序）。
func checkpointShadows(t *testing.T, s *Store, id string) [][]int {
	t.Helper()
	rows, err := s.db.Query(`SELECT shadowed_seqs FROM messages WHERE session_id = ? AND checkpoint = 1 ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]int
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var seqs []int
		if raw != "" && raw != "[]" {
			if err := json.Unmarshal([]byte(raw), &seqs); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, seqs)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRewindMovesLeafWithoutDeleting：撤回 = 叶移到锚点的父节点；锚点及其之后
// **一条都不删**（旧分支留在树里），回放结果与旧的删除语义逐条相同。
func TestRewindMovesLeafWithoutDeleting(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 5) // A B C D E（seq 1..5）

	removed, leaf, err := s.RewindTo(id, 3) // 回到 C 之前
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Fatalf("removed 应为 3（C D E 离开路径），实际 %d", removed)
	}
	if leaf != 2 {
		t.Fatalf("新叶应为 B（seq 2），实际 %d", leaf)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != msgs[0].Content || got[1].Content != msgs[1].Content {
		t.Fatalf("回放应只剩 A B: %+v", got)
	}
	// **一行不删**：库里仍是 5 条
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("撤回不该删行：库里应有 5 条，实际 %d", n)
	}
	// 旧分支可切回：叶移回 E，历史全回来
	if _, _, err := s.SetLeaf(id, 5); err != nil {
		t.Fatal(err)
	}
	back, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 5 {
		t.Fatalf("切回旧分支应看到全部 5 条，实际 %d", len(back))
	}
	// 侧栏条数跟着路径走
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range list {
		if meta.ID == id && meta.Messages != 5 {
			t.Fatalf("切回旧分支后侧栏条数应为 5，实际 %d", meta.Messages)
		}
	}
}

// TestRewindIsIdempotent：重复撤回同一条返回 0 不报错；锚点不存在（含被影子掉的
// 原文、废弃分支的节点）同样是空操作。报错会把「两个客户端同时点撤回」变成可见故障。
func TestRewindIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 3)

	if n, leaf, err := s.RewindTo(id, 2); err != nil || n != 2 || leaf != 1 {
		t.Fatalf("首次撤回: n=%d leaf=%d err=%v", n, leaf, err)
	}
	// 锚点 2 已不在路径上（路径只剩 seq 1）
	if n, leaf, err := s.RewindTo(id, 2); err != nil || n != 0 || leaf != 1 {
		t.Fatalf("重复撤回应是空操作: n=%d leaf=%d err=%v", n, leaf, err)
	}
	if n, _, err := s.RewindTo(id, 99); err != nil || n != 0 {
		t.Fatalf("未知序号应返回 0 不报错: n=%d err=%v", n, err)
	}
	got, _ := s.Load(id)
	if len(got) != 1 || got[0].Content != "消息A" {
		t.Fatalf("空操作不该改动路径: %+v", got)
	}
}

// TestRewindKeepsCheckpointInHistory：撤回锚点**之后**的检查点在路径之外，但它
// 引用的行仍在库里——检查点本身也不删（树模型里"撤回"不改变任何行）。
//
// 旧实现要把这类检查点整条删掉（没有影子可替了），新实现不需要：它只是不在当前
// 路径上，切回去仍然生效。
func TestRewindKeepsCheckpointInHistory(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 6) // A..F
	// 影子 D E（存活集下标 3..4）→ 当前历史 = [A, B, C, 检查点, F]
	cp, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "摘要"}, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 5 {
		t.Fatalf("前置状态不符（应为 5 条）: %+v", before)
	}

	removed, leaf, err := s.RewindTo(id, 3) // 回到 C 之前
	if err != nil {
		t.Fatal(err)
	}
	// 当前历史 [A, B, C, 检查点, F] 里 C 及其之后离开路径 = 3 条
	if removed != 3 {
		t.Fatalf("removed 应为 3（C + 检查点 + F），实际 %d", removed)
	}
	if leaf != 2 {
		t.Fatalf("新叶应为 B，实际 %d", leaf)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != msgs[0].Content || got[1].Content != msgs[1].Content {
		t.Fatalf("撤回后应只剩 A B: %+v", got)
	}
	// 检查点行还在库里（只是不在路径上），影子集合原样
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM messages WHERE session_id = ? AND seq = ?`, id, cp).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 1 {
		t.Fatal("检查点行不该被删（树模型里撤回不改变任何行）")
	}
	// 切回原叶：检查点重新生效，历史与撤回前逐条一致
	if _, _, err := s.SetLeaf(id, before[len(before)-1].Seq); err != nil {
		t.Fatal(err)
	}
	again, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(before) {
		t.Fatalf("切回后历史条数应恢复为 %d，实际 %d", len(before), len(again))
	}
	for i := range before {
		if again[i].Seq != before[i].Seq {
			t.Fatalf("第 %d 条 seq 不一致: %d vs %d", i, again[i].Seq, before[i].Seq)
		}
	}
}

// TestRewindKeepsCheckpointBeforeAnchor：位置在锚点**之前**的检查点完整活下来，
// 影子集合一个字都不改（它影子掉的原文仍在库里，且不许因为撤回而复活）。
// 这是「按行号一刀切」会踩的坑：检查点行的 seq 比锚点大，但它在历史里的位置在锚点之前。
func TestRewindKeepsCheckpointBeforeAnchor(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 6) // A..F
	// 影子 A B（前缀）→ 当前历史 = [检查点, C, D, E, F]
	if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "前缀摘要"}, 0, 2); err != nil {
		t.Fatal(err)
	}
	shadowsBefore := checkpointShadows(t, s, id)

	// 回到 D 之前（D 是存活集下标 2）→ 保留 [检查点, C]
	if _, _, err := s.RewindTo(id, 4); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "前缀摘要" || got[1].Content != "消息C" {
		t.Fatalf("应剩 [检查点, C]: %+v", got)
	}
	shadowsAfter := checkpointShadows(t, s, id)
	if len(shadowsAfter) != 1 || len(shadowsAfter[0]) != len(shadowsBefore[0]) {
		t.Fatalf("锚点之前的检查点应原样活着: before=%+v after=%+v", shadowsBefore, shadowsAfter)
	}
	// 被影子的原文不该复活（它仍在库里，只是不在当前路径上）
	if hits, _, err := s.Search(SearchQuery{Pattern: "消息A", Max: 10}); err != nil || len(hits) == 0 {
		t.Fatalf("被影子的原文应留在库里: err=%v hits=%d", err, len(hits))
	}
}

// TestRewindNoDanglingCheckpointRefs：撤回后**活着的检查点**（在当前路径上）的每条
// 影子都指向真实存在的行。
//
// 旧实现靠「收缩影子集合」维持这条不变量；新实现靠「一行不删」天然满足它——
// 这个测试留在原地正是为了钉住「退役那段级联收缩逻辑没有破坏不变量」。
func TestRewindNoDanglingCheckpointRefs(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 10) // A..J（seq 1..10）

	// 检查点一影子 G H（存活集下标 5..6）→ 当前历史 = [A..F, CP1, I, J]
	if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "摘要一"}, 5, 2); err != nil {
		t.Fatal(err)
	}
	// 检查点二影子 E 与**检查点一本身**（存活集下标 4..5）→ 当前历史 = [A, B, C, D, CP2, H, I, J]
	if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "摘要二"}, 4, 2); err != nil {
		t.Fatal(err)
	}
	before, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 8 || before[4].Content != "摘要二" {
		t.Fatalf("前置状态不符: %+v", before)
	}

	// 回到 D 之前（seq 4，存活集下标 3）→ 前缀 = [A, B, C]。
	if _, _, err := s.RewindTo(id, 4); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Content != "消息A" || got[1].Content != "消息B" || got[2].Content != "消息C" {
		t.Fatalf("回放应剩 [A, B, C]（撤回后不得让被影子的原文复活）: %+v", got)
	}
	// 不变量：库里**任何**检查点的影子都指向真实存在的行（新模型下是恒真的，
	// 因为行一条都不删——但断言留着，它同时验证「没有意外删行」）。
	for _, shadows := range checkpointShadows(t, s, id) {
		for _, seq := range shadows {
			var exists int
			if err := s.db.QueryRow(`SELECT COUNT(1) FROM messages WHERE session_id = ? AND seq = ?`, id, seq).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists == 0 {
				t.Fatalf("检查点引用了不存在的 seq %d: %+v", seq, shadows)
			}
		}
	}
	// 切回原叶：检查点二重新生效（历史恢复）
	if _, _, err := s.SetLeaf(id, before[len(before)-1].Seq); err != nil {
		t.Fatal(err)
	}
	back, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(before) {
		t.Fatalf("切回后应恢复 %d 条，实际 %d", len(before), len(back))
	}
}

// TestSetLeafRejectsUnknownSeq：坏号报错而**不静默当 0**（静默会把用户送到一个
// 他没点的位置）。
func TestSetLeafRejectsUnknownSeq(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 3)
	if _, _, err := s.SetLeaf(id, 99); err == nil {
		t.Fatal("不存在的 seq 应报错")
	}
	// 0 = 空路径，合法
	if removed, leaf, err := s.SetLeaf(id, 0); err != nil || leaf != 0 || removed != 3 {
		t.Fatalf("seq=0 应是合法空路径: removed=%d leaf=%d err=%v", removed, leaf, err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("空路径应回放为空，实际 %d 条", len(got))
	}
}

// TestRewindRemovedCountMatchesOldDeleteSemantics：**removed 数值与旧删除语义逐例
// 相同**（这是老客户端兼容性的硬要求，见 §B.1 的算例）。
//
// 旧语义 removed = len(surface) − idx（锚点在 surface 里的下标）；
// 新语义 removed = |path(旧叶)| − |path(parent(anchor))|。
func TestRewindRemovedCountMatchesOldDeleteSemantics(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 10) // A..J
	// 前缀压缩（影子 A B C）+ 中间段压缩（影子 E F）→ surface 里有 2 个检查点
	if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "前缀摘要"}, 0, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "中段摘要"}, 2, 2); err != nil {
		t.Fatal(err)
	}
	rows, err := s.readRows(id)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := s.LeafSeq(id)
	if err != nil {
		t.Fatal(err)
	}
	surface := pathRows(rows, int(leaf), s.treeMigrated)
	if len(surface) < 4 {
		t.Fatalf("前置状态不符（surface 太短）: %d", len(surface))
	}

	for idx := range surface {
		anchor := int64(surface[idx].seq)
		wantRemoved := len(surface) - idx // 旧口径

		// 每次用一份干净的库（撤回会移动叶，连续撤回的基准不同）
		s2 := openTestStore(t)
		id2, _ := s2.Create()
		appendN(t, s2, id2, 10)
		if _, err := s2.AppendCheckpoint(id2, llm.Message{Role: "user", Content: "前缀摘要"}, 0, 3); err != nil {
			t.Fatal(err)
		}
		if _, err := s2.AppendCheckpoint(id2, llm.Message{Role: "user", Content: "中段摘要"}, 2, 2); err != nil {
			t.Fatal(err)
		}
		removed, _, err := s2.RewindTo(id2, anchor)
		if err != nil {
			t.Fatalf("idx=%d anchor=%d: %v", idx, anchor, err)
		}
		if removed != wantRemoved {
			t.Fatalf("idx=%d anchor=%d: removed=%d 与旧口径 %d 不一致", idx, anchor, removed, wantRemoved)
		}
		// 路径长度也对上：新路径 = surface[:idx]
		got, err := s2.Load(id2)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != idx {
			t.Fatalf("idx=%d: 新路径应 %d 条，实际 %d", idx, idx, len(got))
		}
	}
	_ = msgs
}
