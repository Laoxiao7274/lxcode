// 工具目录的 CRUD 与校验（id 字符集硬校验在写边界——
// 违反网关约束会被 400 拒收整轮，见 AGENTS.md §5 坑 13）。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

func (s *Store) ListTools() ([]sessiondata.ToolSpec, error) {
	rows, err := s.db.Query(`SELECT id, desc, risk, source, params, doc, server, command, example, package_file, custom FROM tools ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("列出工具失败: %w", err)
	}
	defer rows.Close()
	var out []sessiondata.ToolSpec
	for rows.Next() {
		var t sessiondata.ToolSpec
		var params string
		var custom int
		if err := rows.Scan(&t.ID, &t.Desc, &t.Risk, &t.Source, &params, &t.Doc, &t.Server, &t.Command, &t.Example, &t.PackageFile, &custom); err != nil {
			return nil, fmt.Errorf("读工具行失败: %w", err)
		}
		t.Custom = custom == 1
		if params != "" && params != "[]" {
			if err := json.Unmarshal([]byte(params), &t.Params); err != nil {
				return nil, fmt.Errorf("解析工具参数失败: %w", err)
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) AddTool(t sessiondata.ToolSpec) error {
	if err := validateTool(t); err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tools WHERE id = ?`, t.ID).Scan(&n); err != nil {
		return fmt.Errorf("查重失败: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("工具 id 已存在: %s", t.ID)
	}
	return insertTool(s.db, t, nowNano())
}

func (s *Store) UpdateTool(t sessiondata.ToolSpec) error {
	if err := validateTool(t); err != nil {
		return err
	}
	params, err := json.Marshal(t.Params)
	if err != nil {
		return fmt.Errorf("序列化工具参数失败: %w", err)
	}
	custom := 0
	if t.Custom {
		custom = 1
	}
	res, err := s.db.Exec(`UPDATE tools SET desc=?, risk=?, source=?, params=?, doc=?, server=?, command=?, example=?, package_file=?, custom=?, updated_at=? WHERE id=?`,
		t.Desc, t.Risk, t.Source, string(params), t.Doc, t.Server, t.Command, t.Example, t.PackageFile, custom, nowNano(), t.ID)
	if err != nil {
		return fmt.Errorf("更新工具失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("工具 %s 不存在", t.ID)
	}
	return nil
}

func (s *Store) RemoveTool(id string) error {
	var custom int
	var source string
	err := s.db.QueryRow(`SELECT custom, source FROM tools WHERE id = ?`, id).Scan(&custom, &source)
	if err == sql.ErrNoRows {
		return fmt.Errorf("工具 %s 不存在", id)
	}
	if err != nil {
		return fmt.Errorf("查询工具失败: %w", err)
	}
	if custom == 0 && source != "mcp" {
		return fmt.Errorf("内置工具不可删除（只读条目）")
	}
	rows, err := s.db.Query(`SELECT id, tools FROM agents WHERE tools LIKE ?`, "%"+`"`+id+`"`+"%")
	if err != nil {
		return fmt.Errorf("查引用失败: %w", err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var aid, tools string
		if err := rows.Scan(&aid, &tools); err != nil {
			return fmt.Errorf("读引用行失败: %w", err)
		}
		for _, x := range decodeStrList(tools) {
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
		return fmt.Errorf("工具 %s 被以下 Agent 的白名单引用，先解除引用: %s", id, strings.Join(refs, "、"))
	}
	res, err := s.db.Exec(`DELETE FROM tools WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除工具失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("工具 %s 不存在", id)
	}
	return nil
}

func validateTool(t sessiondata.ToolSpec) error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("工具 id 不能为空")
	}
	// 工具 id 的字符集是**硬约束**，不是风格问题：OpenAI 与 Anthropic 都
	// 要求 ^[a-zA-Z0-9_-]{1,64}$，违反它会被严格网关 400 拒收**整轮**请求
	// （2026-09-23 线上事故：agent.dispatch 的点号让每个带工具的主 Agent
	// 轮次全部失败，见 AGENTS.md §5 坑 13）。拦在写边界：用户导入的目录
	// 条目与 MCP 物化出来的条目都走这里，坏 id 进不了库就传不到模型面前。
	if !validToolID(t.ID) {
		return fmt.Errorf("工具 id 只能含字母/数字/下划线/连字符且不超过 64 字符: %s", t.ID)
	}
	if strings.TrimSpace(t.Desc) == "" {
		return fmt.Errorf("工具说明不能为空")
	}
	if t.Risk != "low" && t.Risk != "high" {
		return fmt.Errorf("risk 必须是 low/high 之一")
	}
	if t.Source != "builtin" && t.Source != "binary" && t.Source != "mcp" {
		return fmt.Errorf("source 必须是 builtin/binary/mcp 之一")
	}
	return nil
}

func validToolID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}
