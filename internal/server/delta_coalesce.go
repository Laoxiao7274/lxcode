// 每客户端 delta 合帧（2026-12「远程第二客户端看不到实时流」修复，组件 A）。
//
// 背景：公网隧道（樱花frp wss）上的第二客户端收 delta 洪水（逐 token 一条、
// 一轮几百条）时，per-client 发送队列（256 条）被灌满 → fast-fail 关连接 →
// 前端 5s 重连 → 重拉 chat.history 快照 → 用户只见执行完的最终结果。帧数
// 才是队满的主因，治本 = 在广播路径上按客户端合帧：连续同键
//（session|kind|dispatch）的 delta 攒批（100ms 时间阈值 / 8KB 字节阈值先到
// 先发），合成**一条**同形状事件再入队——帧数缩两个量级，应答（含心跳）不再
// 被 delta 洪水堵死。
//
// 兼容纪律：
//   - 合并语义与前端归约器一致：reduceDelta 对同键 delta 就是文本拼接，合并帧
//     被归约的结果与逐帧归约一致（前端 DeltaCoalescer 也是同键拼接，两层叠加
//     只是再少几次 setState）；
//   - wire 形状不变（同 method + 同 DeltaParams 形状，只是 text 变长），协议
//     版本不动，CLI 等其它客户端透明；
//   - 不改事件序：其它事件作为屏障先冲刷该客户端的待合批缓冲再入队；同键
//     内部拼接、异键各占一位（跨会话 / text vs reasoning / 不同子会话互不串）。
//
// 对全部客户端启用（不分回环/远程）：隧道连接经 frpc 转发，后端看到的对端
// 地址恒是本机 127.0.0.1——按地址区分远程客户端不可行；合帧对本地前端只是
// 多一层 ≤100ms 的批，无语义影响。
package server

import (
	"encoding/json"
	"time"

	"github.com/moyunteng/lxcode/internal/protocol"
)

// deltaFlushMs 合帧的时间阈值（**毫秒数**，乘 time.Millisecond 使用——裸 int
// 直接传给 AfterFunc 是 100ns，等于关掉合帧）。
// 取值权衡：太小合帧收益缩水；太大流式延迟可感。100ms 与 rAF 帧界同量级，
// 观察端文本推进至多慢 100ms，且只影响 delta（工具行/忙闲等屏障事件即时）。
const deltaFlushMs = 100 * time.Millisecond

// deltaFlushBytes 合帧的字节阈值（按合并后的正文长度计）：单帧不能攒出无界大
// 帧——超大帧在慢隧道上会让后续所有帧（含心跳应答）排在它后面，把延迟问题
// 变相带回来。
const deltaFlushBytes = 8 * 1024

// isDeltaMethod 判定一个事件名是否走合帧路径（主域与助手域的 delta）。
func isDeltaMethod(m string) bool {
	return m == protocol.EventDelta || m == protocol.EventAssistantDelta
}

// deltaKey 合帧归属键：同键才允许拼接。三段与前端 DeltaCoalescer 的键一致
//（session|kind|dispatch）。注意 method 不进键：域由会话决定，同一会话的
// delta method 恒定（首条到达时记录）。
type deltaKey struct {
	sessionID  string
	kind       string
	dispatchID string
}

// pendingDelta 一条待发合并帧。
type pendingDelta struct {
	method string
	params protocol.DeltaParams
	bytes  int
}

// queuedDelta takeAll 的产出（合帧缓冲的不可变快照条目）。
type queuedDelta struct {
	method string
	params protocol.DeltaParams
}

// deltaCoalescer 是一个客户端的待合批缓冲。不自带锁：所有调用都发生在
// wsClient.bcMu 临界区内（broadcast / serve 应答 / AfterFunc 回调三条路径）。
type deltaCoalescer struct {
	pending map[deltaKey]*pendingDelta
	// order 是各键首条 delta 的到达序（冲刷按它逐条入队，与逐帧顺序一致）。
	order []deltaKey
}

func newDeltaCoalescer() *deltaCoalescer {
	return &deltaCoalescer{pending: map[deltaKey]*pendingDelta{}}
}

// feed 把一条 delta 并进缓冲：同键拼接文本，新键占位。返回 true = 该帧正文
// 已达字节阈值（调用方应立即冲刷整个缓冲——冲刷按到达序，先到的文本先发）。
func (dc *deltaCoalescer) feed(method string, p protocol.DeltaParams) bool {
	key := deltaKey{p.SessionID, p.Kind, p.DispatchID}
	if q, ok := dc.pending[key]; ok {
		q.params.Text += p.Text
		q.bytes += len(p.Text)
		return q.bytes >= deltaFlushBytes
	}
	dc.order = append(dc.order, key)
	dc.pending[key] = &pendingDelta{method: method, params: p, bytes: len(p.Text)}
	return len(p.Text) >= deltaFlushBytes
}

func (dc *deltaCoalescer) empty() bool { return len(dc.order) == 0 }

// takeAll 按到达序取出全部待发帧并清空缓冲（时间冲刷与屏障冲刷共用——全量
// 冲刷而不是半冲刷：屏障/定时都意味着「现在就该发了」，留半截只会把本批
// 拆成两次 emit，没有收益）。
func (dc *deltaCoalescer) takeAll() []queuedDelta {
	if len(dc.order) == 0 {
		return nil
	}
	out := make([]queuedDelta, 0, len(dc.order))
	for _, k := range dc.order {
		if q := dc.pending[k]; q != nil {
			out = append(out, queuedDelta{method: q.method, params: q.params})
		}
	}
	dc.pending = make(map[deltaKey]*pendingDelta)
	dc.order = nil
	return out
}

// marshalDeltaEvent 把一条（可能已合并的）delta 序列化成事件帧字节。形状与
// broadcast 的序列化路径完全一致（NewEvent → json.Marshal）。
func marshalDeltaEvent(method string, p protocol.DeltaParams) []byte {
	b, err := json.Marshal(protocol.NewEvent(method, p))
	if err != nil {
		// DeltaParams 全是 string 字段，序列化不会失败；防御性返回 nil，
		// 调用方（enqueueFrames）跳过空帧。
		return nil
	}
	return b
}

// enqueueDeltaCoalesced 是 broadcast 的 delta 入口：并进合帧缓冲（而非直接
// 入队），必要时武装时间冲Flush、按字节阈值立即冲刷。调用方持 s.mu
//（broadcast 临界区），本函数只碰 c.bcMu——锁序恒为 s.mu → bcMu，无反向。
func (c *wsClient) enqueueDeltaCoalesced(method string, p protocol.DeltaParams) {
	c.bcMu.Lock()
	if c.bc == nil {
		// 手工构造的客户端（个别测试没初始化合帧缓冲）：退回直发——nil 安全
		// 优先于强迫所有构造点记得初始化。
		c.bcMu.Unlock()
		c.enqueueBytes(marshalDeltaEvent(method, p))
		return
	}
	if c.bc.feed(method, p) {
		c.flushDeltasLocked()
	} else if c.bcTimer == nil {
		c.bcTimer = time.AfterFunc(deltaFlushMs, c.flushDeltaTimer)
	}
	c.bcMu.Unlock()
}

// flushDeltaTimer 是时间冲刷的 AfterFunc 回调（独立 goroutine——与 broadcast/
// serve 并发，靠 bcMu 串行化）。冲刷出的帧经 enqueueBytes 入队；连接已关时
// enqueueBytes 走 done 分支返回 false，天然无害（close 不清缓冲以避免与
// fast-fail 路径的锁重入，缓冲最迟在此被清掉）。
func (c *wsClient) flushDeltaTimer() {
	c.bcMu.Lock()
	c.bcTimer = nil
	if c.bc != nil && !c.bc.empty() {
		c.flushDeltasLocked()
	}
	c.bcMu.Unlock()
}

// flushDeltasLocked 冲刷缓冲（须持 c.bcMu）：停表、按到达序入队合并帧。
func (c *wsClient) flushDeltasLocked() {
	if c.bcTimer != nil {
		c.bcTimer.Stop()
		c.bcTimer = nil
	}
	for _, f := range c.bc.takeAll() {
		c.enqueueBytes(marshalDeltaEvent(f.method, f.params))
	}
}

// barrierDeltaFlush 是屏障路径：**任何**非 delta 帧入队之前调用（非 delta 广播
// 与请求应答）。缓冲为空时是一把锁 + 一次 len 判断的热路径开销。
func (c *wsClient) barrierDeltaFlush() {
	c.bcMu.Lock()
	if c.bc != nil && !c.bc.empty() {
		c.flushDeltasLocked()
	}
	c.bcMu.Unlock()
}
