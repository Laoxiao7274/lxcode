// 文件快照的会话侧实现：抓取器（读 before、落 blob、插行）+ owner 解析。
//
// 分层纪律：agent 不碰 SQL、不认识 file_snapshots 表——它只把 Persistence 的
// SnapshotWrite/PutBlob 拼成一个 tools.FileSnapshotter 交给工具层（§C.2 的接缝）。
package agent

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/moyunteng/lxcode/internal/sessiondata"
	"github.com/moyunteng/lxcode/internal/tools"
)

// toolsSnapshotSkipMarker 是「本次改动未记快照」的提示文案（工具结果里附加，
// agent 侧按同一句常量识别 external++）。**两处必须逐字相同**——改成常量是为了
// 让两边都引用它，而不是各写一遍字符串字面量。
const toolsSnapshotSkipMarker = "（本次改动未记快照，回滚不到）"

// snapshotReadLimit 是单文件快照上限（1MB，§C.4）。
const snapshotReadLimit = 1 << 20

// snapshotSniffBytes 是二进制嗅探窗口（与 read_file 的 isBinary 同一条启发式：
// 前 8KB 内出现 NUL 即二进制。这里是 agent 侧，不能调 tools 的私有函数——
// 但判定语义逐字相同，两处都写在注释里钉住）。
const snapshotSniffBytes = 8192

// snapshotter 返回本轮的文件快照抓取器（nil = 不抓：没有存储/没有会话 id）。
//
// 抓取时机 = 工具 Exec 内部、真正落盘之前（§C.2）；这里只构造闭包，不读文件。
func (s *Session) snapshotter(ctx context.Context) tools.FileSnapshotter {
	s.mu.Lock()
	st, id, workDir := s.st, s.id, s.workDir
	s.mu.Unlock()
	if st == nil || id == "" {
		return nil
	}
	anchor := tools.SnapshotAnchor(ctx)
	if anchor <= 0 {
		return nil // 没有锚点 = 不知道这次写属于哪条消息 → 不抓（诚实降级）
	}
	ownerID, ownerSeq, ok := tools.SnapshotOwnerFrom(ctx)
	if !ok {
		// 兜底：ctx 没注入 owner（老调用方/单测）→ 回落「自己就是 owner」。
		// 顶层会话的正确性由这条兜底保证；子会话必须走注入（否则会记到子会话账上）。
		ownerID, ownerSeq = id, anchor
	}
	toolCallID := "" // 由 finishToolCall 侧的调用 id 决定不了（这里只按消息锚定）
	return func(ctx context.Context, absPath string) (tools.SnapshotHandle, error) {
		rel, err := filepath.Rel(workDir, absPath)
		if err != nil || rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
			// 工作区外的路径：快照的相对路径不可移植，**不抓**（诚实降级）。
			// 工具允许写绝对路径（用户明确要求时），但那种改动不该被"会话回滚"
			// 动到——回滚是按工作区相对路径执行的。
			return nil, nil
		}
		rel = filepath.ToSlash(rel)
		// before 内容：文件不存在 = 新建（before_hash='' → 回滚 = 删掉它）。
		var before []byte
		exists := false
		if data, rerr := os.ReadFile(absPath); rerr == nil {
			before, exists = data, true
		}
		rec := sessiondata.FileSnapshot{
			SessionID: id, Seq: anchor, OwnerSessionID: ownerID, OwnerSeq: ownerSeq,
			ToolCallID: toolCallID, Path: rel,
		}
		beforeHash := ""
		switch {
		case exists && int64(len(before)) > snapshotReadLimit:
			rec.Skipped, rec.SkipReason = true, "文件过大（>1MB）未快照"
		case exists && isBinaryHead(before):
			rec.Skipped, rec.SkipReason = true, "二进制文件未快照"
		case exists:
			hash, herr := st.PutBlob(before)
			if herr != nil {
				// blob 落盘失败：如实记 skipped（回滚时明说还原不到），
				// **不让工具失败**——写文件本身还没发生呢，快照是旁路。
				log.Printf("文件快照 blob 落盘失败（本次改动将无法回滚）: %v", err)
				rec.Skipped, rec.SkipReason = true, "快照落盘失败"
			} else {
				beforeHash = hash
				rec.Size = int64(len(before))
			}
		}
		rec.BeforeHash = beforeHash
		return &snapshotHandle{st: st, rec: rec}, nil
	}
}

// snapshotHandle 是一次写操作的快照句柄（tools.SnapshotHandle 的实现）。
type snapshotHandle struct {
	st   Persistence
	rec  sessiondata.FileSnapshot
	done bool // Commit 之后置位：Abort 变成 no-op（defer 写法不会重复落行）
}

// Commit 落一行快照（写成功之后调用）。
//
// 失败只记日志：快照是旁路记录，写文件本身已经成功——为了快照失败而回滚用户的写
// 是本末倒置（§C.2）。
func (h *snapshotHandle) Commit(afterHash string, size int64) error {
	if h.done {
		return nil
	}
	h.done = true
	h.rec.AfterHash = afterHash
	if h.rec.Size == 0 {
		h.rec.Size = size
	}
	if err := h.st.SnapshotWrite(h.rec); err != nil {
		log.Printf("文件快照落库失败（本次改动将无法回滚）: %v", err)
		return err
	}
	return nil
}

// Abort 在写失败/被中断时调用（不落行）。
func (h *snapshotHandle) Abort() { h.done = true }

// SkipNote 返回「这次快照没抓」的人话（空 = 正常抓到）。
//
// 工具层用它把提示附到结果正文里——**为什么不回传结构体**：那要改 Def.Exec 的
// 签名，波及全部 24 个工具；一句常量提示是最薄的耦合，而 agent 侧按**同一句常量**
// 识别「edit/write_file 的快照被跳过 → external++」（见 external.go）。
func (h *snapshotHandle) SkipNote() string {
	if h.rec.Skipped {
		return toolsSnapshotSkipMarker
	}
	return ""
}

// isBinaryHead 是二进制判定（与 tools/file_read.go 的 isBinary 逐字同语义：
// 前 8KB 内出现 NUL 即二进制）。两处不能共用是因为分层（agent 不能调 tools 的
// 私有函数），但判定必须一致——所以这里只嗅探前 8KB，与 read_file 同一窗口。
func isBinaryHead(data []byte) bool {
	head := data
	if len(head) > snapshotSniffBytes {
		head = head[:snapshotSniffBytes]
	}
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	return false
}

// snapshotOwnerFor 解析本轮的**回滚归属**（docs §C.2.1）。
//
//	顶层会话：owner = 自己、ownerSeq = 本轮 assistant 消息的 seq；
//	子会话：  owner = 它继承来的顶层会话 id、ownerSeq = **父会话那条派发消息**的 seq。
//
// 子会话的 ownerSeq 来自 openChildSession 写入的 child_owner_*（每次派发重写——
// 续跑按本次派发算，§C.5.1 边界①）。读不到就回落「自己就是 owner」：顶层会话
// 的正确性靠这条兜底；子会话读不到意味着存储异常，如实降级而不是编一个数。
func (s *Session) snapshotOwnerFor(anchor int64) (ownerID string, ownerSeq int64, ok bool) {
	s.mu.Lock()
	st, id, owner := s.st, s.id, s.ownerID
	s.mu.Unlock()
	if st == nil || id == "" {
		return "", 0, false
	}
	// 时间线归属（ownerID）非空 = 这是子会话（顶层会话自己就是 owner）。
	if owner == "" || owner == id {
		return id, anchor, true
	}
	oid, oseq, err := st.SnapshotOwner(id)
	if err != nil || oid == "" || oseq <= 0 {
		// 老会话/存储异常：回落自己（诚实降级——这批改动会记在子会话账上，
		// 父会话回滚时漏掉它们；比编一个假 owner_seq 好）。
		return id, anchor, true
	}
	return oid, oseq, true
}

// SnapshotterForTest 暴露抓取器给单测（与 AppendForTest 同一条理由：生产路径
// 只有 runTurn 会构造它，测试需要直接验证抓取行为）。
func (s *Session) SnapshotterForTest(ctx context.Context) tools.FileSnapshotter {
	return s.snapshotter(ctx)
}
