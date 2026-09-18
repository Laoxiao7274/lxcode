// agent 注册表与拓展目录的持久化（M1——路线图 docs/backend-roadmap.md）。
// 四张表与 sessions/messages/projects 同库；JSON 列存数组/映射（工具白名单、
// 参数表等），引用完整性由应用层校验（删被引用条目拒绝并列出引用方——
// 与 MCP 导入的查重语义一致）。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// agentSchema 是四张目录表（幂等建表；与 sessions 同库同 DSN）。
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

// initAgents 建表 + 首次种子（库空时；幂等——有数据不覆盖）。
// Open 调用（store.go 的 schema 之外追加，避免混在一起）。
func (s *Store) initAgents() error {
	if _, err := s.db.Exec(agentSchema); err != nil {
		return fmt.Errorf("初始化目录表失败: %w", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agents`).Scan(&n); err != nil {
		return fmt.Errorf("查 agents 行数失败: %w", err)
	}
	if n > 0 {
		return nil // 已有数据（含旧种子）——不覆盖用户改动
	}
	// 种子（内容对齐前端 agent-seeds.ts 的目录部分；演示 Agent 名单
	// 只给主 Agent + 一个子 Agent——真实后端不该替用户预置一堆）
	now := nowNano()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开种子事务失败: %w", err)
	}
	defer tx.Rollback()
	for _, t := range seedTools {
		if err := insertTool(tx, t, now); err != nil {
			return err
		}
	}
	for _, m := range seedModules {
		if err := insertModule(tx, m, now); err != nil {
			return err
		}
	}
	for _, a := range seedAgents {
		if err := insertAgent(tx, a, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertAgent(exec execer, a sessiondata.AgentDef, now string) error {
	tools, _ := json.Marshal(a.Tools)
	skills, _ := json.Marshal(a.Skills)
	delegates, _ := json.Marshal(a.Delegates)
	custom := 1
	if a.IsMain {
		custom = 0 // 主 Agent 是结构性的（不可删不可自建第二个）
	}
	enabled, isMain := 0, 0
	if a.Enabled {
		enabled = 1
	}
	if a.IsMain {
		isMain = 1
	}
	_, err := exec.Exec(`INSERT INTO agents
		(id, name, desc, color, model, tools, workflow, skills, delegates, approval, enabled, is_main, prompt, protocol, custom, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.Desc, a.Color, a.Model, string(tools), a.Workflow, string(skills), string(delegates), a.Approval, enabled, isMain, a.Prompt, a.Protocol, custom, now, now)
	if err != nil {
		return fmt.Errorf("写入 Agent %s 失败: %w", a.ID, err)
	}
	return nil
}

func insertModule(exec execer, m sessiondata.ModuleSpec, now string) error {
	custom := 1
	if !m.Custom {
		custom = 0
	}
	_, err := exec.Exec(`INSERT INTO modules (id, desc, kind, body, custom, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
		m.ID, m.Desc, m.Kind, m.Body, custom, now, now)
	if err != nil {
		return fmt.Errorf("写入模块 %s 失败: %w", m.ID, err)
	}
	return nil
}

func insertTool(exec execer, t sessiondata.ToolSpec, now string) error {
	params, err := json.Marshal(t.Params)
	if err != nil {
		return fmt.Errorf("序列化工具参数失败: %w", err)
	}
	custom := 1
	if !t.Custom {
		custom = 0
	}
	_, err = exec.Exec(`INSERT INTO tools
		(id, desc, risk, source, params, doc, server, command, example, package_file, custom, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Desc, t.Risk, t.Source, string(params), t.Doc, t.Server, t.Command, t.Example, t.PackageFile, custom, now, now)
	if err != nil {
		return fmt.Errorf("写入工具 %s 失败: %w", t.ID, err)
	}
	return nil
}

// execer 抽象 tx 与 db（增删改共用）。
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// ---- agents ----

// ListAgents 列出全部 Agent（主 Agent 恒排首位，其余按创建序）。
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

// AddAgent 注册 Agent（id 冲突拒绝；校验前置——调用方也可以先 ValidateAgent）。
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

// UpdateAgent 全量替换。is_main/custom 不可变更（结构性字段——
// 主 Agent 身份由种子初始化，不允许编辑翻转）。
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

// RemoveAgent 删除 Agent。主 Agent 拒绝；被其他 Agent 的 delegates
// 引用时拒绝并列出引用方（用户先解除引用）。
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

// validateAgent 是写路径的前置校验（错误自解释——与前端表单对齐）。
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

// ---- modules ----

// ListModules 列出全部模块（按创建序）。
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

// AddModule 新建模块（id 冲突拒绝）。
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

// UpdateModule 全量替换。
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

// RemoveModule 删除模块（内置 custom=0 只读；被 Agent 的 workflow/skills
// 引用时拒绝并列出引用方）。
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

// ---- tools ----

// ListTools 列出全部工具（内置与第三方/自定义统一）。
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

// UpsertTool 新增或更新工具（导入/表单创建共用——前端工具目录的 add 与
// update 在后端收敛为按 id 的 upsert 语义？不——保持显式两条路径，
// 与 Agent/Module 一致）。
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

// RemoveTool 删除工具（内置 custom=0 只读；被 Agent 的 tools 引用时
// 拒绝并列出引用方）。
func (s *Store) RemoveTool(id string) error {
	var custom int
	err := s.db.QueryRow(`SELECT custom FROM tools WHERE id = ?`, id).Scan(&custom)
	if err == sql.ErrNoRows {
		return fmt.Errorf("工具 %s 不存在", id)
	}
	if err != nil {
		return fmt.Errorf("查询工具失败: %w", err)
	}
	if custom == 0 {
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

// ---- mcp_servers ----

// ListMcServers 列出全部 MCP 服务器。
func (s *Store) ListMcServers() ([]sessiondata.McServerSpec, error) {
	rows, err := s.db.Query(`SELECT id, desc, transport, command, args, env, url, enabled, custom FROM mcp_servers ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("列出 MCP 服务器失败: %w", err)
	}
	defer rows.Close()
	var out []sessiondata.McServerSpec
	for rows.Next() {
		var m sessiondata.McServerSpec
		var args, env string
		var enabled, custom int
		if err := rows.Scan(&m.ID, &m.Desc, &m.Transport, &m.Command, &args, &env, &m.URL, &enabled, &custom); err != nil {
			return nil, fmt.Errorf("读 MCP 服务器行失败: %w", err)
		}
		m.Enabled = enabled == 1
		m.Custom = custom == 1
		m.Args = decodeStrList(args)
		if env != "" && env != "{}" {
			if err := json.Unmarshal([]byte(env), &m.Env); err != nil {
				return nil, fmt.Errorf("解析 MCP 环境变量失败: %w", err)
			}
		}
		if m.Env == nil {
			m.Env = map[string]string{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMcServer 注册服务器（id 冲突拒绝）。
func (s *Store) AddMcServer(m sessiondata.McServerSpec) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("服务器名不能为空")
	}
	if m.Transport != "stdio" && m.Transport != "sse" {
		return fmt.Errorf("transport 必须是 stdio/sse 之一")
	}
	if m.Transport == "stdio" && strings.TrimSpace(m.Command) == "" {
		return fmt.Errorf("stdio 传输需要 command（可执行文件）")
	}
	if m.Transport == "sse" && strings.TrimSpace(m.URL) == "" {
		return fmt.Errorf("sse 传输需要端点 URL")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM mcp_servers WHERE id = ?`, m.ID).Scan(&n); err != nil {
		return fmt.Errorf("查重失败: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("服务器名已存在: %s", m.ID)
	}
	args, _ := json.Marshal(m.Args)
	env, err := json.Marshal(m.Env)
	if err != nil {
		return fmt.Errorf("序列化环境变量失败: %w", err)
	}
	enabled, custom := 0, 1
	if m.Enabled {
		enabled = 1
	}
	if !m.Custom {
		custom = 0
	}
	_, err = s.db.Exec(`INSERT INTO mcp_servers (id, desc, transport, command, args, env, url, enabled, custom, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.Desc, m.Transport, m.Command, string(args), string(env), m.URL, enabled, custom, nowNano(), nowNano())
	if err != nil {
		return fmt.Errorf("写入 MCP 服务器失败: %w", err)
	}
	return nil
}

// UpdateMcServer 全量替换。
func (s *Store) UpdateMcServer(m sessiondata.McServerSpec) error {
	args, _ := json.Marshal(m.Args)
	env, err := json.Marshal(m.Env)
	if err != nil {
		return fmt.Errorf("序列化环境变量失败: %w", err)
	}
	enabled, custom := 0, 1
	if m.Enabled {
		enabled = 1
	}
	if !m.Custom {
		custom = 0
	}
	res, err := s.db.Exec(`UPDATE mcp_servers SET desc=?, transport=?, command=?, args=?, env=?, url=?, enabled=?, custom=?, updated_at=? WHERE id=?`,
		m.Desc, m.Transport, m.Command, string(args), string(env), m.URL, enabled, custom, nowNano(), m.ID)
	if err != nil {
		return fmt.Errorf("更新 MCP 服务器失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("MCP 服务器 %s 不存在", m.ID)
	}
	return nil
}

// RemoveMcServer 删除服务器（工具表里 server 指回它的条目一并删——
// 服务器是接入单元，它的工具随它生灭；被 Agent 白名单引用的工具会先
// 拒绝（上面的 RemoveTool 校验），所以这里直接级联删工具是安全的）。
func (s *Store) RemoveMcServer(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback()
	// 先删该服务器的工具（若被引用，删除前显式校验——把引用错误报给用户）
	rows, err := tx.Query(`SELECT id FROM tools WHERE server = ?`, id)
	if err != nil {
		return fmt.Errorf("查服务器工具失败: %w", err)
	}
	var toolIDs []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			rows.Close()
			return fmt.Errorf("读工具 id 失败: %w", err)
		}
		toolIDs = append(toolIDs, tid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// 逐个走 RemoveTool 的引用校验（事务外——校验只读，事务里删）
	for _, tid := range toolIDs {
		if err := s.checkToolRefs(tid); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM tools WHERE server = ?`, id); err != nil {
		return fmt.Errorf("级联删工具失败: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM mcp_servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除 MCP 服务器失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("MCP 服务器 %s 不存在", id)
	}
	return tx.Commit()
}

// checkToolRefs 是 RemoveTool 的引用校验（供级联删除复用）。
func (s *Store) checkToolRefs(id string) error {
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
	return nil
}
