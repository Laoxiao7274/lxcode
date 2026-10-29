// 流式 delta 合帧（移植自桌面端 frontend/src/agent/ws/coalesce.ts，2026-12 渲染
// 性能 Top1 的安卓等价物）。
//
// 后端的 chat.delta 是 token 级（一轮几百条），每条都 post = RealBackend 每条一次
// 归约 + Compose 状态写 + LazyColumn 重组调度——流式渲染风暴的主力。合帧后同一帧
// 内到达的同键 delta 合并成一次投递。
//
// 契约（DeltaCoalescerTest 钉住，对齐桌面端 tests/ws-coalesce.test.mjs）：
// 1. 合帧收益：一次突发 N 条 delta → 投递次数远小于 N，文本拼接无丢失；
// 2. 保序：非 delta 事件投递前先冲刷挂起的 delta（先到先发）——done/toolCall
//    不能插到已到达文本的前面；
// 3. 异键不合并：不同会话 / 不同 kind（text vs reasoning）/ 子会话 dispatch
//    各占一位，互不串文本。
// 4. 每帧至多一次冲刷：delta 只入队并排程一次冲刷；冲刷进行中新到的 delta
//    只入队等下一拍（防重入）。
//
// 线程模型：调用方（LxWsClient）在**主线程**调用 emit/flush——队列无并发访问，
// 纯 Kotlin 无 Android 依赖 → 可 JVM 单测。
package com.moyunteng.lxcode.remote.net

import org.json.JSONObject

/** delta 合帧兜底间隔（毫秒）：主线程排程的冲刷（Handler.post 无帧回调可用）；
 *  16ms ≈ 一帧。 */
const val DELTA_COALESCE_MS = 16L

/**
 * 合帧器：delta 入队同键拼接，拍末一次投递（保序纪律在 [emit]）。
 *
 * @param emitRaw 原始投递口（主线程回调，语义与合帧前的 eventListener.onEvent 一致）。
 * @param schedule 拍末冲刷的排程器（可注入——测试用同步直调，生产用 Handler 延迟）。
 * @param cancel 取消已排程的冲刷（保序冲刷前取消，避免拍末重复投递）。
 */
class DeltaCoalescer(
    private val emitRaw: (method: String, params: JSONObject) -> Unit,
    private val schedule: (flush: () -> Unit) -> (() -> Unit),
) {
    /** 队列：key = `session\0kind\0dispatch`，值 = 合并中的 (method, params, text)。 */
    private val queue = LinkedHashMap<String, QueuedDelta>()
    private var cancelScheduled: (() -> Unit)? = null
    private var scheduled = false
    private var flushing = false

    private class QueuedDelta(
        val sessionId: String,
        val kind: String,
        val dispatchId: String,
        var text: String,
    )

    /** 事件入口：delta 走合帧，其他事件先冲刷挂起的 delta 再直发（保序）。 */
    fun emit(method: String, params: JSONObject) {
        if (method == "chat.delta") {
            enqueue(params)
            return
        }
        // 非 delta 事件先冲刷挂起的 delta 再发（保序纪律：先到先发，delta 合并
        // 不打乱它与其他事件的相对顺序——比如 done 前积压的文本必须先归约，
        // 否则 done 会把还在流式中的块定格成缺文）。
        flush()
        emitRaw(method, params)
    }

    /** delta 合帧入队（同键拼接，异键各占一位——不同会话/不同 kind/主子会话
     *  的 delta 互不合并）。 */
    private fun enqueue(params: JSONObject) {
        val key = "${params.optString("session_id")}\u0000${params.optString("kind", "text")}\u0000${params.optString("dispatch_id")}"
        val text = params.optString("text")
        val queued = queue[key]
        if (queued != null) {
            queued.text += text
        } else {
            queue[key] = QueuedDelta(
                sessionId = params.optString("session_id"),
                kind = params.optString("kind", "text"),
                dispatchId = params.optString("dispatch_id"),
                text = text,
            )
        }
        scheduleFlush()
    }

    /** 排程拍末冲刷：已排程就不重复排（一拍至多一次投递）。 */
    private fun scheduleFlush() {
        if (scheduled) return
        scheduled = true
        cancelScheduled = schedule { flush() }
    }

    /** 冲刷合帧队列：按到达序逐条投递（队列插入序 = 各键首条 delta 的到达序）。
     *  冲刷后队列清空；flushing 防重入（冲刷期间新 delta 只入队等下一拍）。 */
    fun flush() {
        cancelScheduled?.invoke()
        cancelScheduled = null
        scheduled = false
        if (flushing || queue.isEmpty()) return
        flushing = true
        val queued = queue.values.toList()
        queue.clear()
        try {
            for (q in queued) {
                val params = JSONObject()
                if (q.sessionId.isNotEmpty()) params.put("session_id", q.sessionId)
                if (q.kind != "text") params.put("kind", q.kind)
                if (q.dispatchId.isNotEmpty()) params.put("dispatch_id", q.dispatchId)
                params.put("text", q.text)
                emitRaw("chat.delta", params)
            }
        } finally {
            flushing = false
        }
    }

    /** 清账（断连语义）：挂起的 delta 没有投递对象了（会话已断，留着的文本
     *  只会在之后的一拍投给旧状态）。 */
    fun reset() {
        cancelScheduled?.invoke()
        cancelScheduled = null
        scheduled = false
        queue.clear()
    }
}
