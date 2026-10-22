// 派发卡状态判定的纯 JVM 单测（工程约定「构建即验证」）。
//
// 钉住两条用户可见的契约：
//   * 运行中的子 Agent 必须有**入口**——`canOpenChild` 只看子会话 id，与有没有
//     工具调用无关（用户报「只输出文字的子 Agent 在时间线上看不到入口」）；
//   * 「中断占位态」可以被实时子事件翻回运行中，正常收尾的卡不许被翻（用户报
//     「子 Agent 在运行时总是红色，看起来像中断了」的根因是回放占位态没被翻回来）。
package com.moyunteng.lxcode.remote.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DispatchStateTest {

    // ===== 打开入口 =====

    @Test
    fun childSessionWithIdIsOpenable() {
        // 只输出文字、零工具调用的子 Agent：卡上照样进得去（入口不绑定工具调用）
        assertTrue(canOpenChild("child-9f3a2b1c"))
    }

    @Test
    fun missingChildSessionIsNotOpenable() {
        // 老数据 / 纯内存子会话没有 id：渲染成静态行，不给点了必失败的按钮
        assertFalse(canOpenChild(""))
    }

    // ===== 占位态（可以被翻回运行中）=====

    @Test
    fun interruptedPlaceholderIsRecognized() {
        assertTrue(isDispatchPlaceholder(done = true, isError = true, conclusion = DISPATCH_INTERRUPTED))
    }

    @Test
    fun completedSuccessIsNotPlaceholder() {
        assertFalse(isDispatchPlaceholder(done = true, isError = false, conclusion = "子 Agent 的结论"))
    }

    @Test
    fun realFailureIsNotPlaceholder() {
        // 真失败（有真实错误正文）不许被迟到事件翻成"还在跑"
        assertFalse(isDispatchPlaceholder(done = true, isError = true, conclusion = "错误: 端点 500"))
    }

    @Test
    fun runningCardIsNotPlaceholder() {
        // 已经在跑的卡（done=false）本来就不是占位态——翻它的判定必须跳过
        assertFalse(isDispatchPlaceholder(done = false, isError = false, conclusion = ""))
    }

    @Test
    fun emptyConclusionIsNotPlaceholder() {
        // 收尾但正文为空（"（子 Agent 没有产出文本结论）"之类）不是占位态
        assertFalse(isDispatchPlaceholder(done = true, isError = true, conclusion = ""))
    }

    // ===== 跨历史重载的子会话 id 记忆 =====

    private fun dispatch(dispatchId: String, sessionId: String, done: Boolean = true) =
        ThreadBlock.Dispatch(
            agentName = "researcher", task = "调研", conclusion = "结论",
            sessionId = sessionId, done = done, isError = false, dispatchId = dispatchId,
        )

    @Test
    fun reloadKeepsKnownChildSessionId() {
        // 历史重载建的卡没有子会话 id（结果还没落库）——沿用上一份块里同 dispatch_id 的
        val previous = listOf(dispatch("call_1", "child-abc"))
        val fresh = listOf(dispatch("call_1", ""))
        val merged = mergeKnownChildIds(fresh, previous)
        assertEquals("child-abc", (merged.single() as ThreadBlock.Dispatch).sessionId)
    }

    @Test
    fun reloadDoesNotOverrideFreshChildSessionId() {
        val previous = listOf(dispatch("call_1", "child-old"))
        val fresh = listOf(dispatch("call_1", "child-new"))
        val merged = mergeKnownChildIds(fresh, previous)
        assertEquals("child-new", (merged.single() as ThreadBlock.Dispatch).sessionId)
    }

    @Test
    fun reloadIgnoresCardsWithoutDispatchId() {
        // 没有归属键的卡不按位置猜（猜会把 A 的子会话挂到 B 上）
        val previous = listOf(dispatch("", "child-abc"))
        val fresh = listOf(dispatch("", ""))
        val merged = mergeKnownChildIds(fresh, previous)
        assertEquals("", (merged.single() as ThreadBlock.Dispatch).sessionId)
    }

    @Test
    fun reloadKeepsOtherBlockTypesUntouched() {
        val previous = listOf(dispatch("call_1", "child-abc"))
        val fresh = listOf(ThreadBlock.User("你好"), dispatch("call_1", ""))
        val merged = mergeKnownChildIds(fresh, previous)
        assertEquals(fresh.size, merged.size)
        assertTrue(merged[0] is ThreadBlock.User)
    }

    @Test
    fun placeholderTextIsStable() {
        // 文案是跨端契约（桌面端 history.ts 的 DISPATCH_INTERRUPTED 同一句）：
        // 改这句话会让回放卡与实时事件断链（占位态认不出来），单测在这里拦一下
        assertTrue(DISPATCH_INTERRUPTED.startsWith("这次派发在结束前中断了"))
    }

    // ===== 窗口外派发结果剥出的子会话 id（第三来源——孤儿结果提示行）=====

    @Test
    fun splitDispatchResultExtractsChildSessionId() {
        // 后端在 dispatch 结果末尾追加的续跑提示行（internal/agent/tools.go 的格式）
        val (body, sid) = splitDispatchResult("结论正文\n\n[子会话 id: sess-abc123 —— 只在这次没做完时填进 session 参数续跑]")
        assertEquals("结论正文", body)
        assertEquals("sess-abc123", sid)
    }

    @Test
    fun splitDispatchResultWithoutHintKeepsBody() {
        val (body, sid) = splitDispatchResult("（子 Agent 没有产出文本结论）")
        assertEquals("（子 Agent 没有产出文本结论）", body)
        assertEquals("", sid)
    }

    @Test
    fun orphanHintFillsPlaybackCardWithoutId() {
        // 回放窗口截掉了 dispatch 调用、只留下结果——剥出的 id 喂给 mergeKnownChildIds
        // 的 previous（RealBackend.applyHistory 的组装方式），占位卡据此补上 id
        val orphan = dispatch("call_9", "sess-xyz", done = false)
        val fresh = listOf(dispatch("call_9", ""))
        val merged = mergeKnownChildIds(fresh, listOf(orphan))
        assertEquals("sess-xyz", (merged.single() as ThreadBlock.Dispatch).sessionId)
    }
}
