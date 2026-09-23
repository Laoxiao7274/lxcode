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

// initAgents 建表 + 种子：库空时整套注入；库非空时**种子同步**（幂等，见
// syncCatalogSeeds——代码修好了种子，老库必须也能吃到）。Open 调用。
func (s *Store) initAgents() error {
	if _, err := s.db.Exec(agentSchema); err != nil {
		return fmt.Errorf("初始化目录表失败: %w", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agents`).Scan(&n); err != nil {
		return fmt.Errorf("查 agents 行数失败: %w", err)
	}
	now := nowNano()
	if n > 0 {
		// 老库：先把改过名的工具 id 迁过来（幂等，见 migration.go），再同步目录种子
		//（不碰 Agent 名单——主 Agent 的委派名单等字段用户可改，覆盖就是吃掉用户的配置）
		if err := s.migrateDispatchToolID(); err != nil {
			return err
		}
		return s.syncCatalogSeeds(now)
	}
	// 首次：整套种子（内容对齐前端 agent-seeds.ts 的目录部分；演示 Agent 名单
	// 只给主 Agent + 一个子 Agent——真实后端不该替用户预置一堆）
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

// syncCatalogSeeds 把种子目录同步进已有库（每次 Open 跑一次，幂等）。
//
// 为什么必须做：种子只在库空时注入，于是**代码修好了种子，老库永远吃不到**。
// 用户报告过「给了 ripgrep，模型答『注册表没有』」正是这一类：种子里那条
// command 为空的承诺，代码补上真实命令后老库仍是空的——而种子行 custom=0，
// 目录页原先又不给编辑入口，用户连手动补都做不到。
//
// 边界（只动代码拥有的行）：
//   - custom=0 的条目：按代码更新（command/params/desc/doc 等全字段）；
//   - custom=1 的条目：用户自建或改过的（保存时会置 1）——一律不碰；
//   - 不删除：种子删掉的条目留在库里（可能已被白名单引用），不制造悬空引用。
//
// Agent 名单是同一件事的另一半，但边界更严（见两个辅助函数的注释）：
// ensureSeedAgents 只插缺失的种子 Agent（已有行一律不碰），
// topUpMainDelegates 只在主 Agent 的委派名单没被用户动过时才补新增的子 Agent。
func (s *Store) syncCatalogSeeds(now string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开种子同步事务失败: %w", err)
	}
	defer tx.Rollback()
	for _, t := range seedTools {
		if err := upsertSeedTool(tx, t, now); err != nil {
			return err
		}
	}
	for _, m := range seedModules {
		if err := upsertSeedModule(tx, m, now); err != nil {
			return err
		}
	}
	if err := ensureSeedAgents(tx, now); err != nil {
		return err
	}
	if err := topUpMainDelegates(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

// seedDelegatesBaseline 是**上一版**种子主 Agent 的委派名单快照：只用来判断
// 用户的名单是否被动过（新增种子子 Agent 时，把上一版的值留在这里）。
//
// 为什么需要它：主 Agent 的 delegates 是用户可改的字段，库里没有"改没改过"的
// 标记（UpdateAgent 刻意不碰 custom，见其注释），所以只能拿上一版种子值当基线：
// 名单仍包含基线的全部 id → 没被动过；少了一个 → 用户删过 → 一律不碰。
var seedDelegatesBaseline = []string{"coder"}

// ensureSeedAgents 把种子里**新增**的 Agent 补进已有库：只插入缺失的 id，
// 已有行一律不 UPDATE、绝不 DELETE。
//
// 为什么需要：Agent 种子原先只在库空时整套注入，于是「代码加了新 Agent，
// 老库永远吃不到」——与工具/模块同一类问题（见上面的注释）。
// 为什么不能像工具/模块那样按 custom=0 全字段更新：子 Agent 种子插入时是
// custom=1（insertAgent——用户可在 Agents 页自由改它，真实库里就有被用户加过
// 工具的 coder 行），主 Agent 的 delegates 更是用户配置——更新就是吃掉用户的
// 编辑。所以这里只做"缺失才插入"。
//
// 已知代价：用户若**删掉**过某个种子 Agent，下次 Open 会把它插回来（工具/模块
// 的种子同步本来就有同一性质——种子是代码拥有的目录条目，不想要应该停用而不是
// 删除）。
func ensureSeedAgents(exec execer, now string) error {
	for _, a := range seedAgents {
		var n int
		if err := exec.QueryRow(`SELECT COUNT(*) FROM agents WHERE id = ?`, a.ID).Scan(&n); err != nil {
			return fmt.Errorf("查种子 Agent %s 失败: %w", a.ID, err)
		}
		if n > 0 {
			continue // 已有行：用户可能改过（名字/工具/提示词/权限），一律不碰
		}
		if err := insertAgent(exec, a, now); err != nil {
			return err
		}
	}
	return nil
}

// topUpMainDelegates 给主 Agent 的委派名单补上本版**新增**的种子子 Agent。
//
// 为什么需要：只插行不改名单的话，主 Agent 的提示词里没有它们（compose 的
// 可委派名单来自这里），agent_dispatch 的名单校验也会拒绝（server 的有效名单
// = 默认 ∩ 启用）——「加了却派不出去」。
// 为什么不是无条件追加：用户删过某个种子子 Agent 时，无条件追加会把它反复塞
// 回去（每次 Open 都塞）——那就是吃掉用户的编辑。所以只在名单仍含上一版种子的
// 全部子 Agent（= 没被动过）时才补；用户动过就完全不碰。
func topUpMainDelegates(exec execer, now string) error {
	var delegates string
	err := exec.QueryRow(`SELECT delegates FROM agents WHERE is_main = 1`).Scan(&delegates)
	if err == sql.ErrNoRows {
		return nil // 没有主 Agent（半截库）：不制造结构，交给正常写路径
	}
	if err != nil {
		return fmt.Errorf("查主 Agent 委派名单失败: %w", err)
	}
	current := decodeStrList(delegates)
	if !containsAll(current, seedDelegatesBaseline) {
		return nil // 用户动过这份名单（删过种子子 Agent）：一律不碰
	}
	added := false
	for _, a := range seedAgents {
		if a.IsMain || containsStr(current, a.ID) {
			continue
		}
		current = append(current, a.ID)
		added = true
	}
	if !added {
		return nil // 已是最新：幂等，不写库（也不动 updated_at）
	}
	next, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("序列化委派名单失败: %w", err)
	}
	if _, err := exec.Exec(`UPDATE agents SET delegates=?, updated_at=? WHERE is_main = 1`, string(next), now); err != nil {
		return fmt.Errorf("补主 Agent 委派名单失败: %w", err)
	}
	return nil
}

// containsStr 判断列表里有没有 s。
func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// containsAll 判断 list 是否包含 want 的全部元素（want 为空恒真）。
func containsAll(list, want []string) bool {
	for _, w := range want {
		if !containsStr(list, w) {
			return false
		}
	}
	return true
}

// upsertSeedTool 按 id 同步一个种子工具：缺失则插入，custom=0 则按代码更新。
func upsertSeedTool(exec execer, t sessiondata.ToolSpec, now string) error {
	var custom int
	err := exec.QueryRow(`SELECT custom FROM tools WHERE id = ?`, t.ID).Scan(&custom)
	if err == sql.ErrNoRows {
		return insertTool(exec, t, now)
	}
	if err != nil {
		return fmt.Errorf("查种子工具 %s 失败: %w", t.ID, err)
	}
	if custom != 0 {
		return nil // 用户自建或改过的行：不碰
	}
	params, err := json.Marshal(t.Params)
	if err != nil {
		return fmt.Errorf("序列化工具参数失败: %w", err)
	}
	if _, err := exec.Exec(`UPDATE tools SET desc=?, risk=?, source=?, params=?, doc=?, server=?, command=?, example=?, package_file=?, updated_at=? WHERE id=?`,
		t.Desc, t.Risk, t.Source, string(params), t.Doc, t.Server, t.Command, t.Example, t.PackageFile, now, t.ID); err != nil {
		return fmt.Errorf("同步种子工具 %s 失败: %w", t.ID, err)
	}
	return nil
}

// upsertSeedModule 按 id 同步一个种子模块（语义同 upsertSeedTool）。
func upsertSeedModule(exec execer, m sessiondata.ModuleSpec, now string) error {
	var custom int
	err := exec.QueryRow(`SELECT custom FROM modules WHERE id = ?`, m.ID).Scan(&custom)
	if err == sql.ErrNoRows {
		return insertModule(exec, m, now)
	}
	if err != nil {
		return fmt.Errorf("查种子模块 %s 失败: %w", m.ID, err)
	}
	if custom != 0 {
		return nil
	}
	if _, err := exec.Exec(`UPDATE modules SET desc=?, kind=?, body=?, updated_at=? WHERE id=?`,
		m.Desc, m.Kind, m.Body, now, m.ID); err != nil {
		return fmt.Errorf("同步种子模块 %s 失败: %w", m.ID, err)
	}
	return nil
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

// execer 抽象 tx 与 db（增删改查共用）。
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
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
