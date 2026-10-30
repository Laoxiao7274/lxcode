package tools

import (
	"context"
	"sync"
)

// 每会话注入态：todo 清单的写回口与技能目录**经 ctx 注入**，不再挂在注册表上。
//
// 为什么必须 ctx 注入（2026-09-22 子会话改造）：注册表是进程级单例，而
// todo 清单与技能目录都是**会话级**状态——挂在注册表上意味着"最后一个设置的人
// 赢"。子 Agent 过去只是主会话里的一段临时上下文，所以那个全局态碰巧没暴露问题；
// 一旦子 Agent 变成**独立会话**（自己的历史、自己的 todo、自己的技能白名单），
// 父子会互相踩：子会话一建就把父会话的 todo sink 顶掉（清单串台）、子会话的技能
// 目录会污染父会话（原来的 save/restore hack 就是被这件事逼出来的补丁）。
//
// 与 workdir.go 同一套机制与理由：每会话独立 ctx，没有全局可变状态，
// 多会话（父/子）并存天然安全。

// todoSinkKey / skillSourceKey 是 ctx 键（空 struct 零值键，避免碰撞）。
type todoSinkKey struct{}
type skillSourceKey struct{}

// WithTodoSink 把 todo 清单的写回口放进 ctx（会话持有清单状态并广播事件）。
func WithTodoSink(ctx context.Context, fn TodoWriteFn) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, todoSinkKey{}, fn)
}

// todoSinkFrom 取 ctx 里的清单写回口（nil = 未注入——清单只回显不入会话状态）。
func todoSinkFrom(ctx context.Context) TodoWriteFn {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(todoSinkKey{}).(TodoWriteFn)
	return fn
}

// sessionIDKey 是会话 id 的 ctx 键。
type sessionIDKey struct{}

// WithSessionID 把当前会话 id 放进 ctx（agent 每轮开始时快照挂上）。
//
// 为什么也走 ctx 而不是注册表：后台任务要记**归属会话**（Spec.SessionID），
// 唤醒投递按它找回会话；注册表是进程级单例，多会话并发时"最后一个设置的人赢"，
// 任务会被记到别的会话名下——与 workdir/todo sink 同一条理由。
func WithSessionID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDKey{}, id)
}

// SessionID 取 ctx 里的会话 id；空串 = 未注入（无存储模式或单测直调）。
func SessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(sessionIDKey{}).(string)
	return id
}

// ownerSessionIDKey 是时间线归属的 ctx 键。
type ownerSessionIDKey struct{}

// WithOwnerSessionID 把**时间线归属**（顶层会话 id）放进 ctx。
//
// 为什么需要它：子 Agent 是独立会话，它起的后台任务记的归属会话是子会话；但用户
// 在父会话里看着那条时间线，唤醒通告也只有投给父会话才有人能行动（子会话不进侧栏，
// 投给它等于投给一个没人看的会话）。与 WithSessionID 同一套 ctx 注入理由。
func WithOwnerSessionID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ownerSessionIDKey{}, id)
}

// OwnerSessionID 取 ctx 里的时间线归属；空串 = 未注入（调用方回落 SessionID）。
func OwnerSessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(ownerSessionIDKey{}).(string)
	return id
}

// workspaceKey 是当前会话归属项目的 ctx 键。
type workspaceKey struct{}

// WithWorkspace 把当前会话归属的项目 id 放进 ctx（session_search 的默认扫描
// 范围「当前项目」取它）。会话装配路径注入（runTurn 快照，与 WithWorkDir /
// WithSessionID 同一批），子会话经 AttachTo 恢复出**自己行上的** workspace
//（CreateChild 拷贝父的值），所以子 Agent 拿到的也是正确的项目 id——
// 不在 dispatch 里特判。
//
// 空串也注入（区别于 WithSessionID 的空值跳过）：未分组会话的「当前项目」
// 不存在，session_search 据此退回全库扫描（语义：未分组会话本就不属于任何
// 项目，限定「当前项目」无从谈起）——这是明确语义，不是「没注入」。
func WithWorkspace(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, workspaceKey{}, id)
}

// Workspace 取 ctx 里的归属项目 id；空串 = 未分组会话或未注入（两者都按
// 「没有当前项目」处理——session_search 退回全库）。
func Workspace(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(workspaceKey{}).(string)
	return id
}

// WithSkillSource 把技能目录放进 ctx（本轮 Agent 白名单内的技能）。
func WithSkillSource(ctx context.Context, fn SkillSourceFn) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, skillSourceKey{}, fn)
}

// skillSourceFrom 取 ctx 里的技能目录（nil = 本轮没有技能可读）。
func skillSourceFrom(ctx context.Context) SkillSourceFn {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(skillSourceKey{}).(SkillSourceFn)
	return fn
}

// readStateKey 是「本会话读过哪些文件」的 ctx 键。
type readStateKey struct{}

// ReadState 记本会话的读取观测（read_file 的重复读去重与 edit 的
// 「改前先读」提示共用同一份记录）。
//
// 为什么是会话级而不是进程级（与 todo sink / 技能目录同一条理由）：
// 注册表是进程级单例，父子会话并存时「读到过没有」必须各算各的——
// 父会话读过的文件不能替子会话满足前置条件，否则子 Agent 会凭父会话的
// 观测去改一个它自己没看过的文件。
//
// 并发：同一会话内工具调用可能并行（一条 assistant 消息里的多个调用），
// 所以自带互斥；跨会话天然隔离（各持一个实例）。
type ReadState struct {
	mu   sync.Mutex
	step int
	last map[string]ReadObservation // 键 = 绝对路径
}

// ReadObservation 是一次读取的观测快照：判「文件是否变过」用它，判
// 「是不是同一区间」用 Offset/Limit。
type ReadObservation struct {
	// Size / ModUnixNano 是读取那一刻的 stat 结果（判「未被修改」的廉价判据：
	// 同一会话内同一纳秒被改回原大小几乎不可能，而内容 hash 要多读一遍全文）。
	Size        int64
	ModUnixNano int64
	// Offset / Limit 是这次读取用的分页参数（已归一：offset 从 1 起、
	// limit 已按默认值/上限收敛）——只有完全相同才回摘要，不同区间照常返回全文。
	Offset int
	Limit  int
	// Step 是第几次 read_file 调用（会话内自增，只用于回话里的「上次读取于第 N 步」）。
	Step int
	// TotalLines 是文件总行数（回摘要时告诉模型「共 M 行」，它才知道
	// 要不要换个区间读别的部分）。
	TotalLines int
}

// NewReadState 创建一个空会话读取记录（agent 每轮开始时注入 ctx）。
func NewReadState() *ReadState {
	return &ReadState{last: map[string]ReadObservation{}}
}

// WithReadState 把会话读取记录放进 ctx（nil = 不注入——纯内存模式/单测直调，
// 那时不去重，行为与改动前逐字节一致）。
func WithReadState(ctx context.Context, st *ReadState) context.Context {
	if st == nil {
		return ctx
	}
	return context.WithValue(ctx, readStateKey{}, st)
}

// readStateFrom 取 ctx 里的会话读取记录（nil = 未注入）。
func readStateFrom(ctx context.Context) *ReadState {
	if ctx == nil {
		return nil
	}
	st, _ := ctx.Value(readStateKey{}).(*ReadState)
	return st
}

// NextStep 分配本会话的下一个读取步号（0 = 未注入，不记步）。
func (s *ReadState) NextStep() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.step++
	return s.step
}

// Peek 取某文件的最后一次**交付全文**的观测（edit 的前置提示与 read_file
// 的重复读判定都读它）；ok=false = 本会话还没读过这个文件。
func (s *ReadState) Peek(path string) (ReadObservation, bool) {
	if s == nil {
		return ReadObservation{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	obs, ok := s.last[path]
	return obs, ok
}

// Record 记下这次**交付了全文**的读取（回摘要的那次不记——它没有交付内容，
// 记了会把「上次读取于第 N 步」改成一个没给出任何内容的步号）。
func (s *ReadState) Record(path string, obs ReadObservation) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[string]ReadObservation{}
	}
	s.last[path] = obs
}
