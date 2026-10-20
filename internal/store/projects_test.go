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
