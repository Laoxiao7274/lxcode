// 派发卡跨「历史重载」保留子会话 id 的**纯函数**（不 import Compose / Android）。
//
// 为什么需要它：历史里只有消息（assistant 的 agent_dispatch 调用 + tool 结果），
// **没有 dispatchStart 事件**。子会话 id 的来源：
//   ① tool 结果末尾的 `[子会话 id: …]` 提示行——派发**结束后**才有；
//   ② 实时 chat.dispatchStart 的 session_id——重开会话/重连时收不到；
//   ③ 窗口外的派发结果正文剥出的 id（parseHistoryBlocks 的 orphanChildIds 喂给
//      mergeKnownChildIds 的 previous）——派发卡被 tail 窗口截掉时，①的配对目标
//      不在窗口里，这条是回放占位卡补 id 的最后一道兜底（不编造：提示行缺失不动）。
// 于是"派发正在跑的时候打开这个会话"会出现一张既没有结果、也没有子会话 id 的卡：
// 用户在主时间线上看不到任何能点进子会话的入口（问题 2 的另一半）。
//
// 解：重载历史时把**上一份块列表里同一 dispatch_id 的子会话 id 合并回来**（用户
// 之前看到过实时卡/带结果的卡，这份记忆不该被一次重载抹掉）。这不编造任何 id：
// 只在「同一个 dispatch_id」且「新卡没有 id」时沿用旧值。
package com.moyunteng.lxcode.remote.model

/**
 * 把 [previous] 里已知的子会话 id 合并进 [fresh] 的派发卡。
 *
 * 匹配键 = dispatchId（实时卡与历史卡同源，都是工具调用 id）；dispatchId 为空的
 * 卡不参与合并（没有归属键，按位置猜会把 A 的子会话挂到 B 上——宁可空着）。
 */
fun mergeKnownChildIds(
    fresh: List<ThreadBlock>,
    previous: List<ThreadBlock>,
): List<ThreadBlock> {
    val known = previous.filterIsInstance<ThreadBlock.Dispatch>()
        .filter { it.dispatchId.isNotEmpty() && it.sessionId.isNotEmpty() }
        .associate { it.dispatchId to it.sessionId }
    if (known.isEmpty()) return fresh
    return fresh.map { b ->
        if (b is ThreadBlock.Dispatch && b.sessionId.isEmpty() && b.dispatchId.isNotEmpty()) {
            known[b.dispatchId]?.let { b.copy(sessionId = it) } ?: b
        } else {
            b
        }
    }
}

/** 续跑提示行的解析（对齐桌面端 history.ts 的 SUB_SESSION_HINT）。 */
private val SUB_SESSION_HINT = Regex("\\[子会话 id:\\s*([^\\]\\s]+)[^\\]]*\\]")

/** 拆分 dispatch 结果：结论正文 + 子会话 id（没有提示行时 id 为空串）。 */
fun splitDispatchResult(raw: String): Pair<String, String> {
    val m = SUB_SESSION_HINT.find(raw) ?: return raw.trimEnd() to ""
    val body = (raw.substring(0, m.range.first) + raw.substring(m.range.last + 1)).trimEnd()
    return body to m.groupValues[1]
}
