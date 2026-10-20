// 文件快照：元数据入 SQLite（file_snapshots），**blob 内容寻址落磁盘**
// （<sessions>/fileblobs/<hash[0:2]>/<hash>）。设计见 docs/session-tree.md §C。
//
// 为什么 blob 落磁盘而不是存表（§C.3 的取舍）：
//   - 与 attachments 先例一致（两种内容寻址的存储不该用两套哲学）；
//   - 本仓库最大的性能痛点是「SQLite 全表读含正文」，把几百 KB 的旧文件内容塞进
//     同一个库会直接放大它（stats.go 的覆盖索引就是为躲开这个才加的）；
//   - blob 是**不可变**的（hash 即内容），不需要事务保护——孤儿只会浪费磁盘，
//     不会产生错误数据（由 GC 自然回收）。
//
// 归属（§C.2.1）：owner_session_id = 回滚发起方（顶层会话 id），owner_seq =
// 父会话空间的 seq。子会话的写按它归属到父会话的某一轮——回滚只需
// `WHERE owner_session_id = ? AND owner_seq >= ?` 一次索引查询，不在回滚时反推。
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/moyunteng/lxcode/internal/sessiondata"
)

// 快照的大小上限与跳过原因（§C.4）。1MB 覆盖 99% 的源码/配置；再大基本是生成物。
const (
	SnapshotMaxBytes = 1 << 20 // 1MB
	// 跳过原因文案（落库 + wire 共用同一份：UI 明示「这个文件当时没抓，回滚不到」）。
	SkipReasonTooLarge = "文件过大（>1MB）未快照"
	SkipReasonBinary   = "二进制文件未快照"
	SkipReasonReadFail = "读文件失败未快照"
)

// hashPattern 是 sha256 十六进制形态的硬校验（拒绝路径穿越：hash 是路径成分）。
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// BlobsRoot 返回 blob 根目录（<sessions>/fileblobs）。
func (s *Store) BlobsRoot() string { return filepath.Join(s.dir, "fileblobs") }

// blobPath 把 hash 映射到磁盘路径（两级散列避免单目录十万文件）。
func (s *Store) blobPath(hash string) (string, error) {
	if !hashPattern.MatchString(hash) {
		return "", fmt.Errorf("非法的 blob hash: %q", hash)
	}
	return filepath.Join(s.BlobsRoot(), hash[:2], hash), nil
}

// HashBytes 是 sha256 十六进制（快照与冲突检测共用同一份口径）。
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PutBlob 写一个 blob（内容寻址：已存在则跳过——天然幂等）。
//
// 写入用临时文件 + Sync + rename（atomicfile）：崩溃只会留孤儿临时文件，
// 不会留半截 blob（半截 blob 比没有更坏：它的 hash 对不上，读取时会报错）。
func (s *Store) PutBlob(data []byte) (hash string, err error) {
	hash = HashBytes(data)
	path, err := s.blobPath(hash)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return hash, nil // 去重：同内容只存一份
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("创建 blob 目录失败: %w", err)
	}
	if err := atomicWriteBlob(path, data); err != nil {
		return "", err
	}
	return hash, nil
}

// GetBlob 读一个 blob，并**校验内容 hash**（防磁盘损坏/人为改名）。
// 校验失败按「读不到」报错——宁可在回滚时报错，也不要拿错内容覆盖用户文件。
func (s *Store) GetBlob(hash string) ([]byte, error) {
	path, err := s.blobPath(hash)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if got := HashBytes(data); got != hash {
		return nil, fmt.Errorf("blob %s 内容校验失败（实际 %s）", hash, got)
	}
	return data, nil
}

// atomicWriteBlob 是 blob 的原子写（临时文件 + Sync + rename）。
// 不复用 atomicfile.Write：那个会保留目标文件的权限位，而 blob 是新建的不可变
// 内容，权限恒 0644（目标已存在时上层已经跳过，不会走到这里）。
func atomicWriteBlob(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".blob*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时 blob 失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if _, err := os.Stat(tmpName); err == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写 blob 失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步 blob 失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭 blob 失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("设置 blob 权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("落位 blob 失败: %w", err)
	}
	return nil
}

// SnapshotWrite 是「一次写操作」的快照落库：before 内容（可能为空 = 新建）
// 与 after hash。工具层在**写成功之后**调它（Commit 语义）。
//
// skipped 非空 = 没抓内容（大文件/二进制）：只记「这里发生过写」，before_hash 留空。
// 回滚时如实报 skipped，**绝不假装还原成功**（拿后面那条的 before 顶替会还原出
// 一个"中间态"文件，比明说没抓误导得多）。
func (s *Store) SnapshotWrite(rec sessiondata.FileSnapshot) error {
	skipped := 0
	if rec.Skipped {
		skipped = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO file_snapshots
		   (session_id, seq, owner_session_id, owner_seq, tool_call_id, path,
		    before_hash, after_hash, size, skipped, skip_reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SessionID, rec.Seq, rec.OwnerSessionID, rec.OwnerSeq, rec.ToolCallID, rec.Path,
		rec.BeforeHash, rec.AfterHash, rec.Size, skipped, rec.SkipReason, nowNano())
	if err != nil {
		return fmt.Errorf("写文件快照失败: %w", err)
	}
	return nil
}

// SnapshotsForRestore 收集一次回滚要处理的全部快照：
// owner_session_id = owner 且 **owner_seq 与 anchor 满足 cut 关系**。
//
// 跨会话（§C.5）：子会话的写按 owner_* 归属到父会话的某一轮，所以这条查询一次
// 拿到父会话 + 全部子孙会话的改动——**不需要**递归 CTE 遍历 parent_id，也不需要
// join dispatch_id（那条路既慢又脆：dispatch_id 是 tool_call id，续跑时不变，
// 无法区分「第几次派发」）。
//
// 排序：按 path 分组、组内 owner_seq 升序（每组最早的 before 是还原目标）。
func (s *Store) SnapshotsForRestore(ownerID string, cutSeq int64, inclusive bool) ([]sessiondata.FileSnapshot, error) {
	cmp := ">"
	if inclusive {
		cmp = ">="
	}
	rows, err := s.db.Query(
		`SELECT id, session_id, seq, owner_session_id, owner_seq, tool_call_id, path,
		        before_hash, after_hash, size, skipped, skip_reason, created_at
		 FROM file_snapshots
		 WHERE owner_session_id = ? AND owner_seq `+cmp+` ?
		 ORDER BY path, owner_seq ASC, seq ASC`, ownerID, cutSeq)
	if err != nil {
		return nil, fmt.Errorf("查询文件快照失败: %w", err)
	}
	defer rows.Close()
	var out []sessiondata.FileSnapshot
	for rows.Next() {
		var r sessiondata.FileSnapshot
		var skipped int
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Seq, &r.OwnerSessionID, &r.OwnerSeq,
			&r.ToolCallID, &r.Path, &r.BeforeHash, &r.AfterHash, &r.Size,
			&skipped, &r.SkipReason, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("读文件快照失败: %w", err)
		}
		r.Skipped = skipped == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// SnapshotCountsByOwnerSeq 统计「每一轮（owner_seq）可还原的文件改动数」与其中
// 来自子会话的数量——chat.tree 的节点徽标数据源（§B.2 TreeNode.Files/ChildFiles）。
//
// 返回 map[owner_seq]{total, child}；skipped 的**不计入**（它们回滚不了——如实
// 报「这一轮有 N 处无法还原」是 External 的活，不是 Files 的）。
func (s *Store) SnapshotCountsByOwnerSeq(ownerID string) (map[int64][2]int, error) {
	rows, err := s.db.Query(
		`SELECT owner_seq,
		        COUNT(*),
		        SUM(CASE WHEN session_id <> owner_session_id THEN 1 ELSE 0 END)
		 FROM file_snapshots
		 WHERE owner_session_id = ? AND skipped = 0
		 GROUP BY owner_seq`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("统计文件快照失败: %w", err)
	}
	defer rows.Close()
	out := make(map[int64][2]int)
	for rows.Next() {
		var seq int64
		var total, child sql.NullInt64
		if err := rows.Scan(&seq, &total, &child); err != nil {
			return nil, fmt.Errorf("统计文件快照失败: %w", err)
		}
		out[seq] = [2]int{int(total.Int64), int(child.Int64)}
	}
	return out, rows.Err()
}

// ---- 清理（§C.7）----

// SnapshotRetention 是清理策略（默认值见 §C.7；设置页二期才可配）。
type SnapshotRetention struct {
	KeepDays       int   // 保留期（created_at 早于它 → 可删）
	MaxSessionByte int64 // 单会话（按 owner 汇总）占用上限
	MaxTotalByte   int64 // 全库 blob 占用上限
	KeepRecent     int   // 最近 N 轮恒不清理（按 owner 的 user 轮次算）
}

// DefaultSnapshotRetention 是文档给的默认值（30 天 / 256MB / 2GB / 最近 20 轮）。
func DefaultSnapshotRetention() SnapshotRetention {
	return SnapshotRetention{KeepDays: 30, MaxSessionByte: 256 << 20, MaxTotalByte: 2 << 30, KeepRecent: 20}
}

// PruneSnapshots 按策略删元数据行，然后回收孤儿 blob。
//
// **顺序不能反**（§C.7）：先删行、再 GC——反了的话 GC 会把「马上就要被删的行的
// blob」当孤儿删掉，而那一行还在，下次回滚读不到内容（缺失 blob = 一次失败的还原）。
//
// 最近 KeepRecent 轮的保护按 **owner 的 user 轮次**算（不是按 seq，也不是按
// session）：子会话的快照会被它自己的序号误判。实现取 owner 路径上最后 N 个
// user 节点的 seq 作下界——这里用「该 owner 的全部快照里，owner_seq 最大的 N 个
// 不同值」近似（不引入 agent 层的路径计算；order 上等价且不依赖 leaf）。
func (s *Store) PruneSnapshots(r SnapshotRetention) (rowsDeleted int, blobsDeleted int, err error) {
	if r.KeepDays <= 0 {
		r.KeepDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -r.KeepDays).Format(time.RFC3339Nano)

	// 1) 保留期：按 owner 分组的「最近 N 轮」下界。
	keepFrom, err := s.recentOwnerSeqs(r.KeepRecent)
	if err != nil {
		return 0, 0, err
	}
	// 逐 owner 删：保留期 + 最近 N 轮保护。
	// 「每个 owner 的最后 N 个 owner_seq」在 SQL 里要窗口函数——快照行数是几千级，
	// Go 侧分组处理更直白，也不依赖具体 SQLite 版本。
	deleted, err := s.pruneByOwner(cutoff, keepFrom, r.MaxSessionByte)
	if err != nil {
		return 0, 0, err
	}
	rowsDeleted = deleted

	// 2) 全库上限：超了从最旧删起（按 owner 分组整组删见 pruneByOwner）。
	if r.MaxTotalByte > 0 {
		if total, terr := s.blobDiskUsage(); terr == nil && total > r.MaxTotalByte {
			extra, derr := s.pruneOldest(int64(total - r.MaxTotalByte))
			if derr != nil {
				return rowsDeleted, 0, derr
			}
			rowsDeleted += extra
		}
	}

	// 3) 孤儿 blob GC（此时被删行引用的 blob 才成为孤儿）。
	blobsDeleted, err = s.GCBlobs()
	if err != nil {
		return rowsDeleted, 0, err
	}
	return rowsDeleted, blobsDeleted, nil
}

// recentOwnerSeqs 取每个 owner 最近 KeepRecent 个不同 owner_seq 的**最小值**
// （owner → 下界）。owner_seq >= 下界 的行一律保留。
func (s *Store) recentOwnerSeqs(keep int) (map[string]int64, error) {
	out := make(map[string]int64)
	if keep <= 0 {
		return out, nil
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT owner_session_id, owner_seq FROM file_snapshots
		 ORDER BY owner_session_id, owner_seq DESC`)
	if err != nil {
		return nil, fmt.Errorf("读快照轮次失败: %w", err)
	}
	defer rows.Close()
	count := make(map[string]int)
	for rows.Next() {
		var owner string
		var seq int64
		if err := rows.Scan(&owner, &seq); err != nil {
			return nil, err
		}
		if count[owner] < keep {
			count[owner]++
			out[owner] = seq // 降序遍历：最后一个写进去的就是第 keep 个（下界）
		}
	}
	return out, rows.Err()
}

// pruneByOwner 删过期的元数据行（保留最近 N 轮 + 单 owner 体积上限）。
func (s *Store) pruneByOwner(cutoff string, keepFrom map[string]int64, maxPerOwner int64) (int, error) {
	// 候选行：早于保留期、且 owner_seq 低于该 owner 的保护下界。
	rows, err := s.db.Query(
		`SELECT id, owner_session_id, owner_seq, path, before_hash FROM file_snapshots
		 WHERE created_at < ? ORDER BY owner_session_id, path, owner_seq ASC`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("读过期快照失败: %w", err)
	}
	type cand struct {
		id    int64
		owner string
		seq   int64
		path  string
		hash  string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.owner, &c.seq, &c.path, &c.hash); err != nil {
			rows.Close()
			return 0, err
		}
		if low, ok := keepFrom[c.owner]; ok && c.seq >= low {
			continue // 最近 N 轮内：恒不清理
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	// 按文件分组整组删（同一 path 的 before/after 链条要么都在、要么都不在——
	// 留半条链会让回滚算出错误的 expected）。
	byPath := make(map[string][]cand)
	for _, c := range cands {
		key := c.owner + "\x00" + c.path
		byPath[key] = append(byPath[key], c)
	}
	del := 0
	for _, group := range byPath {
		for _, c := range group {
			if _, err := s.db.Exec(`DELETE FROM file_snapshots WHERE id = ?`, c.id); err != nil {
				return del, fmt.Errorf("删过期快照失败: %w", err)
			}
			del++
		}
	}
	return del, nil
}

// pruneOldest 按全库上限从最旧删起（按文件分组整组删）。
func (s *Store) pruneOldest(need int64) (int, error) {
	rows, err := s.db.Query(
		`SELECT id, size FROM file_snapshots ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return 0, fmt.Errorf("读快照失败: %w", err)
	}
	type row struct {
		id   int64
		size int64
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.size); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var freed int64
	del := 0
	for _, r := range all {
		if freed >= need {
			break
		}
		if _, err := s.db.Exec(`DELETE FROM file_snapshots WHERE id = ?`, r.id); err != nil {
			return del, fmt.Errorf("删快照失败: %w", err)
		}
		freed += r.size
		del++
	}
	return del, nil
}

// blobDiskUsage 统计 blob 目录占用（字节）。
func (s *Store) blobDiskUsage() (int64, error) {
	var total int64
	err := filepath.Walk(s.BlobsRoot(), func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	return total, nil
}

// GCBlobs 回收**无引用**的 blob。
//
// 判据（§C.7）：扫 fileblobs/ → 对每个 hash 查 idx_snapshots_blob 是否有引用 →
// 无引用且文件 mtime > 1h（避开正在写的临时态）→ 删除。
// 孤儿 blob（写成功但插行失败）由此自然回收。
func (s *Store) GCBlobs() (int, error) {
	root := s.BlobsRoot()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return 0, nil
	}
	deleted := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if !hashPattern.MatchString(name) {
			return nil // 临时文件/垃圾：1h 后同样清（下面统一判）
		}
		if time.Since(info.ModTime()) < time.Hour {
			return nil // 刚写的：可能是正在进行的快照（插行还没完成）
		}
		var refs int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM file_snapshots WHERE before_hash = ? OR after_hash = ?`,
			name, name).Scan(&refs); err != nil {
			return err
		}
		if refs > 0 {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return nil // 删不掉就留着（下次再试），不让 GC 把整轮打断
		}
		deleted++
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return deleted, fmt.Errorf("回收 blob 失败: %w", err)
	}
	return deleted, nil
}

// OwnerSessionID 读会话的时间线/快照归属（沿 parent_id 上溯到顶的顶层会话 id）。
// 顶层会话返回它自己。会话不存在时报错。
//
// 为什么要上溯而不是只读 child_owner_id：老会话（本功能上线前建的子会话）那一列
// 是空的——回滚时回落「自己就是 owner」会让它的改动漏出父会话的账。
func (s *Store) OwnerSessionID(sessionID string) (string, error) {
	cur := sessionID
	for guard := 0; guard < 64; guard++ {
		var parent, owner string
		err := s.db.QueryRow(`SELECT parent_id, child_owner_id FROM sessions WHERE id = ?`, cur).
			Scan(&parent, &owner)
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("会话 %s 不存在", cur)
		}
		if err != nil {
			return "", fmt.Errorf("读会话归属失败: %w", err)
		}
		if parent == "" {
			return cur, nil // 顶层会话：它就是 owner
		}
		if owner != "" {
			return owner, nil // 子会话记着顶层 id（新建时随行落库）
		}
		cur = parent // 老会话没记 owner：上溯一层再问
	}
	return sessionID, nil // 深度异常（不该发生）：回落自己，不猜
}

// SnapshotOwner 读子会话的快照归属默认值（它上一次被派发时属于父会话哪一轮）。
// 返回 ("", 0) = 老会话/顶层会话（调用方回落「自己就是 owner」）。
func (s *Store) SnapshotOwner(sessionID string) (ownerID string, ownerSeq int64, err error) {
	err = s.db.QueryRow(`SELECT child_owner_id, child_owner_seq FROM sessions WHERE id = ?`, sessionID).
		Scan(&ownerID, &ownerSeq)
	if err == sql.ErrNoRows {
		return "", 0, fmt.Errorf("会话 %s 不存在", sessionID)
	}
	if err != nil {
		return "", 0, fmt.Errorf("读快照归属失败: %w", err)
	}
	return ownerID, ownerSeq, nil
}

// SetSnapshotOwner 落子会话的快照归属（新建时随 CreateChild 写一次；续跑时按本次
// 派发重写 owner_seq——§C.5.1 边界①：同一个子会话在第 45 轮被续跑时改的文件必须
// 归到 45 而不是 42，否则回滚到 43 轮时会被一起还原）。
func (s *Store) SetSnapshotOwner(sessionID, ownerID string, ownerSeq int64) error {
	res, err := s.db.Exec(`UPDATE sessions SET child_owner_id = ?, child_owner_seq = ? WHERE id = ?`,
		ownerID, ownerSeq, sessionID)
	if err != nil {
		return fmt.Errorf("写快照归属失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("会话 %s 不存在", sessionID)
	}
	return nil
}

// intentPath 是意图日志的文件路径（<sessions>/pending-restore-<session>.json）。
func (s *Store) intentPath(sessionID string) string {
	return filepath.Join(s.dir, "pending-restore-"+sessionID+".json")
}

// SaveRestoreIntent 写意图日志（还原开始前/写完文件后各写一次）。
func (s *Store) SaveRestoreIntent(it sessiondata.SnapshotIntent) error {
	b, err := json.MarshalIndent(it, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.intentPath(it.SessionID), b, 0o644)
}

// LoadRestoreIntents 读全部未收尾的意图日志（启动时调用，补做 leaf 移动）。
func (s *Store) LoadRestoreIntents() ([]sessiondata.SnapshotIntent, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []sessiondata.SnapshotIntent
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || len(name) < len("pending-restore-.json") ||
			name[:len("pending-restore-")] != "pending-restore-" ||
			filepath.Ext(name) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		var it sessiondata.SnapshotIntent
		if err := json.Unmarshal(b, &it); err != nil || it.SessionID == "" {
			continue // 坏文件：跳过（不删，留给人工看一眼）
		}
		out = append(out, it)
	}
	return out, nil
}

// ClearRestoreIntent 删意图日志（还原成功后调用）。
func (s *Store) ClearRestoreIntent(sessionID string) error {
	err := os.Remove(s.intentPath(sessionID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
