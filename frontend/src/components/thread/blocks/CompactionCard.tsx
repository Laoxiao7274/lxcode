// 历史压缩的标记块（compaction 的渲染形态）：一行分隔条 + 摘要可展开。
// 为什么要有这个块：压缩后历史前缀被替换成摘要，界面必须让用户看见
// 「这里发生过一次压缩」——否则用户会以为对话被吞了（早期轮次突然消失）。
import { useRef, useState } from "react";
import type { ThreadBlock } from "../../../shared/store";
import { useEnterRef } from "../../../shared/anim";
import { Markdown } from "../../../shared/markdown";

export function CompactionCard({ block }: { block: Extract<ThreadBlock, { kind: "compacted" }> }) {
  const [open, setOpen] = useState(false);
  const cardRef = useEnterRef<HTMLDivElement>();
  const bodyRef = useRef<HTMLDivElement | null>(null);
  const hasSummary = block.summary.trim().length > 0;
  // 历史回放时拿不到前后用量（后端只带摘要正文）——那时不显示统计行
  const hasStats = block.before > 0 || block.shadowed > 0;

  return (
    <div className="compact-card" data-manual={block.manual ? "true" : undefined} ref={cardRef}>
      <button
        type="button"
        className="compact-head"
        onClick={() => hasSummary && setOpen((o) => !o)}
        aria-expanded={open}
        disabled={!hasSummary}
      >
        <span className="compact-icon" aria-hidden>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M4 7h16" />
            <path d="M4 12h16" />
            <path d="M4 17h10" />
          </svg>
        </span>
        <span className="compact-title">
          {block.manual ? "已压缩历史（手动）" : "已压缩历史"}
        </span>
        {hasStats ? (
          <span className="compact-stats mono">
            {block.shadowed} 条 · {fmt(block.before)} → {fmt(block.after)}
          </span>
        ) : null}
        {hasSummary && (
          <svg
            className={"compact-chev" + (open ? " open" : "")}
            width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden
          >
            <path d="m6 9 6 6 6-6" />
          </svg>
        )}
      </button>
      {open && hasSummary && (
        <div className="compact-body" ref={bodyRef}>
          <Markdown text={block.summary} />
        </div>
      )}
    </div>
  );
}

function fmt(n: number): string {
  if (n <= 0) return "0";
  if (n < 1000) return String(n);
  return (n / 1000).toFixed(1).replace(/\.0$/, "") + "k";
}
