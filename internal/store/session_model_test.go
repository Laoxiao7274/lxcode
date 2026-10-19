// session_model_test.go —— 会话级模型选择的落库契约：写读往返 / 空列 = 没选过 /
// 会话不存在报错 / 落库不碰 updated_at（切模型不能把会话顶到侧栏最前面）。
package store

import (
	"strings"
	"testing"
	"time"
)

func TestSessionModelRoundTrip(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	// 没选过 = 空串（调用方回落 Agent 绑定/default 角色），不是某个编出来的模型。
	if got, err := s.SessionModelOf(id); err != nil || got != "" {
		t.Fatalf("新会话模型应为空串，got %q err=%v", got, err)
	}
	for _, m := range []string{"deepseek-v3", "glm-4.6", ""} {
		if err := s.SaveSessionModel(id, m); err != nil {
			t.Fatalf("SaveSessionModel(%q) 失败: %v", m, err)
		}
		if got, err := s.SessionModelOf(id); err != nil || got != m {
			t.Fatalf("往返不一致: want %q got %q err=%v", m, got, err)
		}
	}
}

func TestSessionModelMissingSessionErrors(t *testing.T) {
	s := openTestStore(t)
	if err := s.SaveSessionModel("没有这个会话", "m1"); err == nil {
		t.Fatal("不存在的会话应报错")
	}
	if _, err := s.SessionModelOf("没有这个会话"); err == nil {
		t.Fatal("读不存在的会话应报错")
	}
}

func TestSessionModelDoesNotTouchUpdatedAt(t *testing.T) {
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
	if err := s.SaveSessionModel(id, "m2"); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := s.db.QueryRow(`SELECT updated_at FROM sessions WHERE id = ?`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(before, after) {
		t.Fatalf("落库模型不该动 updated_at: before=%s after=%s", before, after)
	}
}
