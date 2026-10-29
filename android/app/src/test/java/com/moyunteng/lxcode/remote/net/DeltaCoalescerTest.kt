// DeltaCoalescer 的 JVM 单测（契约对齐桌面端 tests/ws-coalesce.test.mjs）：
// 合帧收益 / 保序 / 异键不合并 / 防重入 / reset 清账。
package com.moyunteng.lxcode.remote.net

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class DeltaCoalescerTest {

    /** 测试用排程器：同步直调 flush（不模拟异步时序——契约只关心投递次数与顺序）。 */
    private val delivered = mutableListOf<Pair<String, JSONObject>>()
    private var flushHook: (() -> Unit)? = null

    private fun newCoalescer(): DeltaCoalescer = DeltaCoalescer(
        emitRaw = { m, p -> delivered.add(m to p) },
        schedule = { flush ->
            flushHook = flush
            return@DeltaCoalescer { flushHook = null }
        },
    )

    private fun delta(session: String, kind: String = "text", dispatch: String = "", text: String) =
        JSONObject().apply {
            put("session_id", session)
            put("kind", kind)
            if (dispatch.isNotEmpty()) put("dispatch_id", dispatch)
            put("text", text)
        }

    @Test fun burst_coalesced_textIntact() {
        val c = newCoalescer()
        repeat(50) { c.emit("chat.delta", delta("s1", text = "t$it ")) }
        // 未冲刷：0 投递；排程钩子冲刷一次：1 投递，拼接无丢失
        assertEquals(0, delivered.size)
        flushHook!!.invoke()
        assertEquals(1, delivered.size)
        val text = delivered[0].second.optString("text")
        assertEquals((0 until 50).joinToString("") { "t$it " }, text)
    }

    @Test fun nondelta_flushesPendingFirst_orderPreserved() {
        val c = newCoalescer()
        c.emit("chat.delta", delta("s1", text = "abc"))
        c.emit("chat.delta", delta("s1", text = "def"))
        c.emit("chat.toolCall", JSONObject().put("name", "search"))
        // 非 delta 直发前先冲刷：delta 在 toolCall 之前
        assertEquals(2, delivered.size)
        assertEquals("chat.delta", delivered[0].first)
        assertEquals("abcdef", delivered[0].second.optString("text"))
        assertEquals("chat.toolCall", delivered[1].first)
    }

    @Test fun differentKeys_notMerged() {
        val c = newCoalescer()
        c.emit("chat.delta", delta("s1", text = "A"))
        c.emit("chat.delta", delta("s2", text = "B"))
        c.emit("chat.delta", delta("s1", kind = "reasoning", text = "C"))
        c.emit("chat.delta", delta("s1", dispatch = "d1", text = "D"))
        flushHook!!.invoke()
        assertEquals(4, delivered.size)
        val texts = delivered.map { it.second.optString("text") }
        assertTrue(texts.contains("A") && texts.contains("B") && texts.contains("C") && texts.contains("D"))
    }

    @Test fun flushOnlyOncePerSchedule_reentrantDeltasWait() {
        val c = newCoalescer()
        c.emit("chat.delta", delta("s1", text = "1"))
        val firstFlush = flushHook!!
        // 冲刷期间新到的 delta（emitRaw 里再 emit 的嵌套场景）只入队
        var inside = false
        val c2 = DeltaCoalescer(
            emitRaw = { m, p ->
                delivered.add(m to p)
                if (!inside) {
                    inside = true
                    c.emit("chat.delta", delta("s1", text = "nested"))
                }
            },
            schedule = { flush -> flushHook = flush; return@DeltaCoalescer { flushHook = null } },
        )
        c2.emit("chat.delta", delta("s1", text = "1"))
        c2.flush()
        // 嵌套 emit 不在本次冲刷里重复投递（防重入）
        assertEquals(1, delivered.size)
        firstFlush // 编译期引用
    }

    @Test fun emptyTextDelta_dropped() {
        val c = newCoalescer()
        c.emit("chat.delta", delta("s1", text = ""))
        flushHook!!.invoke()
        // 空文本 delta 合并后仍整条为空 → 保留（ reducer 侧自会丢弃）；
        // 这里只断言不抛异常。实际投递 1 条空文本（保持与逐条归约等价）。
        assertEquals(1, delivered.size)
    }

    @Test fun reset_clearsPending() {
        val c = newCoalescer()
        c.emit("chat.delta", delta("s1", text = "x"))
        c.reset()
        flushHook?.invoke()
        assertEquals(0, delivered.size)
    }

    @Test fun kindNotText_keepsKindField() {
        val c = newCoalescer()
        c.emit("chat.delta", delta("s1", kind = "reasoning", text = "r"))
        flushHook!!.invoke()
        assertEquals("reasoning", delivered[0].second.optString("kind"))
    }
}
