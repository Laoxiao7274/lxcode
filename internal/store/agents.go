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
	// user_modified 置 1（2026-10）：这是**用户侧**的 Agent 编辑写路径（协议
	// agent.update ← 前端 AgentEditor/设置面板保存），置位后内置 Agent 定义的
	// hash 热更新永远跳过这行——用户的自定义不被种子升级覆盖。系统侧的种子化
	// 与 top-up 走 catalog_seeds.go 里的裸 SQL，不经过这里，不会误置位。
	res, err := s.db.Exec(`UPDATE agents SET name=?, desc=?, color=?, model=?, tools=?, workflow=?, skills=?, delegates=?, approval=?, enabled=?, prompt=?, protocol=?, user_modified=1, updated_at=? WHERE id=?`,
		a.Name, a.Desc, a.Color, a.Model, string(tools), a.Workflow, string(skills), string(delegates), a.Approval, enabled, a.Prompt, a.Protocol, nowNano(), a.ID)
	if err != nil {
		return fmt.Errorf("更新 Agent 失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("Agent %s 不存在", a.ID)
	}
	return nil
}

// BindAgentModel 只改一条 Agent 定义的 model 绑定（2026-12：子会话页切模型的
// 绑定更新路径——chat.model 带子会话 id 时服务端在落库 sessions.model 的同时
// 同步更新它归属 Agent 的绑定，语义从「改那一条子会话」升级为「给这个 Agent
// 换模型」——否则主 Agent 下次**新**派发（新子会话 id）不继承旧子会话的选择
//（CreateChild 不复制 model 列，解析优先级是会话选择 > Agent 绑定），
// 用户看到的正是「能选但不生效」）。
//
// 为什么不用 UpdateAgent/UpdateAgentFields：那是**全量/多字段**写路径，要求
// 调用方先读出整条定义再写回——切模型这个动作在两步之间会覆盖别的并发编辑，
// 而且把 user_modified 置位逻辑拖进来（模型绑定只是 Agent 定义的一列）。
// 这里是**单列窄写**：别的字段一个不碰，user_modified 置 1（用户侧编辑语义，
// 与 UpdateAgent 同一条纪律——种子热更新从此跳过这行）。
//
// 行不存在时报错（不静默）：子会话的 agent_id 指向被删的 Agent / ghost 行时，
// 调用方（服务端 chat.model）要把失败如实报回 UI。
func (s *Store) BindAgentModel(id, model string) error {
	res, err := s.db.Exec(`UPDATE agents SET model=?, user_modified=1, updated_at=? WHERE id=?`,
		model, nowNano(), id)
	if err != nil {
		return fmt.Errorf("更新 Agent 模型绑定失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("Agent %s 不存在", id)
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

// UpdateAgentFields 对一条 Agent 定义做**字段级**更新（agent_write 工具的
// 落库路径）：只动 u 里提供的字段，其余原样保留（含 enabled/is_main/custom）。
//
// 与前端编辑器（agent.update → UpdateAgent 全量写）共用同一套底层：校验走
// sessiondata.ValidateAgentFieldUpdate（硬校验：主 Agent 拒改、模板非空、
// 白名单不清空且名字真实存在、技能绑定存在）+ UpdateAgent 里的 validateAgent
// 基础校验，落库走同一条 UPDATE（user_modified=1——agent 代用户改的配置同样
// 受「种子热更新不覆盖用户改动」保护）。
//
// 「存在」的判定基准是**目录表**（tools / modules）而不是运行期注册表：
// 目录是编辑器 chip 与白名单的事实源，注册表里没有的条目（未配置 command 的
// binary 工具、未连接的 MCP 服务）在目录里合法存在——拿注册表当裁判会把
// 现存合法白名单判死。
func (s *Store) UpdateAgentFields(id string, u sessiondata.AgentFieldUpdate) error {
	defs, err := s.ListAgents()
	if err != nil {
		return err
	}
	var def *sessiondata.AgentDef
	for i := range defs {
		if defs[i].ID == id {
			def = &defs[i]
			break
		}
	}
	if def == nil {
		return fmt.Errorf("Agent %s 不存在", id)
	}
	toolExists := func(name string) bool {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM tools WHERE id = ?`, name).Scan(&n); err != nil || n == 0 {
			return false
		}
		return true
	}
	skillExists := func(mid string) bool {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM modules WHERE id = ?`, mid).Scan(&n); err != nil || n == 0 {
			return false
		}
		return true
	}
	if err := sessiondata.ValidateAgentFieldUpdate(*def, u, toolExists, skillExists); err != nil {
		return err
	}
	if u.Desc != nil {
		def.Desc = *u.Desc
	}
	if u.Prompt != nil {
		def.Prompt = *u.Prompt
	}
	if u.Tools != nil {
		def.Tools = u.Tools
	}
	if u.Skills != nil {
		def.Skills = u.Skills
	}
	return s.UpdateAgent(*def)
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
