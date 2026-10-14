/** 产物汇总卡：一轮任务的改动文件列表 + 增删统计 + 展开看 diff
 *  （Codex 的 artifact viewer）。文件展开走 gsap 高度动画，
 *  挂载 playEnter，行交错浮现。
 *
 *  列表收缩/展开（2026-12）：改动文件多时全量铺开很难看——超过阈值
 *  （FILES_COLLAPSE_THRESHOLD，5）默认收起，只显示头部（图标 + 计数 +
 *  增删徽标）加一行紧凑摘要（最近两个文件名 + 箭头），点摘要行展开
 *  完整列表再点收起。≤ 阈值照常全显且**无箭头**（不可折叠的卡不装
 *  可折叠的样子）。
 *  展开状态是组件内 useState：每张卡独立，不做跨会话记忆；同一轮内
 *  多次渲染（回放）都回到同一默认态（> 阈值收起）。
 *  高度过渡走 motionAllowed 门控（reduced-motion 直接切换无动画）。 */
import { useEffect, useRef, useState } from "react";
import type { FileChange } from "../../../shared/types";
import { gsap } from "gsap";
import { playEnter } from "../../../shared/anim";
import { motionAllowed, staggerIn } from "../../../shared/motion";

/** 超过这个数默认收起列表（只显示头部 + 文件名摘要 + 箭头）。 */
export const FILES_COLLAPSE_THRESHOLD = 5;

/** 默认态判定（纯函数，测试钉子共用）：≤ 阈值恒展开（无折叠概念），
 *  > 阈值默认收起——回放/重渲染都从这条判定取同一初始值（无跨会话记忆）。 */
export function filesDefaultExpanded(count: number): boolean {
  return count <= FILES_COLLAPSE_THRESHOLD;
}

export function FilesCard({ files, "data-uid": dataUid }: { files: FileChange[]; "data-uid"?: number }) {
  const [open, setOpen] = useState<string | null>(null);
  // > 阈值默认收起（回放/重渲染回到同一默认态）；≤ 阈值无折叠概念，恒展开。
  const [expanded, setExpanded] = useState(() => filesDefaultExpanded(files.length));
  const collapsible = files.length > FILES_COLLAPSE_THRESHOLD;
  const rootRef = useRef<HTMLDivElement>(null);
  const headRowRef = useRef<HTMLDivElement>(null);
  // 展开区引用：折叠卡的列表**保持挂载**（height:0 收起），高度补间才有
  // 收起方向的动画目标——条件卸载只能做展开方向，收起就是瞬灭。
  const listRef = useRef<HTMLDivElement>(null);
  const firstRun = useRef(true);

  // 列表高度过渡（motionAllowed 门控）：展开 0→auto、收起 auto→0；
  // reduced-motion 直接置位不补间。首次挂载不播（默认收起的卡静默置 0）。
  useEffect(() => {
    const el = listRef.current;
    if (!el || !collapsible) return;
    const first = firstRun.current;
    firstRun.current = false;
    const COLLAPSED = { height: 0, opacity: 0, overflow: "hidden" };
    if (first || !motionAllowed()) {
      if (!expanded) gsap.set(el, COLLAPSED);
      else if (!first) gsap.set(el, { clearProps: "height,opacity,overflow" });
      return;
    }
    if (expanded) {
      gsap.set(el, { overflow: "hidden" });
      gsap.fromTo(el, { height: 0, opacity: 0 }, {
        height: "auto", opacity: 1, duration: 0.28, ease: "power2.out",
        onComplete: () => gsap.set(el, { clearProps: "height,opacity,overflow" }),
      });
    } else {
      gsap.to(el, { ...COLLAPSED, duration: 0.22, ease: "power2.in" });
    }
  }, [expanded, collapsible]);

  useEffect(() => {
    playEnter(rootRef.current, { y: 10, opacity: 0 }, { y: 0, opacity: 1, duration: 0.34, ease: "power2.out", clearProps: "transform,opacity" });
    // 行交错浮现只对当前可见的行（收起时列表不在 DOM 里，自然跳过）。
    const rows = rootRef.current?.querySelectorAll<HTMLElement>(".files-file");
    if (rows && motionAllowed()) {
      gsap.fromTo(rows, { opacity: 0, y: 5 }, { opacity: 1, y: 0, duration: 0.24, ease: "power2.out", stagger: 0.05, delay: 0.08, clearProps: "transform,opacity" });
    }
    staggerIn(headRowRef.current ? [headRowRef.current] : [], {});
  }, []);

  const added = files.reduce((n, f) => n + f.added, 0);
  const deleted = files.reduce((n, f) => n + f.deleted, 0);

  return (
    <div className="files-card" data-uid={dataUid} ref={rootRef}>
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
      {collapsible ? (
        <>
          <div className="files-list" ref={listRef}>
            {files.map((f) => (
              <FilesRow key={f.path} file={f} open={open === f.path} onToggle={() => setOpen(open === f.path ? null : f.path)} />
            ))}
          </div>
          <button type="button" className="files-toggle" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
            <span className="files-chevron">{expanded ? "▾" : "▸"}</span>
            <span className="files-summary">
              {expanded ? "收起列表" : files.slice(-2).map((f) => f.path).join("、")}
            </span>
          </button>
        </>
      ) : (
        files.map((f) => (
          <FilesRow key={f.path} file={f} open={open === f.path} onToggle={() => setOpen(open === f.path ? null : f.path)} />
        ))
      )}
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
