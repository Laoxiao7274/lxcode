// 会话树与叶指针在服务端的接线：chat.setLeaf / chat.tree / chat.leafChanged 的
// 转换与辅助（agent 类型 → wire 类型；leaf 读取）。
//
// 文件还原（files:true）的编排在 files_restore.go（批次 3）：本文件只放
// 「纯历史指针」这部分，两者分开的理由是它们的前置条件完全不同——切 leaf 只读库，
// 还原文件要动磁盘。
package server

import (
	"errors"
	"log"

	"github.com/moyunteng/lxcode/internal/agent"
	"github.com/moyunteng/lxcode/internal/protocol"
)

// defaultTreeMaxNodes 是 chat.tree 的缺省节点上限（docs §B.4：2000 个节点约
// 200KB，覆盖绝大多数会话；超出只回最近 N 个并置 truncated——**不静默截断**）。
const defaultTreeMaxNodes = 2000

// leafOf 读会话当前的叶（0 = 空路径 / 会话不存在时也返回 0）。
//
// 只在错误结果里用（"冲突时 leaf 未变"要如实回带当前叶）。读失败返回 0 而不是
// 报错：这条路径已经在返回业务结果，一个展示用的指针不该把结果变成错误。
func (s *Server) leafOf(sessionID string) int64 {
	if s.st == nil {
		return 0
	}
	leaf, err := s.st.LeafSeq(sessionID)
	if err != nil {
		return 0
	}
	return leaf
}

// toProtocolTree 把内核树骨架转成 wire 形态。
func toProtocolTree(t agent.Tree) protocol.ChatTreeResult {
	nodes := make([]protocol.TreeNode, len(t.Nodes))
	for i, n := range t.Nodes {
		nodes[i] = protocol.TreeNode{
			Seq: n.Seq, ParentSeq: n.ParentSeq, Kind: n.Kind, Turn: n.Turn,
			Summary: n.Summary, Children: n.Children, OnPath: n.OnPath,
			Orphan: n.Orphan, Files: n.Files, ChildFiles: n.ChildFiles,
			External: n.External, Shadowed: n.Shadowed,
		}
	}
	return protocol.ChatTreeResult{LeafSeq: t.LeafSeq, Nodes: nodes, Total: t.Total, Truncated: t.Truncated}
}

// errNotPersisted 是「没有存储」的统一人话（setLeaf 与文件还原共用）。
var errNotPersisted = errors.New("未启用持久化")

// ResumePendingRestores 补做上次没收尾的文件回滚（崩溃自愈，docs §C.5）。
//
// 场景：④ 文件已还原、⑥ leaf 未移动之间进程被杀。用户看到旧时间线 + 新文件；
// 重试 files:true 会因 hash 不符报冲突（磁盘已经是还原后的内容）。这里在启动时
// 把 leaf 补移过去——文件已经对了，只差指针。
//
// 只移指针、**不碰文件**：文件状态是崩溃前那次还原的结果，重放文件操作既没必要
// 也有风险（用户可能在两次启动之间又改了文件）。
//
// 失败只记日志（自愈是缓解，不是启动前提）；成功则删意图日志。
func (s *Server) ResumePendingRestores() {
	if s.st == nil {
		return
	}
	intents, err := s.st.LoadRestoreIntents()
	if err != nil {
		log.Printf("读未收尾的文件回滚失败（跳过自愈）: %v", err)
		return
	}
	for _, it := range intents {
		target := it.CutSeq
		if it.CutKind == "rewind" {
			// rewind 的落点 = parent(anchor)；读不到父指针（锚点已被清掉）时跳过。
			parent, perr := s.st.ParentSeq(it.SessionID, it.CutSeq)
			if perr != nil {
				log.Printf("补做文件回滚 %s：锚点 %d 已不存在，跳过（%v）", it.SessionID, it.CutSeq, perr)
				_ = s.st.ClearRestoreIntent(it.SessionID)
				continue
			}
			target = parent
		}
		if _, _, err := s.st.SetLeaf(it.SessionID, target); err != nil {
			log.Printf("补做文件回滚 %s 的 leaf 移动失败（保留意图日志）: %v", it.SessionID, err)
			continue
		}
		if err := s.st.ClearRestoreIntent(it.SessionID); err != nil {
			log.Printf("清意图日志失败（下次启动会重复补做一次，无害）: %v", err)
		}
		log.Printf("已补做上次未收尾的文件回滚：会话 %s → leaf %d（%d 个文件当时已还原）",
			it.SessionID, target, len(it.Written))
	}
}
