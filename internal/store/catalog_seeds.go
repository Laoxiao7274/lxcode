// 目录种子（工具/模块/Agent）的注入与增量同步。
// 为什么需要增量同步：种子只在库空时整套注入的话，代码修好了种子、老库永远吃不到
// （用户报告过「给了 ripgrep，模型答注册表没有」）。三条纪律：custom=0 按代码更新全字段、
// custom=1 一律不碰、不删除（避免动到白名单可能引用的行）。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

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
	// 子 Agent 的白名单与职责描述同理：白名单漏了没人勾得上，描述漏了主 Agent
	// 不知道派给谁（web_search 就栽过——工具与白名单都到位了，能力照样等于不存在）。
	if err := topUpSeedAgents(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

var seedDelegatesBaseline = []string{"coder"}

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
		if a.IsMain || slices.Contains(current, a.ID) {
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

// seedAgentBaseline 是**上一版**某个种子子 Agent 的白名单与职责描述快照：只用来
// 判断用户动没动过（新增种子内容时，把上一版的值留在这里）。
//
// 为什么需要基线：Agent 名单走的是「只插缺失、绝不 UPDATE 已有行」的同步
// （见 ensureSeedAgents），库里没有「改没改过」的标记——所以只能拿上一版
// 种子值当基线：仍等于基线 → 没被动过 → 可以补；不等 → 用户改过 → 一律不碰。
type seedAgentBaseline struct {
	Tools []string // 上一版工具白名单
	Desc  string   // 上一版职责描述（主 Agent 的选人信号）
}

var seedAgentBaselines = map[string]seedAgentBaseline{
	// 本版给调研 Agent 补了 web_fetch：白名单加一项，职责描述里点明「抓取网页正文」。
	// 基线随之上移到上一版（含 web_search、不含 web_fetch）——基线的语义就是
	// 「上一版长什么样」，留在更早的版本上会让「用户删掉 web_search」被误判成
	// 「没动过」，每次 Open 都把它塞回去。
	"researcher": {
		Tools: []string{"read_file", "search", "session_search", "ripgrep", "web_search"},
		Desc:  "代码库与资料勘察：全文检索、历史会话、联网搜索与跨文件脉络梳理，只给结论与出处。",
	},
}

// topUpSeedAgents 给种子子 Agent 补上本版**新增**的东西：工具白名单里缺的
// 工具、职责描述里本版新增的能力说明。
//
// 为什么需要（与 topUpMainDelegates 是同一件事的另一半）：白名单漏了，用户在
// Agent 编辑器里根本勾不上这个工具；职责描述漏了，主 Agent 的选人信号里就没有
// 这项能力——工具与白名单都到位了，能力照样等于不存在（web_search 栽过）。
// 为什么不是无条件补：白名单是用户可改的字段（真实库里就有用户动过的行），
// 无条件补会把用户删掉的工具每次 Open 都塞回去——那是吃掉用户的编辑。所以只在
// 该字段仍等于上一版基线时才补；用户动过就完全不碰。
// 为什么跳过主 Agent：它的工具是结构性的（只有 agent_dispatch），不参与补种。
func topUpSeedAgents(exec execer, now string) error {
	byID := map[string]sessiondata.AgentDef{}
	for _, a := range seedAgents {
		byID[a.ID] = a
	}
	for id, base := range seedAgentBaselines {
		seed, ok := byID[id]
		if !ok {
			continue // 种子里没有这个 Agent（基线留错了 id）：不动
		}
		if seed.IsMain {
			continue // 主 Agent 的工具是结构性的
		}
		var tools, desc string
		err := exec.QueryRow(`SELECT tools, desc FROM agents WHERE id = ?`, id).Scan(&tools, &desc)
		if err == sql.ErrNoRows {
			continue // 行还没插进来：ensureSeedAgents 会补行
		}
		if err != nil {
			return fmt.Errorf("查 Agent %s 失败: %w", id, err)
		}
		current := decodeStrList(tools)
		if containsAll(current, base.Tools) {
			// 没被动过：补上本版新增的工具（只加不减——基线里的一个都不能丢）
			added := current
			for _, t := range seed.Tools {
				if !slices.Contains(added, t) {
					added = append(added, t)
				}
			}
			if len(added) != len(current) {
				encoded, err := json.Marshal(added)
				if err != nil {
					return fmt.Errorf("序列化 Agent %s 工具白名单失败: %w", id, err)
				}
				if _, err := exec.Exec(`UPDATE agents SET tools=?, updated_at=? WHERE id = ?`, string(encoded), now, id); err != nil {
					return fmt.Errorf("补 Agent %s 工具白名单失败: %w", id, err)
				}
			}
		}
		// 描述同理：只在仍等于上一版基线时才补（用户改过就不碰）
		if base.Desc != "" && desc == base.Desc && seed.Desc != desc {
			if _, err := exec.Exec(`UPDATE agents SET desc=?, updated_at=? WHERE id = ?`, seed.Desc, now, id); err != nil {
				return fmt.Errorf("补 Agent %s 职责描述失败: %w", id, err)
			}
		}
	}
	return nil
}

func containsAll(list, want []string) bool {
	for _, w := range want {
		if !slices.Contains(list, w) {
			return false
		}
	}
	return true
}

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
