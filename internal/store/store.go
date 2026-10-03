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
//
// 本文件只留「打开/关闭 + 库结构 + 类型」；会话、消息、项目、搜索按域拆到
// sessions.go / messages.go / projects.go / search.go（同包多文件，见 AGENTS.md §四）。

package store

import (
	crand "crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	db  *sql.DB
	dir string // 会话数据库所在目录；worktree 工作目录放在其独立子目录
	// mu 只保护 Create 的 id 生成竞态；数据库自身并发由
	// 连接池 + WAL + busy_timeout 保证。
	mu sync.Mutex
}

// Open 打开（必要时创建）会话库：建目录、建表、开 WAL。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建会话目录 %s 失败: %w", dir, err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("解析会话目录失败: %w", err)
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
	// 子会话（2026-09-22）：派发给子 Agent 的任务开一个**独立会话**——自己的行 +
	// 自己的消息历史 + 自己的压缩检查点，靠 parent_id 挂回父会话。于是：
	//   - 子 Agent 的进度天然可续（历史在库里，续跑附着同一个 id 继续跑）；
	//   - 压缩同款（子会话就是会话，走同一套检查点/影子区间）；
	//   - parent_id = '' 的是顶层会话（用户自己的会话），List/Latest 只认它们，
	//     否则重启会恢复到子会话、侧栏会被子会话淹没。
	for _, col := range []string{
		`ALTER TABLE sessions ADD COLUMN parent_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN agent_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN dispatch_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN worktree_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN worktree_branch TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN worktree_base TEXT NOT NULL DEFAULT ''`,
		// 上下文占用测量（2026-09-30）：每轮主轮的真实用量落在这里，后端重启后打开
		// 旧会话仍能显示占用——原先只在内存里，重启后旧会话的指示器就是空的（用户实测）。
		// 空串 = 从没跑过主轮（老会话）→ 调用方按已加载的历史回落估算，而不是当成 0。
		`ALTER TABLE sessions ADD COLUMN context_usage TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(col); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("迁移会话表失败: %w", err)
		}
	}
	// 子会话索引：按父查子（归档级联、将来的子会话列表）
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_id)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("建子会话索引失败: %w", err)
	}
	// 压缩检查点的影子区间（2026-09-22）：checkpoint=1 的行是一条摘要检查点，
	// 它替换（影子）当前历史里的一段——权威判定是 `shadowed_seqs`（被影子行的 seq
	// 集合）；`shadow_start_seq/shadow_end_seq` 只记这段在**历史位置**上的首尾，供对账，
	// 回放不读它们（中间段影子的 seq 集合可以是不连续的，区间形式表达不了）。
	// 被影子的原文**不删**——翻旧账仍可查（Search 照旧搜全量日志），
	// 只是历史回放（Load/Latest）跳过它们。
	// 每轮生成的簿记（2026-09-30）：首 token 延迟 / 总耗时 / 输出 token / 实际用的
	// 模型。为什么落库而不是只放内存：用户刷新后这些数字不能消失——而本仓库已经为
	// "live 与 replay 两条路径不一致"吃过三次亏（子 Agent 卡退化、两条路径分叉、seq
	// 只在一条路径上）。落库后 chat.history 回放与 chat.done 实时是同一份数字。
	// 零值 = 未知（工具轮没有首 token / provider 不回报用量），回读时原样保留。
	for _, col := range []string{
		`ALTER TABLE messages ADD COLUMN checkpoint INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN shadow_start_seq INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN shadow_end_seq INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN shadowed_seqs TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE messages ADD COLUMN first_token_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN usage_tokens INTEGER NOT NULL DEFAULT 0`,
		// 输入侧用量三桶（2026-09-30）：会话统计的折叠输入——缓存命中率与计费口径
		// 都要它们，而 usage_tokens 是**输出**（生成速度的分母）。老行恒 0（未知），
		// 那正是诚实的值：那时候的适配器根本没解析缓存字段。
		`ALTER TABLE messages ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0`,
		// notice（2026-09-30）：这条 user 消息是**注入的提示条**（重复调用提醒 /
		// 后台任务通告），不是用户说的话。会话统计的「轮数」按它排除——前缀匹配
		// 在 Go 侧做不了（RepeatNoticePrefix 在 agent、JobNoticePrefix 在 protocol，
		// 而 store 谁都不能 import：分层规则），所以把判定**记在写边界**。
		`ALTER TABLE messages ADD COLUMN notice INTEGER NOT NULL DEFAULT 0`,
		// usage_split（2026-09-30）：这一行的用量是**拆分口径**（usage_tokens 真的是
		// 输出、输入侧三桶另记）。本功能上线前的行是另一种口径——那时 usage_tokens 装的是
		// provider 的 total_tokens（输入+输出）——混算会把生成速度报得离谱（实测 687.8 tok/s）。
		// 为什么用写边界的一位标记而不是"输入桶为 0"去猜：猜在"端点只报 completion_tokens
		// 不报 prompt_tokens"时会把新行误判成老行（少显示）。ALTER 的 DEFAULT 0 正好把
		// 所有**已存在的行**标成老口径（它们确实是旧二进制写的），新写入一律置 1。
		`ALTER TABLE messages ADD COLUMN usage_split INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(col); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("迁移消息表失败: %w", err)
		}
	}
	st := &Store{db: db, dir: absDir}
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
