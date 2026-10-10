package store

import (
	"database/sql"
	"fmt"
)

// LastUserTurn 返回会话的轮次序号与最近一条真实用户消息正文（供轮结束的检查点
// 提交组装提交信息）。
//
// 轮次 = 真实用户消息数（与 foldSessionStats 的 turns 同口径：注入的提示条 notice
// 与压缩检查点 checkpoint 都不算）。钩子在**本轮用户消息已落库之后**触发，所以这个
// 计数就是从 1 开始的轮次序号——复用同一口径，不新增表列、也不另设自增计数器。
//
// 只读两条聚合查询（COUNT + 最后一条），不把整段历史读进内存。
func (s *Store) LastUserTurn(id string) (turn int, content string, err error) {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", fmt.Errorf("会话 %s 不存在", id)
		}
		return 0, "", fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	const cond = `session_id = ? AND role = 'user' AND checkpoint = 0 AND notice = 0`
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE `+cond, id).Scan(&turn); err != nil {
		return 0, "", fmt.Errorf("统计会话 %s 轮次失败: %w", id, err)
	}
	if err := s.db.QueryRow(
		`SELECT content FROM messages WHERE `+cond+` ORDER BY seq DESC LIMIT 1`, id,
	).Scan(&content); err != nil && err != sql.ErrNoRows {
		return 0, "", fmt.Errorf("读取会话 %s 最近用户消息失败: %w", id, err)
	}
	return turn, content, nil
}
