/** 产物汇总卡：一轮任务的改动文件列表 + 增删统计 + 展开看 diff
 *  （Codex 的 artifact viewer）。文件展开走 gsap 高度动画，
 *  挂载 playEnter，行交错浮现。 */
import { useEffect, useRef, useState } from "react";
import type { FileChange } from "../../../shared/types";
import { gsap } from "gsap";
import { playEnter } from "../../../shared/anim";
import { motionAllowed, staggerIn } from "../../../shared/motion";

export function FilesCard({ files }: { files: FileChange[] }) {
  const [open, setOpen] = useState<string | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const headRowRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    playEnter(rootRef.current, { y: 10, opacity: 0 }, { y: 0, opacity: 1, duration: 0.34, ease: "power2.out", clearProps: "transform,opacity" });
    const rows = rootRef.current?.querySelectorAll<HTMLElement>(".files-file");
    if (rows && motionAllowed()) {
      gsap.fromTo(rows, { opacity: 0, y: 5 }, { opacity: 1, y: 0, duration: 0.24, ease: "power2.out", stagger: 0.05, delay: 0.08, clearProps: "transform,opacity" });
    }
    staggerIn(headRowRef.current ? [headRowRef.current] : [], {});
  }, []);

  const added = files.reduce((n, f) => n + f.added, 0);
  const deleted = files.reduce((n, f) => n + f.deleted, 0);

  return (
    <div className="files-card" ref={rootRef}>
      <div className="files-head" ref={headRowRef}>
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9Z" />
          <path d="M13 2v7h7" />
        </svg>
        <span className="files-title">改动 {files.length} 个文件</span>
        <span className="diff-stats">
          <span className="stat-badge stat-del">−{deleted}</span>
          <span className="stat-badge stat-add">+{added}</span>
        </span>
      </div>
      {files.map((f) => (
        <FilesRow key={f.path} file={f} open={open === f.path} onToggle={() => setOpen(open === f.path ? null : f.path)} />
      ))}
    </div>
  );
}

/** 单文件行 + 展开动画（gsap 高度补间：收起保持 height:0，展开收尾
 *  clearProps 让内容自然伸展）。 */
function FilesRow({ file, open, onToggle }: { file: FileChange; open: boolean; onToggle: () => void }) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const firstRun = useRef(true);

  useEffect(() => {
    const el = bodyRef.current;
    if (!el) return;
    const first = firstRun.current;
    firstRun.current = false;
    const COLLAPSED = { height: 0, opacity: 0, overflow: "hidden" };
    if (first || !motionAllowed()) {
      if (!open) gsap.set(el, COLLAPSED);
      else if (!first) gsap.set(el, { clearProps: "height,opacity,overflow" });
      return;
    }
    if (open) {
      gsap.set(el, { overflow: "hidden" });
      gsap.fromTo(el, { height: 0, opacity: 0 }, {
        height: "auto", opacity: 1, duration: 0.3, ease: "power2.out",
        onComplete: () => gsap.set(el, { clearProps: "height,opacity,overflow" }),
      });
      const rows = el.querySelectorAll<HTMLElement>(".diff-line");
      gsap.fromTo(rows, { opacity: 0, x: -4 }, { opacity: 1, x: 0, duration: 0.24, ease: "power2.out", stagger: 0.014, delay: 0.05, clearProps: "transform,opacity" });
    } else {
      gsap.to(el, { ...COLLAPSED, duration: 0.22, ease: "power2.in" });
    }
  }, [open]);

  return (
    <div className="files-row">
      <button type="button" className="files-file" aria-expanded={open} onClick={onToggle}>
        <span className="files-chevron">{open ? "▾" : "▸"}</span>
        <span className="files-path">{file.path}</span>
        <span className="diff-stats">
          <span className="stat-badge stat-del">−{file.deleted}</span>
          <span className="stat-badge stat-add">+{file.added}</span>
        </span>
      </button>
      <div className="files-diff-wrap" ref={bodyRef}>
        <div className="diff-body">
          {file.diff.split("\n").map((l, i) => (
            <div
              key={i}
              className={"diff-line" + (l.startsWith("+") ? " add" : l.startsWith("-") && !l.startsWith("---") ? " del" : "")}
            >
              <span className="ln" aria-hidden>{l.startsWith("@") ? "@" : ""}</span>
              <span className="sign" aria-hidden>{l.startsWith("+") ? "+" : l.startsWith("-") ? "−" : ""}</span>
              <span className="code">{l || " "}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
