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
