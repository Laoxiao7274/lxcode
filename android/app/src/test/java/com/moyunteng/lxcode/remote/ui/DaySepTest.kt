// 日分隔线（daysep）的纯 JVM 单元测试（工程约定「构建即验证」，不引 robolectric）：
//   * daySepLabel：今天 / 昨天 / 更早日期三种标签分支（跨夜长会话的真实路径）；
//   * threadBlocksWithDaySeps：只在**带 localEpochMs 锚点**的 user 消息前插 DaySep、
//     连续锚点同标签不重复画、历史回放（无锚点）整段零插入——锚点缺失不编数，
//     是「wire/落库没有 per-message 时间戳」这一数据面事实的诚实呈现；
//   * 缓存往返：SessionCacheCodec 对 User.localEpochMs 的保留与缺席容忍
//    （老缓存 decode 不带 local_ts → null → 不画）。
// 注意：块渲染（DaySepBar Composable）是真机清单覆盖的 UI 面，这里钉纯逻辑。
package com.moyunteng.lxcode.remote.ui

import com.moyunteng.lxcode.remote.model.ThreadBlock
import com.moyunteng.lxcode.remote.model.daySepLabel
import com.moyunteng.lxcode.remote.net.SessionCacheCodec
import com.moyunteng.lxcode.remote.net.SessionCacheEntry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

class DaySepTest {

    // 固定「现在」做断言基准：2026-12-30 18:00 本机时区。
    private fun at(y: Int, mo: Int, d: Int, h: Int, mi: Int): Long =
        java.time.LocalDateTime.of(y, mo, d, h, mi)
            .atZone(ZoneId.systemDefault()).toInstant().toEpochMilli()

    private val now = at(2026, 12, 30, 18, 0)

    // ===== daySepLabel 三分支 =====

    @Test
    fun todayLabel() {
        assertEquals("今天 14:28", daySepLabel(at(2026, 12, 30, 14, 28), now))
    }

    @Test
    fun yesterdayLabel() {
        assertEquals("昨天 23:05", daySepLabel(at(2026, 12, 29, 23, 5), now))
    }

    @Test
    fun olderDateLabel() {
        assertEquals("12 月 26 日 09:00", daySepLabel(at(2026, 12, 26, 9, 0), now))
    }

    // ===== 插入规则 =====

    @Test
    fun insertsBeforeAnchoredUserMessageOnly() {
        val blocks = listOf(
            ThreadBlock.User("无锚点（历史回放）", emptyList()),
            ThreadBlock.Assistant("答", null, 0, "m"),
            ThreadBlock.User("实时发的", emptyList(), localEpochMs = at(2026, 12, 30, 14, 0)),
        )
        val out = threadBlocksWithDaySeps(blocks, nowMs = now)
        assertEquals(4, out.size)
        assertEquals(true, out[0] is ThreadBlock.User) // 无锚点前不插
        assertEquals(true, out[2] is ThreadBlock.DaySep) // 锚点前插
        assertEquals("今天 14:00", (out[2] as ThreadBlock.DaySep).label)
        assertEquals(true, out[3] is ThreadBlock.User)
    }

    @Test
    fun noAnchorNoInsert() {
        val blocks = listOf<ThreadBlock>(
            ThreadBlock.User("问", emptyList()),
            ThreadBlock.Assistant("答", null, 0, "m"),
            ThreadBlock.User("再问", emptyList()),
        )
        // 无任何锚点 → 原样返回（同一实例，避免无谓复制）
        assertEquals(true, threadBlocksWithDaySeps(blocks) === blocks)
    }

    @Test
    fun consecutiveAnchoredUsersSingleSep() {
        val t = at(2026, 12, 30, 14, 0)
        val blocks = listOf(
            ThreadBlock.User("一", emptyList(), localEpochMs = t),
            ThreadBlock.User("二", emptyList(), localEpochMs = t + 60_000),
        )
        val out = threadBlocksWithDaySeps(blocks, nowMs = t)
        assertEquals(3, out.size)
        assertEquals(true, out[0] is ThreadBlock.DaySep)
        assertEquals(true, out[1] is ThreadBlock.User)
        assertEquals(true, out[2] is ThreadBlock.User)
    }

    // ===== 缓存往返 =====

    @Test
    fun cacheRoundTripKeepsAnchor() {
        val ts = at(2026, 12, 30, 14, 0)
        val entry = SessionCacheEntry(
            sessionId = "s1",
            blocks = listOf(ThreadBlock.User("问", emptyList(), localEpochMs = ts)),
        )
        val back = SessionCacheCodec.decode("s1", SessionCacheCodec.encode(entry))
        val u = back.blocks[0] as ThreadBlock.User
        assertEquals(ts, u.localEpochMs)
    }

    @Test
    fun cacheWithoutAnchorDecodesNull() {
        val entry = SessionCacheEntry(
            sessionId = "s1",
            blocks = listOf(ThreadBlock.User("问", emptyList())),
        )
        val back = SessionCacheCodec.decode("s1", SessionCacheCodec.encode(entry))
        assertNull((back.blocks[0] as ThreadBlock.User).localEpochMs)
    }
}
