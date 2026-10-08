// 滚动跟随状态机（主时间线与子会话页**共用**——之前只有主时间线有，子会话页
// 漏装了：流式输出时永不跟随，用户报「子会话尤其明显」的根因）。
//
// 语义：用户贴底 → sticky 跟随；上翻 → 解除；滚回底部 → 恢复。
// 用户意图 = wheel/touch/keydown（程序置底绝不触发）+ scroll 兜底
// （覆盖滚动条拖动；prog 时间窗跳过程序置底自身的事件，防自激）。
// 内容增高（流式增量、工具/确认卡挂载、gsap 展开逐帧撑高）全走
// ResizeObserver → 每帧置底——只要 sticky 还在。
import { useEffect, useRef } from "react";
import { isNearBottom } from "../../shared/scroll-metrics";

export function useStickyFollow(opts: {
  /** 空态↔会话切换会替换 endRef 所在子树与滚动容器首个子节点——effect 必须随
   *  边界重装（[] 会在空态挂载时因 endRef 为空而永不安装跟随：页面加载即失效
   *  的根因）。 */
  empty: boolean;
  /** 输入区（含任务清单卡）高度变化也要跟随：它只改线程区的 padding-bottom。
   *  查找范围必须是**本页**的面板而不是全局（工作区面板保活，聊天页与子会话页
   *  的 Composer 可能同时挂载——查 .main 会查到别人家的）。 */
  composerScope: string;
  /** RO 触发时的额外让位条件（主时间线的发送缓动窗口内让位给 tween，避免瞬跳
   *  打断滚底动画）。 */
  holdScroll?: () => boolean;
  /** 容器即将换代时的额外清理（杀进行中的 tween，别让 onUpdate 写游离节点）。 */
  onDetach?: () => void;
}) {
  const endRef = useRef<HTMLDivElement>(null);
  const stickyRef = useRef(true);
  const scrollRef = useRef<HTMLElement | null>(null);
  // prog：程序置底时间窗（scroll 兜底判定跳过用）
  const progRef = useRef(-1e9);
  const { empty, composerScope } = opts;
  // 回调走 ref：调用方常传内联箭头（身份每渲染都变）——进依赖数组会让跟随
  // 状态机每渲染重装一遍（监听器 churn + 每次都 toBottom）。
  const holdRef = useRef(opts.holdScroll);
  holdRef.current = opts.holdScroll;
  const detachRef = useRef(opts.onDetach);
  detachRef.current = opts.onDetach;
  useEffect(() => {
    const el = endRef.current?.parentElement?.parentElement ?? null;
    scrollRef.current = el;
    if (!el) return;
    const toBottom = () => {
      progRef.current = performance.now();
      el.scrollTop = el.scrollHeight;
    };
    const userIntent = () => {
      // 贴底判定与「回到底部」按钮（ScrollToBottom）**同源**：同一个常量、同一个
      // 函数（shared/scroll-metrics）。在这里再写一个 200 就是分叉的开始。
      stickyRef.current = isNearBottom(el);
    };
    const onWheel = () => userIntent();
    const onTouch = () => userIntent();
    const onKey = (e: KeyboardEvent) => {
      if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End"].includes(e.key)) {
        requestAnimationFrame(userIntent);
      }
    };
    const onScroll = () => {
      if (performance.now() - progRef.current < 120) return;
      userIntent();
    };
    el.addEventListener("wheel", onWheel, { passive: true });
    el.addEventListener("touchmove", onTouch, { passive: true });
    el.addEventListener("keydown", onKey);
    el.addEventListener("scroll", onScroll, { passive: true });
    const ro = new ResizeObserver(() => {
      if (holdRef.current?.()) return;
      if (stickyRef.current) toBottom();
    });
    // 观察内容容器（.thread 直属子节点——注释/加载态与时间线互斥时也能找对）
    ro.observe(el.querySelector<HTMLElement>(":scope > .thread") ?? el.firstElementChild ?? el);
    const composerZone = el.closest(composerScope)?.querySelector<HTMLElement>(".composer-zone");
    if (composerZone) ro.observe(composerZone);
    // 装上先贴一次底：首条消息挂载早于任何 RO 回调，先对齐
    toBottom();
    return () => {
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchmove", onTouch);
      el.removeEventListener("keydown", onKey);
      el.removeEventListener("scroll", onScroll);
      ro.disconnect();
      detachRef.current?.();
    };
  }, [empty, composerScope]);
  return { endRef, stickyRef, scrollRef, progRef };
}
