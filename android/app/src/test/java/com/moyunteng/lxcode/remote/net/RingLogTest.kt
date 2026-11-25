// RingLog 纯逻辑单测：上限淘汰（FIFO 环形）、时间戳格式、快照与版本号。
// clock 注入固定 LocalDateTime——时间戳断言不依赖真实墙钟（工程约定
// 「构建即验证」的纯 JVM 测试，与 ScreenParsingTest 同模式）。
package com.moyunteng.lxcode.remote.net

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.LocalDateTime

class RingLogTest {

    /** 固定时刻（时间戳格式断言的输入）。 */
    private fun at(h: Int, m: Int, s: Int, ms: Int = 0) = LocalDateTime.of(2026, 1, 1, h, m, s, ms * 1_000_000)

    @Test
    fun `上限淘汰 - 超容量丢最旧`() {
        val log = RingLog(capacity = 3, clock = { at(0, 0, 0) })
        log.add("e1")
        log.add("e2")
        log.add("e3")
        log.add("e4") // e1 被淘汰
        assertEquals(listOf("00:00:00 e2", "00:00:00 e3", "00:00:00 e4"), log.snapshot())
    }

    @Test
    fun `时间戳格式 - HH mm ss 补零`() {
        val log = RingLog(clock = { at(9, 5, 3) })
        log.add("start")
        assertEquals("09:05:03 start", log.snapshot().single())
    }

    @Test
    fun `快照 - 顺序为时间序且只读副本`() {
        val log = RingLog(clock = { at(23, 59, 59) })
        log.add("a")
        log.add("b")
        val snap = log.snapshot()
        assertEquals(2, snap.size)
        assertEquals("23:59:59 a", snap[0])
        assertEquals("23:59:59 b", snap[1])
        // 快照是副本：后续写入不影响已取出的快照
        log.add("c")
        assertEquals(2, snap.size)
    }

    @Test
    fun `版本号 - 每次写入自增`() {
        val log = RingLog()
        val v0 = log.version()
        log.add("x")
        assertTrue(log.version() > v0)
    }

    @Test
    fun `空日志 - isEmpty 为真快照为空`() {
        val log = RingLog()
        assertTrue(log.isEmpty())
        assertTrue(log.snapshot().isEmpty())
        log.add("x")
        assertFalse(log.isEmpty())
    }
}
