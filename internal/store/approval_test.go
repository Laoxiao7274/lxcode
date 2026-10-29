// approval_test.go —— 会话审批档的落库契约：写读往返 / 空列 = 没落过 /
// 会话不存在报错 / 落库不碰 updated_at（改档不能把会话顶到侧栏最前面）。
package store

import (
	"strings"
	"testing"
	"time"
)

func TestApprovalRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	// 没落过 = 空串（调用方回落 behavior 默认档），不是某个编出来的档。
	if got, err := s.ApprovalOf(id); err != nil || got != "" {
		t.Fatalf("新会话审批档应为空串，got %q err=%v", got, err)
	}
	for _, mode := range []string{"strict", "auto", "confirm"} {
		if err := s.SaveApproval(id, mode); err != nil {
			t.Fatalf("SaveApproval(%q) 失败: %v", mode, err)
		}
		if got, err := s.ApprovalOf(id); err != nil || got != mode {
			t.Fatalf("往返不一致: want %q got %q err=%v", mode, got, err)
		}
	}
}

func TestApprovalMissingSessionErrors(t *testing.T) {
	s := openTestStore(t)
	if err := s.SaveApproval("没有这个会话", "strict"); err == nil {
		t.Fatal("不存在的会话应报错")
	}
	if _, err := s.ApprovalOf("没有这个会话"); err == nil {
		t.Fatal("读不存在的会话应报错")
	}
}

func TestApprovalDoesNotTouchUpdatedAt(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.db.QueryRow(`SELECT updated_at FROM sessions WHERE id = ?`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // updated_at 粒度足够分辨一次写
	if err := s.SaveApproval(id, "strict"); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := s.db.QueryRow(`SELECT updated_at FROM sessions WHERE id = ?`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(before, after) {
		t.Fatalf("落库审批档不该动 updated_at: before=%s after=%s", before, after)
	}
}
