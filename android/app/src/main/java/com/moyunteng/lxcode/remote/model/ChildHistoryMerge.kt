// 子会话历史重载时的**保留**规则（纯函数，不 import Compose / Android——JVM 单测可直接钉住）。
//
// 用户报的两个现象（2026-12，与桌面端同源）：
//  ① 「子 Agent 正文输出后消失」：assistant 消息由后端**整轮结束**才落库
//     （internal/agent/turn.go 的 s.append 在 streamRound 返回之后），所以正在流式的
//     那段正文**永远不在 chat.history 里**；而子会话的 chat.history 是「现建运行时 +
//     AttachTo」（internal/server/session_ops.go 的 session()：子会话不进运行时缓存），
//     AttachTo 出来的 Session busy 恒 false。于是 applyChildHistory 一旦 `clear()` 再
//     `addAll(parsed)`，用户已经看到的流式正文就整段消失。
//  ② 「任务列表有时不显示」：chat.history 的 `todos` 是 omitempty（Go 侧空切片不上
//     wire）。重载时 todosArr 为空 = **未知**，不是「没有清单」——照旧清空块列表就会
//     把子会话视图里的 Todo 块一起抹掉。
//
// 两条保留都只在「同一条回复 / 同一份清单」上生效，不编造任何内容：
//   - 流式尾部按**正文关系**去重（相同 / 互为前缀 = 同一条回复，取较长的那份），
//     快照里没有这条回复才追加；
//   - 清单只在快照**没带**时才沿用上一份（带了就以快照为准——后端是事实源）。
package com.moyunteng.lxcode.remote.model

/**
 * 把上一份块列表里**还没落库的实时尾巴**接回重建结果。
 *
 * 口径只有一条：上一份的**尾块**是流式中的 assistant（[ThreadBlock.Assistant.streaming]）。
 * 只取尾块而不是「所有 streaming 块」：delta 开新块时会把前一块定格，流式块恒在末尾；
 * 多接就是重复。
 *
 * @param parsed   历史快照重建出来的块（[RealBackend.parseHistoryBlocks] 的产物）
 * @param previous 重载之前的块列表（含实时流）
 */
fun mergeChildLiveTail(
    parsed: List<ThreadBlock>,
    previous: List<ThreadBlock>,
): List<ThreadBlock> {
    val tail = previous.lastOrNull() as? ThreadBlock.Assistant ?: return parsed
    if (!tail.streaming) return parsed
    val lastParsed = parsed.lastOrNull() as? ThreadBlock.Assistant
    // 同一条回复（相同 / 互为前缀）：取较长的那份，并标回 streaming 让后续 delta 续写
    // 同一块（否则会另开一块，把一条回复劈成两半）。
    if (lastParsed != null && sameReply(lastParsed.content, tail.content)) {
        val content = if (tail.content.length >= lastParsed.content.length) tail.content else lastParsed.content
        val reasoning = if ((tail.reasoning?.length ?: 0) >= (lastParsed.reasoning?.length ?: 0)) {
            tail.reasoning ?: lastParsed.reasoning
        } else {
            lastParsed.reasoning
        }
        return parsed.dropLast(1) + lastParsed.copy(content = content, reasoning = reasoning, streaming = true)
    }
    return parsed + tail
}

/** 两条正文是不是**同一条回复**：完全相同，或一方是另一方的前缀（delta 只追加不修改）。 */
private fun sameReply(a: String, b: String): Boolean {
    if (a == b) return true
    if (a.isEmpty() || b.isEmpty()) return false
    return a.startsWith(b) || b.startsWith(a)
}

/**
 * 重载后的子会话清单：快照带了就以它为准，没带（null/空）就沿用上一份。
 *
 * 「没带」= 未知（wire 上 omitempty，空数组与键缺席同形），**不许**当成「清单被清空」
 * ——那正是「任务列表有时不显示」的另一半。返回 null = 两份都没有（不渲染计划条）。
 */
fun mergeChildTodoList(
    snapshot: List<TodoItem>?,
    previous: List<TodoItem>?,
): List<TodoItem>? = when {
    snapshot != null && snapshot.isNotEmpty() -> snapshot
    previous != null && previous.isNotEmpty() -> previous
    else -> null
}
