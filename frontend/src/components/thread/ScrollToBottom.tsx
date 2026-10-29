// 「回到底部」浮标：主时间线（Thread）与子会话页（ChildSessionPage）**共用同一份实现**。
//
// 为什么不各写一份：两处都需要它，各写一份的下场是修了一处漏一处（这个仓库反复踩过的
// 一类 bug）。也正因为要共用，它不能对某一种页面结构有假设。
//
// 为什么自己找滚动祖先、而不是接一个"滚动容器 ref"：
//   Thread 的 scrollRef 是在**它自己的 useEffect 里**赋值的，而 React 的子 effect 先于
//   父 effect 执行——把那个 ref 传下来，本组件首帧拿到的永远是 null；静态页面（子会话
//   页读完历史就不再重渲染）甚至不会再有第二次挂载机会，按钮就成了死的。从自己的锚点
//   往上找 overflow 祖先则零时序耦合：两处的容器（.thread-scroll / .child-session-body）
//   都按行为被找到，类名换了也不会漏。
//
// 定位契约：浮标是绝对定位，它的**定位锚必须落在滚动容器之外**（主时间线 = .chat-main，
//   子会话页 = .child-session-page）。锚在滚动容器里面的话，浮标会跟着历史内容一起滚走。
//   所以那两个滚动容器自己不许加 position: relative（thread.css / panels.css 里都有告警注释）。
//
// 显示判定与 Thread 的贴底判定**同源**（shared/scroll-metrics 的 isNearBottom，同一个
// 200px 阈值）——两处各写一个阈值，按钮就会在"其实已经在底部"时还亮着。
import { useCallback, useEffect, useRef, useState } from "react";
import { motionAllowed } from "../../shared/motion";
import { scrollToBottomTarget, shouldShowScrollToBottom } from "../../shared/scroll-metrics";
import { Button } from "../form";

/** 往上找最近的滚动容器。按**行为**找而不是按类名找：两处的容器类名不同
 *  （.thread-scroll / .child-session-body），写死选择器等于把结构耦合进来。 */
function nearestScrollContainer(from: HTMLElement | null): HTMLElement | null {
  const scrollable = (value: string) => value === "auto" || value === "scroll" || value === "overlay";
  for (let el = from?.parentElement ?? null; el; el = el.parentElement) {
    const style = window.getComputedStyle(el);
    if (scrollable(style.overflowY) || scrollable(style.overflowX)) return el;
  }
  return null;
}

export function ScrollToBottom({ label = "回到底部" }: { label?: string } = {}) {
  // 锚点用外层 span 而不是 Button 自身：表单套件的 Button 不透传 ref（改它超出本次范围），
  // 而定位本来也该由外层承担（.scroll-to-bottom 是绝对定位的浮层盒子）。
  const anchorRef = useRef<HTMLSpanElement>(null);
  const scrollerRef = useRef<HTMLElement | null>(null);
  const [show, setShow] = useState(false);

  useEffect(() => {
    const scroller = nearestScrollContainer(anchorRef.current);
    if (!scroller) return;
    scrollerRef.current = scroller;
    const sync = () => setShow(shouldShowScrollToBottom(scroller));
    sync();
    scroller.addEventListener("scroll", sync, { passive: true });
    // 内容变高（流式增量、工具/确认卡挂载、展开）改的是内容的尺寸而不是容器的盒子，
    // 所以 ResizeObserver 盯内容；容器自身尺寸变化（窗口缩放/大纲栏开合）走 resize。
    const ro = new ResizeObserver(sync);
    ro.observe(scroller.firstElementChild ?? scroller);
    window.addEventListener("resize", sync);
    return () => {
      scroller.removeEventListener("scroll", sync);
      window.removeEventListener("resize", sync);
      ro.disconnect();
      scrollerRef.current = null;
    };
  }, []);

  const jump = useCallback(() => {
    const el = scrollerRef.current;
    if (!el) return;
    const top = scrollToBottomTarget(el);
    // 动效门控（reduced-motion / 测试开关）：直接跳，不做缓动
    if (!motionAllowed()) {
      el.scrollTop = top;
      return;
    }
    el.scrollTo({ top, behavior: "smooth" });
  }, []);

  return (
    // data-show 而不是条件渲染：锚点必须一直在 DOM 里，否则"找滚动祖先"在首帧就落空。
    // 隐藏用 visibility（不进 Tab 序列、不进无障碍树），显示/隐藏都留一个淡入淡出。
    <span className="scroll-to-bottom" ref={anchorRef} data-show={show ? "true" : "false"}>
      <Button className="scroll-to-bottom-btn" onClick={jump} aria-label={label} title={label}>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M12 5v14M19 12l-7 7-7-7" />
        </svg>
      </Button>
    </span>
  );
}
