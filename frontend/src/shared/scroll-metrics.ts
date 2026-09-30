// 滚动判定（纯函数，零 DOM 依赖）：两个 UI 需求共用同一份判定，避免"两处分叉"。
//
// ① 「回到底部」按钮（主时间线 Thread 与子会话页 ChildSessionPage 共用同一份组件）：
//    贴底判定必须与 Thread 跟随状态机（stickyRef）**同源**——同一个常量、同一个函数。
//    各写一份的代价是阈值悄悄漂移，按钮会在"其实已经在底部"时还亮着，而这是用户
//    看得见的 bug（同一个东西两份实现，这个仓库反复踩过）。
// ② 标签条两侧的溢出渐隐方向 + 滚轮是否该被 strip 横向吃掉：方向由运行时 scrollLeft
//    决定，CSS 读不到（纯 CSS 只能画"永远两侧都渐隐"，那在没溢出时是假提示），
//    所以在这里算成 data 属性，样式只负责按方向画。

/** 贴底阈值（px）：Thread 的跟随状态机与「回到底部」按钮共用它。
 *  写第二个常量就是分叉的开始——阈值只在这里定义一次。 */
export const NEAR_BOTTOM_PX = 200;

/** 亚像素容差：滚动位置是浮点数，1px 以内的差值不算"还有内容/还能滚"。 */
const EDGE_EPSILON = 1;

/** 滚动位置三件套（HTMLElement 结构上就是这三个量，可直接把元素传进来）。 */
export interface ScrollMetrics {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}

/** 横向溢出三件套（同上，HTMLElement 结构上满足它）。 */
export interface OverflowMetrics {
  scrollLeft: number;
  scrollWidth: number;
  clientWidth: number;
}

/** 两侧是否还有没露出来的内容（渐隐画在有内容的那一侧）。 */
export interface OverflowEdges {
  left: boolean;
  right: boolean;
}

/** data-overflow 的取值（CSS 只认这四个）。 */
export type OverflowAttr = "none" | "left" | "right" | "both";

/** 非有限值一律当 0：NaN 参与比较会让所有判断为 false，
 *  表现为空容器上按钮常亮/渐隐常亮——比"当成没有内容"更糟。 */
function finite(value: number): number {
  return Number.isFinite(value) ? value : 0;
}

/** 距底距离（px）。不可滚（内容比容器短）时是 0——"已经在底部"。 */
export function distanceFromBottom(m: ScrollMetrics): number {
  return Math.max(0, finite(m.scrollHeight) - finite(m.scrollTop) - finite(m.clientHeight));
}

/** 是否贴底（含阈值内）。Thread 的 sticky 跟随与「回到底部」按钮**共用这一条**。 */
export function isNearBottom(m: ScrollMetrics): boolean {
  return distanceFromBottom(m) <= NEAR_BOTTOM_PX;
}

/** 「回到底部」按钮是否该出现：离开贴底区就出现，回到贴底区就消失。
 *  与 isNearBottom 严格互补（同一阈值、同一函数族），不存在第二个判据。 */
export function shouldShowScrollToBottom(m: ScrollMetrics): boolean {
  return !isNearBottom(m);
}

/** 回到底部的目标滚动位置。 */
export function scrollToBottomTarget(m: ScrollMetrics): number {
  return Math.max(0, finite(m.scrollHeight));
}

/** 横向可滚的最大距离；<= 容差 = 内容不够长（两侧都不渐隐、滚轮不拦）。 */
function maxScrollLeft(m: OverflowMetrics): number {
  return finite(m.scrollWidth) - finite(m.clientWidth);
}

/** 溢出方向：左侧还有内容 = 已经滚离起点；右侧还有内容 = 还没滚到终点。 */
export function overflowEdges(m: OverflowMetrics): OverflowEdges {
  const max = maxScrollLeft(m);
  if (max <= EDGE_EPSILON) return { left: false, right: false };
  const scrollLeft = finite(m.scrollLeft);
  return {
    left: scrollLeft > EDGE_EPSILON,
    right: scrollLeft < max - EDGE_EPSILON,
  };
}

/** 渐隐方向 → data 属性值（样式侧只认这四个字面量）。 */
export function overflowAttr(edges: OverflowEdges): OverflowAttr {
  if (edges.left && edges.right) return "both";
  if (edges.left) return "left";
  if (edges.right) return "right";
  return "none";
}

/** 滚轮是否该被 strip 吃掉（deltaY 转成 scrollLeft）。
 *  两条都要满足，缺一条都会伤到用户：
 *  ① 真的横向溢出——不溢出还拦，36px 的标签栏会把页面自身的纵向滚动整段吞掉；
 *  ② 该方向还有可滚空间——已经贴到右端还继续下滚时不拦，让事件冒泡给页面
 *     （否则用户在最右端继续滚动会觉得"卡住了"）。 */
export function shouldInterceptWheel(m: OverflowMetrics, deltaY: number): boolean {
  const max = maxScrollLeft(m);
  if (max <= EDGE_EPSILON) return false;
  const scrollLeft = finite(m.scrollLeft);
  if (deltaY > 0) return scrollLeft < max - EDGE_EPSILON;
  if (deltaY < 0) return scrollLeft > EDGE_EPSILON;
  return false; // deltaY === 0：横向滚动浏览器原生就会处理，不必插手
}
