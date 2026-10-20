// 子会话历史重载的保留规则 + 派发卡终态判定（纯 JVM 单测）。
//
// 钉住用户报的两个现象的根因（与桌面端 frontend/tests/child-stream-history.test.mjs、
// child-todo-visibility.test.mjs 同源）：
//  ① 子 Agent 正文输出后消失：assistant 整轮结束才落库，正在流式的正文不在
//     chat.history 里；子会话快照的 busy 恒 false（现建运行时 + AttachTo）——
//     重载时 `clear() + addAll(parsed)` 会把用户看到的文字整段抹掉。
//  ② 任务列表有时不显示：空 todos 快照（omitempty）不等于「没有清单」；回放占位卡
//     （done + isError + 中断文案）不是终态，不该挡掉迟到的 todo.updated。
package com.moyunteng.lxcode.remote.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ChildHistoryMergeTest {

    private fun assistant(content: String, streaming: Boolean = false) =
        ThreadBlock.Assistant(content = content, reasoning = null, reasoningMs = 0, model = "", streaming = streaming)

    private fun user(text: String) = ThreadBlock.User(text)

    private fun todos(vararg pairs: Pair<String, String>) =
        pairs.map { TodoItem(it.first, it.second) }

    // ===== ① 流式尾部 =====

    @Test
    fun liveTailSurvivesReload() {
        val previous = listOf(user("任务说明书"), assistant("正在写结论", streaming = true))
        val parsed = listOf(user("任务说明书"))
        val merged = mergeChildLiveTail(parsed, previous)
        assertEquals(2, merged.size)
        assertEquals("正在写结论", (merged[1] as ThreadBlock.Assistant).content)
        assertTrue("接回来的尾巴必须仍是 streaming（后续 delta 续写同一块）", (merged[1] as ThreadBlock.Assistant).streaming)
    }

    @Test
    fun persistedReplyIsNotDuplicated() {
        // 快照已含这条回复（append 之后、done 之前重载）
        val previous = listOf(user("任务说明书"), assistant("第一段", streaming = true))
        val parsed = listOf(user("任务说明书"), assistant("第一段"))
        val merged = mergeChildLiveTail(parsed, previous)
        assertEquals("同一条回复不许出现两遍", 2, merged.size)
        assertEquals("第一段", (merged[1] as ThreadBlock.Assistant).content)
    }

    @Test
    fun snapshotPrefixKeepsLongerLocalText() {
        // 快照落后几个 delta（本地领先）
        val previous = listOf(user("任务说明书"), assistant("ABCD", streaming = true))
        val parsed = listOf(user("任务说明书"), assistant("AB"))
        val merged = mergeChildLiveTail(parsed, previous)
        assertEquals(2, merged.size)
        assertEquals("ABCD", (merged[1] as ThreadBlock.Assistant).content)
    }

    @Test
    fun snapshotLongerThanLocalWins() {
        // 本地落后（快照里有更多已落库的字）：不许把已落库的文字吞回去
        val previous = listOf(user("任务说明书"), assistant("AB", streaming = true))
        val parsed = listOf(user("任务说明书"), assistant("ABCD"))
        val merged = mergeChildLiveTail(parsed, previous)
        assertEquals("ABCD", (merged[1] as ThreadBlock.Assistant).content)
    }

    @Test
    fun nonStreamingTailIsNotCarriedOver() {
        // 已定格的回复由快照重建（否则重复）
        val previous = listOf(user("任务说明书"), assistant("上一轮结论"))
        val parsed = listOf(user("任务说明书"), assistant("上一轮结论"))
        val merged = mergeChildLiveTail(parsed, previous)
        assertEquals(2, merged.size)
    }

    @Test
    fun emptyPreviousIsNoop() {
        val parsed = listOf(user("任务说明书"))
        assertEquals(parsed, mergeChildLiveTail(parsed, emptyList()))
    }

    // ===== ② 清单 =====

    @Test
    fun emptySnapshotKeepsPreviousTodos() {
        val prev = todos("定位" to "done", "修复" to "active")
        assertEquals(prev, mergeChildTodoList(emptyList(), prev))
        assertEquals("null 快照同样沿用上一份", prev, mergeChildTodoList(null, prev))
    }

    @Test
    fun snapshotTodosWin() {
        val snapshot = todos("定位" to "done", "修复" to "done")
        val prev = todos("定位" to "active")
        assertEquals(snapshot, mergeChildTodoList(snapshot, prev))
    }

    @Test
    fun noTodosAnywhereIsNull() {
        assertNull(mergeChildTodoList(null, null))
        assertNull(mergeChildTodoList(emptyList(), emptyList()))
    }

    // ===== ③ 派发卡终态判定 =====

    @Test
    fun interruptedPlaceholderIsNotFinal() {
        // 占位卡不是终态：会话仍在跑时清单必须能写上去（否则「任务列表有时不显示」）
        assertFalse(isDispatchFinal(done = true, isError = true, conclusion = DISPATCH_INTERRUPTED))
    }

    @Test
    fun completedSuccessIsFinal() {
        assertTrue(isDispatchFinal(done = true, isError = false, conclusion = "结论"))
    }

    @Test
    fun realFailureIsFinal() {
        assertTrue(isDispatchFinal(done = true, isError = true, conclusion = "错误: 端点 500"))
    }

    @Test
    fun runningCardIsNotFinal() {
        assertFalse(isDispatchFinal(done = false, isError = false, conclusion = ""))
    }
}
