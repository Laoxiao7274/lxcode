// agent 注册表与拓展目录的持久化（M1——路线图 docs/backend-roadmap.md）。
// 四张表与 sessions/messages/projects 同库；JSON 列存数组/映射（工具白名单、
// 参数表等），引用完整性由应用层校验（删被引用条目拒绝并列出引用方——
// 与 MCP 导入的查重语义一致）。
//
// 本文件是 **Agent 名单** 这一域的 CRUD + 建表。种子的注入与增量同步在
// catalog_seeds.go，模块在 modules.go，工具在 tools.go，MCP 服务器在 mcpservers.go——
// 拆分的理由是同一天里改这四类条目的人几乎不重叠，合在一起读要跳 1000 行。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

const agentSchema = `
CREATE TABLE IF NOT EXISTS agents (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  desc       TEXT NOT NULL DEFAULT '',
  color      TEXT NOT NULL DEFAULT '#3b82f6',
  model      TEXT NOT NULL DEFAULT '',
  tools      TEXT NOT NULL DEFAULT '[]',
  workflow   TEXT NOT NULL DEFAULT '',
  skills     TEXT NOT NULL DEFAULT '[]',
  delegates  TEXT NOT NULL DEFAULT '[]',
  approval   TEXT NOT NULL DEFAULT 'confirm',
  enabled    INTEGER NOT NULL DEFAULT 1,
  is_main    INTEGER NOT NULL DEFAULT 0,
  prompt     TEXT NOT NULL DEFAULT '',
  protocol   TEXT NOT NULL DEFAULT '',
  custom     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS modules (
  id         TEXT PRIMARY KEY,
  desc       TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL CHECK (kind IN ('process','skill')),
  body       TEXT NOT NULL DEFAULT '',
  custom     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS tools (
  id           TEXT PRIMARY KEY,
  desc         TEXT NOT NULL DEFAULT '',
  risk         TEXT NOT NULL CHECK (risk IN ('low','high')),
  source       TEXT NOT NULL CHECK (source IN ('builtin','binary','mcp')),
  params       TEXT NOT NULL DEFAULT '[]',
  doc          TEXT NOT NULL DEFAULT '',
  server       TEXT NOT NULL DEFAULT '',
  command      TEXT NOT NULL DEFAULT '',
  example      TEXT NOT NULL DEFAULT '',
  package_file TEXT NOT NULL DEFAULT '',
  custom       INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mcp_servers (
  id         TEXT PRIMARY KEY,
  desc       TEXT NOT NULL DEFAULT '',
  transport  TEXT NOT NULL CHECK (transport IN ('stdio','sse')),
  command    TEXT NOT NULL DEFAULT '',
  args       TEXT NOT NULL DEFAULT '[]',
  env        TEXT NOT NULL DEFAULT '{}',
  url        TEXT NOT NULL DEFAULT '',
  enabled    INTEGER NOT NULL DEFAULT 1,
  custom     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Store) ListAgents() ([]sessiondata.AgentDef, error) {
	rows, err := s.db.Query(`SELECT id, name, desc, color, model, tools, workflow, skills, delegates, approval, enabled, is_main, prompt, protocol, custom
		FROM agents ORDER BY is_main DESC, rowid`)
	if err != nil {
		return nil, fmt.Errorf("列出 Agent 失败: %w", err)
	}
	defer rows.Close()
	var out []sessiondata.AgentDef
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAgent(rows *sql.Rows) (sessiondata.AgentDef, error) {
	var a sessiondata.AgentDef
	var tools, skills, delegates string
	var enabled, isMain, custom int
	if err := rows.Scan(&a.ID, &a.Name, &a.Desc, &a.Color, &a.Model, &tools, &a.Workflow, &skills, &delegates, &a.Approval, &enabled, &isMain, &a.Prompt, &a.Protocol, &custom); err != nil {
		return sessiondata.AgentDef{}, fmt.Errorf("读 Agent 行失败: %w", err)
	}
	a.Enabled = enabled == 1
	a.IsMain = isMain == 1
	a.Custom = custom == 1
	a.Tools = decodeStrList(tools)
	a.Skills = decodeStrList(skills)
	a.Delegates = decodeStrList(delegates)
	return a, nil
}

func decodeStrList(s string) []string {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []string{} // 坏数据防御：空列表比炸掉好
	}
	return out
}

func (s *Store) AddAgent(a sessiondata.AgentDef) error {
	if err := validateAgent(a); err != nil {
		return err
	}
	// id 唯一
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE id = ?`, a.ID).Scan(&n); err != nil {
		return fmt.Errorf("查重失败: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("Agent id 已存在: %s", a.ID)
	}
	// 主 Agent 唯一性：库里已有主就拒绝再建
	if a.IsMain {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE is_main = 1`).Scan(&n); err != nil {
			return fmt.Errorf("查主 Agent 失败: %w", err)
		}
		if n > 0 {
			return fmt.Errorf("主 Agent 已存在——不可自建第二个（两类制：主 = 结构性唯一）")
		}
	}
	return insertAgent(s.db, a, nowNano())
}

func (s *Store) UpdateAgent(a sessiondata.AgentDef) error {
	if err := validateAgent(a); err != nil {
		return err
	}
	tools, _ := json.Marshal(a.Tools)
	skills, _ := json.Marshal(a.Skills)
	delegates, _ := json.Marshal(a.Delegates)
	enabled := 0
	if a.Enabled {
		enabled = 1
	}
	res, err := s.db.Exec(`UPDATE agents SET name=?, desc=?, color=?, model=?, tools=?, workflow=?, skills=?, delegates=?, approval=?, enabled=?, prompt=?, protocol=?, updated_at=? WHERE id=?`,
		a.Name, a.Desc, a.Color, a.Model, string(tools), a.Workflow, string(skills), string(delegates), a.Approval, enabled, a.Prompt, a.Protocol, nowNano(), a.ID)
	if err != nil {
		return fmt.Errorf("更新 Agent 失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("Agent %s 不存在", a.ID)
	}
	return nil
}

func (s *Store) RemoveAgent(id string) error {
	var isMain int
	err := s.db.QueryRow(`SELECT is_main FROM agents WHERE id = ?`, id).Scan(&isMain)
	if err == sql.ErrNoRows {
		return fmt.Errorf("Agent %s 不存在", id)
	}
	if err != nil {
		return fmt.Errorf("查询 Agent 失败: %w", err)
	}
	if isMain == 1 {
		return fmt.Errorf("主 Agent 不可删除——它是唯一调度者（结构成员）")
	}
	// 引用校验：delegates 是 JSON 数组——用 LIKE 粗筛后应用层精筛
	//（目录规模小，全表扫可承受；LIKE 防漏掉转义形态）
	rows, err := s.db.Query(`SELECT id, delegates FROM agents WHERE delegates LIKE ?`, "%"+`"`+id+`"`+"%")
	if err != nil {
		return fmt.Errorf("查引用失败: %w", err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var aid, delegates string
		if err := rows.Scan(&aid, &delegates); err != nil {
			return fmt.Errorf("读引用行失败: %w", err)
		}
		for _, d := range decodeStrList(delegates) {
			if d == id {
				refs = append(refs, aid)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(refs) > 0 {
		return fmt.Errorf("Agent %s 被以下 Agent 的委派名单引用，先解除引用: %s", id, strings.Join(refs, "、"))
	}
	res, err := s.db.Exec(`DELETE FROM agents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除 Agent 失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("Agent %s 不存在", id)
	}
	return nil
}

func validateAgent(a sessiondata.AgentDef) error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("Agent id 不能为空")
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("Agent 名称不能为空")
	}
	switch a.Approval {
	case "", "auto", "confirm", "strict":
	default:
		return fmt.Errorf("approval 必须是 auto/confirm/strict 之一")
	}
	return nil
}
