// 打字机缓冲（移植自 lx-dsh harness 的 ui-primitives/markdown/stream-reveal）：
// 流式文本新到达的字符不整块蹦出，而是按固定「浇注窗口」匀速流出——
// 积压越大流速越快（30~1000 字/秒），流结束后缓冲继续排空，观感即 Codex
// 那种顺滑打字机。节奏属于内容呈现而非装饰动效：reduced-motion 不关闭，
// 仅测试通道（__LX_TEST_MOTION_OFF__）禁用。
//
// 页面隐藏（document.hidden）纪律（2026-12 卡顿修复）：
// - rAF 在隐藏页停转，浇注循环本来就跑不动；修复前积压一旦超过
//   REVEAL_MAX_LAG 还会**每条 delta 全量对齐**（setShown(全文) → Markdown
//   对全文重新解析）——为不可见的内容持续烧 CPU。
// - 现在 hidden 期间一律**冻结** shown（revealPlan 返回 "hold"）：只低成本
//   累积 state（TextBlocks 的 memo 按字符串命中，shown 不变 = 零解析），
//   不浇注、不对齐、不 setShown。
// - 恢复可见那一刻（visibilitychange）统一落账：积压 > REVEAL_RESUME_JUMP
//   直接整块落下（一次提交，跳过动画——大积压浇注的每帧成本是 O(全文)
//   解析 + 置底强制布局，恢复后会是数秒的逐帧风暴）；小积压照常浇注，
//   观感不变。落账判定在 revealPlan（纯函数，单测钉住）。
import { useEffect, useRef, useState } from "react";

/** 有积压时最慢揭示速度（字/秒）。 */
const REVEAL_MIN_CPS = 30;
/** 无论积压多大都不超过的最快揭示速度——爆发段也要看得见地流出。 */
const REVEAL_MAX_CPS = 1000;
/** 每段积压都按这个秒数浇注完；积压越大按比例浇得越快。 */
const REVEAL_POUR_SECONDS = 0.35;
/** 积压超过这个字符数直接对齐：分叉的流不能回放。 */
const REVEAL_MAX_LAG = 4000;
/** 恢复可见那一刻的落账阈值（字）：隐藏期间冻结的积压超过它就整块落下。
 *  1000 字 ≈ 最快速度下 1s 的浇注——超过这个时长，逐帧全文解析 + 置底
 *  强制布局的代价已经大于「整块落下」的一次性成本。 */
export const REVEAL_RESUME_JUMP = 1000;

function pacingAllowed(): boolean {
  if (typeof window === "undefined") return false;
  return (window as unknown as { __LX_TEST_MOTION_OFF__?: boolean }).__LX_TEST_MOTION_OFF__ !== true;
}

/** 页面当前是否隐藏（无 DOM 环境——node 测试/SSR——视为可见）。 */
export function documentHidden(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "hidden";
}

/** 一次 text 到达（或恢复可见）的落账决策（纯函数，可单测）。
 *  - "none"：无积压，不用动；
 *  - "snap"：整块落下（分叉流不能回放 / 积压超限 / 动效禁用）；
 *  - "pour"：按节奏浇注（rAF 驱动，走 pourStep 推进）；
 *  - "hold"：页面隐藏，冻结 shown 等恢复钩子落账——**无论积压多大都冻结**
 *    （修复前 hidden 期间积压超限会每条 delta 全量对齐，纯烧 CPU）。 */
export type RevealPlan = "none" | "snap" | "pour" | "hold";

export function revealPlan(
  shown: string,
  text: string,
  opts: { visible: boolean; motion: boolean; lagLimit: number },
): RevealPlan {
  if (!opts.visible) return "hold";
  if (!text.startsWith(shown)) return "snap"; // 分叉的流不能回放
  if (text === shown) return "none";
  if (!opts.motion) return "snap";
  if (text.length - shown.length > opts.lagLimit) return "snap";
  return "pour";
}

/** 浇注一步：从 shown 前缀按积压自适应速度推进一步（纯函数，可单测）。
 *  返回新的 shown 前缀（无积压时原样返回）。 */
export function pourStep(shown: string, text: string, elapsedMs: number): string {
  const gap = text.length - shown.length;
  if (gap <= 0 || !text.startsWith(shown)) return text;
  const rate = Math.min(REVEAL_MAX_CPS, Math.max(REVEAL_MIN_CPS, gap / REVEAL_POUR_SECONDS));
  const step = Math.max(1, Math.round((rate * elapsedMs) / 1000));
  return text.slice(0, Math.min(text.length, shown.length + step));
}

/**
 * 按节奏揭示持续增长的流式文本。
 * @param text - 已累计的全部流式文本。
 * @returns 当前可见的 text 前缀。
 */
export function useStreamReveal(text: string): string {
  // 挂载即显示全文（流中途重挂载不能从头回放）；只有挂载后的增长走节奏。
  const [shown, setShown] = useState(text);
  const shownRef = useRef(text);
  const textRef = useRef(text);
  const frameRef = useRef(0);
  const lastRef = useRef(0);

  const cancelFrame = (): void => {
    if (frameRef.current !== 0) {
      cancelAnimationFrame(frameRef.current);
      frameRef.current = 0;
    }
  };
  const snap = (): void => {
    cancelFrame();
    shownRef.current = textRef.current;
    setShown(shownRef.current);
  };
  const pour = (): void => {
    cancelFrame();
    const tick = (now: number): void => {
      const elapsed = Math.max(0, now - lastRef.current);
      lastRef.current = now;
      const next = pourStep(shownRef.current, textRef.current, elapsed);
      if (next === shownRef.current) {
        frameRef.current = 0;
        return;
      }
      shownRef.current = next;
      setShown(next);
      frameRef.current = next === textRef.current ? 0 : requestAnimationFrame(tick);
    };
    lastRef.current = performance.now();
    frameRef.current = requestAnimationFrame(tick);
  };

  useEffect(() => {
    textRef.current = text;
    const plan = revealPlan(shownRef.current, text, {
      visible: !documentHidden(),
      motion: pacingAllowed(),
      lagLimit: REVEAL_MAX_LAG,
    });
    if (plan === "pour") {
      pour();
      return cancelFrame;
    }
    if (plan === "snap") snap();
    else cancelFrame(); // none / hold（hold：冻结，等恢复钩子落账）
  }, [text]);

  // 恢复可见的统一落账口：hidden 期间冻结的积压在这里处理。流在隐藏期间
  // 已结束（不再有 delta 触发 effect）也必须落账，否则内容永远不上屏。
  useEffect(() => {
    const onVisibilityChange = (): void => {
      if (documentHidden()) {
        cancelFrame();
        return;
      }
      const plan = revealPlan(shownRef.current, textRef.current, {
        visible: true,
        motion: pacingAllowed(),
        lagLimit: REVEAL_RESUME_JUMP,
      });
      if (plan === "snap") snap();
      else if (plan === "pour") pour();
      else cancelFrame();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => document.removeEventListener("visibilitychange", onVisibilityChange);
  }, []);

  return shown;
}
