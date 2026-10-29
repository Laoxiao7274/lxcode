package tools

import (
	"context"
	"path/filepath"
)

// 会话工作目录经 ctx 注入（agent 在每轮工具循环开始时快照挂上）：
// 项目会话的工具不落在后端进程目录里——相对路径、bash 默认目录、
// search 默认根全部按项目根解析。多会话并发天然安全（每会话独立 ctx，
// 没有全局可变状态），这是相对「注册表挂工作目录」方案的根本优势。

// workDirKey 是 ctx 键（空 struct 零值键，避免与其他 WithValue 碰撞）。
type workDirKey struct{}

// WithWorkDir 把会话工作目录放进 ctx。空串 = 不注入（等价于旧版行为：
// 相对路径由 OS 按进程当前目录解析）。
func WithWorkDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, workDirKey{}, dir)
}

// WorkDir 取 ctx 里的工作目录；空串 = 未注入。
func WorkDir(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	dir, _ := ctx.Value(workDirKey{}).(string)
	return dir
}

// resolveToolPath 把工具参数里的相对路径解析到会话工作目录：
//   - 绝对路径：原样；
//   - 相对路径 + 已注入工作目录：join 工作目录（绝对化）；
//   - 相对路径 + 未注入：原样返回——最终解析由 OS 按进程目录完成，
//     与注入机制之前的旧行为逐字节一致。
//
// 返回给模型的文案仍用模型给的原始形式（往返一致）；这里只负责
// 系统调用边界上的路径落地。
func resolveToolPath(ctx context.Context, p string) string {
	if p == "" {
		return p
	}
	wd := WorkDir(ctx)
	if wd == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(wd, p)
}
