// 文件快照的存储层契约（docs/session-tree.md §C）：
//
//   - blob 内容寻址（同内容只存一份）+ 读取校验 hash；
//   - 快照按 owner_session_id/owner_seq 收集（跨会话一次查询）；
//   - 按 owner_seq 汇总每轮的文件数（树徽标）；
//   - 清理：先删元数据行、再 GC 孤儿 blob（顺序不能反）；最近 N 轮恒不清理。
package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// FileSnapshotRec 是测试用的快照行构造助手（位置参数太多，读起来容易错位）。
func FileSnapshotRec(sessionID string, seq int64, ownerID string, ownerSeq int64, path, before, after string,
	size int64, skipped bool, skipReason string) sessiondata.FileSnapshot {
	return sessiondata.FileSnapshot{
		SessionID: sessionID, Seq: seq, OwnerSessionID: ownerID, OwnerSeq: ownerSeq,
		Path: path, BeforeHash: before, AfterHash: after, Size: size,
		Skipped: skipped, SkipReason: skipReason,
	}
}

// TestBlobDedupAndVerify：同内容只存一份 blob；读取时校验内容 hash（防磁盘损坏）。
func TestBlobDedupAndVerify(t *testing.T) {
	s := openTestStore(t)
	data := []byte("hello 快照")
	h1, err := s.PutBlob(data)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.PutBlob(data)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("同内容 hash 必须相同: %s vs %s", h1, h2)
	}
	if h1 != HashBytes(data) {
		t.Fatalf("hash 应是 sha256: %s", h1)
	}
	// 只存了一份
	n := 0
	_ = filepath.Walk(s.BlobsRoot(), func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n++
		}
		return nil
	})
	if n != 1 {
		t.Fatalf("同内容应只存一份 blob，实际 %d", n)
	}
	// 读回
	got, err := s.GetBlob(h1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("读回内容不符: %q", got)
	}
	// 内容被篡改 → 读取报错（宁可在回滚时报错，也不要拿错内容覆盖用户文件）
	path, _ := s.blobPath(h1)
	if err := os.WriteFile(path, []byte("被篡改"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetBlob(h1); err == nil {
		t.Fatal("内容校验失败时必须报错")
	}
}

// TestBlobRejectsBadHash：hash 形态硬校验（拒绝路径穿越）。
func TestBlobRejectsBadHash(t *testing.T) {
	s := openTestStore(t)
	for _, bad := range []string{"", "../etc/passwd", "xyz", "ABCDEF"} {
		if _, err := s.GetBlob(bad); err == nil {
			t.Fatalf("非法 hash %q 应被拒绝", bad)
		}
	}
}

// TestSnapshotsForRestoreAcrossSessions：子会话的写按 owner 归属到父会话的某一轮——
// 一次查询拿到父 + 全部子孙（不需要递归 parent_id）。
func TestSnapshotsForRestoreAcrossSessions(t *testing.T) {
	s := openTestStore(t)
	parent, _ := s.Create()
	child, err := s.CreateChild(parent, "coder", "d1", parent, 42)
	if err != nil {
		t.Fatal(err)
	}
	// 父会话第 42 轮改 a.txt（owner_seq = 42）
	if err := s.SnapshotWrite(FileSnapshotRec(child, 10, parent, 42, "a.txt", "h1", "h2", 10, false, "")); err != nil {
		t.Fatal(err)
	}
	// 子会话第 44 轮改 b.txt（owner 仍是父会话、owner_seq = 44）
	if err := s.SnapshotWrite(FileSnapshotRec(child, 20, parent, 44, "b.txt", "h3", "h4", 20, false, "")); err != nil {
		t.Fatal(err)
	}
	// 子会话第 45 轮改 c.txt
	if err := s.SnapshotWrite(FileSnapshotRec(child, 21, parent, 45, "c.txt", "h5", "h6", 30, false, "")); err != nil {
		t.Fatal(err)
	}

	// 切段 >= 44：只拿到 b、c（a 属于第 42 轮）
	rows, err := s.SnapshotsForRestore(parent, 44, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Path != "b.txt" || rows[1].Path != "c.txt" {
		t.Fatalf(">= 44 应只拿到 b.txt 与 c.txt: %+v", rows)
	}
	// 切段 > 44：只拿到 c
	rows, err = s.SnapshotsForRestore(parent, 44, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Path != "c.txt" {
		t.Fatalf("> 44 应只拿到 c.txt: %+v", rows)
	}
	// 全部（cut=0）：三条都在，按 path 分组序
	rows, err = s.SnapshotsForRestore(parent, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("cut=0 应拿到全部 3 条: %+v", rows)
	}
}

// TestSnapshotCountsByOwnerSeq：按 owner_seq 汇总每轮的文件数（含子会话计数）。
func TestSnapshotCountsByOwnerSeq(t *testing.T) {
	s := openTestStore(t)
	parent, _ := s.Create()
	child, _ := s.CreateChild(parent, "coder", "d1", parent, 42)
	for _, rec := range []sessiondata.FileSnapshot{
		FileSnapshotRec(parent, 10, parent, 42, "a.txt", "h1", "h2", 1, false, ""),
		FileSnapshotRec(child, 20, parent, 42, "b.txt", "h3", "h4", 1, false, ""),
		FileSnapshotRec(child, 21, parent, 44, "c.txt", "h5", "h6", 1, false, ""),
		// skipped 的不计入（回滚不了，进 external）
		FileSnapshotRec(parent, 11, parent, 44, "big.bin", "", "", 1, true, SkipReasonTooLarge),
	} {
		if err := s.SnapshotWrite(rec); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := s.SnapshotCountsByOwnerSeq(parent)
	if err != nil {
		t.Fatal(err)
	}
	if c := counts[42]; c[0] != 2 || c[1] != 1 {
		t.Fatalf("第 42 轮应有 2 个文件、其中 1 个来自子会话，实际 %+v", c)
	}
	if c := counts[44]; c[0] != 1 || c[1] != 1 {
		t.Fatalf("第 44 轮应有 1 个文件（skipped 不计）、其中 1 个来自子会话，实际 %+v", c)
	}
}

// TestGCBlobsKeepsReferenced：孤儿 blob 被回收、被引用的不动；刚写的不回收（mtime < 1h）。
func TestGCBlobsKeepsReferenced(t *testing.T) {
	s := openTestStore(t)
	parent, _ := s.Create()

	referenced, err := s.PutBlob([]byte("被引用的内容"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SnapshotWrite(FileSnapshotRec(parent, 1, parent, 1, "a.txt", referenced, "after", 10, false, "")); err != nil {
		t.Fatal(err)
	}
	orphan, err := s.PutBlob([]byte("孤儿内容"))
	if err != nil {
		t.Fatal(err)
	}
	// 把两个 blob 的 mtime 都拨到 2 小时前（绕过「刚写的不回收」）
	for _, h := range []string{referenced, orphan} {
		p, _ := s.blobPath(h)
		old := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.GCBlobs()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应回收 1 个孤儿 blob，实际 %d", n)
	}
	if _, err := s.GetBlob(referenced); err != nil {
		t.Fatalf("被引用的 blob 不该被删: %v", err)
	}
	if _, err := s.GetBlob(orphan); err == nil {
		t.Fatal("孤儿 blob 应被回收")
	}
}

// TestPruneKeepsRecentRounds：最近 N 轮恒不清理（即使超过保留期）。
func TestPruneKeepsRecentRounds(t *testing.T) {
	s := openTestStore(t)
	parent, _ := s.Create()
	// 3 轮快照，全部伪造为 40 天前（超过 30 天保留期）
	for i, seq := range []int64{1, 2, 3} {
		rec := FileSnapshotRec(parent, seq, parent, seq, "f.txt", "h", "h2", 1, false, "")
		if err := s.SnapshotWrite(rec); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	old := time.Now().AddDate(0, 0, -40).Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE file_snapshots SET created_at = ?`, old); err != nil {
		t.Fatal(err)
	}

	r := DefaultSnapshotRetention()
	r.KeepRecent = 2 // 最近 2 轮（owner_seq 2、3）恒不清理
	if _, _, err := s.PruneSnapshots(r); err != nil {
		t.Fatal(err)
	}
	rows, err := s.SnapshotsForRestore(parent, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("最近 2 轮应保留，只删更早的：剩 %d 条", len(rows))
	}
	for _, r := range rows {
		if r.OwnerSeq < 2 {
			t.Fatalf("第 %d 轮不该保留（超出最近 2 轮且过期）", r.OwnerSeq)
		}
	}
}

// TestRestoreIntentRoundTrip：意图日志写/读/清（崩溃自愈的持久化面）。
func TestRestoreIntentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	it := sessiondata.SnapshotIntent{SessionID: "s1", CutSeq: 42, CutKind: "rewind", Written: []string{"a.txt", "b.txt"}}
	if err := s.SaveRestoreIntent(it); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadRestoreIntents()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "s1" || got[0].CutSeq != 42 || got[0].CutKind != "rewind" {
		t.Fatalf("意图日志往返不符: %+v", got)
	}
	if len(got[0].Written) != 2 {
		t.Fatalf("written 应保留: %+v", got[0].Written)
	}
	if err := s.ClearRestoreIntent("s1"); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadRestoreIntents()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("清掉之后应为空: %+v", got)
	}
}
