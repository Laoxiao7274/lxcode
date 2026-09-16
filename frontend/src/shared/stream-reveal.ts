// 打字机缓冲（移植自 lx-dsh harness 的 ui-primitives/markdown/stream-reveal）：
// 流式文本新到达的字符不整块蹦出，而是按固定「浇注窗口」匀速流出——
// 积压越大流速越快（30~1000 字/秒），流结束后缓冲继续排空，观感即 Codex
// 那种顺滑打字机。节奏属于内容呈现而非装饰动效：reduced-motion 不关闭，
// 仅测试通道（__LX_TEST_MOTION_OFF__）禁用。
import { useEffect, useRef, useState } from "react";

/** 有积压时最慢揭示速度（字/秒）。 */
const REVEAL_MIN_CPS = 30;
/** 无论积压多大都不超过的最快揭示速度——爆发段也要看得见地流出。 */
const REVEAL_MAX_CPS = 1000;
/** 每段积压都按这个秒数浇注完；积压越大按比例浇得越快。 */
const REVEAL_POUR_SECONDS = 0.35;
/** 积压超过这个字符数直接对齐：分叉的流不能回放。 */
const REVEAL_MAX_LAG = 4000;

function pacingAllowed(): boolean {
  if (typeof window === "undefined") return false;
  return (window as unknown as { __LX_TEST_MOTION_OFF__?: boolean }).__LX_TEST_MOTION_OFF__ !== true;
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
  const frameRef = useRef(0);
  const lastRef = useRef(0);

  useEffect(() => {
    if (!pacingAllowed() || !text.startsWith(shownRef.current) || text === shownRef.current) {
      if (shownRef.current !== text) {
        shownRef.current = text;
        setShown(text);
      }
      return;
    }
    if (text.length - shownRef.current.length > REVEAL_MAX_LAG) {
      shownRef.current = text;
      setShown(text);
      return;
    }
    const tick = (now: number): void => {
      const elapsed = Math.max(0, now - lastRef.current);
      lastRef.current = now;
      const gap = text.length - shownRef.current.length;
      if (gap <= 0) {
        frameRef.current = 0;
        return;
      }
      const rate = Math.min(REVEAL_MAX_CPS, Math.max(REVEAL_MIN_CPS, gap / REVEAL_POUR_SECONDS));
      const step = Math.max(1, Math.round((rate * elapsed) / 1000));
      const next = Math.min(text.length, shownRef.current.length + step);
      shownRef.current = text.slice(0, next);
      setShown(shownRef.current);
      frameRef.current = next === text.length ? 0 : requestAnimationFrame(tick);
    };
    lastRef.current = performance.now();
    frameRef.current = requestAnimationFrame(tick);
    return () => {
      if (frameRef.current !== 0) {
        cancelAnimationFrame(frameRef.current);
        frameRef.current = 0;
      }
    };
  }, [text]);

  return shown;
}
