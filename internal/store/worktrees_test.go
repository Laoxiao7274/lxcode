package store

import (
	"path/filepath"
	"testing"
)

func TestWorktreeMetadataSurvivesReopenAndRejectsRebind(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.Create()
	if err != nil {
		t.Fatal(err)
	}
	want := WorktreeMeta{
		Path:       filepath.Join(dir, "worktrees", "project", id),
		Branch:     "lxcode/session-" + id,
		BaseCommit: "0123456789abcdef",
	}
	if err := st.SetWorktree(id, want); err != nil {
		t.Fatal(err)
	}
	if got, err := st.WorktreeOf(id); err != nil || got != want {
		t.Fatalf("WorktreeOf = %+v, %v; want %+v", got, err, want)
	}
	if err := st.SetWorktree(id, WorktreeMeta{Path: want.Path, Branch: "other", BaseCommit: want.BaseCommit}); err == nil {
		t.Fatal("rebinding session to a different branch should fail")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if got, err := st.WorktreeOf(id); err != nil || got != want {
		t.Fatalf("metadata after reopen = %+v, %v; want %+v", got, err, want)
	}
}
