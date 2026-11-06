// project.remove 的存储层语义（2026-12 用户报「没有删除项目的地方」）：
//
//   - 只删 projects 行：磁盘不归 store 管，会话行一条不动；
//   - **会话归属清空** → 落进「未分组」。留着悬空 workspace 会让这些会话在侧栏
//     彻底消失（侧栏只按「未分组 / 已注册项目」分桶）——数据在、入口没了；
//   - 项目不存在 → 明确报错，且不改任何会话。
package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func TestRemoveProjectKeepsSessionsAndClearsWorkspace(t *testing.T) {
	s := openAt(t, t.TempDir())
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, err := s.AddProject("demo", dir)
	if err != nil {
		t.Fatal(err)
	}
	// 两条会话（一条顶层、一条子会话——子会话继承 workspace，移除时也要跟着清）
	id1, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionWorkspace(id1, meta.ID); err != nil {
		t.Fatal(err)
	}
	id2, err := s.CreateChild(id1, "coder", "d1", id1, 1)
	if err != nil {
		t.Fatal(err)
	}

	if n, err := s.ProjectSessionCount(meta.ID); err != nil || n != 2 {
		t.Fatalf("移除前项目会话数应为 2: n=%d err=%v", n, err)
	}

	if err := s.RemoveProject(meta.ID); err != nil {
		t.Fatalf("RemoveProject: %v", err)
	}

	// 项目没了
	if _, found, err := s.ProjectByID(meta.ID); err != nil || found {
		t.Fatalf("项目应已移除: found=%v err=%v", found, err)
	}
	// 会话还在（两行都在），归属清空
	for _, id := range []string{id1, id2} {
		ws, err := s.WorkspaceOf(id)
		if err != nil {
			t.Fatalf("会话 %s 应仍在库里: %v", id, err)
		}
		if ws != "" {
			t.Fatalf("会话 %s 的归属应清空（归入未分组）: %q", id, ws)
		}
	}
	// 磁盘目录没被动过
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("移除项目不该删磁盘目录: %v", err)
	}
}

func TestRemoveProjectUnknown(t *testing.T) {
	s := openAt(t, t.TempDir())
	if err := s.RemoveProject("no-such"); err == nil {
		t.Fatal("移除不存在的项目应报错")
	}
}

// ListProjectStats 的口径（助手项目全景三件套的数据源）：
//   - 顶层非助手会话才计入（子会话是派发产物；助手域不属于任何项目）；
//   - 无会话项目照常返回（Sessions=0、最近活动空串）；
//   - 按最近活动倒序，无活动的沉底（created_at 兜底保证确定性顺序）。
func TestListProjectStats(t *testing.T) {
	s := openAt(t, t.TempDir())
	// 两个项目：p1 带会话，p2 无会话
	p1, err := s.AddProject("proj-one", filepath.Join(t.TempDir(), "one"))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.AddProject("proj-two", filepath.Join(t.TempDir(), "two"))
	if err != nil {
		t.Fatal(err)
	}
	top, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionWorkspace(top, p1.ID); err != nil {
		t.Fatal(err)
	}
	child, err := s.CreateChild(top, "coder", "d1", top, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 子会话继承 workspace 但**不应**计入会话数（parent_id 过滤）
	if ws, err := s.WorkspaceOf(child); err != nil || ws != p1.ID {
		t.Fatalf("子会话应继承项目归属: %q err=%v", ws, err)
	}
	// 助手域会话：workspace 为空（防御口径——即便归属着项目也不计入）
	aid, err := s.CreateIn(sessiondata.SpaceAssistant)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionWorkspace(aid, p1.ID); err != nil {
		t.Fatal(err)
	}
	// 主域另一条会话（把 p1 的会话数顶到 2，且比第一条晚——排序依据）
	top2, err := s.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SessionWorkspace(top2, p1.ID); err != nil {
		t.Fatal(err)
	}
	// 活动时间区分开（updated_at 粒度是纳秒，写入间隔足够）
	time.Sleep(20 * time.Millisecond)
	if _, err := s.AppendMsg(top2, llm.Message{Role: "user", Content: "活动一下"}); err != nil {
		t.Fatal(err)
	}

	stats, err := s.ListProjectStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("应有两个项目（无会话项目不隐藏）: %+v", stats)
	}
	if stats[0].ID != p1.ID {
		t.Fatalf("有活动的项目应排在前: %+v", stats[0])
	}
	if stats[0].Sessions != 2 {
		t.Fatalf("p1 顶层会话数应为 2（子会话与助手会话都不计）: %+v", stats[0])
	}
	if stats[0].UpdatedAt == "" {
		t.Fatal("有活动的项目应带最近活动时间")
	}
	if stats[1].ID != p2.ID || stats[1].Sessions != 0 || stats[1].UpdatedAt != "" {
		t.Fatalf("无会话项目应为 0 会话、空活动: %+v", stats[1])
	}
}

// ProjectsByName：精确匹配；重名返回多条（调用方消歧，不猜）。
func TestProjectsByName(t *testing.T) {
	s := openAt(t, t.TempDir())
	a, err := s.AddProject("demo", filepath.Join(t.TempDir(), "a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddProject("demo", filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProject("other", filepath.Join(t.TempDir(), "c")); err != nil {
		t.Fatal(err)
	}
	hits, err := s.ProjectsByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("重名应返回两条: %+v", hits)
	}
	ids := map[string]bool{hits[0].ID: true, hits[1].ID: true}
	if !ids[a.ID] || !ids[b.ID] {
		t.Fatalf("重名条目应都在结果里: %+v", hits)
	}
	if hits, err := s.ProjectsByName("other"); err != nil || len(hits) != 1 {
		t.Fatalf("唯一名应返回一条: %+v err=%v", hits, err)
	}
	if hits, err := s.ProjectsByName("不存在"); err != nil || len(hits) != 0 {
		t.Fatalf("不存在的名字应返回空列表（不是错误）: %+v err=%v", hits, err)
	}
}
