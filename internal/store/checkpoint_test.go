// checkpoint_test.go —— 压缩检查点的影子区间行为（2026-09-22）。
// 断言的是对外契约：检查点行替换当前历史里 [skip, skip+count) 这段存活历史、被影子的
// 原文留在库里（翻旧账仍可查）、回放（Load/Latest）把检查点放回被影子段原本占据的
// 位置、侧栏计数只算当前历史、落盘落后于内存时拒绝写一个错的影子区间。不关心表结构细节。
package store

import (
	"strings"
	"testing"

	"github.com/moyunteng/lxcode/internal/llm"
)

// appendN 追加 n 条普通消息（内容可辨）。
func appendN(t *testing.T, s *Store, id string, n int) []llm.Message {
	t.Helper()
	var out []llm.Message
	for i := 0; i < n; i++ {
		m := llm.Message{Role: "user", Content: "消息" + string(rune('A'+i))}
		if err := s.AppendMsg(id, m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func TestCheckpointShadowsPrefix(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 4)

	// 检查点替换前 3 条（存活集里的前 3 条）——skip=0 也是**向后兼容的钉子**：
	// 老库里的检查点行影子集合都是前缀，锚点因此落在库内最小 seq 上，回放顺序与
	// 「检查点一律排最前」的旧规则逐字节一致。
	checkpoint := llm.Message{Role: "user", Content: "这是压缩检查点"}
	if err := s.AppendCheckpoint(id, checkpoint, 0, 3); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("当前历史应为 检查点 + 最后一条 = 2，实际 %d: %+v", len(got), got)
	}
	if got[0].Content != checkpoint.Content {
		t.Fatalf("首条应是检查点: %q", got[0].Content)
	}
	if got[1].Content != msgs[3].Content {
		t.Fatalf("检查点之后应保留第 4 条: %q", got[1].Content)
	}

	// 被影子的原文留在库里（翻旧账可查）：搜索仍能命中
	hits, err := s.Search("消息A", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("被影子的原文应留在库里（搜索仍可命中）")
	}

	// Latest 也走同一套过滤（重启恢复最近会话时不能把被影子的原文带回来）
	lid, lmsgs, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if lid != id || len(lmsgs) != 2 {
		t.Fatalf("Latest 应返回当前历史: id=%s 条数=%d", lid, len(lmsgs))
	}

	// 侧栏消息数与当前历史同口径（压缩后不能显示原始条数）
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range list {
		if meta.ID == id && meta.Messages != 2 {
			t.Fatalf("侧栏消息数应与当前历史一致（2），实际 %d", meta.Messages)
		}
	}
}

// 多轮压缩：后来的检查点可以把先前的检查点一起影子掉（合并成一份摘要）。
func TestCheckpointNestedMerges(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 4)

	cp1 := llm.Message{Role: "user", Content: "第一份摘要"}
	if err := s.AppendCheckpoint(id, cp1, 0, 2); err != nil {
		t.Fatal(err)
	}
	// 当前历史 = [cp1, C, D]；第二份摘要把这三条全影子掉
	cp2 := llm.Message{Role: "user", Content: "合并后的摘要"}
	if err := s.AppendCheckpoint(id, cp2, 0, 3); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != cp2.Content {
		t.Fatalf("嵌套压缩后应只剩最后一份摘要: %+v", got)
	}
}

// 影子区间解析必须按"存活集"算：被影子过的行不再参与计数（否则会二次影子）。
func TestCheckpointShadowSkipsAlreadyShadowed(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 5) // A B C D E

	// 影子存活集最前面 3 条 = A B C
	if err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "CP1"}, 0, 3); err != nil {
		t.Fatal(err)
	}
	// 存活集现在是 [CP1, D, E]；再影子最前面 2 条 = CP1、D（不是 A、B——
	// 它们已被影子过，不该再被数一次）
	if err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "CP2"}, 0, 2); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "CP2" || got[1].Content != "消息E" {
		t.Fatalf("应剩 CP2 + 消息E（CP1 与 D 被影子）: %+v", got)
	}
}

// 落盘落后于内存（某次写入失败过）：拒绝写一个错的影子区间，压缩必须失败。
func TestCheckpointRejectsWhenStoreBehind(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 2)

	err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "摘要"}, 0, 5)
	if err == nil || !strings.Contains(err.Error(), "落盘落后") {
		t.Fatalf("存活条数不足应拒绝: %v", err)
	}
	got, _ := s.Load(id)
	if len(got) != 2 {
		t.Fatalf("拒绝时不得写入任何行: %d", len(got))
	}
}

// 旧库升级：没有 checkpoint 列的库能直接打开并正常读写（幂等 ALTER）。
func TestCheckpointColumnsMigrateOldDB(t *testing.T) {
	dir := t.TempDir()
	// 先用旧结构建库（不含 checkpoint 列）
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.Create()
	appendN(t, s, id, 2)
	if _, err := s.db.Exec(`DROP TABLE messages`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TABLE messages (
		session_id TEXT NOT NULL, seq INTEGER NOT NULL, role TEXT NOT NULL,
		content TEXT NOT NULL DEFAULT '', reasoning TEXT NOT NULL DEFAULT '',
		reasoning_sig TEXT NOT NULL DEFAULT '', tool_calls TEXT NOT NULL DEFAULT '[]',
		tool_call_id TEXT NOT NULL DEFAULT '', PRIMARY KEY (session_id, seq))`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// 重新打开：ALTER 迁移应把三列补上，读写正常
	re, err := Open(dir)
	if err != nil {
		t.Fatalf("旧库升级应成功: %v", err)
	}
	t.Cleanup(func() { _ = re.Close() })
	id2, _ := re.Create()
	appendN(t, re, id2, 3)
	if err := re.AppendCheckpoint(id2, llm.Message{Role: "user", Content: "摘要"}, 0, 2); err != nil {
		t.Fatal(err)
	}
	got, err := re.Load(id2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("升级后的库应能正常压缩: %+v", got)
	}
}

// 中间段影子（子会话保护头部任务说明书时就是 skip=1）：检查点落在被影子段原本占据
// 的位置上——头部那条留在最前，摘要紧随其后，其余照旧按 seq 升序。
func TestCheckpointMiddleSpanKeepsHeadFirst(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 5) // A B C D E

	// 影子 [B, C]（存活集下标 1..2）——头部 A 不在区间里
	cp := llm.Message{Role: "user", Content: "中间段摘要"}
	if err := s.AppendCheckpoint(id, cp, 1, 2); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{msgs[0].Content, cp.Content, msgs[3].Content, msgs[4].Content}
	if len(got) != len(want) {
		t.Fatalf("回放应为 头部 + 摘要 + 其余 = %d 条，实际 %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Fatalf("第 %d 条不符：want %q got %q（全量 %+v）", i, w, got[i].Content, got)
		}
	}

	// Latest 与侧栏计数走同一套存活判定：条数与顺序都不能与 Load 分叉
	lid, lmsgs, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if lid != id || len(lmsgs) != len(want) || lmsgs[0].Content != msgs[0].Content {
		t.Fatalf("Latest 应与 Load 同口径: id=%s 条数=%d 首条=%q", lid, len(lmsgs), lmsgs[0].Content)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range list {
		if meta.ID == id && meta.Messages != len(want) {
			t.Fatalf("侧栏消息数应为 %d，实际 %d", len(want), meta.Messages)
		}
	}
	// 被影子的原文仍在库里（翻旧账可查）
	if hits, err := s.Search("消息B", 10); err != nil || len(hits) == 0 {
		t.Fatalf("被影子的原文应留在库里: err=%v hits=%d", err, len(hits))
	}
}

// 两次压缩的锚点可以"后写的更靠前"（先压中间、再回头压头部）：回放顺序按被替换段在
// 历史里的**位置**排，不是按检查点自身的写入顺序（seq）。按 seq 排会把两份摘要的先后
// 搞反——后来的那份明明顶替的是更前面的位置。
func TestCheckpointLaterWriteCanAnchorEarlier(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 4) // A B C D

	cp1 := llm.Message{Role: "user", Content: "中间摘要"}
	if err := s.AppendCheckpoint(id, cp1, 1, 2); err != nil { // 影子 B C → [A, cp1, D]
		t.Fatal(err)
	}
	cp2 := llm.Message{Role: "user", Content: "头部摘要"}
	if err := s.AppendCheckpoint(id, cp2, 0, 1); err != nil { // 影子 A → [cp2, cp1, D]
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{cp2.Content, cp1.Content, msgs[3].Content}
	if len(got) != len(want) {
		t.Fatalf("应剩 3 条（两份摘要 + D），实际 %d: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Fatalf("第 %d 条不符：want %q got %q（全量 %+v）", i, w, got[i].Content, got)
		}
	}
}

// 中间段影子 + 嵌套：第二次压缩把中间段检查点自己也影子掉（合并成一份摘要）时，
// 头部仍在最前。
func TestCheckpointNestedMiddleSpan(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	msgs := appendN(t, s, id, 4) // A B C D

	cp1 := llm.Message{Role: "user", Content: "第一份摘要"}
	if err := s.AppendCheckpoint(id, cp1, 1, 2); err != nil { // 影子 B C → [A, cp1, D]
		t.Fatal(err)
	}
	cp2 := llm.Message{Role: "user", Content: "合并后的摘要"}
	if err := s.AppendCheckpoint(id, cp2, 1, 2); err != nil { // 影子 cp1 与 D → [A, cp2]
		t.Fatal(err)
	}
	got, err := s.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != msgs[0].Content || got[1].Content != cp2.Content {
		t.Fatalf("头部应保留 + 只剩最后一份摘要: %+v", got)
	}
}

// skip 越界同样要拒（不只是 count 超界）：位置寻址与库内存活集不一致时，写下去的影子
// 区间会指到别的行上——回放就会丢掉不该丢的历史。
func TestCheckpointRejectsOutOfRangeSkip(t *testing.T) {
	s := openTestStore(t)
	id, _ := s.Create()
	appendN(t, s, id, 3)

	err := s.AppendCheckpoint(id, llm.Message{Role: "user", Content: "摘要"}, 5, 1)
	if err == nil || !strings.Contains(err.Error(), "落盘落后") {
		t.Fatalf("skip 越界应拒绝: %v", err)
	}
	got, _ := s.Load(id)
	if len(got) != 3 {
		t.Fatalf("拒绝时不得写入任何行: %d", len(got))
	}
}
