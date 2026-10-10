// dispatch 卡「卡头主区点下去该干什么」的纯判定——**不 import React**。
//
// 为什么单独抽成一个模块：这是**交互语义**（不是渲染细节），而交互语义恰恰最容易在
// 重构里被悄悄改坏。抽成纯函数后 node:test 能直接钉住它
// （tests/dispatch-card-click.test.mjs），不必起 DOM、不必装测试渲染器。
//
// 语义（2026-09-30 用户拍板去掉卡内折叠之后）：子 Agent 是**独立会话**
//（AGENTS.md §2.3——自己的历史与压缩检查点，**实时过程在它自己的标签页里**），
// 所以卡头主区只有**一个**操作 = 打开那个会话；卡里不再有"展开/收起"可回落。
//
// 但只有「有子会话 id」**且**「调用方接了 onOpenChild」时才真能打开，否则主区
// **不许**留一个点了没反应的按钮（把"不可发现"换成"坏了"更糟）——渲染成静态行，
// 由调用方按本判定决定（见 DispatchCard 的 button/div 分支）。两条缺一不可：
//   - 老数据/演示态没有子会话 id（历史里只有 agent_dispatch 的 arguments，
//     sessionId 是空——回放路径的 childSessionId 缺席）；
//   - 未接线的调用方（老测试、别处的复用）拿不到 onOpenChild。

/** 主区点击的结果：open = 打开子会话（独立工作区标签）；none = 进不去（渲染成静态行）。 */
export type DispatchPrimaryAction = "open" | "none";

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
  // 没有子会话的卡去开一个空 id 的标签；少判 hasOpenChild 会让未接线调用方
  // 点了没反应。
  if (sessionId !== "" && hasOpenChild) return "open";
  return "none";
}

/**
 * 卡头主区的 title / aria-label。
 *
 * 与 dispatchPrimaryAction 共用同一份判定（**不许各判一遍**——两处漂移的后果是
 * 提示语说"打开子会话"而点下去什么都没发生，比没有提示更误导）。
 * 可打开时把子会话 id 前 8 位写进提示：用户在一屏多张卡里要靠它区分是哪次派发。
 * 进不去时**如实说明为什么**（没有 id / 没接线），不许继续挂着"打开子会话"。
 */
export function dispatchPrimaryTitle(
  block: { sessionId?: string | null },
  hasOpenChild: boolean,
): string {
  if (dispatchPrimaryAction(block, hasOpenChild) === "open") {
    return `打开子会话 ${(block.sessionId ?? "").slice(0, 8)}（独立会话：实时过程与完整时间线都在它里面）`;
  }
  return "这次派发没有记下子会话 id（老数据/演示态）——进不去子会话";
}

/** 失败卡 hover / aria 里的错误原因截断上限（码点数）。
 *  错误正文可能是整段堆栈——title 属性塞几万字符浏览器自己也会截断，还拖 hover
 *  渲染；800 字符足够看清「为什么失败」，全文在子会话里。 */
export const DISPATCH_ERROR_TITLE_LIMIT = 800;

/**
 * 失败卡的错误原因提示（2026-10-09：失败卡原先只写「✗ 失败」，错误文本躺在
 * block.result 里从不展示——用户原话「失败的子代理，鼠标移入要能展示错误原因」）。
 *
 * 全文按**码点**截断（代理对从中间切开就是半个字符），超限注明完整原因的位置。
 * 没有错误文本（老数据 result 缺席 / 空白）返回 null——调用方回落到默认 title，
 * 不许渲染成「失败：」后面空空如也。
 */
export function dispatchErrorTitle(result: string | undefined | null): string | null {
  const text = (result ?? "").trim();
  if (text === "") return null;
  const chars = [...text];
  if (chars.length <= DISPATCH_ERROR_TITLE_LIMIT) return text;
  return chars.slice(0, DISPATCH_ERROR_TITLE_LIMIT).join("") + "…（完整原因在子会话里）";
}
