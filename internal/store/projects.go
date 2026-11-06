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

// RemoveProject 从项目列表移除注册（**只解绑**：不删磁盘目录、不删会话）。
// 该项目的会话保留在库里（历史/统计/子会话一条不动），**归属清空 → 落进
// 「未分组」**——侧栏只按「未分组 / 已注册项目」两个口径分桶，留着悬空的
// workspace id 会让这些会话在侧栏彻底消失（数据在、入口没了，同 leaf_len=0
// 那条注释的教训）。同一事务里删项目行 + 清会话归属，不留中间态。
//
// 项目不存在时报错（调用方先查再删，错误要能被人看懂）。
func (s *Store) RemoveProject(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("移除项目失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("移除项目失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("项目 %s 不存在", id)
	}
	// 会话保留，只把归属置空（未分组）——历史/统计/子会话一行不动
	if _, err := tx.Exec(`UPDATE sessions SET workspace = '' WHERE workspace = ?`, id); err != nil {
		return fmt.Errorf("清空项目会话归属失败: %w", err)
	}
	return tx.Commit()
}

// ProjectSessionCount 返回归属该项目的会话数（含子会话，含已归档——用于删除
// 前的边界判定与提示文案：数字必须与用户「这个项目里有多少会话」的认知一致）。
func (s *Store) ProjectSessionCount(id string) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace = ?`, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计项目会话数失败: %w", err)
	}
	return n, nil
}

// SessionIDsOfProject 列出归属该项目的全部会话 id（含子会话、含已归档）——
// 删除项目前的「有没有会话在跑」判定用它扫运行时忙闲。
func (s *Store) SessionIDsOfProject(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE workspace = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("列出项目会话失败: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, fmt.Errorf("读会话 id 失败: %w", err)
		}
		out = append(out, sid)
	}
	return out, rows.Err()
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

// ListProjectStats 列出全部项目的全景统计（id/name/path + 顶层会话数 +
// 最近活动），按最近活动倒序（活跃项目在前——助手的注入段与 project_list
// 都按这个顺序展示，用户最可能问的是最近在用的项目）。
//
// 会话数口径 = 顶层（parent_id=''）且非助手域（space != 'assistant'）：
//   - 排除子会话——它们是派发的产物，与侧栏「项目分组」的可见会话一致；
//   - 排除助手域——助手会话的 workspace 恒为空，这条过滤是防御性口径
//     （万一有历史行 workspace 带着旧项目 id，也不该算进项目的正经会话）。
// 无会话的项目照常返回（Sessions=0、UpdatedAt 空——助手说「这个项目还没有
// 会话」是有效回答，不把零会话项目从全景里藏掉）。
func (s *Store) ListProjectStats() ([]sessiondata.ProjectStats, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.name, p.path,
		       COUNT(s.id) AS sessions,
		       COALESCE(MAX(s.updated_at), '') AS last_activity
		 FROM projects p
		 LEFT JOIN sessions s
		   ON s.workspace = p.id AND s.parent_id = '' AND s.space != 'assistant'
		 GROUP BY p.id, p.name, p.path
		 ORDER BY MAX(s.updated_at) DESC, p.created_at DESC, p.rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("统计项目全景失败: %w", err)
	}
	defer rows.Close()
	var out []sessiondata.ProjectStats
	for rows.Next() {
		var ps sessiondata.ProjectStats
		if err := rows.Scan(&ps.ID, &ps.Name, &ps.Path, &ps.Sessions, &ps.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读项目统计行失败: %w", err)
		}
		if ps.UpdatedAt != "" {
			ps.UpdatedAt = fmtTime(ps.UpdatedAt)
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// ProjectsByName 按项目名**精确匹配**列出全部条目（run_task 的按名兜底）。
// 返回列表而不是单个：重名是合法状态（两个目录注册了同一个名字），此时
// 调用方必须让用户消歧，不能静默选一个——猜错的代价比问一句高得多。
func (s *Store) ProjectsByName(name string) ([]ProjectMeta, error) {
	rows, err := s.db.Query(`SELECT id, name, path FROM projects WHERE name = ? ORDER BY created_at ASC, rowid ASC`, name)
	if err != nil {
		return nil, fmt.Errorf("按名查询项目失败: %w", err)
	}
	defer rows.Close()
	var out []ProjectMeta
	for rows.Next() {
		var p ProjectMeta
		if err := rows.Scan(&p.ID, &p.Name, &p.Path); err != nil {
			return nil, fmt.Errorf("读项目行失败: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
