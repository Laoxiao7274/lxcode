// 读图（read_image）的每会话注入态：视觉能力门控与「读到的图片引用」收集器，
// 都经 ctx 注入，不挂注册表。
//
// 为什么是 ctx 注入（与 workdir/attachment save/todo sink 同一条理由）：注册表是
// 进程级单例，而「当前会话用的模型支不支持视觉」「这张图归属哪次工具调用」都是
// **会话级/调用级**状态——挂在注册表上父子会话互相踩。会话在开轮时把门控注入
// ctx（agent/turn.go 按本轮模型声明判定），收集器由内核按**每次工具调用**新建
// （agent/tools.go），读到的引用挂到那条 tool 结果消息上（视觉请求的编码在
// llm 层请求构造瞬间完成——base64 绝不经这里）。

package tools

import (
	"context"
	"sync"

	"github.com/moyunteng/lxcode/internal/llm"
)

// visionCapableKey 是视觉能力门控的 ctx 键。
type visionCapableKey struct{}

// WithVisionCapable 把「当前模型是否声明视觉能力」放进 ctx（会话开轮时按本轮
// 实际会用的模型判定，与 modelFor 同一条解析路径）。未注入 = 不支持（fail-closed
// ——读一张发不出去的图只会浪费上下文，还可能让严格端点 400 拒收整轮）。
func WithVisionCapable(ctx context.Context, ok bool) context.Context {
	return context.WithValue(ctx, visionCapableKey{}, ok)
}

// VisionCapable 取 ctx 里的视觉门控；未注入 = false（fail-closed，见上）。
func VisionCapable(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	ok, _ := ctx.Value(visionCapableKey{}).(bool)
	return ok
}

// ImageCollector 收集一次工具执行期间读到的图片引用（内核按每次调用新建，
// 注入该次执行的 ctx）。并发安全：工具实现可能在内部起 goroutine。
type ImageCollector struct {
	mu   sync.Mutex
	refs []llm.ImageRef
}

// Add 登记一个图片引用（nil 接收器安全——未注入时工具照常执行，只是图不进
// 视觉通道）。
func (c *ImageCollector) Add(ref llm.ImageRef) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refs = append(c.refs, ref)
}

// Take 取走已收集的引用（调用后清空——每次调用的结果只带它自己那份）。
func (c *ImageCollector) Take() []llm.ImageRef {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	refs := c.refs
	c.refs = nil
	return refs
}

// imageCollectorKey 是收集器的 ctx 键。
type imageCollectorKey struct{}

// WithImageCollector 把收集器放进 ctx（内核按每次工具调用注入）。
func WithImageCollector(ctx context.Context, c *ImageCollector) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, imageCollectorKey{}, c)
}

// ImageCollectorFrom 取 ctx 里的收集器；nil = 未注入（单测直调等场景——
// 工具照常执行，只是图不进视觉通道）。
func ImageCollectorFrom(ctx context.Context) *ImageCollector {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(imageCollectorKey{}).(*ImageCollector)
	return c
}
