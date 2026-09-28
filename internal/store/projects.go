// 项目：增列与按 id 查询（项目根用于项目守则与 worktree 解析）。

package store

import (
	"database/sql"
	"fmt"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// ProjectMeta 是项目列表条目（侧栏「项目」分组的数据源）。
type ProjectMeta = sessiondata.ProjectMeta

// AddProject 注册项目（幂等：path 已注册返回既有条目）。
func (s *Store) AddProject(name, path string) (ProjectMeta, error) {
	// 已注册（按路径）→ 直接返回
	var existing ProjectMeta
	err := s.db.QueryRow(`SELECT id, name, path FROM projects WHERE path = ?`, path).
		Scan(&existing.ID, &existing.Name, &existing.Path)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return ProjectMeta{}, fmt.Errorf("查询项目失败: %w", err)
	}
	id := newSessionID() // 时间戳+随机，项目与会话共用 id 生成器（形态相同）
	_, err = s.db.Exec(`INSERT INTO projects (id, name, path, created_at) VALUES (?, ?, ?, ?)`,
		id, name, path, nowNano())
	if err != nil {
		return ProjectMeta{}, fmt.Errorf("写入项目失败: %w", err)
	}
	return ProjectMeta{ID: id, Name: name, Path: path}, nil
}

// ProjectByID 按项目 id 查项目——会话归属 → 项目根目录的解析源
// （agent 把项目根设为会话工作目录时调用）。found=false 表示项目不存在
// （不是错误，调用方据此回退默认目录）。
func (s *Store) ProjectByID(id string) (meta ProjectMeta, found bool, err error) {
	err = s.db.QueryRow(`SELECT id, name, path FROM projects WHERE id = ?`, id).
		Scan(&meta.ID, &meta.Name, &meta.Path)
	if err == sql.ErrNoRows {
		return ProjectMeta{}, false, nil
	}
	if err != nil {
		return ProjectMeta{}, false, fmt.Errorf("查询项目失败: %w", err)
	}
	return meta, true, nil
}
