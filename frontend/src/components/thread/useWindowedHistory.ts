// 窗口化渲染的「窗口状态 + 上翻扩窗 + 滚动锚定」（主时间线与子会话页**共用**）。
//
// 之前两处各写一份（Thread 的 useState + useEffect 重置、ChildSessionPage 的
// useState + useEffect 重置），修了锚定漏一处就是"主会话好了子会话还跳顶"——
// 这个仓库反复踩过的一类 bug（见 ScrollToBottom 的头注）。所以收敛成一份。
//
// 三条语义（都是用户报过的问题的直接来源）：
//
// ① **窗口按会话派生，不靠 effect 事后重置**：`useEffect(() => setWindowSize(INITIAL),
//    [sessionId])` 在提交之后才跑，切会话的**第一帧**仍按上一条会话的窗口切片
//    ——上一条上翻到 900 块时，这一帧要挂 900 个块（markdown 解析 + 工具行 +
//    入场动效全在里面），就是「切长会话卡一下」。这里在**渲染期**按 sessionKey
//    纠正（React 的「props 变化时调整 state」标准做法：同步重渲染，不提交旧值），
//    首帧就是 40 块；切回同一条会话同样是初始窗口（与旧语义一致）。
//
// ② **上翻前插必须补偿 scrollTop**：插更早的块会整体撑高内容，浏览器保持
//    scrollTop 不变 → 视口内容被整批往下推 = 用户说的「往上滑一丢丢就跳到顶」。
//    扩窗前记 scrollHeight/scrollTop，插入后在**绘制前**（layout effect）按
//    高度差补回去，视口内容一动不动。
//
// ③ **扩窗只认「当前会话」的窗口大小**：跨会话的陈旧 size 不参与计算。
import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { NEAR_BOTTOM_PX } from "../../shared/scroll-metrics";

/** 首屏窗口与每批前插的块数（视口内块数远小于此——含工具行展开态）。
 *  两处（主时间线 / 子会话页）必须用同一份参数：不一致就是两套手感。 */
export const WINDOW_INITIAL = 40;
export const WINDOW_BATCH = 40;

/** 前插扩窗时要不要记锚点（纯函数，主时间线与子会话页共用一份判定）。
 *
 *  贴底（sticky）时**不锚定**：切会话首帧内容不足「视口 + 哨兵 rootMargin」时，
 *  顶部哨兵的 IO 初始交付就是 intersecting → expandOlder → captureAnchor 会把
 *  贴底态的 top 记下来，随后的锚定补偿把视口挪到中间——而贴底场景跟随状态机的
 *  RO 会把视口带回底部，锚定补上反而打架。非贴底（用户正在上翻）时才需要锚定。 */
export function shouldAnchorForExpand(
  sticky: boolean,
  scrollTop: number,
  scrollHeight: number,
  viewport: number,
): boolean {
  if (sticky) return false;
  return scrollHeight - scrollTop - viewport > NEAR_BOTTOM_PX;
}

export function useWindowedHistory(opts: {
  /** 会话键（currentId / 子会话 id）：变了就回到初始窗口。 */
  sessionKey: string;
  /** 当前会话的块总数（窗口从尾部往前取）。 */
  total: number;
  /** 滚动容器（锚定补偿写它的 scrollTop）。 */
  scrollRef: { current: HTMLElement | null };
  /** 跟随状态机是否贴底（Thread/ChildSessionPage 传各自的 stickyRef 读数）。
   *  贴底时不记锚点（shouldAnchorForExpand）——扩窗后 RO 跟随保持贴底。 */
  isSticky?: () => boolean;
}) {
  const { sessionKey, total, scrollRef, isSticky } = opts;
  const [win, setWin] = useState<{ key: string; size: number }>(() => ({ key: sessionKey, size: WINDOW_INITIAL }));
  // 锚点 ref 提前到窗口纠正之前声明：切会话的同一帧里既换窗口、又作废旧锚点。
  const anchorRef = useRef<{ top: number; height: number } | null>(null);
  // 渲染期纠正（不是 effect）：切会话的那一帧就必须是初始窗口，否则首帧要按
  // 上一条会话扩过的窗口挂载一整片块——那正是切换卡顿的一大块。
  if (win.key !== sessionKey) {
    setWin({ key: sessionKey, size: WINDOW_INITIAL });
    // 分页在途切会话：旧锚点属于旧会话的滚动位置，新会话消费它就是一次凭空的
    // 错补（把视口挪到中间）——顺手在窗口换代时一并作废。
    anchorRef.current = null;
  }
  const windowSize = win.key === sessionKey ? win.size : WINDOW_INITIAL;
  const hidden = Math.max(0, total - windowSize);
  /** 改窗口大小：prev 一律按**当前会话**的窗口算。 */
  const update = useCallback((fn: (prev: number) => number) => {
    setWin((cur) => ({ key: sessionKey, size: fn(cur.key === sessionKey ? cur.size : WINDOW_INITIAL) }));
  }, [sessionKey]);
  /** 记下「前插之前」的滚动位置（补偿锚点）。
   *
   *  扩窗（本地窗口）与**后端分页**（before_seq 拉更早一页）都要先记它：
   *  两条路径都会在时间线头部插入内容，而浏览器保持 scrollTop 不变 → 视口内容
   *  被整批往下推（用户说的「往上滑一丢丢就跳到顶」）。分页是异步的，锚点必须在
   *  **请求发出前**记（应答到达时 scrollTop 已经被别的东西动过了）。 */
  const captureAnchor = useCallback(() => {
    const el = scrollRef.current;
    // 贴底时不记锚点：扩窗后 RO 跟随把视口保持在底部，锚定补偿反而把它挪到中间。
    if (!el || (isSticky?.() ?? false)
      || !shouldAnchorForExpand(false, el.scrollTop, el.scrollHeight, el.clientHeight)) {
      anchorRef.current = null;
      return;
    }
    anchorRef.current = { top: el.scrollTop, height: el.scrollHeight };
  }, [scrollRef, isSticky]);
  /** 请求失败/没有新内容时丢弃锚点（否则下一次无关的布局变化会拿它错补一次）。 */
  const clearAnchor = useCallback(() => {
    anchorRef.current = null;
  }, []);
  /** 前插一批更早的块（带滚动锚定）。 */
  const expandOlder = useCallback(() => {
    captureAnchor();
    update((n) => n + WINDOW_BATCH);
  }, [captureAnchor, update]);
  useLayoutEffect(() => {
    const anchor = anchorRef.current;
    anchorRef.current = null;
    const el = scrollRef.current;
    if (!anchor || !el) return;
    const grew = el.scrollHeight - anchor.height;
    // 只补"内容长高"的那部分：高度没变（窗口已到底、没有更早的块）不动滚动位置。
    if (grew > 0) el.scrollTop = anchor.top + grew;
  }, [hidden, scrollRef]);
  return { windowSize, hidden, update, expandOlder, captureAnchor, clearAnchor };
}
