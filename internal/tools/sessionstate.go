package tools

import (
	"context"
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
