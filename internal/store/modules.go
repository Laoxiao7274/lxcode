// 上下文模块目录的 CRUD（技能/流程/模板——纯内容条目）。
package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func (s *Store) ListModules() ([]sessiondata.ModuleSpec, error) {
	rows, err := s.db.Query(`SELECT id, desc, kind, body, custom FROM modules ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("列出模块失败: %w", err)
	}
	defer rows.Close()
	var out []sessiondata.ModuleSpec
	for rows.Next() {
		var m sessiondata.ModuleSpec
		var custom int
		if err := rows.Scan(&m.ID, &m.Desc, &m.Kind, &m.Body, &custom); err != nil {
			return nil, fmt.Errorf("读模块行失败: %w", err)
		}
		m.Custom = custom == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddModule(m sessiondata.ModuleSpec) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("模块 id 不能为空")
	}
	if strings.TrimSpace(m.Desc) == "" {
		return fmt.Errorf("模块摘要不能为空")
	}
	if m.Kind != "process" && m.Kind != "skill" {
		return fmt.Errorf("kind 必须是 process（模板）或 skill（技能）")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM modules WHERE id = ?`, m.ID).Scan(&n); err != nil {
		return fmt.Errorf("查重失败: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("模块 id 已存在: %s", m.ID)
	}
	return insertModule(s.db, m, nowNano())
}

func (s *Store) UpdateModule(m sessiondata.ModuleSpec) error {
	if strings.TrimSpace(m.Desc) == "" {
		return fmt.Errorf("模块摘要不能为空")
	}
	custom := 0
	if m.Custom {
		custom = 1
	}
	res, err := s.db.Exec(`UPDATE modules SET desc=?, kind=?, body=?, custom=?, updated_at=? WHERE id=?`,
		m.Desc, m.Kind, m.Body, custom, nowNano(), m.ID)
	if err != nil {
		return fmt.Errorf("更新模块失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("模块 %s 不存在", m.ID)
	}
	return nil
}

func (s *Store) RemoveModule(id string) error {
	var custom int
	err := s.db.QueryRow(`SELECT custom FROM modules WHERE id = ?`, id).Scan(&custom)
	if err == sql.ErrNoRows {
		return fmt.Errorf("模块 %s 不存在", id)
	}
	if err != nil {
		return fmt.Errorf("查询模块失败: %w", err)
	}
	if custom == 0 {
		return fmt.Errorf("内置模块不可删除（只读条目）")
	}
	// 引用校验：workflow（单值 LIKE）+ skills（数组 LIKE 粗筛精筛）
	var refs []string
	rows, err := s.db.Query(`SELECT id, workflow, skills FROM agents WHERE workflow = ? OR skills LIKE ?`,
		id, "%"+`"`+id+`"`+"%")
	if err != nil {
		return fmt.Errorf("查引用失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var aid, workflow, skills string
		if err := rows.Scan(&aid, &workflow, &skills); err != nil {
			return fmt.Errorf("读引用行失败: %w", err)
		}
		if workflow == id {
			refs = append(refs, aid)
			continue
		}
		for _, x := range decodeStrList(skills) {
			if x == id {
				refs = append(refs, aid)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(refs) > 0 {
		return fmt.Errorf("模块 %s 被以下 Agent 引用（workflow/skills），先解除引用: %s", id, strings.Join(refs, "、"))
	}
	res, err := s.db.Exec(`DELETE FROM modules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除模块失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("模块 %s 不存在", id)
	}
	return nil
}
