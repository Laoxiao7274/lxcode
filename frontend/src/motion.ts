// gsap 动效中枢：所有 UI chrome 动画经 motionAllowed 门控
// （prefers-reduced-motion / __LX_TEST_MOTION_OFF__），context 由调用方管理。
import { gsap } from "gsap";

export function motionAllowed(): boolean {
  if (typeof window === "undefined") return false;
  if ((window as { __LX_TEST_MOTION_OFF__?: boolean }).__LX_TEST_MOTION_OFF__) return false;
  return !window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

/** 标准入场：上浮 + 淡入（Codex 的柔和节奏）。 */
export const enter = { y: 8, opacity: 0 };
export const enterTo = { y: 0, opacity: 1 };
export const enterEase = "power2.out";

/** 交错浮现（空态卡片/消息块）。 */
export function staggerIn(targets: Element | Element[] | NodeListOf<Element> | string, opts: { delay?: number; each?: number } = {}) {
  if (!motionAllowed()) return;
  gsap.fromTo(targets as Element, enter, {
    ...enterTo,
    duration: 0.5,
    ease: enterEase,
    delay: opts.delay ?? 0,
    stagger: opts.each ?? 0.06,
  });
}
