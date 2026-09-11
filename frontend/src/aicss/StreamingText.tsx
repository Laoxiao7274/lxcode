// @aicss/react 0.1.3 (MIT) vendor — 视觉资产原样，标签中文化。
// 适配：追加型流式——text 增长时只播新增量，不回退重播（原版每次
// text 变化从头重播，段落累加时会造成"前面内容闪现两遍"）。
import styles from "./StreamingText.module.css";
import { useEffect, useRef, useState } from "react";

export function StreamingText({ text }: { text: string }) {
  const [shown, setShown] = useState(() => "");
  // 已确认播放到的长度（跨 text 变化保留——text 只增不减）
  const committedRef = useRef(0);
  const shownRef = useRef("");

  useEffect(() => {
    const prev = text.slice(0, committedRef.current);
    let i = committedRef.current;
    // 新 text 比已播放长：从 committed 续播增量；text 缩短（不该发生）：重播
    if (text.length < committedRef.current) {
      committedRef.current = 0;
      i = 0;
    }
    // 立即显示已确认的部分（上轮已播完的内容不闪）
    if (shownRef.current !== prev) {
      shownRef.current = prev;
      setShown(prev);
    }
    const id = setInterval(() => {
      i += 2;
      const next = text.slice(0, i);
      shownRef.current = next;
      setShown(next);
      if (i >= text.length) {
        committedRef.current = text.length;
        clearInterval(id);
      }
    }, 9);
    return () => {
      clearInterval(id);
      // 卸载（含 text 再次变化）：把已显示进度记为 committed——
      // 已完整播过的部分不再重播
      committedRef.current = Math.min(shownRef.current.length, text.length);
    };
  }, [text]);

  const streaming = shown.length < text.length;
  return (
    <p className={styles.prose}>
      {shown}
      <span className={streaming ? styles.caret + " " + styles.caretSteady : styles.caret} />
    </p>
  );
}
