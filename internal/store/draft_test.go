// draft_test.go —— 会话输入草稿的落库契约：写读往返 / 空串 = 没存过 / 多字节文本
// 不失真 / 会话不存在报错 / 落库不碰 updated_at（存草稿不能把会话顶到侧栏最前面）。
package store

import (
	"strings"
	"testing"
	"time"
)

func TestDraftRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	// 没存过 = 空串（wire 上经 omitempty 整键缺席，客户端回落本地记账）。
	if got, err := s.DraftOf(id); err != nil || got != "" {
		t.Fatalf("新会话草稿应为空串，got %q err=%v", got, err)
	}
	for _, text := range []string{"写一半的函数", "multi\nline draft ✨", ""} {
		if err := s.SaveDraft(id, text); err != nil {
			t.Fatalf("SaveDraft(%q) 失败: %v", text, err)
		}
		if got, err := s.DraftOf(id); err != nil || got != text {
			t.Fatalf("往返不一致: want %q got %q err=%v", text, got, err)
		}
	}
}

func TestDraftMissingSessionErrors(t *testing.T) {
	s := openTestStore(t)
	if err := s.SaveDraft("没有这个会话", "hello"); err == nil {
		t.Fatal("不存在的会话应报错")
	}
	if _, err := s.DraftOf("没有这个会话"); err == nil {
		t.Fatal("读不存在的会话应报错")
	}
}

func TestDraftDoesNotTouchUpdatedAt(t *testing.T) {
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
	if err := s.SaveDraft(id, "写一半"); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := s.db.QueryRow(`SELECT updated_at FROM sessions WHERE id = ?`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(before, after) {
		t.Fatalf("存草稿不该动 updated_at: before=%s after=%s", before, after)
	}
}
