// Package store 实现会话的磁盘持久化：SQLite 单库（modernc.org/sqlite 纯 Go——
// 无 CGO，符合仓库 FFI 禁令；性能足够且 WAL 模式下已提交事务断电不丢）。
//
// 为什么从初版 JSONL 切到 SQLite（2026-12 用户拍板）：
//   - compaction（历史摘要改写）在 append-only 格式上只能整文件重写；
//   - 语义记忆/向量检索（FTS5/sqlite-vec）规划在同库扩展，避免会话与记忆
//     两个存储并存的分裂；
//   - List/Latest/Search 从全目录逐文件读降为 SQL 查询；
//   - 会话重命名/归档是 UPDATE 而不是文件改名。
//
// 崩溃安全：WAL 模式 + synchronous=NORMAL——已提交事务不丢；modernc 纯 Go
// 实现无进程内崩溃面，进程被杀 = 连接断开 = WAL 回放，语义与文件版一致。
package store

import (
	crand "crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/moyunteng/lxcode/internal/llm"
	"github.com/moyunteng/lxcode/internal/sessiondata"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动（注册 database/sql 接口）
)

// schema 是库结构（幂等建表）。seq 显式保序——JSONL 的行序语义显式化；
// title 在写入时维护（首条 user 消息），List 不再逐行全读。
// workspace 列经 ALTER 兼容追加（旧库升级不炸：已存在时忽略错误）。
const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  id         TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  title      TEXT NOT NULL DEFAULT '',
  archived   INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
  session_id    TEXT NOT NULL,
  seq           INTEGER NOT NULL,
  role          TEXT NOT NULL,
  content       TEXT NOT NULL DEFAULT '',
  reasoning     TEXT NOT NULL DEFAULT '',
  reasoning_sig TEXT NOT NULL DEFAULT '',
  tool_calls    TEXT NOT NULL DEFAULT '[]', -- llm.ToolCall 数组 JSON
  tool_call_id  TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (session_id, seq),
  FOREIGN KEY (session_id) REFERENCES sessions(id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC, archived);
CREATE TABLE IF NOT EXISTS projects (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  path       TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);
`

// SessionMeta 是会话列表的条目（resume 选择器的数据源）。
type SessionMeta = sessiondata.SessionMeta

// Store 管理单个 SQLite 库（sessions 目录下的 sessions.db）。
type Store struct {
	db *sql.DB
	// mu 只保护 Create 的 id 生成竞态；数据库自身并发由
	// 连接池 + WAL + busy_timeout 保证。
	mu sync.Mutex
}

// Open 打开（必要时创建）会话库：建目录、建表、开 WAL。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建会话目录 %s 失败: %w", dir, err)
	}
	// DSN 参数：WAL（读写不互斥、断电回放）；busy_timeout 防 SQLITE_BUSY
	// （连接池多连接下的写争用）；_txlock=immediate 让事务开始即取写锁——
	// 默认的延迟升级（deferred→写时升级）在两个事务同时升级时会立刻
	// SQLITE_BUSY 不等 busy_timeout（实测 TestConcurrentAppend 踩过），
	// immediate 模式下等待语义才真正生效。_pragma 每连接生效故写在 DSN 里。
	dsn := "file:" + filepath.ToSlash(filepath.Join(dir, "sessions.db")) +
		"?_txlock=immediate" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开会话库失败: %w", err)
	}
	// 连接池上限：SQLite 单写者——过多连接只会增加 BUSY 概率；
	// 4 足够（读并发 + 一个写）。
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化会话表失败: %w", err)
	}
	// 列级迁移（旧库升级）：sessions.workspace 追加列。重复执行报 duplicate
	// column 是预期——忽略即可（幂等）。
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN workspace TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("迁移会话表失败: %w", err)
		}
	}
	st := &Store{db: db}
	// Agent 注册表与拓展目录（M1——四张表 + 首次种子，幂等）
	if err := st.initAgents(); err != nil {
		db.Close()
		return nil, err
	}
	return st, nil
}

// Close 关闭库连接（进程退出/测试清理时调用；幂等）。
func (s *Store) Close() error { return s.db.Close() }

// newSessionID 生成会话 id：时间戳前缀 + 随机后缀防碰撞。
func newSessionID() string {
	return time.Now().Format("20060102-150405") + "-" + randomSuffix()
}

func randomSuffix() string {
	b := make([]byte, 2)
	if _, err := crand.Read(b); err != nil {
		return "0000" // 退化路径：crypto 失败极罕见，时间戳本身已近唯一
	}
	return fmt.Sprintf("%x", b)
}

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

// nowNano 是库内时间戳格式（纳秒精度 + 时区）——排序依据 updated_at，
// 秒级精度会在同秒创建/更新的会话间产生不可预测的顺序（文件版同秒
// 文件名序不可靠的同族坑，SQL 版用精度根治）。
func nowNano() string { return time.Now().Format(time.RFC3339Nano) }

// AppendMsg 把一条消息追加到会话（事务：INSERT 消息 + UPDATE 会话时间戳）。
func (s *Store) AppendMsg(id string, m llm.Message) error {
	toolCalls, err := json.Marshal(m.ToolCalls)
	if err != nil {
		return fmt.Errorf("序列化工具调用失败: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开事务失败: %w", err)
	}
	defer tx.Rollback() // 已提交时是 no-op
	// seq = 当前会话最大 seq + 1（单会话写入串行，无竞态窗口）
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE session_id = ?`, id).Scan(&seq); err != nil {
		return fmt.Errorf("取序号失败: %w", err)
	}
	// 标题懒维护：首条 user 消息截断（写入时算好，List 零计算）
	if m.Role == "user" {
		var title string
		if err := tx.QueryRow(`SELECT title FROM sessions WHERE id = ?`, id).Scan(&title); err != nil {
			return fmt.Errorf("读标题失败: %w", err)
		}
		if title == "" {
			if _, err := tx.Exec(`UPDATE sessions SET title = ? WHERE id = ?`, clipTitle(m.Content), id); err != nil {
				return fmt.Errorf("写标题失败: %w", err)
			}
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, role, content, reasoning, reasoning_sig, tool_calls, tool_call_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, seq, m.Role, m.Content, m.ReasoningContent, m.ReasoningSignature, string(toolCalls), m.ToolCallID,
	); err != nil {
		return fmt.Errorf("写消息失败: %w", err)
	}
	if _, err := tx.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, nowNano(), id); err != nil {
		return fmt.Errorf("更新会话时间失败: %w", err)
	}
	return tx.Commit()
}

// Load 读出会话的全部消息（按 seq 升序）。会话不存在时报错
// （调用方 SwitchTo 依赖此语义区分「空会话」与「不存在」）。
func (s *Store) Load(id string) ([]llm.Message, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("会话 %s 不存在", id)
		}
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	rows, err := s.db.Query(
		`SELECT role, content, reasoning, reasoning_sig, tool_calls, tool_call_id
		 FROM messages WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("查询会话 %s 失败: %w", id, err)
	}
	defer rows.Close()
	var msgs []llm.Message
	for rows.Next() {
		var m llm.Message
		var toolCalls string
		if err := rows.Scan(&m.Role, &m.Content, &m.ReasoningContent, &m.ReasoningSignature, &toolCalls, &m.ToolCallID); err != nil {
			return nil, fmt.Errorf("读消息行失败: %w", err)
		}
		if toolCalls != "" && toolCalls != "[]" {
			if err := json.Unmarshal([]byte(toolCalls), &m.ToolCalls); err != nil {
				return nil, fmt.Errorf("解析工具调用失败: %w", err)
			}
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// List 列出全部会话（按更新时间倒序，归档的沉底；rowid 兜底保证确定性顺序）。
func (s *Store) List() ([]SessionMeta, error) {
	rows, err := s.db.Query(
		`SELECT s.id, s.title, s.updated_at, s.archived, s.workspace,
		        (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id) AS n
		 FROM sessions s
		 ORDER BY s.archived ASC, s.updated_at DESC, s.rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("列出会话失败: %w", err)
	}
	defer rows.Close()
	var out []SessionMeta
	for rows.Next() {
		var meta SessionMeta
		var archived int
		if err := rows.Scan(&meta.ID, &meta.Title, &meta.UpdatedAt, &archived, &meta.Workspace, &meta.Messages); err != nil {
			return nil, fmt.Errorf("读会话行失败: %w", err)
		}
		meta.Archived = archived == 1 // 结构化归档态（不再是标题前缀 hack）
		if meta.Title == "" {
			meta.Title = "（空会话）"
		}
		meta.UpdatedAt = fmtTime(meta.UpdatedAt)
		out = append(out, meta)
	}
	return out, rows.Err()
}

// Latest 找最近的会话（按 updated_at；无任何会话返回 ""——调用方走全新开始）。
func (s *Store) Latest() (string, []llm.Message, error) {
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM sessions WHERE archived = 0 ORDER BY updated_at DESC, rowid DESC LIMIT 1`).Scan(&id)
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
func (s *Store) Archive(id string, archived bool) error {
	v := 0
	if archived {
		v = 1
	}
	res, err := s.db.Exec(`UPDATE sessions SET archived = ? WHERE id = ?`, v, id)
	if err != nil {
		return fmt.Errorf("归档失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", id)
	}
	return nil
}

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

// SearchHit 是一条会话搜索命中。
type SearchHit = sessiondata.SearchHit

// searchClip 是命中内容的展示截断长度。
const searchClip = 120

// Search 在全部会话的消息内容里按正则搜索，按会话更新时间从近到远。
// 保持 Go 正则语义（与文件版行为一致）；FTS5 分词匹配留给语义记忆层。
func (s *Store) Search(pattern string, max int) ([]SearchHit, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("模式不是合法正则: %w", err)
	}
	if max <= 0 {
		max = 30
	}
	if max > 100 {
		max = 100
	}
	rows, err := s.db.Query(
		`SELECT m.session_id, m.role, m.content
		 FROM messages m
		 JOIN (SELECT id, rowid AS srow FROM sessions WHERE archived = 0
		       ORDER BY updated_at DESC, rowid DESC) s ON m.session_id = s.id
		 ORDER BY s.srow DESC, m.seq`) // 会话从近到远（srow 大 = 近），会话内按 seq
	if err != nil {
		return nil, fmt.Errorf("搜索查询失败: %w", err)
	}
	defer rows.Close()
	var hits []SearchHit
	seqIn := map[string]int{} // 会话内消息序号（1 起）
	for rows.Next() {
		var sid, role, content string
		if err := rows.Scan(&sid, &role, &content); err != nil {
			return nil, fmt.Errorf("读搜索行失败: %w", err)
		}
		seqIn[sid]++
		if !re.MatchString(content) {
			continue
		}
		hits = append(hits, SearchHit{
			SessionID: sid, Index: seqIn[sid], Role: role,
			Content: clipStr(content, searchClip),
		})
		if len(hits) >= max {
			break
		}
	}
	return hits, rows.Err()
}

// clipTitle 截标题：一行以内、40 个字符封顶（列表预览用）。
func clipTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// clipStr 截断到 n 个字符（rune 安全），尾部加省略号。
func clipStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// fmtTime 把库内时间戳（RFC3339Nano）格式化为列表展示形态。
func fmtTime(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts // 解析失败原样返回（新格式不该失败；旧数据无）
	}
	return t.Format("2006-01-02 15:04")
}

// FormatSearchHits 把命中渲染成给模型的文本（session_search 工具的输出）。
func FormatSearchHits(hits []SearchHit, total int) string {
	return sessiondata.FormatSearchHits(hits, total)
}
