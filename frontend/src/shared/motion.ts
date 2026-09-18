// gsap 动效中枢：所有 UI chrome 动画经 motionAllowed 门控
// （prefers-reduced-motion / __LX_TEST_MOTION_OFF__），context 由调用方管理。
import { gsap } from "gsap";

export function motionAllowed(): boolean {
  if (typeof window === "undefined") return false;
  if ((window as { __LX_TEST_MOTION_OFF__?: boolean }).__LX_TEST_MOTION_OFF__) return false;
  return !window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

/** 标准入场：上浮 + 淡入（Codex 的柔和节奏）。 */
const enter = { y: 8, opacity: 0 };
const enterTo = { y: 0, opacity: 1 };
export const enterEase = "power2.out";

/** 交错浮现（空态卡片/消息块）。收尾清掉内联 transform——残留的
 *  translate(0,0) 会让每个元素自成 stacking context，把弹层 z-index 困住。
 *  2026-09-17 收紧：0.3s/0.04（0.5s 的原档位用户反馈发卡），且交错总量
 *  封顶 ~0.36s——几十个条目时不再拖出长尾。 */
export function staggerIn(targets: Element | Element[] | NodeListOf<Element> | string, opts: { delay?: number; each?: number } = {}) {
  if (!motionAllowed()) return;
  const items = gsap.utils.toArray(targets as Element);
  if (items.length === 0) return;
  const each = Math.min(opts.each ?? 0.04, 0.36 / items.length);
  gsap.fromTo(items, enter, {
    ...enterTo,
    duration: 0.3,
    ease: enterEase,
    delay: opts.delay ?? 0,
    stagger: each,
    clearProps: "transform,opacity",
  });
}
