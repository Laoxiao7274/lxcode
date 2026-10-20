// 文件还原的 plan 与执行（docs/session-tree.md §C.5）。
//
// 两个入口的**切段口径差一格**（必须写成两个具名常量，不能在调用处各写一个
// >= / >）：
//
//	rewind(anchor)  → 还原 owner_seq >= anchor（锚点自己也被移出路径）
//	setLeaf(seq)    → 还原 owner_seq >  seq   （seq 这条消息自己的改动属于
//	                                           「目标状态」，不该被还原掉）
//
// 差一个等号就会造成「切到第 42 轮，结果第 42 轮自己改的文件被还原了」。
//
// 执行顺序（跨磁盘与 DB 不可能真原子，只能把失败面缩到最小）：
//
//	① 算 plan（纯函数，可单测）
//	② 冲突检查（有冲突即返回，一个字节都不写）
//	③ 读盘缓存当前内容（供失败回退用）
//	④ 逐文件原子写（restore=='' 则删文件）；记录已写清单
//	⑤ 任一步失败 → 逆序还原已写文件（用 ③ 的缓存）→ 返回错误，leaf 不动
//	⑥ 写库（**调用方**做：leaf_seq/leaf_len，只改顶层会话那一行）
//	⑦ 重载内存历史 + 重算 context + 发事件（调用方做）
//
// 崩溃自愈：④ 与 ⑥ 之间写一条意图日志；下次启动补做 ⑥（文件已经对了）。
package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/atomicfile"
	"github.com/moyunteng/lxcode/internal/protocol"
	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/store"
)

// 切段口径（两个具名常量，见文件头）。
const (
	rewindCut  = true  // owner_seq >= anchor
	setLeafCut = false // owner_seq >  seq
)

// restorePlan 是一次回滚的完整计划（纯函数产物，测试直接断言）。
type restorePlan struct {
	// Restore 是按 path 分组后的还原项（每组一条：owner_seq 最小的那条的 before）。
	Restore []restoreEntry
	// Skipped 是「当时没抓快照」的组（如实报，不拿后面的 before 顶替）。
	Skipped []protocol.FileSkip
	// ChildSessions 是涉及的子会话 id（父会话自己不算）。
	ChildSessions []string
}

// restoreEntry 是一个文件的还原计划。
type restoreEntry struct {
	Path string
	// BeforeHash 是该 path 在段内**最早**那条的 before（'' = 当时不存在 → 删掉）。
	BeforeHash string
	// ExpectedHash 是该 path 在段内**最晚**那条的 after（冲突检测基准）。
	ExpectedHash string
	// FromSeq / FromOwnerSeq / SessionID 追溯「取的是谁的 before」。
	FromSeq      int64
	FromOwnerSeq int64
	SessionID    string
	Size         int64
	// Skipped 非空 = 这一组没抓快照（不参与还原，只如实报）。
	SkippedReason string
}

// buildRestorePlan 把快照行折叠成「每个文件一条还原动作」。
//
// 规则（§C.5 的 plan）：
//   - 按 path 分组（**跨会话混在一起分**：父会话与子会话改同一个文件要合成一条链）；
//   - restore  = 该 path **owner_seq 最小**的那条的 before_hash；
//   - expected = 该 path **owner_seq 最大**的那条的 after_hash；
//   - owner_seq 相同时按 (session_id, seq) 再排——同一轮里父子会话都改同一个文件时，
//     子会话的写必然发生在父会话那条 dispatch 之后（父会话要先落库那条 assistant
//     消息才会派发），所以定序是确定的：父会话的写在前；
//   - 若 owner_seq 最小那条 skipped=1 → **整组进 skipped**，不拿后面那条的 before
//     顶替（那会还原出一个「中间态」文件，比「明说没抓」误导得多）。
//
// 输入必须已按 (path, owner_seq, seq) 升序（store.SnapshotsForRestore 的 ORDER BY）。
func buildRestorePlan(rows []sessiondata.FileSnapshot, ownerID string) restorePlan {
	var plan restorePlan
	byPath := make(map[string][]sessiondata.FileSnapshot)
	var order []string // 保持确定顺序（按首次出现）
	for _, r := range rows {
		if _, ok := byPath[r.Path]; !ok {
			order = append(order, r.Path)
		}
		byPath[r.Path] = append(byPath[r.Path], r)
	}
	childSeen := make(map[string]bool)
	for _, path := range order {
		group := byPath[path]
		// 组内定序：owner_seq 升序，同 owner_seq 按 (session_id, seq) 升序。
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].OwnerSeq != group[j].OwnerSeq {
				return group[i].OwnerSeq < group[j].OwnerSeq
			}
			if group[i].SessionID != group[j].SessionID {
				return group[i].SessionID < group[j].SessionID
			}
			return group[i].Seq < group[j].Seq
		})
		first := group[0]
		last := group[len(group)-1]
		if first.Skipped {
			// 最早那条没抓：整组放弃（不拿后面那条的 before 顶替——那会还原出
			// 一个「中间态」文件，比「明说没抓」误导得多）。
			plan.Skipped = append(plan.Skipped, protocol.FileSkip{
				Path: path, Reason: first.SkipReason,
			})
			continue
		}
		plan.Restore = append(plan.Restore, restoreEntry{
			Path: path, BeforeHash: first.BeforeHash, ExpectedHash: last.AfterHash,
			FromSeq: first.Seq, FromOwnerSeq: first.OwnerSeq, SessionID: first.SessionID,
			Size: first.Size,
		})
		for _, r := range group {
			if r.SessionID != ownerID && !childSeen[r.SessionID] {
				childSeen[r.SessionID] = true
				plan.ChildSessions = append(plan.ChildSessions, r.SessionID)
			}
		}
	}
	sort.Strings(plan.ChildSessions)
	return plan
}

// restoreFiles 是 files:true 的编排：收集 → plan → 冲突检查 → 执行 → 意图日志。
//
// 返回 (结果, 冲突数, 错误)。**冲突不是错误**（正常业务结果，要带清单给用户看）：
// 调用方在冲突数 > 0 时**不切 leaf**、removed=0。
//
// workDir 从会话运行时取（子会话继承父会话的 workDir——跨会话回滚的前提，
// §C.5 的「还有一条跨会话的前提」）。
func (s *Server) restoreFiles(sessionID string, cutSeq int64, inclusive bool) (*protocol.FileRestoreResult, int, error) {
	cutKind := "rewind"
	if !inclusive {
		cutKind = "setLeaf"
	}
	if s.st == nil {
		return nil, 0, errNotPersisted
	}
	ownerID, err := s.st.OwnerSessionID(sessionID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.st.SnapshotsForRestore(ownerID, cutSeq, inclusive)
	if err != nil {
		return nil, 0, err
	}
	plan := buildRestorePlan(rows, ownerID)
	res := &protocol.FileRestoreResult{
		Skipped:       plan.Skipped,
		ChildSessions: plan.ChildSessions,
	}
	if len(plan.Restore) == 0 {
		return res, 0, nil
	}

	// workDir：顶层会话的工作目录（子会话恒继承它——跨会话回滚的前提）。
	workDir, err := s.workDirOf(sessionID)
	if err != nil {
		return nil, 0, err
	}

	// ② 冲突检查（**一个字节都不写**）：磁盘现状必须等于 expected。
	type action struct {
		entry     restoreEntry
		abs       string
		writeData []byte // nil + BeforeHash=="" → 删文件
		recreated bool
	}
	var actions []action
	for _, e := range plan.Restore {
		abs := filepath.Join(workDir, filepath.FromSlash(e.Path))
		cur, exists := readFileIfExists(abs)
		curHash := ""
		if exists {
			curHash = store.HashBytes(cur)
		}
		switch {
		case !exists && e.BeforeHash == "":
			continue // 已经是目标状态（本来就没有，当时也不存在）
		case !exists && e.BeforeHash != "":
			// 磁盘没有、当时有 → 恢复写入（无损：不会覆盖任何东西）。
			data, gerr := s.st.GetBlob(e.BeforeHash)
			if gerr != nil {
				return nil, 0, fmt.Errorf("读快照内容失败（%s）: %w", e.Path, gerr)
			}
			actions = append(actions, action{entry: e, abs: abs, writeData: data, recreated: true})
		case exists && e.ExpectedHash == "":
			// 我们以为此刻不该有这个文件（多半是外部新建/回滚到中途）→ 冲突，不删。
			res.Conflicts = append(res.Conflicts, protocol.FileConflict{
				Path: e.Path, Expected: "", Actual: curHash, SessionID: e.SessionID,
				Reason: "磁盘上存在这个文件，但快照期望它此刻不存在（可能被外部新建或 bash 改过）",
			})
		case curHash != e.ExpectedHash:
			res.Conflicts = append(res.Conflicts, protocol.FileConflict{
				Path: e.Path, Expected: e.ExpectedHash, Actual: curHash, SessionID: e.SessionID,
				Reason: "磁盘内容与快照期望不符（可能被手动修改或 bash 改过）",
			})
		case e.BeforeHash == "":
			// 磁盘内容确实是 agent 写的那份，且当时不存在 → 删掉（回滚「新建」）。
			actions = append(actions, action{entry: e, abs: abs})
		default:
			data, gerr := s.st.GetBlob(e.BeforeHash)
			if gerr != nil {
				return nil, 0, fmt.Errorf("读快照内容失败（%s）: %w", e.Path, gerr)
			}
			actions = append(actions, action{entry: e, abs: abs, writeData: data})
		}
	}
	if len(res.Conflicts) > 0 {
		// fail-closed：整批放弃，leaf 不动（调用方据冲突数决定不切）。
		return res, len(res.Conflicts), nil
	}
	if len(actions) == 0 {
		return res, 0, nil
	}

	// ③ 读盘缓存（供失败回退用）。
	type undoEntry struct {
		abs     string
		content []byte // nil = 原本不存在
		existed bool
	}
	undo := make([]undoEntry, 0, len(actions))
	for _, a := range actions {
		cur, exists := readFileIfExists(a.abs)
		undo = append(undo, undoEntry{abs: a.abs, content: cur, existed: exists})
	}

	// 意图日志（崩溃自愈）：记**切段锚点 + 切段口径**（不是新叶）——自愈时按同一
	// 口径重算目标叶（rewind → parent(anchor)、setLeaf → anchor），不需要在还原
	// 阶段知道 leaf 语义。崩溃在「文件已还原、leaf 未移」之间时，下次启动补做。
	intent := sessiondata.SnapshotIntent{
		SessionID: sessionID, CutSeq: cutSeq, CutKind: cutKind, CreatedAt: nowRFC3339(),
	}
	for _, a := range actions {
		intent.Written = append(intent.Written, a.entry.Path)
	}
	_ = s.st.SaveRestoreIntent(intent) // 失败只记日志（自愈是缓解，不是前提）

	// ④ 逐文件执行。
	written := 0
	for _, a := range actions {
		var werr error
		if a.writeData == nil {
			werr = os.Remove(a.abs)
			if werr != nil && os.IsNotExist(werr) {
				werr = nil // 已经不在了：目标状态达成
			}
		} else {
			werr = writeFileAtomic(a.abs, a.writeData)
		}
		if werr != nil {
			// ⑤ 逆序回退已写的文件，然后返回错误（leaf 不动）。
			for i := written - 1; i >= 0; i-- {
				u := undo[i]
				if u.existed {
					_ = writeFileAtomic(u.abs, u.content)
				} else {
					_ = os.Remove(u.abs)
				}
			}
			_ = s.st.ClearRestoreIntent(sessionID)
			return nil, 0, fmt.Errorf("还原 %s 失败（已回退本次全部改动）: %w", a.entry.Path, werr)
		}
		written++
		if a.writeData == nil {
			res.Deleted = append(res.Deleted, protocol.FileDeleteItem{
				Path: a.entry.Path, SessionID: a.entry.SessionID,
			})
		} else {
			res.Restored = append(res.Restored, protocol.FileRestoreItem{
				Path: a.entry.Path, FromSeq: a.entry.FromSeq, FromOwnerSeq: a.entry.FromOwnerSeq,
				SessionID: a.entry.SessionID, Bytes: int64(len(a.writeData)), Recreated: a.recreated,
			})
		}
	}
	return res, 0, nil
}

// workDirOf 取会话的工作目录（顶层会话的项目根；子会话恒继承）。
//
// 为什么读运行时而不是库：workDir 由 server 的 prepareWorkspace 解析（项目根 /
// 默认目录），库里只有 workspace id。运行时没有该会话时按项目解析一次。
func (s *Server) workDirOf(sessionID string) (string, error) {
	if sess := s.peekSession(sessionID); sess != nil {
		if dir := sess.WorkDir(); dir != "" {
			return dir, nil
		}
	}
	sess, err := s.session(sessionID)
	if err != nil {
		return "", err
	}
	return sess.WorkDir(), nil
}

// writeFileAtomic 原子替换文件（复用 atomicfile：同目录临时文件 + fsync + rename，
// Windows 的 rename 不覆盖由它内部回退处理）。
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, data)
}

// nowRFC3339 是意图日志的时间戳（纳秒精度，与库内时间戳同一形态）。
func nowRFC3339() string { return time.Now().Format(time.RFC3339Nano) }

// readFileIfExists 读文件；不存在返回 (nil, false)。
func readFileIfExists(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// 编译期断言：agent 包在本文件被引用（restoreFiles 的错误映射用 agent.ErrBusy 等）。
var _ = agent.ErrBusy
