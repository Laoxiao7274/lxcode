// 文件快照的抓取接缝（docs/session-tree.md §C.2）。
//
// **叶子包**：这里不 import store/agent——工具层不知道快照长什么样、落在哪张表，
// 只知道「写之前给我一个句柄，写成功后 Commit(afterHash, size)，失败 Abort()」。
//
// 为什么走 ctx 注入（与 WithTodoSink / WithSkillSource / WithAttachmentSave 同一套
// 机制与理由）：注册表是进程级单例，而快照归属（owner 会话 / 锚点 seq）是**会话级**
// 状态——父子会话会互相踩。
//
// 为什么时机是「工具 Exec 内部、真正落盘之前」：那个位置天然满足三条既有纪律——
//
//	① 在权限门之后（被拒绝的调用根本不会进 Exec，所以不会记下「一次没发生的写」）；
//	② 在 atomicfile.Write 之前（edit 已经 ReadFile 拿到全文，before 内容零额外 IO）；
//	③ 不新增 agent→tools 的依赖（工具只调 ctx 里的注入口）。
package tools

import "context"

// SnapshotHandle 是一次写操作快照的句柄。
//
// 语义必须窄（这是能把它做成纯接缝的关键）：Commit 只收 (afterHash, size)，
// **before 的内容与 hash 由实现方（store）自己从 absPath 读**——工具不需要把文件
// 内容在内存里转手一遍（edit 已经有了，但让它多传一个 []byte 会让两个工具的写法
// 分叉）。
type SnapshotHandle interface {
	// Commit 在写成功之后调用：实现方据此落一行快照（afterHash = 写完的内容 hash）。
	// 失败**不该**让工具失败（快照是旁路记录，写文件本身已经成功）——调用方只记日志。
	Commit(afterHash string, size int64) error
	// Abort 在写失败/被中断时调用：不落行（避免「没写成却记了快照」）。
	// Commit 成功后必须是 no-op（defer h.Abort() 的写法才不会重复落行）。
	Abort()
}

// FileSnapshotter 在写之前调用：实现方读旧内容、算 before_hash、返回句柄。
type FileSnapshotter func(ctx context.Context, absPath string) (SnapshotHandle, error)

// snapshotterKey / snapshotAnchorKey / snapshotOwnerKey 是 ctx 键。
type snapshotterKey struct{}
type snapshotAnchorKey struct{}
type snapshotOwnerKey struct{}

// snapshotOwner 是快照归属（回滚切段用）。
type snapshotOwner struct {
	id  string // 回滚发起方（顶层会话 id）
	seq int64  // 父会话空间的 seq
}

// WithFileSnapshotter 把快照抓取器放进 ctx（agent 每轮开始时挂上）。
func WithFileSnapshotter(ctx context.Context, fn FileSnapshotter) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, snapshotterKey{}, fn)
}

// FileSnapshotterFrom 取 ctx 里的抓取器；nil = 未注入（**不抓，静默降级**——
// 纯内存模式/单测直调；这一轮的改动回滚不了，UI 在轮节点上标 ⚠️）。
func FileSnapshotterFrom(ctx context.Context) FileSnapshotter {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(snapshotterKey{}).(FileSnapshotter)
	return fn
}

// WithSnapshotAnchor 把本轮的**锚点 seq**（携带 tool_call 的 assistant 消息 seq）
// 放进 ctx。落 file_snapshots.seq——「这个快照是哪条消息引起的」用它追溯。
//
// 拿不到（Seq == 0：纯内存模式 / 落盘失败）→ 不抓快照（诚实降级）。
func WithSnapshotAnchor(ctx context.Context, seq int64) context.Context {
	if seq <= 0 {
		return ctx
	}
	return context.WithValue(ctx, snapshotAnchorKey{}, seq)
}

// SnapshotAnchor 取 ctx 里的锚点 seq（0 = 未注入 → 不抓）。
func SnapshotAnchor(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	seq, _ := ctx.Value(snapshotAnchorKey{}).(int64)
	return seq
}

// WithSnapshotOwner 把**回滚归属**放进 ctx（docs §C.2.1）。
//
//	ownerSessionID：顶层会话 id（顶层就是自己；子会话 = 它继承来的 child_owner_id）
//	ownerSeq：父会话空间的 seq ——
//	          顶层会话：就是本轮 assistant 消息自己的 seq；
//	          子会话：**父会话那条派发消息**的 seq（由 runDispatch 传进子会话的 runTurn）
//
// 与 WithSnapshotAnchor 的分工：锚点是**本会话自己的** seq（落 file_snapshots.seq，
// 追溯用），owner 是**父会话空间的** seq（落 owner_seq，回滚切段用）。两者在顶层
// 会话里相等，在子会话里不同——**这正是必须分成两个字段的原因**。
func WithSnapshotOwner(ctx context.Context, ownerSessionID string, ownerSeq int64) context.Context {
	if ownerSessionID == "" || ownerSeq <= 0 {
		return ctx
	}
	return context.WithValue(ctx, snapshotOwnerKey{}, snapshotOwner{id: ownerSessionID, seq: ownerSeq})
}

// SnapshotOwnerFrom 取 ctx 里的回滚归属；ok=false = 未注入。
func SnapshotOwnerFrom(ctx context.Context) (ownerSessionID string, ownerSeq int64, ok bool) {
	if ctx == nil {
		return "", 0, false
	}
	o, ok := ctx.Value(snapshotOwnerKey{}).(snapshotOwner)
	if !ok || o.id == "" || o.seq <= 0 {
		return "", 0, false
	}
	return o.id, o.seq, true
}
