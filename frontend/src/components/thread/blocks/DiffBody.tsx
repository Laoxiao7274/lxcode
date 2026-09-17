/** diff 渲染：GitHub 级视觉（行号列 + 左色条 + 淡底色 + hover）；
 *  行交错淡入（gsap stagger，对齐 .md-seg 的逐块揭示节奏）。 */
import { useEffect, useRef } from "react";
import { gsap } from "gsap";
import { motionAllowed } from "../../../shared/motion";

export function DiffBody({ oldText, newText }: { oldText: string; newText: string }) {
  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = rootRef.current;
    if (!el || !motionAllowed()) return;
    const rows = el.querySelectorAll<HTMLElement>(".diff-line");
    gsap.fromTo(rows, { opacity: 0, x: -4 }, { opacity: 1, x: 0, duration: 0.26, ease: "power2.out", stagger: 0.018, clearProps: "transform,opacity" });
  }, []);

  const oldLines = oldText.split("\n");
  const newLines = newText.split("\n");
  let ln = 310; // 行号从演示剧本的上下文起（视觉真实感）
  return (
    <div className="diff-body" ref={rootRef}>
      {oldLines.map((l, i) => (
        <div key={"o" + i} className="diff-line del">
          <span className="ln" aria-hidden>{ln + i}</span>
          <span className="sign" aria-hidden>−</span>
          <span className="code">{l || " "}</span>
        </div>
      ))}
      {newLines.map((l, i) => (
        <div key={"n" + i} className="diff-line add">
          <span className="ln" aria-hidden>{ln + oldLines.length + i}</span>
          <span className="sign" aria-hidden>+</span>
          <span className="code">{l || " "}</span>
        </div>
      ))}
    </div>
  );
}
