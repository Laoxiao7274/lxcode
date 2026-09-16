// gsap 挂载/卸载动画的复用行为（门控统一走 motionAllowed）。
// 约定：入场动画收尾 clearProps——残留的内联 transform 会让元素自成
// stacking context，把子层弹层的 z-index 困住。
import { useCallback, useRef } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "./motion";

/** 挂载入场：from→to 一次性补间（默认上浮淡入）。 */
export function playEnter(
  el: HTMLElement | null,
  from: gsap.TweenVars = { opacity: 0, y: 8 },
  to: gsap.TweenVars = { opacity: 1, y: 0, duration: 0.3, ease: "power2.out", clearProps: "transform,opacity" },
): void {
  if (!el || !motionAllowed()) return;
  gsap.fromTo(el, from, to);
}

/** 回调 ref：元素挂载即 playEnter（条件渲染/列表新增行的入场）。
 *  from/to 取首次渲染的配置（静态配置用途）；ref identity 恒定，
 *  React 只在挂载/卸载时调用——动画不会因重渲染重播。 */
export function useEnterRef<T extends HTMLElement>(
  from?: gsap.TweenVars,
  to?: gsap.TweenVars,
): (el: T | null) => void {
  const optsRef = useRef({ from, to });
  return useCallback((el: T | null) => playEnter(el, optsRef.current.from, optsRef.current.to), []);
}

/** 收拢退场：高度/内边距/外边距归零 + 淡出，动画结束回调（条件渲染的
 *  组件先播动画再卸载——直接卸载是瞬灭）。无动画能力时立即回调。
 *  vars 可追加/覆盖属性（如 borderTopWidth、duration）。 */
export function collapseAway(
  el: HTMLElement | null,
  onDone: () => void,
  vars: gsap.TweenVars = {},
): void {
  if (!el || !motionAllowed()) {
    onDone();
    return;
  }
  gsap.set(el, { overflow: "hidden" });
  gsap.to(el, {
    height: 0, opacity: 0, marginTop: 0, paddingTop: 0, paddingBottom: 0,
    duration: 0.22, ease: "power2.in",
    ...vars,
    onComplete: onDone,
  });
}
