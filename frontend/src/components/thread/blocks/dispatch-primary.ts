// dispatch 卡「卡头主区点下去该干什么」的纯判定——**不 import React**。
//
// 为什么单独抽成一个模块：这是**交互语义**（不是渲染细节），而交互语义恰恰最容易在
// 重构里被悄悄改坏。抽成纯函数后 node:test 能直接钉住它
// （tests/dispatch-card-click.test.mjs），不必起 DOM、不必装测试渲染器。
//
// 语义（用户实测报的「点击现在还是展开和收缩，并不是新标签页」的修法）：
// 子 Agent 是**独立会话**（AGENTS.md §2.3——自己的历史与压缩检查点），所以卡头主区的
// 主操作 = 打开它，展开降级为次要操作（chevron 小按钮）。
//
// 但只有「有子会话 id」**且**「调用方接了 onOpenChild」时才真能打开，否则回落成切换
// 展开——两条缺一不可：
//   - 老数据/演示态没有子会话 id（历史里只有 agent_dispatch 的 arguments，
//     sessionId 是空），点了「打开」无从打开；
//   - 未接线的调用方（老测试、别处的复用）拿不到 onOpenChild。
// 这两种情况都必须**回落成切换展开**（与这一版之前的行为逐字一致）——点了没反应
// 比"点开的是展开"更糟，那是把"不可发现"换成"坏了"。

/** 主区点击的结果：open = 打开子会话（独立工作区标签）；toggle = 切换展开/收起。 */
export type DispatchPrimaryAction = "open" | "toggle";

/**
 * 卡头主区点击该干什么。
 *
 * @param block         dispatch 块（只读 sessionId 一个字段——纯函数不依赖整块形状）
 * @param hasOpenChild  调用方是否接了 onOpenChild（没接就没有"打开"这个能力）
 */
export function dispatchPrimaryAction(
  block: { sessionId?: string | null },
  hasOpenChild: boolean,
): DispatchPrimaryAction {
  // ?? "" 而不是直接判真值：sessionId 在类型上是 string，但回放路径（history.ts）
  // 与老事件里它可能是 undefined——统一归一成空串，别让两种"没有"走两条分支。
  const sessionId = block.sessionId ?? "";
  // 顺序无关紧要（两个条件都是必要条件），但**必须都判**：少判 sessionId 会让
  // 没有子会话的卡去开一个空 id 的标签；少判 hasOpenChild 会让未接线调用方点了没反应。
  if (sessionId !== "" && hasOpenChild) return "open";
  return "toggle";
}

/**
 * 卡头主区的 title / aria-label。
 *
 * 与 dispatchPrimaryAction 共用同一份判定（**不许各判一遍**——两处漂移的后果是
 * 提示语说"打开子会话"而点下去只是展开，比没有提示更误导）。
 * 可打开时把子会话 id 前 8 位写进提示：用户在一屏多张卡里要靠它区分是哪次派发。
 */
export function dispatchPrimaryTitle(
  block: { sessionId?: string | null },
  hasOpenChild: boolean,
): string {
  if (dispatchPrimaryAction(block, hasOpenChild) === "open") {
    return `打开子会话 ${(block.sessionId ?? "").slice(0, 8)}（独立会话：完整时间线）`;
  }
  // 回落成切换展开时，提示语必须如实说"展开/收起"——不许继续挂着"打开子会话"
  // 那是在骗用户（老数据点下去只会展开）。
  return "展开 / 收起子过程";
}