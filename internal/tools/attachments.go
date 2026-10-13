// 附件共享（share_file）的每会话注入态：把文件登记进会话附件目录的写回口
// 与执行结果的收集器，**都经 ctx 注入**，不挂注册表。
//
// 为什么是 ctx 注入（与 workdir/todo sink/sessionID 同一条理由）：注册表是
// 进程级单例，而「附件落在哪个会话的目录里」是**会话级**状态——挂在注册表上
// 意味着父子会话互相踩（子会话共享的文件落进父会话的目录）。会话在开轮时把
// save 注入 ctx（agent/turn.go），工具执行时取用；收集器由内核按**每次工具
// 调用**新建（agent/tools.go），结果元数据随那条 tool 消息落库与发事件。

package tools

import (
	"context"
	"sync"

	"github.com/moyunteng/lxcode/internal/llm"
)

// AttachmentSaveFn 是「把一个文件登记进会话附件目录」的实现约定（会话注入）：
// 给净化前的原名与原始字节，落盘到 `<sessions>/attachments/<session-id>/`，
// 返回附件引用（相对 attachments 根的路径 + 展示元数据）。
// 实现方（agent）负责大小上限与穿越校验——工具层只管调。
type AttachmentSaveFn func(name string, data []byte) (llm.AttachmentRef, error)

// attachmentSaveKey 是 save 写回口的 ctx 键。
type attachmentSaveKey struct{}

// WithAttachmentSave 把附件保存口放进 ctx（会话持有目录与落盘实现）。
func WithAttachmentSave(ctx context.Context, fn AttachmentSaveFn) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, attachmentSaveKey{}, fn)
}

// AttachmentSave 取 ctx 里的保存口；nil = 未注入（无存储模式——调用方报
// 「会话存储未启用」的人话错误，不猜一个临时目录）。
func AttachmentSave(ctx context.Context) AttachmentSaveFn {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(attachmentSaveKey{}).(AttachmentSaveFn)
	return fn
}

// AttachmentCollector 收集一次工具执行期间产生的附件引用（内核按每次调用新建，
// 注入该次执行的 ctx）。并发安全：工具实现可能在内部起 goroutine。
type AttachmentCollector struct {
	mu   sync.Mutex
	refs []llm.AttachmentRef
}

// Add 登记一个附件引用（nil 接收器安全——未注入时工具照常执行，只是没有
// 元数据随结果流动）。
func (c *AttachmentCollector) Add(ref llm.AttachmentRef) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refs = append(c.refs, ref)
}

// Take 取走已收集的引用（调用后清空——每次调用的结果只带它自己那份）。
func (c *AttachmentCollector) Take() []llm.AttachmentRef {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	refs := c.refs
	c.refs = nil
	return refs
}

// attachmentCollectorKey 是收集器的 ctx 键。
type attachmentCollectorKey struct{}

// WithAttachmentCollector 把收集器放进 ctx（内核按每次工具调用注入）。
func WithAttachmentCollector(ctx context.Context, c *AttachmentCollector) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, attachmentCollectorKey{}, c)
}

// AttachmentCollectorFrom 取 ctx 里的收集器；nil = 未注入（单测直调等场景
// ——工具照常执行，只是没有元数据随结果流动）。
func AttachmentCollectorFrom(ctx context.Context) *AttachmentCollector {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(attachmentCollectorKey{}).(*AttachmentCollector)
	return c
}
