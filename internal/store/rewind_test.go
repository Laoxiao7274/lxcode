// rewind_test.go —— 会话回退（撤回）在存储层的契约：删掉 seq 那条消息**及其之后的全部
// 历史**、检查点的影子集合跟着收缩、返回从当前历史里删掉的条数、重复撤回幂等。
//
// 为什么检查点要单独判：检查点行是**追加在末尾**的（它的 seq 比它顶替的那段历史里任何
// 一行都大），按行号一刀切会把位置在锚点之前的摘要删掉，被它影子掉的原文随即「复活」——
// 磁盘回放就与内存历史分叉了。这里钉住的是「回放结果 == 撤回后应有的那段前缀」。
package store

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// checkpointShadows 读出某会话全部检查点的影子集合（按 seq 升序）——撤回收缩影子区间
// 这件事在对外行为上不可见（被影子行本来就看不见），只能直接查库断言。
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

// TestRewindDeletesAnchorAndAfter：撤回删掉锚点及其之后的全部消息，之前的原样保留，
// 返回删掉的条数；剩下的消息回放时仍带序号（前端还要按它继续撤回）。
func TestRewindDeletesAnchorAndAfter(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 5) // A B C D E（seq 1..5）

	removed, err := s.Rewind(id, 3) // 撤回 C
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Fatalf("应删掉 3 条（C D E），实际 %d", removed)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != msgs[0].Content || got[1].Content != msgs[1].Content {
		t.Fatalf("撤回后应只剩 A B: %+v", got)
	}
	if got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("回放的消息应带序号（前端还要拿它当锚点）: %+v", got)
	}
	// 侧栏计数与当前历史同口径
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range list {
		if meta.ID == id && meta.Messages != 2 {
			t.Fatalf("侧栏消息数应为 2，实际 %d", meta.Messages)
		}
	}
}

// TestRewindIsIdempotent：重复撤回同一条返回 0 不报错；锚点不存在（含被影子掉的原文）
// 同样是空操作。报错会把「两个客户端同时点撤回」变成可见故障。
func TestRewindIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 3)

	if n, err := s.Rewind(id, 2); err != nil || n != 2 {
		t.Fatalf("首次撤回应删 2 条: n=%d err=%v", n, err)
	}
	if n, err := s.Rewind(id, 2); err != nil || n != 0 {
		t.Fatalf("重复撤回应返回 0 不报错: n=%d err=%v", n, err)
	}
	if n, err := s.Rewind(id, 99); err != nil || n != 0 {
		t.Fatalf("未知序号应返回 0 不报错: n=%d err=%v", n, err)
	}
	got, _ := s.Load(id)
	if len(got) != 1 || got[0].Content != "消息A" {
		t.Fatalf("空操作不该改动历史: %+v", got)
	}
}

// TestRewindDeletesCheckpointAfterAnchor：撤回锚点**之后**的检查点被整条删掉——
// 它影子掉的那段历史整个都在锚点之后，没有影子可替了，留着它只会把位置占错。
func TestRewindDeletesCheckpointAfterAnchor(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 6) // A..F
	// 影子 D E（存活集下标 3..4）→ 当前历史 = [A, B, C, 检查点, F]
	if _, err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "摘要"}, 3, 2); err != nil {
		t.Fatal(err)
	}
	before, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 5 {
		t.Fatalf("前置状态不符（应为 5 条）: %+v", before)
	}

	removed, err := s.Rewind(id, 3) // 撤回 C（存活集下标 2）
	if err != nil {
		t.Fatal(err)
	}
	// 当前历史 [A, B, C, 检查点, F] 里删掉 C、检查点、F = 3 条
	if removed != 3 {
		t.Fatalf("应删掉 3 条（C + 检查点 + F），实际 %d", removed)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != msgs[0].Content || got[1].Content != msgs[1].Content {
		t.Fatalf("撤回后应只剩 A B（检查点随之消失）: %+v", got)
	}
	if cps := checkpointShadows(t, s, id); len(cps) != 0 {
		t.Fatalf("锚点之后的检查点应被整条删掉: %+v", cps)
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

	// 撤回 D（存活集下标 2）→ 保留 [检查点, C]
	if _, err := s.Rewind(id, 4); err != nil {
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
	// 被影子的原文不该复活（它仍在库里，只是回放跳过）
	if hits, _, err := s.Search(SearchQuery{Pattern: "消息A", Max: 10}); err != nil || len(hits) == 0 {
		t.Fatalf("被影子的原文应留在库里: err=%v hits=%d", err, len(hits))
	}
}

// TestRewindDropsCheckpointWhoseShadowsAllGone：检查点的影子被删光 → 整条检查点删掉
// （没有影子可替的摘要留着只会把位置占错）。嵌套压缩（检查点 A 影子了检查点 B）是最容易
// 踩到这条的情形：A 的影子集合里既有普通行、又有 B 自己的行。
//
// 这里断言的是**不变量**而不是某一条实现路径：撤回后
//
//	① 回放结果 == 撤回后应有的那段前缀（内存与磁盘同口径）；
//	② 活下来的检查点，每一条影子都指向**真实存在**的行（不许引用已删的 seq）。
func TestRewindLeavesNoDanglingCheckpointRefs(t *testing.T) {
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

	// 撤回 D（seq 4，存活集下标 3）→ 前缀 = [A, B, C]。
	// 这一刀同时把检查点一影子掉的 G H 与检查点二影子掉的 E 都删掉：
	// 检查点一的影子全没了 → 整条删掉；检查点二先收缩到只剩「检查点一」这一条影子，
	// 再因为检查点一已被删而一起消失（级联）。
	if _, err := s.Rewind(id, 4); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Content != "消息A" || got[1].Content != "消息B" || got[2].Content != "消息C" {
		t.Fatalf("回放应剩 [A, B, C]（撤回后不得让被影子的原文复活）: %+v", got)
	}
	for _, shadows := range checkpointShadows(t, s, id) {
		for _, seq := range shadows {
			var exists int
			if err := s.db.QueryRow(`SELECT COUNT(1) FROM messages WHERE session_id = ? AND seq = ?`, id, seq).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists == 0 {
				t.Fatalf("活下来的检查点引用了已删的 seq %d: %+v", seq, shadows)
			}
		}
	}
}
