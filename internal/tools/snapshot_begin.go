// 写工具的快照接入辅助：beginSnapshot + HashBytes。
//
// 为什么把「抓取 + 句柄」封成一个小助手：edit 与 write_file 的写法必须一致
// （一处漏 Commit、一处漏 Abort 就是「快照记了但没写成功」/「写成功但没记」）。
// 句柄的 Abort 在 Commit 之后是 no-op，所以 defer h.Abort() 的写法安全。
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// HashBytes 是 sha256 十六进制（快照 after_hash 与冲突检测共用同一份口径）。
// store 侧有一份同名实现（它不能 import tools，反之亦然）——两处都用标准库的
// 同一函数，不存在「口径漂移」的空间。
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// noopSnapshot 是未注入抓取器时的句柄（Commit/Abort 都是 no-op）。
type noopSnapshot struct{}

func (noopSnapshot) Commit(string, int64) error { return nil }
func (noopSnapshot) Abort()                     {}

// SnapshotSkipMarker 是「本次改动未记快照」的提示文案。工具层把它附到结果正文里，
// agent 侧按**同一句常量**识别「edit/write_file 的快照被跳过 → external++」。
//
// 为什么用一句人话而不是结构体：那要改 Def.Exec 的签名，波及全部 24 个工具。
// 提示同时是给用户看的（「这个改动回滚不到」是必须如实告知的事实）。
const SnapshotSkipMarker = "（本次改动未记快照，回滚不到）"

// skipNoter 是可选能力：句柄实现它就能告诉工具层「这次没抓到，为什么」。
// 不加进 SnapshotHandle 接口——noop 句柄不需要它，加进去等于逼所有实现都写一遍。
type skipNoter interface {
	SkipNote() string
}

// skipNote 取句柄的跳过提示（空 = 正常抓到 / 未注入）。
func skipNote(h SnapshotHandle) string {
	if n, ok := h.(skipNoter); ok {
		return n.SkipNote()
	}
	return ""
}

// beginSnapshot 在写之前抓快照。返回的句柄**恒非 nil**（调用方直接调，不用判空）：
//   - 未注入抓取器（纯内存模式/单测）→ noop 句柄（不抓，静默降级）；
//   - 抓取失败（文件不存在/读不动）→ noop 句柄 + 日志由实现方记（工具不因此失败：
//     「读不到旧内容」不该让用户的写失败，只是这一条改动回滚不了）。
func beginSnapshot(ctx context.Context, absPath string) SnapshotHandle {
	fn := FileSnapshotterFrom(ctx)
	if fn == nil {
		return noopSnapshot{}
	}
	h, err := fn(ctx, absPath)
	if err != nil || h == nil {
		return noopSnapshot{}
	}
	return h
}
