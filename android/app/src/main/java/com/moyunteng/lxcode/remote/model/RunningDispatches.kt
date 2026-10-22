// 观察端回填的**纯函数**（不 import Compose / Android——JVM 单测可直接钉住）。
//
// 场景（2026-12 安卓端报）：派发由电脑（桌面端）发起，安卓端只是观察者。安卓
// 重进会话走历史回放（parseHistoryBlocks），重建的派发卡**没有子会话 id**——
// dispatchStart 是本机不在场时错过的事件、结果提示行要派发结束才落库。后果：
//   ① 点不开子会话（canOpenChild 只看 sessionId）；
//   ② 带 dispatch_id 的 todo/子工具事件按卡反查归属——卡上没有 id 就无处可投
//      （「任务进度消失」的根因之一）。
//
// 修法：后端在 chat.history 应答带 running_dispatches——**此刻正在跑**的派发
// 快照（agent.Session 的运行中登记，跑完即注销，不会把已收尾的报成运行中）。
// 按 dispatch_id 处理回放结果：
//   - 卡不在（回放窗口被 tail 截掉）→ 追加一张运行中卡（进度事件有归属可投）；
//   - 卡是回放「中断」占位态 → 恢复成运行中并补 id/名字/任务（后端事实，比
//     「只认最后一张」的 busy 启发式强——多派发并行时每张都能恢复）；
//   - 卡是真终态 → 绝不碰（快照说"在跑"只对活着的派发成立）；
//   - 卡已在运行中 → 只补空缺。
package com.moyunteng.lxcode.remote.model

/** running_dispatches 的一个条目（wire 形态——[com.moyunteng.lxcode.remote.net.RealBackend]
 *  解析出 List&lt;RunningDispatchInfo&gt; 交给本文件的纯函数）。 */
data class RunningDispatchInfo(
    val dispatchId: String,
    val childSessionId: String,
    val agentName: String,
    val task: String,
)

/**
 * 把运行中派发快照应用进回放重建的块列表。
 *
 * @param fresh    历史快照重建出的块（mergeKnownChildIds 之后的产物）
 * @param running  chat.history 的 running_dispatches（null/空 = 老后端或空闲，原样返回）
 * @param previous 重载之前的块列表（同 dispatch_id 已知 id 的第三来源——与
 *                 [mergeKnownChildIds] 的合并口径一致：只补空值不覆盖）
 */
fun applyRunningDispatches(
    fresh: List<ThreadBlock>,
    running: List<RunningDispatchInfo>?,
    previous: List<ThreadBlock>,
): List<ThreadBlock> {
    if (running.isNullOrEmpty()) return fresh
    val previousIds = previous.filterIsInstance<ThreadBlock.Dispatch>()
        .filter { it.dispatchId.isNotEmpty() && it.sessionId.isNotEmpty() }
        .associate { it.dispatchId to it.sessionId }
    val out = fresh.toMutableList()
    for (r in running) {
        if (r.dispatchId.isEmpty()) continue
        val idx = out.indexOfFirst { it is ThreadBlock.Dispatch && it.dispatchId == r.dispatchId }
        if (idx < 0) {
            // 卡不在：回放窗口没截到它的历史（派发在窗口之前发起）——按快照补一张
            // 运行中卡。子会话 id 为空的条目（纯内存子会话）同样补卡：Agent 名与
            // 任务至少让用户知道有一个派发在跑（入口等 id 落库后自然出现）。
            out.add(
                ThreadBlock.Dispatch(
                    agentName = r.agentName.ifBlank { "子 Agent" },
                    task = r.task,
                    conclusion = "",
                    sessionId = r.childSessionId,
                    done = false,
                    isError = false,
                    dispatchId = r.dispatchId,
                ),
            )
            continue
        }
        val d = out[idx] as ThreadBlock.Dispatch
        val placeholder = isDispatchPlaceholder(d.done, d.isError, d.conclusion)
        if (d.done && !placeholder) continue // 真终态绝不翻回运行中
        out[idx] = d.copy(
            conclusion = if (placeholder) "" else d.conclusion,
            done = false,
            isError = false,
            // 子会话 id 的优先级：卡上已有的 > 快照的 > 上一份块的（不覆盖权威值）
            sessionId = d.sessionId.ifBlank { r.childSessionId.ifBlank { previousIds[r.dispatchId] ?: "" } },
            agentName = d.agentName.ifBlank { r.agentName },
            task = d.task.ifBlank { r.task },
        )
    }
    return out
}
