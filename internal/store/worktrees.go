package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
)

// WorktreeMeta 是顶层项目会话对应的独立 Git 工作树元数据。
type WorktreeMeta struct {
	Path       string
	Branch     string
	BaseCommit string
}

// WorktreeRoot 返回会话数据目录下的工作树根目录（绝不写入项目主工作树）。
func (s *Store) WorktreeRoot() string { return filepath.Join(s.dir, "worktrees") }

// WorktreeOf 返回会话已绑定的工作树；尚未创建时返回零值。
func (s *Store) WorktreeOf(sessionID string) (WorktreeMeta, error) {
	var meta WorktreeMeta
	err := s.db.QueryRow(
		`SELECT worktree_path, worktree_branch, worktree_base FROM sessions WHERE id = ?`, sessionID,
	).Scan(&meta.Path, &meta.Branch, &meta.BaseCommit)
	if err == sql.ErrNoRows {
		return WorktreeMeta{}, fmt.Errorf("会话 %s 不存在", sessionID)
	}
	if err != nil {
		return WorktreeMeta{}, fmt.Errorf("读取会话 worktree 元数据失败: %w", err)
	}
	return meta, nil
}

// SetWorktree 只允许首次绑定或写入完全相同的元数据，避免并发初始化互相改绑。
func (s *Store) SetWorktree(sessionID string, meta WorktreeMeta) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开始 worktree 元数据事务失败: %w", err)
	}
	defer tx.Rollback()
	var old WorktreeMeta
	if err := tx.QueryRow(
		`SELECT worktree_path, worktree_branch, worktree_base FROM sessions WHERE id = ?`, sessionID,
	).Scan(&old.Path, &old.Branch, &old.BaseCommit); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("会话 %s 不存在", sessionID)
		}
		return fmt.Errorf("读取 worktree 元数据失败: %w", err)
	}
	if old.Path != "" && old != meta {
		return fmt.Errorf("会话 %s 已绑定另一 worktree，拒绝覆盖", sessionID)
	}
	if _, err := tx.Exec(
		`UPDATE sessions SET worktree_path = ?, worktree_branch = ?, worktree_base = ? WHERE id = ?`,
		meta.Path, meta.Branch, meta.BaseCommit, sessionID,
	); err != nil {
		return fmt.Errorf("保存 worktree 元数据失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交 worktree 元数据失败: %w", err)
	}
	return nil
}
