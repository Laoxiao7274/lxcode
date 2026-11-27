// 定时/提醒任务（timer）的持久化（B2）：重启重建 timer 的唯一事实源。
//
// 为什么不存 jobs 的内存注册表：Manager 是进程级内存态（bash 的日志有全量
// 落盘但句柄不可跨进程），而 timer 的**约定本体**（什么时候、提醒什么、投给谁）
// 必须跨重启——「下午 3 点提醒我」在后端重启后失效不是可接受的降级。
// 落库时机：schedule 工具 Start 成功后写行；到点 settle 后 server 侧标记 done
//（行保留——对账/审计可见，重建只取未 done 的）。
//
// due_at 存 RFC3339Nano（与 sessions.updated_at 同一格式约定）。

package store

import (
	"database/sql"
	"fmt"
	"time"
)

// TimerRow 是 timers 表的一行（重建与对账共用）。
type TimerRow struct {
	JobID          string
	SessionID      string
	OwnerSessionID string
	Workspace      string
	Label          string
	Note           string
	DueAt          time.Time
	Done           bool
}

const timersSchema = `CREATE TABLE IF NOT EXISTS timers (
	job_id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	owner_session_id TEXT NOT NULL DEFAULT '',
	workspace TEXT NOT NULL DEFAULT '',
	label TEXT NOT NULL DEFAULT '',
	note TEXT NOT NULL,
	due_at TEXT NOT NULL,
	done INTEGER NOT NULL DEFAULT 0
)`

// SaveTimer 插入/覆盖一行（job_id 主键——重启重建会以相同 id 重新 Start）。
func (s *Store) SaveTimer(row TimerRow) error {
	if row.JobID == "" || row.SessionID == "" || row.Note == "" {
		return fmt.Errorf("timer 行不完整（job_id/session_id/note 必填）")
	}
	if _, err := s.db.Exec(
		`INSERT INTO timers (job_id, session_id, owner_session_id, workspace, label, note, due_at, done)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(job_id) DO UPDATE SET session_id=excluded.session_id, owner_session_id=excluded.owner_session_id,
		   workspace=excluded.workspace, label=excluded.label, note=excluded.note, due_at=excluded.due_at, done=excluded.done`,
		row.JobID, row.SessionID, row.OwnerSessionID, row.Workspace, row.Label, row.Note,
		row.DueAt.Format(time.RFC3339Nano), boolInt(row.Done),
	); err != nil {
		return fmt.Errorf("写 timer 失败: %w", err)
	}
	return nil
}

// PendingTimers 列出未到点（未 done）的 timer 行——重启重建的数据源。
// 按 due_at 升序（先到的先重建，语义清晰）。
func (s *Store) PendingTimers() ([]TimerRow, error) {
	rows, err := s.db.Query(`SELECT job_id, session_id, owner_session_id, workspace, label, note, due_at, done
		 FROM timers WHERE done = 0 ORDER BY due_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("读 timers 失败: %w", err)
	}
	defer rows.Close()
	var out []TimerRow
	for rows.Next() {
		var r TimerRow
		var due string
		var done int
		if err := rows.Scan(&r.JobID, &r.SessionID, &r.OwnerSessionID, &r.Workspace, &r.Label, &r.Note, &due, &done); err != nil {
			return nil, fmt.Errorf("读 timer 行失败: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, due)
		if err != nil {
			return nil, fmt.Errorf("timer %s 的 due_at 不是合法时间戳: %w", r.JobID, err)
		}
		r.DueAt = t
		r.Done = done != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkTimerDone 标记 timer 已触发（到点 settle 后 server 侧调用）。
// 幂等：重复标记是 no-op。
func (s *Store) MarkTimerDone(jobID string) error {
	res, err := s.db.Exec(`UPDATE timers SET done = 1 WHERE job_id = ?`, jobID)
	if err != nil {
		return fmt.Errorf("标记 timer 完成失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 没有这一行（删除后的迟到 settle 等）：不报错——标记是尽力而为
		return nil
	}
	return nil
}

// TimerDoneOf 查某个 job 是否已触发（对账/测试用）。
func (s *Store) TimerDoneOf(jobID string) (bool, error) {
	var done int
	err := s.db.QueryRow(`SELECT done FROM timers WHERE job_id = ?`, jobID).Scan(&done)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查 timer 状态失败: %w", err)
	}
	return done != 0, nil
}
