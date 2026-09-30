// 会话行的生命周期：创建（含子会话）、列举、重命名、归档、工作区绑定。
// 子会话不进侧栏（List 只认 parent_id = 空串），归档级联见 Archive。

package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// Create 开一个新会话（INSERT sessions 行），返回会话 id。
// 文件版返回追加句柄的概念已消失——写操作按 session id 走库。
func (s *Store) Create() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := newSessionID()
	now := nowNano()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, created_at, updated_at, title, archived) VALUES (?, ?, ?, '', 0)`,
		id, now, now)
	if err != nil {
		return "", fmt.Errorf("创建会话失败: %w", err)
	}
	return id, nil
}

// CreateWithID 按指定 id 建会话（带初始标题）——演示数据注入用。
// id 冲突报错（幂等由调用方保证——重复注入前先清库）。
func (s *Store) CreateWithID(id, title string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := nowNano()
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, created_at, updated_at, title, archived) VALUES (?, ?, ?, ?, 0)`,
		id, now, now, title)
	if err != nil {
		return "", fmt.Errorf("创建会话 %s 失败: %w", id, err)
	}
	return id, nil
}

// CreateChild 开一个子会话（派发给子 Agent 的任务 = 一个独立会话）：
// parent_id 指回派发方，agent_id 记住它是哪个 Agent（续跑时按同一套四层组合
// 组装），dispatch_id 是哪次调度开的（对账用）；**workspace 直接继承父会话的行**
// （子会话的工具相对路径与 bash 默认目录必须与父一致——在 SQL 里 SELECT 过来，
// 免得 agent 层还要记住 workspace id）。
func (s *Store) CreateChild(parentID, agentID, dispatchID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := newSessionID()
	now := nowNano()
	res, err := s.db.Exec(
		`INSERT INTO sessions (id, created_at, updated_at, title, archived, parent_id, agent_id, dispatch_id, workspace)
		 SELECT ?, ?, ?, '', 0, id, ?, ?, workspace FROM sessions WHERE id = ?`,
		id, now, now, agentID, dispatchID, parentID)
	if err != nil {
		return "", fmt.Errorf("创建子会话失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// SELECT 没选到行 = 父会话不存在（插入 0 行）——不能默默开一个孤儿
		return "", fmt.Errorf("父会话 %s 不存在", parentID)
	}
	return id, nil
}

// ChildrenOf 列出某会话的子会话（按创建序）——归档级联与将来的子会话视图用。
func (s *Store) ChildrenOf(parentID string) ([]SessionMeta, error) {
	rows, err := s.db.Query(
		`SELECT id, title, updated_at, archived, workspace, parent_id, agent_id
		 FROM sessions WHERE parent_id = ? ORDER BY created_at ASC, rowid ASC`, parentID)
	if err != nil {
		return nil, fmt.Errorf("列出子会话失败: %w", err)
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var meta SessionMeta
		var archived int
		if err := rows.Scan(&meta.ID, &meta.Title, &meta.UpdatedAt, &archived, &meta.Workspace,
			&meta.ParentID, &meta.AgentID); err != nil {
			return nil, fmt.Errorf("读子会话行失败: %w", err)
		}
		meta.Archived = archived == 1
		if meta.Title == "" {
			meta.Title = "（空子会话）"
		}
		meta.UpdatedAt = fmtTime(meta.UpdatedAt)
		out = append(out, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 消息数与 List 同口径（按当前历史算——压缩后不能显示原始条数）
	counts, err := s.surfaceCounts()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Messages = counts[out[i].ID]
	}
	return out, nil
}

// nowNano 是库内时间戳格式（纳秒精度 + 时区）——排序依据 updated_at，
// 秒级精度会在同秒创建/更新的会话间产生不可预测的顺序（文件版同秒
// 文件名序不可靠的同族坑，SQL 版用精度根治）。
func nowNano() string { return time.Now().Format(time.RFC3339Nano) }

// List 列出全部**顶层**会话（按更新时间倒序，归档的沉底；rowid 兜底保证确定性顺序）。
// 子会话（parent_id 非空）不进侧栏——它们是派发的产物，在 dispatch 卡里可见；
// 列进来会把用户的会话列表淹掉。消息数按**当前历史**算（跳过被压缩检查点影子掉的
// 行）——与 Load 同口径，否则压缩后侧栏条数与用户看到的历史对不上。
func (s *Store) List() ([]SessionMeta, error) {
	rows, err := s.db.Query(
		`SELECT s.id, s.title, s.updated_at, s.archived, s.workspace, s.parent_id, s.agent_id
		 FROM sessions s
		 WHERE s.parent_id = ''
		 ORDER BY s.archived ASC, s.updated_at DESC, s.rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("列出会话失败: %w", err)
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var meta SessionMeta
		var archived int
		if err := rows.Scan(&meta.ID, &meta.Title, &meta.UpdatedAt, &archived, &meta.Workspace,
			&meta.ParentID, &meta.AgentID); err != nil {
			return nil, fmt.Errorf("读会话行失败: %w", err)
		}
		meta.Archived = archived == 1 // 结构化归档态（不再是标题前缀 hack）
		if meta.Title == "" {
			meta.Title = "（空会话）"
		}
		meta.UpdatedAt = fmtTime(meta.UpdatedAt)
		out = append(out, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 消息数：按当前历史算（复用回放的同一套存活判定）
	counts, err := s.surfaceCounts()
	if err != nil {
		return nil, err
	}
	visible := out[:0]
	for i := range out {
		out[i].Messages = counts[out[i].ID]
		// 空白草稿与旧版懒建语义一致：未发过消息的 Session 不进入历史列表。
		if out[i].Messages > 0 {
			visible = append(visible, out[i])
		}
	}
	return visible, nil
}

// surfaceCounts 统计每个会话当前历史的条数（跳过被影子掉的行）。
func (s *Store) surfaceCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT session_id, seq, checkpoint, shadowed_seqs FROM messages ORDER BY session_id, seq`)
	if err != nil {
		return nil, fmt.Errorf("统计消息数失败: %w", err)
	}
	defer rows.Close()
	bySession := make(map[string][]rowData)
	for rows.Next() {
		var r rowData
		var id, shadowedSeqs string
		var cp int
		if err := rows.Scan(&id, &r.seq, &cp, &shadowedSeqs); err != nil {
			return nil, fmt.Errorf("统计消息数失败: %w", err)
		}
		r.checkpoint = cp == 1
		if r.checkpoint && shadowedSeqs != "" && shadowedSeqs != "[]" {
			if err := json.Unmarshal([]byte(shadowedSeqs), &r.shadowed); err != nil {
				return nil, fmt.Errorf("解析影子区间失败: %w", err)
			}
		}
		bySession[id] = append(bySession[id], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(bySession))
	for id, rs := range bySession {
		counts[id] = len(surfaceRows(rs))
	}
	return counts, nil
}

// Latest 找最近的**顶层**会话（按 updated_at；无任何会话返回 ""——调用方走全新开始）。
// 子会话不参与：否则重启会把用户恢复到某个子 Agent 的会话上。
func (s *Store) Latest() (string, []llm.Message, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM sessions WHERE archived = 0 AND parent_id = ''
		 ORDER BY updated_at DESC, rowid DESC LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("查最近会话失败: %w", err)
	}
	msgs, err := s.Load(id)
	if err != nil {
		return "", nil, err
	}
	return id, msgs, nil
}

// Rename 重命名会话（侧栏管理功能）。
func (s *Store) Rename(id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("标题不能为空")
	}
	res, err := s.db.Exec(`UPDATE sessions SET title = ? WHERE id = ?`, title, id)
	if err != nil {
		return fmt.Errorf("重命名失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", id)
	}
	return nil
}

// Archive 归档/取消归档会话。
// Archive 归档/恢复会话。**级联到子会话**：子会话不进侧栏，用户没法单独操作它们，
// 父会话归档后把子会话留在列表外会让库里长期堆积孤儿；恢复时一并恢复（子会话的
// 历史与检查点都还在，dispatch 卡回放不受影响）。
func (s *Store) Archive(id string, archived bool) error {
	v := 0
	if archived {
		v = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("归档失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op
	res, err := tx.Exec(`UPDATE sessions SET archived = ? WHERE id = ?`, v, id)
	if err != nil {
		return fmt.Errorf("归档失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", id)
	}
	if _, err := tx.Exec(`UPDATE sessions SET archived = ? WHERE parent_id = ?`, v, id); err != nil {
		return fmt.Errorf("归档子会话失败: %w", err)
	}
	return tx.Commit()
}

// ListProjects 列出全部项目（注册序）。
func (s *Store) ListProjects() ([]ProjectMeta, error) {
	rows, err := s.db.Query(`SELECT id, name, path FROM projects ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("列出项目失败: %w", err)
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

// WorkspaceOf 返回会话归属的项目 id（空串 = 未分组）。会话不存在时报错
// （与 Load 的语义一致，调用方先 Load 过再查这里）。
func (s *Store) WorkspaceOf(sessionID string) (string, error) {
	var ws string
	err := s.db.QueryRow(`SELECT workspace FROM sessions WHERE id = ?`, sessionID).Scan(&ws)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("会话 %s 不存在", sessionID)
	}
	if err != nil {
		return "", fmt.Errorf("查询会话归属失败: %w", err)
	}
	return ws, nil
}

// SessionAgentID 返回会话运行的 Agent 名单 id（空 = 顶层会话/旧数据）。
//
// 为什么需要它：服务端为某个会话 id 建运行时（刷新后重新打开会话页）时要按归属
// Agent 解析模型——子 Agent 可以绑自己的模型，不读这一列就只能回落主 Agent 的模型，
// 于是子会话页显示一个它没用过的模型（编数据）。会话不存在时返回空串不报错：调用方
// 只是要一个"能不能解析出模型"的答案。
func (s *Store) SessionAgentID(id string) (string, error) {
	var agentID string
	err := s.db.QueryRow(`SELECT agent_id FROM sessions WHERE id = ?`, id).Scan(&agentID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("查询会话 Agent 失败: %w", err)
	}
	return agentID, nil
}

// SaveContextUsage 落库一次上下文占用测量（只有**主轮**写它——子上下文有自己的窗口）。
//
// 为什么要落库：占用原先只在内存里，后端一重启，重启前跑过的会话打开时指示器就是空的
// （用户实测「查看会话，他的上下文信息怎么是空的」）。占用是**已知事实**（那条会话的历史
// 就摆在库里），不该因为进程重启变成未知。
//
// **刻意不动 updated_at**：那是侧栏排序与「重启恢复最近会话」的依据，占用测量是会话的
// 附属信息、不是"用户刚用过这个会话"——写它把会话顶到列表最前面是错的。
func (s *Store) SaveContextUsage(id string, u sessiondata.ContextUsage) error {
	b, err := json.Marshal(u)
	if err != nil {
		return fmt.Errorf("序列化上下文占用失败: %w", err)
	}
	res, err := s.db.Exec(`UPDATE sessions SET context_usage = ? WHERE id = ?`, string(b), id)
	if err != nil {
		return fmt.Errorf("写上下文占用失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", id)
	}
	return nil
}

// ContextUsageOf 读回会话最近一次落库的占用测量。ok=false = 库里没有（老会话/从没跑过
// 主轮）——调用方据此**回落估算**，绝不能把它当成 0：0 会被 UI 显示成 0%（一个编出来的
// 数字比中性态更坏）。会话不存在时报错（与 Load/WorkspaceOf 的语义一致）。
func (s *Store) ContextUsageOf(id string) (sessiondata.ContextUsage, bool, error) {
	var raw string
	err := s.db.QueryRow(`SELECT context_usage FROM sessions WHERE id = ?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return sessiondata.ContextUsage{}, false, fmt.Errorf("会话 %s 不存在", id)
	}
	if err != nil {
		return sessiondata.ContextUsage{}, false, fmt.Errorf("查询上下文占用失败: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return sessiondata.ContextUsage{}, false, nil
	}
	var u sessiondata.ContextUsage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		return sessiondata.ContextUsage{}, false, fmt.Errorf("解析上下文占用失败: %w", err)
	}
	if u.Used <= 0 {
		// 落了一个"没有测量"的记录（历史为空时的估算）：按"库里没有"处理，让调用方
		// 走回落估算，而不是把 0 当成一个测量值
		return sessiondata.ContextUsage{}, false, nil
	}
	return u, true, nil
}

// SessionWorkspace 设置会话归属的项目（空串 = 未分组）。
func (s *Store) SessionWorkspace(id, workspace string) error {
	res, err := s.db.Exec(`UPDATE sessions SET workspace = ? WHERE id = ?`, workspace, id)
	if err != nil {
		return fmt.Errorf("设置归属失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", id)
	}
	return nil
}
