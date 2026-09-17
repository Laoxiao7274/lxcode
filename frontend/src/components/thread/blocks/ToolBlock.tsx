// 工具行（lx-dsh ui-tool 的 ToolRow/DisclosureRow/TerminalBlock 逐项对齐）：
// 折叠行 [leading 16px] 6 [标题 13/24] [sep 2px] [摘要 ellipsis] [chevron]；
// leading：空闲=工具图标（hover 淡出换 chevron），运行/出错=StateDot；
// 展开体 = TerminalBlock（浅色代码卡 + 左 gutter 状态点 + 命令 banner +
// 输出 22px 行高 max 224px）；gsap 高度补间 0.22s/0.16s。
// edit 工具走 diff 卡（DSH DiffBlock 同款语言）。
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ThreadBlock } from "../../../shared/store";
import { gsap } from "gsap";
import { playEnter } from "../../../shared/anim";
import { motionAllowed } from "../../../shared/motion";
import { clip, shortArgs, prettyCmdline } from "../helpers";
import { DiffBody } from "./DiffBody";

const TOOL_TITLES: Record<string, string> = {
  read_file: "读文件",
  search: "搜索",
  session_search: "查历史",
  edit: "改文件",
  write_file: "写文件",
  bash: "bash",
  todo: "任务清单",
};

export function ToolBlock({ block }: { block: Extract<ThreadBlock, { kind: "tool" }> }) {
  const rootRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [bodyMounted, setBodyMounted] = useState(false);
  const bodyRef = useRef<HTMLDivElement>(null);
  const running = block.result === undefined;
  const expandable = !running;

  useEffect(() => {
    playEnter(rootRef.current);
  }, []);

  // 展开/收起（DSH DisclosureRow 同款节奏：开 0.22s power2.out 补高度，
  // 关 0.16s power2.in 压到 0 再卸载 body——收起动画完整可见）。
  // useLayoutEffect：挂载帧前就位起始态，无闪烁。
  useLayoutEffect(() => {
    const el = bodyRef.current;
    if (!el || !motionAllowed()) return;
    if (open) {
      gsap.fromTo(el, { height: 0, opacity: 0 }, {
        height: "auto", opacity: 1, duration: 0.22, ease: "power2.out", overwrite: true,
        onComplete: () => gsap.set(el, { clearProps: "height,opacity,overflow" }),
      });
      gsap.set(el, { overflow: "hidden" });
    } else if (bodyMounted) {
      gsap.to(el, {
        height: 0, opacity: 0, duration: 0.16, ease: "power2.in", overwrite: true,
        onComplete: () => setBodyMounted(false),
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, bodyMounted]);
  useEffect(() => {
    if (open) setBodyMounted(true);
  }, [open]);

  // JSON 解析只做一次/参数变化（memo(Block) 已挡掉大部分重渲染）
  const { argsSummary, cmdline } = useMemo(
    () => ({ argsSummary: clip(shortArgs(block), 72), cmdline: prettyCmdline(block.name, block.arguments) }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [block.name, block.arguments],
  );

  // edit 工具：diff 视图（改动即所见——不再让用户读 JSON 参数）
  const editDiff = useMemo((): { path: string; old_string: string; new_string: string } | null => {
    if (block.name !== "edit" || !block.arguments) return null;
    try {
      const a = JSON.parse(block.arguments) as Partial<{ path: string; old_string: string; new_string: string }>;
      if (!a.path || a.old_string === undefined || a.new_string === undefined) return null;
      return { path: a.path, old_string: a.old_string, new_string: a.new_string };
    } catch {
      return null;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [block.arguments]);

  if (editDiff && block.name === "edit") {
    return (
      <div className="tool-block diff-block" ref={rootRef}>
        <div className="diff-file-head">
          <span className="ticon" aria-hidden>
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 20h9" />
              <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
            </svg>
          </span>
          <span className="tname">{editDiff.path}</span>
          <span className="diff-stats">
            <span className="stat-badge stat-del">−{editDiff.old_string.split("\n").length}</span>
            <span className="stat-badge stat-add">+{editDiff.new_string.split("\n").length}</span>
          </span>
          {running && <span className="tool-open-hint">执行中…</span>}
        </div>
        {!running && (
          <DiffBody oldText={editDiff.old_string} newText={editDiff.new_string} />
        )}
      </div>
    );
  }

  const title = TOOL_TITLES[block.name] ?? block.name;
  // 出错：错误输出的首行（DSH 的 errorSummary = firstLine(output)）
  const errorSummary = !running && block.isError ? (block.result ?? "").split("\n")[0] : null;
  const summary = errorSummary ?? (running ? "执行中…" : argsSummary);

  return (
    <div
      className={"trow" + (running ? " trow-running" : "") + (block.isError ? " trow-error" : "") + (open ? " trow-open" : "")}
      ref={rootRef}
    >
      <div
        className="trow-row"
        role={expandable ? "button" : undefined}
        tabIndex={expandable ? 0 : undefined}
        aria-expanded={expandable ? open : undefined}
        onClick={expandable ? () => setOpen((o) => !o) : undefined}
        onKeyDown={(e) => {
          if (expandable && (e.key === "Enter" || e.key === " ")) {
            e.preventDefault();
            setOpen((o) => !o);
          }
        }}
      >
        {/* leading：运行/出错 = StateDot；空闲 = 图标（hover 淡出换 chevron 提示可展开） */}
        <span className="trow-leading" aria-hidden>
          {running || block.isError ? (
            <span className={"sdot" + (running ? " run" : " err")} />
          ) : (
            <>
              <svg className="trow-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
                <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
              </svg>
              <svg className="trow-chev" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <path d="m6 9 6 6 6-6" />
              </svg>
            </>
          )}
        </span>
        <span className="trow-title">{title}</span>
        <span className="trow-sep" aria-hidden />
        <span className={"trow-summary" + (errorSummary ? " err" : "")}>{summary}</span>
      </div>
      {/* 展开体：TerminalBlock 形态（浅色代码卡 + 左 gutter 状态点 + 命令 banner +
          输出）——banner 的状态点与行 leading 同步（DSH 的 runState） */}
      {bodyMounted && (
        <div className="trow-body" ref={bodyRef}>
          <div className="tterm" data-running={running ? "true" : undefined}>
            <div className="tterm-banner">
              {running && <span className="sdot run" aria-hidden />}
              <span className="tterm-cmd">{cmdline}</span>
            </div>
            <pre className="tterm-out" data-error={block.isError ? "true" : undefined}>
              {clip(block.result ?? "", 1400) || <span className="tterm-empty">（无输出）</span>}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}
