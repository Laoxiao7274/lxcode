// 观察端回填（chat.history 的 running_dispatches → 派发卡）的纯 JVM 单测。
//
// 场景与契约见 RunningDispatches.kt 头注（2026-12 用户报：派发由电脑发起、
// 安卓只是观察者——重进会话后回放卡没有子会话 id，点不开、进度事件无处可投）。
package com.moyunteng.lxcode.remote.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class RunningDispatchesTest {

    private fun info(
        dispatchId: String = "call_1",
        childSessionId: String = "child-9",
        agentName: String = "代码 Agent",
        task: String = "跑测试",
    ) = RunningDispatchInfo(dispatchId, childSessionId, agentName, task)

    private fun card(
        dispatchId: String = "call_1",
        sessionId: String = "",
        done: Boolean = true,
        isError: Boolean = true,
        conclusion: String = DISPATCH_INTERRUPTED,
    ) = ThreadBlock.Dispatch(
        agentName = "", task = "跑测试", conclusion = conclusion,
        sessionId = sessionId, done = done, isError = isError, dispatchId = dispatchId,
    )

    @Test
    fun placeholderCardIsRevivedWithChildSessionId() {
        // 回放占位卡（无 id）+ 快照 → 恢复成运行中 + 补上子会话 id（点得开了）
        val out = applyRunningDispatches(listOf(card()), listOf(info()), emptyList())
        val d = out.single() as ThreadBlock.Dispatch
        assertEquals(false, d.done)
        assertEquals(false, d.isError)
        assertEquals("child-9", d.sessionId)
        assertEquals("代码 Agent", d.agentName)
    }

    @Test
    fun windowMissingCardIsAppendedAsRunning() {
        // 回放窗口被截、卡不在 → 按快照追加一张运行中卡（进度事件有归属可投）
        val out = applyRunningDispatches(listOf(ThreadBlock.User("你好")), listOf(info()), emptyList())
        val d = out.last() as ThreadBlock.Dispatch
        assertEquals("call_1", d.dispatchId)
        assertEquals("child-9", d.sessionId)
        assertFalse(d.done)
    }

    @Test
    fun finalStateCardIsNeverRevived() {
        // 已收尾（成功 + 真实结论）的卡绝不被快照翻回运行中
        val finished = card(done = true, isError = false, conclusion = "全绿。")
        val out = applyRunningDispatches(listOf(finished), listOf(info()), emptyList())
        val d = out.single() as ThreadBlock.Dispatch
        assertTrue(d.done)
        assertFalse(d.isError)
        assertEquals("全绿。", d.conclusion)
    }

    @Test
    fun realFailureIsNeverRevived() {
        val failed = card(done = true, isError = true, conclusion = "错误: 端点 500")
        val out = applyRunningDispatches(listOf(failed), listOf(info()), emptyList())
        val d = out.single() as ThreadBlock.Dispatch
        assertTrue(d.done)
        assertEquals("错误: 端点 500", d.conclusion)
    }

    @Test
    fun existingChildSessionIdIsKeptOverSnapshot() {
        // 卡上已有 id（结果提示行/上一份块的权威值）不被快照覆盖
        val out = applyRunningDispatches(listOf(card(sessionId = "child-old")), listOf(info()), emptyList())
        assertEquals("child-old", (out.single() as ThreadBlock.Dispatch).sessionId)
    }

    @Test
    fun snapshotFillsWhenCardAndPreviousAreEmpty() {
        // 卡上没 id、上一份块也没有 → 快照的 id 补上（第三来源的兜底）
        val prev = listOf(card(dispatchId = "call_1", sessionId = "child-prev", done = false))
        val out = applyRunningDispatches(
            listOf(card(dispatchId = "call_1", sessionId = "", done = false, isError = false, conclusion = "")),
            listOf(info(childSessionId = "")),
            prev,
        )
        assertEquals("child-prev", (out.single() as ThreadBlock.Dispatch).sessionId)
    }

    @Test
    fun emptyRunningReturnsFreshUntouched() {
        // 老后端/空闲（null 或空数组）：行为与从前逐字节一致
        val fresh = listOf(card())
        assertEquals(fresh, applyRunningDispatches(fresh, null, emptyList()))
        assertEquals(fresh, applyRunningDispatches(fresh, emptyList(), emptyList()))
    }

    @Test
    fun entriesWithoutDispatchIdAreIgnored() {
        // 没有归属键的条目没法回填任何卡——丢弃，不猜
        val out = applyRunningDispatches(listOf(card()), listOf(info(dispatchId = "")), emptyList())
        val d = out.single() as ThreadBlock.Dispatch
        assertTrue(d.done)
        assertEquals(DISPATCH_INTERRUPTED, d.conclusion)
    }
}
