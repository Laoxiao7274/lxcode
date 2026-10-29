// 派发卡状态判定的**纯函数**（不 import Compose / Android——JVM 单测可直接钉住）。
//
// 这里管两件用户报过的事：
//  ① 「子 Agent 在运行时总是红色，看起来像中断了」——历史回放建卡时先按"中断"
//     定格（那时不知道它是死是活），随后到达的实时子事件必须能把它恢复成运行中；
//  ② 「在运行的子 Agent 没有工具调用就没有打开入口」——卡上能不能进子会话只看
//     **子会话 id 在不在**，与有没有工具调用、有没有子工具流毫无关系。
//
// 判定收窄的理由：只有"占位态"（done + isError + 结论就是那句占位文案）才可以被
// 翻回运行中。正常结束的卡（成功 / 真失败）绝不能被迟到事件改成运行中——那会把
// "已完成"说成"还在跑"。
package com.moyunteng.lxcode.remote.model

/** 历史回放里「派发在结束前中断」的占位结论（与桌面端 history.ts 的 DISPATCH_INTERRUPTED 同义）。 */
const val DISPATCH_INTERRUPTED =
    "这次派发在结束前中断了——历史里没有结果记录（重启前它没跑完）。"

/**
 * 这张卡是不是"回放占位态"（可以被实时子事件翻回运行中）。
 *
 * @param done / [isError] / [conclusion] 卡的三个字段（对齐 ThreadBlock.Dispatch）
 */
fun isDispatchPlaceholder(done: Boolean, isError: Boolean, conclusion: String): Boolean =
    done && isError && conclusion == DISPATCH_INTERRUPTED

/**
 * 卡上能不能进子会话：有子会话 id 就能（**与有没有工具调用无关**）。
 *
 * 桌面端的同一条判定是 dispatch-primary.ts 的 `sessionId !== "" && hasOpenChild`；
 * 安卓侧 onOpenChild 恒接线，所以只剩 id 这一条。
 */
fun canOpenChild(sessionId: String): Boolean = sessionId.isNotEmpty()

/**
 * 这张卡是不是**真终态**（迟到事件不许改）。
 *
 * 与 [isDispatchPlaceholder] 的分工：占位态虽然 `done=true`，但它表达的是「没有证据」
 * （历史里没有配对结果）——会话仍在跑时它会被实时子事件翻回运行中，之后的
 * `todo.updated` 也必须能写上去。只有**真终态**（成功收尾 / 带真实错误正文的失败）
 * 才该把迟到的清单快照挡在外面（终态以 dispatchEnd 的 final_todos 为准）。
 *
 * 漏了这条的表现就是用户报的「任务列表有时不显示」：刷新后历史先建出占位卡
 * （done + isError + DISPATCH_INTERRUPTED），子 Agent 继续跑并更新清单，而卡上的
 * N/M 永远停在初稿（甚至没有徽标）。
 */
fun isDispatchFinal(done: Boolean, isError: Boolean, conclusion: String): Boolean =
    done && !isDispatchPlaceholder(done, isError, conclusion)
