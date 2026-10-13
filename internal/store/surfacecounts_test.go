// surfaceCounts 的 SQL 下推对照测试（性能审查 P1-1，2026-10）。
//
// legacySurfaceCounts 是下推前的旧 Go 实现（全表逐行 Scan + 逐检查点解 JSON +
// surfaceRows 数存活行）的**逐字拷贝**——它就是对照基准本身，不是复刻品：
// 新实现（GROUP BY 下推 + 检查点影子扣减）与它在一切可达状态上必须给出
// 完全相同的计数，本文件用「普通消息 / 多轮嵌套压缩 / 压缩后撤回 / 通知行 /
// 多会话混合」几组状态逐一对照钉住。
package store

import (
	"encoding/json"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// legacySurfaceCounts 旧 Go 实现（改动前 sessions.go 的原函数体，逐字保留）。
func (s *Store) legacySurfaceCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT session_id, seq, checkpoint, shadowed_seqs FROM messages ORDER BY session_id, seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bySession := make(map[string][]rowData)
	for rows.Next() {
		var r rowData
		var id, shadowedSeqs string
		var cp int
		if err := rows.Scan(&id, &r.seq, &cp, &shadowedSeqs); err != nil {
			return nil, err
		}
		r.checkpoint = cp == 1
		if r.checkpoint && shadowedSeqs != "" && shadowedSeqs != "[]" {
			if err := json.Unmarshal([]byte(shadowedSeqs), &r.shadowed); err != nil {
				return nil, err
			}
		}
		bySession[id] = append(bySession[id], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(bySession))
	for id, rs := range bySession {
		counts[id] = len(surfaceRows(rs))
	}
	return counts, nil
}

// compareSurfaceCounts 跑两条实现并断言逐会话一致（新实现有的会话旧实现也必须有，
// 反之亦然；条数逐一相等）。
func compareSurfaceCounts(t *testing.T, s *Store, stage string) {
	t.Helper()
	got, err := s.surfaceCounts()
	if err != nil {
		t.Fatalf("%s: surfaceCounts: %v", stage, err)
	}
	want, err := s.legacySurfaceCounts()
	if err != nil {
		t.Fatalf("%s: legacySurfaceCounts: %v", stage, err)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: 会话数不一致: 新 %v 旧 %v", stage, got, want)
	}
	for id, n := range want {
		if got[id] != n {
			t.Fatalf("%s: 会话 %s 计数不一致: SQL 下推=%d 旧 Go 扫描=%d", stage, id, got[id], n)
		}
	}
}

// TestSurfaceCountsMatchesLegacyScan：普通消息 + 多轮嵌套压缩 + 压缩后撤回 +
// 通知行 + 多会话混合，每个阶段两条实现的计数都必须一致。
func TestSurfaceCountsMatchesLegacyScan(t *testing.T) {
	s := openTestStore(t)

	// 会话 A：普通消息，无压缩
	a, _ := s.Create()
	appendN(t, s, a, 5)
	compareSurfaceCounts(t, s, "普通消息")

	// 会话 B：两轮嵌套压缩（后一份摘要把前一份检查点一起影子掉）
	b, _ := s.Create()
	appendN(t, s, b, 4)
	if _, err := s.AppendCheckpoint(b, llm.Message{Role: "user", Content: "摘要1"}, 0, 2); err != nil {
		t.Fatal(err)
	}
	compareSurfaceCounts(t, s, "第一轮压缩")
	if _, err := s.AppendCheckpoint(b, llm.Message{Role: "user", Content: "摘要2"}, 0, 3); err != nil {
		t.Fatal(err)
	}
	compareSurfaceCounts(t, s, "嵌套压缩")

	// 会话 C：压缩后撤回——检查点影子集合收缩、影子全空的检查点整条删掉
	c, _ := s.Create()
	appendN(t, s, c, 6)
	if _, err := s.AppendCheckpoint(c, llm.Message{Role: "user", Content: "摘要C"}, 0, 4); err != nil {
		t.Fatal(err)
	}
	compareSurfaceCounts(t, s, "压缩后撤回前")
	// 撤回检查点之后的一条消息（锚点在当前历史里）：后面的行被删、检查点影子收缩
	surface, err := s.Load(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rewind(c, surface[len(surface)-1].Seq); err != nil {
		t.Fatal(err)
	}
	compareSurfaceCounts(t, s, "压缩后撤回")

	// 通知行（notice=1）与退化检查点（count=0，无影子集合）：都是普通行计数
	d, _ := s.Create()
	if _, err := s.AppendMsg(d, llm.Message{Role: "user", Content: " hi", Notice: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCheckpoint(d, llm.Message{Role: "user", Content: "空摘要"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	appendN(t, s, d, 2)
	compareSurfaceCounts(t, s, "通知行+退化检查点")

	// 全量对照：List 的侧栏条数（对外契约）与新实现同源
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	counts, err := s.surfaceCounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range list {
		if meta.Messages != counts[meta.ID] {
			t.Fatalf("List 的 Messages 与 surfaceCounts 不一致: %s=%d vs %d",
				meta.ID, meta.Messages, counts[meta.ID])
		}
	}
}
