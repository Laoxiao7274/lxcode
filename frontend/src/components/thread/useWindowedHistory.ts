// 窗口化渲染的「窗口状态 + 上翻扩窗 + 滚动锚定」（主时间线与子会话页**共用**）。
//
// 之前两处各写一份（Thread 的 useState + useEffect 重置、ChildSessionPage 的
// useState + useEffect 重置），修了锚定漏一处就是"主会话好了子会话还跳顶"——
// 这个仓库反复踩过的一类 bug（见 ScrollToBottom 的头注）。所以收敛成一份。
//
// 四条语义（前三条都是用户报过的问题的直接来源；第四条是 OOM 三件套 S4）：
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
//
// ④ **窗口有上界（S4，2026-12 OOM 三件套）**：windowSize 可以通过上翻无限增长
//    ——一次长会话考古挂载几千个块（注释自认「上翻不回收」），全量测试期间整页
//    内存只进不出。上界 WINDOW_MAX：扩到上界后**滑动窗口**——上翻扩窗改为
//    「头部补 BATCH 块更早的 + 尾部丢 BATCH 块」（end -= BATCH），挂载量有界。
//    丢弃的最老块数据仍在 state（sessionStates 保留，S3 负责淘汰），DOM 不渲染；
//    恢复走**贴底整备**：用户滚回底部（isSticky）的渲染帧一次性恢复完整尾部窗口
//    （与切会话重置同款）。滑动帧的滚动锚定用块 uid（captureSlideAnchor）——
//    高度差公式对「前插+后丢」会少补丢掉的那段，uid 锚定在两种 DOM 里都能找到
//    同一块，对任意窗口变换都正确。
import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { NEAR_BOTTOM_PX } from "../../shared/scroll-metrics";

/** 首屏窗口与每批前插的块数（视口内块数远小于此——含工具行展开态）。
 *  两处（主时间线 / 子会话页）必须用同一份参数：不一致就是两套手感。 */
export const WINDOW_INITIAL = 40;
export const WINDOW_BATCH = 40;

/** 窗口上界（S4）：挂载块数的硬上限（4 批）。扩到上界后上翻 = 滑动窗口——
 *  头部补 BATCH 块、尾部丢 BATCH 块，挂载量不再随上翻深度增长。
 *  注意：大纲跳转（expandWindowForJump 的软上限 400）不受此约束——跳转是用户
 *  显式的深度导航，挂载风暴换「必须到达」，落地后由下一次滑动/切会话收窄。 */
export const WINDOW_MAX = 160;

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

interface WinState {
  key: string;
  /** 显示块数（窗口大小——从 end 往回数）。 */
  span: number;
  /** 显示底部边缘（blocks 索引，不含；恒 ≤ total）。上界滑动时减小——尾部
   *  块从 DOM 丢弃但数据仍在 state；恢复时增大（先于真扩窗）。 */
  end: number;
}

const clampSpan = (n: number, total: number) => Math.max(0, Math.min(n, total));

/** expandOlder 的纯转移函数（hook 与测试共用一份）：
 *
 *  - span + BATCH ≤ WINDOW_MAX：真扩窗（span += BATCH，end 不动——纯前插）；
 *  - 已到上界：滑动（span 收到 WINDOW_MAX，end -= BATCH——头部补/尾部丢）。
 *  返回 null = 无转移（没有本地块可扩且已到上界——由调用方走后端分页）。
 *  cur 传 null = 初始窗口（key 换代后的兜底）。 */
export function olderWindowTransition(
  cur: { span: number; end: number } | null,
  total: number,
): { span: number; end: number } | null {
  const span = clampSpan(cur?.span ?? WINDOW_INITIAL, total);
  const end = Math.min(cur?.end ?? total, total);
  if (span + WINDOW_BATCH <= WINDOW_MAX) return { span: span + WINDOW_BATCH, end };
  // 滑动：头部补 BATCH、尾部丢 BATCH。end 至少保住一个完整窗口（短会话在
  // WINDOW_MAX 之上不成立——span ≤ total ≤ end 恒成立，max 只是防御）。
  return { span: Math.min(span, WINDOW_MAX), end: Math.max(end - WINDOW_BATCH, Math.min(span, WINDOW_MAX)) };
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
  const [win, setWin] = useState<WinState>(() => ({ key: sessionKey, span: WINDOW_INITIAL, end: total }));
  // 锚点 ref 提前到窗口纠正之前声明：切会话的同一帧里既换窗口、又作废旧锚点。
  const anchorRef = useRef<{ top: number; height: number } | null>(null);
  // 滑动窗口的锚点（S4）：前插+后丢的 DOM 变化用块 uid 锚定——丢弃前（渲染期，
  // DOM 还是旧内容）记「新旧窗口都包含的那一块」的 uid 与文档位置，提交后在
  // layout effect 里按它的位置差校正 scrollTop（视口内容不动）。
  const slideAnchorRef = useRef<{ uid: string; top: number; scrollTop: number } | null>(null);
  // 渲染期纠正（不是 effect）：切会话的那一帧就必须是初始窗口，否则首帧要按
  // 上一条会话扩过的窗口挂载一整片块——那正是切换卡顿的一大块。
  if (win.key !== sessionKey) {
    setWin({ key: sessionKey, span: WINDOW_INITIAL, end: total });
    // 分页在途切会话：旧锚点属于旧会话的滚动位置，新会话消费它就是一次凭空的
    // 错补（把视口挪到中间）——顺手在窗口换代时一并作废。
    anchorRef.current = null;
    slideAnchorRef.current = null;
  }
  const sameKey = win.key === sessionKey;
  // S4 渲染期整备：① 历史被整块重建/撤光（total 缩水）时收口，窗口永不越界；
  // ② 贴底（用户滚回底部/跟随置底）而窗口尾部还有被丢的块 → 恢复完整尾部窗口
  //（span 收窄到初始窗口——贴底帧用户看的是底部，恢复的头部块挂在视口上方，
  // 不需要锚定；贴底跟随的 RO 自会把视口带回底部）。恢复后窗口回到「尾部窗口」
  // 形态，与切会话重置同款；③ end 本就应 ≤ total（slice 越界会被 React/TS 静默
  // 吞掉，先在这里钳住）。
  //
  // 恢复触发器有两条：本渲染期判定（渲染帧 + sticky 同时成立——内容增高/追加
  // 引发的渲染）与 restoreTail（onStick 回调——滚动事件引发的 false→true 翻转，
  // **滚动本身零渲染**，S4 回归「上翻后回底时定型尾块无限期缺失」的修复口）。
  // 两条路的条件一致（sticky && end < total，不再附加 span < total——span≥total
  // 时恢复本无害，且保证「sticky ⇒ 窗口为尾部窗口」不变式：贴底时看到的永远是
  // 完整尾部）。恢复帧 sticky=true：captureSlideAnchor / shouldAnchorForExpand
  // 都对 sticky 短路（不记锚），与贴底跟随不打架。
  const restoreTail = useCallback(() => {
    const sticky = isSticky?.() ?? false;
    if (!sticky) return;
    setWin((cur) => {
      if (cur.key !== sessionKey) return cur;
      const end = Math.min(cur.end, total);
      // 已是尾部窗口（没有被丢的尾部块）就不动——避免无谓的整备渲染。
      if (end >= total) return cur;
      return { key: sessionKey, span: clampSpan(WINDOW_INITIAL, total), end: total };
    });
  }, [sessionKey, total, isSticky]);
  if (sameKey) {
    const sticky = isSticky?.() ?? false;
    if (win.end > total) {
      setWin({ ...win, end: total, span: clampSpan(win.span, total) });
    } else if (sticky && win.end < total) {
      setWin({ key: sessionKey, span: clampSpan(WINDOW_INITIAL, total), end: total });
    }
  }
  const span = sameKey ? clampSpan(win.span, total) : WINDOW_INITIAL;
  const end = sameKey ? Math.min(win.end, total) : total;
  const hidden = Math.max(0, end - span);
  /** 改窗口大小：prev 一律按**当前会话**的窗口算（大纲跳转路径）。跳转是显式
   *  导航，end 直接恢复到 total——目标可能在被丢的尾部区里。 */
  const update = useCallback((fn: (prev: number) => number) => {
    setWin((cur) => {
      const prev = cur.key === sessionKey ? clampSpan(cur.span, total) : WINDOW_INITIAL;
      return { key: sessionKey, span: clampSpan(fn(prev), total), end: total };
    });
  }, [sessionKey, total]);
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
  /** 记滑动锚点（S4）：上界滑动/恢复前插都会在头部插块 + 尾部丢块——高度差
   *  锚定对「后丢」少补一段，必须按块锚。取「新旧窗口都包含」的第 k 块：
   *  k = 旧窗口在滑动后的位置偏移（span > WINDOW_MAX 的超大窗口丢弃头部之外
   *  还有裁掉的部分；常规滑动 k=0 即旧窗口第一块）。DOM 顺序 = 显示顺序
   *  （Thread/子会话页按序渲染 [data-uid]，流式块恒在末尾不干扰前 k 块）。 */
  const captureSlideAnchor = useCallback((k: number) => {
    const el = scrollRef.current;
    if (!el || (isSticky?.() ?? false)) { slideAnchorRef.current = null; return; }
    const nodes = el.querySelectorAll<HTMLElement>("[data-uid]");
    const target = nodes[k] ?? nodes[0];
    if (!target) { slideAnchorRef.current = null; return; }
    const top = target.getBoundingClientRect().top - el.getBoundingClientRect().top;
    slideAnchorRef.current = { uid: target.dataset.uid ?? "", top, scrollTop: el.scrollTop };
  }, [scrollRef, isSticky]);
  /** 请求失败/没有新内容时丢弃锚点（否则下一次无关的布局变化会拿它错补一次）。 */
  const clearAnchor = useCallback(() => {
    anchorRef.current = null;
    slideAnchorRef.current = null;
  }, []);
  /** 前插一批更早的块（带滚动锚定；到上界后转为滑动窗口——S4）。
   *
   *  滑动 = 头部补 BATCH + 尾部丢 BATCH（end -= BATCH）：挂载量恒 ≤ WINDOW_MAX。
   *  被丢的尾部块数据仍在 state；恢复只有一条路——用户滚回底部（贴底帧的渲染期
   *  整备一次性恢复完整尾部窗口）。这里**不做**「先恢复再扩窗」：恢复把尾部补回
   *  （end += BATCH）会让刚滑动的 end 原地弹回，滑动永远不生效（上翻到深处时
   *  窗口每次都在「滑动 40 → 恢复 40」里打转）。头部锚定对「后补尾部」本来也不
   *  生效——后补不影响视口上方的内容。 */
  const expandOlder = useCallback(() => {
    setWin((cur) => {
      const curState = cur.key === sessionKey
        ? { span: clampSpan(cur.span, total), end: Math.min(cur.end, total) }
        : null;
      const next = olderWindowTransition(curState, total);
      if (!next) return cur;
      if (next.span > (curState?.span ?? 0) && next.end === curState?.end) {
        // 真扩窗（纯前插）：走既有高度差锚定。
        captureAnchor();
      } else {
        // 滑动（头部补/尾部丢）：块 uid 锚定（旧窗口第一块——新旧窗口都包含它）。
        captureSlideAnchor(0);
      }
      return { key: sessionKey, ...next };
    });
  }, [sessionKey, total, captureAnchor, captureSlideAnchor]);

  useLayoutEffect(() => {
    const el = scrollRef.current;
    // 两个锚点同一帧只可能有一个（expandOlder 的两条路径互斥）。
    const slide = slideAnchorRef.current;
    slideAnchorRef.current = null;
    if (slide && el) {
      // 丢帧保护：目标块没渲染出来（被同帧整备恢复/丢弃）就放弃补偿——
      // 用户多滚一下，可接受（S4 的既定取舍）。
      const node = slide.uid
        ? el.querySelector<HTMLElement>(`[data-uid="${slide.uid}"]`)
        : null;
      if (node) {
        const nowTop = node.getBoundingClientRect().top - el.getBoundingClientRect().top;
        const delta = nowTop - slide.top;
        if (delta !== 0) el.scrollTop = slide.scrollTop + delta;
      }
      return;
    }
    const anchor = anchorRef.current;
    anchorRef.current = null;
    if (!anchor || !el) return;
    const grew = el.scrollHeight - anchor.height;
    // 只补"内容长高"的那部分：高度没变（窗口已到底、没有更早的块）不动滚动位置。
    if (grew > 0) el.scrollTop = anchor.top + grew;
  }, [hidden, scrollRef]);
  return { windowSize: span, hidden, end, update, expandOlder, restoreTail, captureAnchor, clearAnchor };
}
