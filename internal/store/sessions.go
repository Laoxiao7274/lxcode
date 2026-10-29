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
//
// ownerID/ownerSeq 是**快照归属**（docs/session-tree.md §C.2.1）：回滚发起方（顶层
// 会话 id）与「这次派发属于父会话哪一轮」（父会话那条 dispatch 消息的 seq）。
// 与子会话行**同一条 INSERT** 落库，避免「子会话建好了但归属没写上」的中间态。
// 续跑时由 SetSnapshotOwner 按本次派发重写 owner_seq（边界①）。
func (s *Store) CreateChild(parentID, agentID, dispatchID, ownerID string, ownerSeq int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := newSessionID()
	now := nowNano()
	res, err := s.db.Exec(
		`INSERT INTO sessions (id, created_at, updated_at, title, archived, parent_id, agent_id, dispatch_id,
		                       workspace, child_owner_id, child_owner_seq)
		 SELECT ?, ?, ?, '', 0, id, ?, ?, workspace, ?, ? FROM sessions WHERE id = ?`,
		id, now, now, agentID, dispatchID, ownerID, ownerSeq, parentID)
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
		`SELECT id, title, updated_at, archived, workspace, parent_id, agent_id, leaf_len,
		        child_owner_id, child_owner_seq
		 FROM sessions WHERE parent_id = ? ORDER BY created_at ASC, rowid ASC`, parentID)
	if err != nil {
		return nil, fmt.Errorf("列出子会话失败: %w", err)
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var meta SessionMeta
		var archived, leafLen int
		var ownerID string
		var ownerSeq int64
		if err := rows.Scan(&meta.ID, &meta.Title, &meta.UpdatedAt, &archived, &meta.Workspace,
			&meta.ParentID, &meta.AgentID, &leafLen, &ownerID, &ownerSeq); err != nil {
			return nil, fmt.Errorf("读子会话行失败: %w", err)
		}
		meta.Archived = archived == 1
		if meta.Title == "" {
			meta.Title = "（空子会话）"
		}
		meta.UpdatedAt = fmtTime(meta.UpdatedAt)
		meta.Messages = leafLen // 与 List 同口径：当前 leaf 路径的节点数（读列，不再聚合）
		meta.OwnerID, meta.OwnerSeq = ownerID, ownerSeq
		out = append(out, meta)
	}
	return out, rows.Err()
}

// nowNano 是库内时间戳格式（纳秒精度 + 时区）——排序依据 updated_at，
// 秒级精度会在同秒创建/更新的会话间产生不可预测的顺序（文件版同秒
// 文件名序不可靠的同族坑，SQL 版用精度根治）。
func nowNano() string { return time.Now().Format(time.RFC3339Nano) }

// List 列出全部**顶层**会话（按更新时间倒序，归档的沉底；rowid 兜底保证确定性顺序）。
// 子会话（parent_id 非空）不进侧栏——它们是派发的产物，在 dispatch 卡里可见；
// 列进来会把用户的会话列表淹掉。
//
// 消息数（docs/session-tree.md §E.5）：读 `sessions.leaf_len`（**当前 leaf 路径**的
// 节点数）——分支之后「全表数 − 影子数」不再等于路径长度（废弃分支的行也在表里），
// 而 `leaf_len` 由三处写入点维护（AppendMsg +1、AppendCheckpoint 按新路径重算、
// SetLeaf/RewindTo 重算——后几处不能增量硬算，见各自函数注释）。副作用：侧栏
// 不再需要跑一次全表聚合（比原来还快）。
//
// ⚠️ **可见性判据不能用 leaf_len**：撤回第一条消息后 leaf_len = 0（空路径是合法状态），
// 而那条会话的废弃分支仍在库里——按 0 过滤会让它从侧栏消失（数据在、入口没了）。
// 所以可见性用「表里有任意行」。
func (s *Store) List() ([]SessionMeta, error) {
	rows, err := s.db.Query(
		`SELECT s.id, s.title, s.updated_at, s.archived, s.workspace, s.parent_id, s.agent_id, s.leaf_len,
		        EXISTS(SELECT 1 FROM messages m WHERE m.session_id = s.id) AS has_rows
		 FROM sessions s
		 WHERE s.parent_id = ''
		 ORDER BY s.archived ASC, s.updated_at DESC, s.rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("列出会话失败: %w", err)
	}
	defer rows.Close()
	var out []SessionMeta
	hasRows := make(map[string]bool)
	for rows.Next() {
		var meta SessionMeta
		var archived, leafLen, has int
		if err := rows.Scan(&meta.ID, &meta.Title, &meta.UpdatedAt, &archived, &meta.Workspace,
			&meta.ParentID, &meta.AgentID, &leafLen, &has); err != nil {
			return nil, fmt.Errorf("读会话行失败: %w", err)
		}
		meta.Archived = archived == 1 // 结构化归档态（不再是标题前缀 hack）
		if meta.Title == "" {
			meta.Title = "（空会话）"
		}
		meta.UpdatedAt = fmtTime(meta.UpdatedAt)
		meta.Messages = leafLen
		hasRows[meta.ID] = has == 1
		out = append(out, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	visible := out[:0]
	for i := range out {
		// 空白草稿与旧版懒建语义一致：未发过消息的 Session 不进入历史列表。
		// 判据是「表里有任意行」而不是 leaf_len > 0——空路径（leaf_len=0）但有
		// 废弃分支的会话必须留在侧栏（见函数注释）。
		if hasRows[out[i].ID] {
			visible = append(visible, out[i])
		}
	}
	return visible, nil
}

// surfaceCounts 统计每个会话当前历史的条数（跳过被影子掉的行）——口径与
// surfaceRows（回放的存活判定）严格一致。
//
// **2026-12 批次 2 起只服务测试**：生产调用点已全部改读 sessions.leaf_len（侧栏
// 条数）与 readPathRows（路径回放）。保留它是因为它是 leaf_len 的**独立对照实现**
// （surfacecounts_test.go 的逐会话比对 + tree_test.go 的随机序列不变量都靠它当
// 基准）；删掉它等于把「三处 leaf_len 维护点没漂移」这条断言一起删掉。
// 它数的是**全表口径**（全库行 − 影子），所以在有废弃分支的会话上与 leaf_len
// 天然不同——对照测试只在单链会话上比。
//
// 计数下推 SQLite（性能审查 P1-1，2026-10）：原实现把**全库**消息行逐行拉进 Go、
// 逐检查点解 shadowed_seqs JSON，只为数条数——53k 行实测 195ms 且随库线性涨，
// 而调用面是侧栏每次刷新。现在分两步：
//  1. 基础数 `GROUP BY session_id` 在 SQLite C 层聚合，Go 侧每会话只收一行；
//  2. 影子扣减只对**检查点行**做（量级 = 压缩次数，极少数）：读它们的
//     shadowed_seqs，把集合大小从基础数里减掉。
//
// 为什么「减 Σ len(影子集合)」与旧实现（数不在影子并集里的行）严格一致——
// 三条不变量（均由写入路径保证，surfaceCountsTest.go 的对照测试钉住）：
//   - AppendCheckpoint 的影子集合取自**当时的存活行**，每条 seq 都真实存在；
//   - 同一会话各检查点的影子集合**两两不相交**（后一份摘要影子的是 surface 行，
//     已被影子过的行不再参与计数——TestCheckpointShadowSkipsAlreadyShadowed）；
//   - 撤回（批次 2 之前真删行；之后只移 leaf、一行不删）不会让检查点引用不存在的
//     行：行永远在库里，GROUP BY 恒含它们——所以这条不变量在分支模型下更稳。
//
// 防御性处理（只对库外改写出来的畸形数据生效，正常路径永不触发）：同会话内
// 跨检查点的重复 seq 按集合去重（旧实现对重复 seq 也只数一次）；扣减后钳到 0
// （旧实现的存活计数不可能为负）。
func (s *Store) surfaceCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT session_id, COUNT(*) FROM messages GROUP BY session_id`)
	if err != nil {
		return nil, fmt.Errorf("统计消息数失败: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("统计消息数失败: %w", err)
		}
		counts[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 影子扣减：只扫检查点行（没压缩过的会话在这步零开销）。
	crows, err := s.db.Query(
		`SELECT session_id, shadowed_seqs FROM messages
		 WHERE checkpoint = 1 AND shadowed_seqs IS NOT NULL AND shadowed_seqs <> '' AND shadowed_seqs <> '[]'`)
	if err != nil {
		return nil, fmt.Errorf("统计消息数失败: %w", err)
	}
	defer crows.Close()
	shadowed := make(map[string]map[int]struct{}) // 会话 → 影子 seq 并集（去重）
	for crows.Next() {
		var id, shadowedSeqs string
		if err := crows.Scan(&id, &shadowedSeqs); err != nil {
			return nil, fmt.Errorf("统计消息数失败: %w", err)
		}
		var seqs []int
		if err := json.Unmarshal([]byte(shadowedSeqs), &seqs); err != nil {
			return nil, fmt.Errorf("解析影子区间失败: %w", err)
		}
		set := shadowed[id]
		if set == nil {
			set = make(map[int]struct{})
			shadowed[id] = set
		}
		for _, seq := range seqs {
			set[seq] = struct{}{}
		}
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}
	for id, set := range shadowed {
		if n, ok := counts[id]; ok {
			if d := n - len(set); d > 0 {
				counts[id] = d
			} else {
				counts[id] = 0 // 畸形数据兜底：存活计数不为负（见函数注释）
			}
		}
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

// TitleOf 返回会话标题（侧栏显示的那一条）。会话不存在时报错。
//
// 为什么需要它：合并进程的任务标签要写「合并 <会话标题>」——标题是人与模型认得的
// 定位信息，只有 id 的话任务列表里看不出这是哪个会话的合并。
func (s *Store) TitleOf(id string) (string, error) {
	var title string
	err := s.db.QueryRow(`SELECT title FROM sessions WHERE id = ?`, id).Scan(&title)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("会话 %s 不存在", id)
	}
	if err != nil {
		return "", fmt.Errorf("查询会话标题失败: %w", err)
	}
	return title, nil
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

// SessionParentID 返回会话的派发方 id（” = 顶层会话）。会话不存在时报错。
//
// 服务端用它判定「按 id 现建运行时」的那条会话是不是子会话：子会话运行时不能
// 进服务端的运行时缓存（缓存里的那份是 AttachTo 时刻的静态快照——收不到事件、
// 不再追加，之后按 id 读历史会一直命中陈旧数据），必须现建现用。
func (s *Store) SessionParentID(id string) (string, error) {
	var parent string
	err := s.db.QueryRow(`SELECT parent_id FROM sessions WHERE id = ?`, id).Scan(&parent)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("会话 %s 不存在", id)
	}
	if err != nil {
		return "", fmt.Errorf("查询会话派发方失败: %w", err)
	}
	return parent, nil
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

// SaveTodos 落库会话当前的任务清单快照（JSON 全量替换）。清单是**会话的状态**：
// 落库后重启恢复，计划条还在、下一轮的快照注入还有事实可注入。
//
// **刻意不动 updated_at**：那是侧栏排序与「重启恢复最近会话」的依据，清单更新是
// 会话的附属状态变化、不是"用户刚用过这个会话"——写它把会话顶到列表最前面是错的。
func (s *Store) SaveTodos(id string, items []sessiondata.TodoItem) error {
	b, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("序列化任务清单失败: %w", err)
	}
	res, err := s.db.Exec(`UPDATE sessions SET todos = ? WHERE id = ?`, string(b), id)
	if err != nil {
		return fmt.Errorf("写任务清单失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", id)
	}
	return nil
}

// TodosOf 读回会话的任务清单快照。ok=false = 库里没有（老会话/从没建过清单）——
// 调用方按"无清单"处理。会话不存在时报错（与 Load/WorkspaceOf 的语义一致）。
func (s *Store) TodosOf(id string) ([]sessiondata.TodoItem, bool, error) {
	var raw string
	err := s.db.QueryRow(`SELECT todos FROM sessions WHERE id = ?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, false, fmt.Errorf("会话 %s 不存在", id)
	}
	if err != nil {
		return nil, false, fmt.Errorf("查询任务清单失败: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, false, nil
	}
	var items []sessiondata.TodoItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, false, fmt.Errorf("解析任务清单失败: %w", err)
	}
	return items, true, nil
}

// SaveApproval 落库会话当前的权限档（chat.approval 改档 / 新会话按 behavior 默认
// 档初始化时写）。为什么落库：审批档是**会话的状态**——用户把某条会话切成 strict
// 后重启，打开这条会话还得是 strict（原先只在内存里，重启必回 confirm，用户实测）。
//
// **刻意不动 updated_at**：那是侧栏排序与「重启恢复最近会话」的依据，改档是会话的
// 附属状态变化、不是"用户刚用过这个会话"——写它把会话顶到列表最前面是错的。
func (s *Store) SaveApproval(id, approval string) error {
	res, err := s.db.Exec(`UPDATE sessions SET approval = ? WHERE id = ?`, approval, id)
	if err != nil {
		return fmt.Errorf("写会话审批档失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", id)
	}
	return nil
}

// ApprovalOf 读回会话落库的审批档。空串 = 没落过（老会话/从没改过档）——调用方
// 据此回落 behavior 默认档，而不是当成 confirm 写死。会话不存在时报错
// （与 Load/WorkspaceOf 的语义一致）。
func (s *Store) ApprovalOf(id string) (string, error) {
	var approval string
	err := s.db.QueryRow(`SELECT approval FROM sessions WHERE id = ?`, id).Scan(&approval)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("会话 %s 不存在", id)
	}
	if err != nil {
		return "", fmt.Errorf("查询会话审批档失败: %w", err)
	}
	return approval, nil
}

// SaveSessionModel 落库会话**选中的模型**（chat.model 切模型 / 子会话继承父会话）。
// 为什么落库：模型选择是会话的状态——用户把某条会话切到模型 X 后重启，打开这条
// 会话还得是 X（原先选择只能改全局 default 角色，一个会话切模型所有会话跟着变）。
//
// **刻意不动 updated_at**：与 SaveApproval 同一条理由——改模型是会话的附属状态
// 变化，不是"用户刚用过这个会话"，写它会把会话顶到侧栏最前面。
func (s *Store) SaveSessionModel(id, model string) error {
	res, err := s.db.Exec(`UPDATE sessions SET model = ? WHERE id = ?`, model, id)
	if err != nil {
		return fmt.Errorf("写会话模型失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", id)
	}
	return nil
}

// SessionModelOf 读回会话落库的模型选择。空串 = 没选过（老会话/从没切过模型）——
// 调用方据此回落 Agent 绑定与 default 角色，而不是把它当成"没模型可用"。
// 会话不存在时报错（与 Load/WorkspaceOf 的语义一致）。
func (s *Store) SessionModelOf(id string) (string, error) {
	var model string
	err := s.db.QueryRow(`SELECT model FROM sessions WHERE id = ?`, id).Scan(&model)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("会话 %s 不存在", id)
	}
	if err != nil {
		return "", fmt.Errorf("查询会话模型失败: %w", err)
	}
	return model, nil
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
