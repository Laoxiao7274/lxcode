// @aicss/react 0.1.3 (MIT) vendor 改造：从自动播放演示组件改为受控组件。
// 视觉（CSS module）与折叠/滚动渐隐机制原样保留；数据面改为 props：
// sentences 由调用方（事件流）驱动，phase 由调用方翻转。
import styles from "./ThinkingReasoning.module.css";
import { useEffect, useRef, useState } from "react";

export interface ThinkingReasoningProps {
  /** 思考句列表。流式期间调用方逐步 append，done 后全量展示。 */
  sentences: string[];
  /** thinking = 进行中（shimmer、不可折叠）；done = 折叠成摘要行。 */
  phase: "thinking" | "done";
  /** 思考耗时（毫秒），done 时显示"思考了 N 秒"。 */
  elapsedMs?: number;
}

// 视口几何参数（句子高度按实际测量，不再用固定公式——中文句子行数不一）。
const MAX_H = 180; // 内容超高后进入滚动视口
const FADE = 16; // 顶部/底部渐隐

export function ThinkingReasoning({ sentences, phase, elapsedMs = 0 }: ThinkingReasoningProps) {
  const done = phase === "done";
  const [open, setOpen] = useState(false);
  const [fade, setFade] = useState({ top: false, bottom: true });
  const viewportRef = useRef<HTMLDivElement>(null);

  const expanded = done ? open : true;
  // 内容高度按实际句子测量（中文句子行数不一——原固定公式 40px/句
  // 会裁掉超出两行的内容）。流式期间句子在变，随渲染重测。
  const streamRef = useRef<HTMLDivElement>(null);
  const [contentH, setContentH] = useState(0);
  useEffect(() => {
    const el = streamRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setContentH(el.offsetHeight));
    ro.observe(el);
    setContentH(el.offsetHeight);
    return () => ro.disconnect();
  }, [sentences.length]);

  const capped = contentH > MAX_H;
  const viewH = capped ? MAX_H : contentH;
  const scrollable = done && open;
  const translate = scrollable ? 0 : capped ? MAX_H - FADE - contentH : 0;

  const showTop = scrollable ? fade.top : capped;
  const showBottom = scrollable ? fade.bottom : capped;
  const mask = capped
    ? `linear-gradient(to bottom, transparent 0, #000 ${showTop ? FADE : 0}px, #000 calc(100% - ${showBottom ? FADE : 0}px), transparent 100%)`
    : "none";

  const onScroll = () => {
    const el = viewportRef.current;
    if (!el) return;
    setFade({
      top: el.scrollTop > 1,
      bottom: el.scrollTop + el.clientHeight < el.scrollHeight - 1,
    });
  };

  const toggle = () => {
    const next = !open;
    if (next) {
      setFade({ top: false, bottom: true });
      if (viewportRef.current) viewportRef.current.scrollTop = 0;
    }
    setOpen(next);
  };

  const elapsedS = Math.max(1, Math.round(elapsedMs / 1000));

  return (
    <div className={styles.tr}>
      <button
        type="button"
        className={styles.trHeader + (done ? " " + styles.isClickable : "")}
        aria-expanded={expanded}
        aria-label="展开或折叠思考过程"
        onClick={done ? toggle : undefined}
      >
        {done ? (
          <span className={styles.trLabel}>
            <span className={styles.trVerb}>思考了</span> {elapsedS} 秒
          </span>
        ) : (
          <span className={styles.trLabel + " " + styles.trShimmer}>思考中…</span>
        )}
        {done && (
          <svg className={styles.trChevron} viewBox="0 0 24 24" width="12" height="12" aria-hidden="true">
            <path d="m4.5 15.75 7.5-7.5 7.5 7.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        )}
      </button>

      <div className={styles.trCollapsible + (expanded ? "" : " " + styles.isCollapsed)}>
        <div className={styles.trInner}>
          <div
            ref={viewportRef}
            className={styles.trViewport + (scrollable ? " " + styles.isScroll : "")}
            style={{ height: `${viewH}px`, WebkitMaskImage: mask, maskImage: mask }}
            onScroll={scrollable ? onScroll : undefined}
          >
            <div ref={streamRef} className={styles.trStream} style={{ transform: `translateY(${translate}px)` }}>
              {sentences.map((line, i) => (
                <p key={i} className={styles.trSentence}>{line}</p>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
