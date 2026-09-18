// 主 Agent 调度子 Agent 的卡（agent.dispatch 的渲染形态——M3 语义的
// 前端部分）：Agent 身份头（色点 + 名 + 任务摘要）+ 状态（运行中 spinner/
// 已完成）+ 子执行过程（嵌套块缩进——子上下文隔离）+ 最终结果（验收）。
// 子块的事件流已按 dispatchId 归属到 subBlocks（store 的归属路由）。
import { useRef, useState } from "react";
import type { ThreadBlock } from "../../../shared/store";
import { useEnterRef } from "../../../shared/anim";
import { Markdown } from "../../../shared/markdown";
import { Block } from "./Block";

export function DispatchCard({ block, onConfirm }: {
  block: Extract<ThreadBlock, { kind: "dispatch" }>;
  onConfirm: (id: string, allow: boolean) => void;
}) {
  // 子过程可折叠（默认展开——运行中盯着进度；完成后自动折叠留结果）
  const [expanded, setExpanded] = useState(block.status === "running");
  const cardRef = useEnterRef<HTMLDivElement>();
  const bodyRef = useRef<HTMLDivElement | null>(null);
  const done = block.status === "done";
  const subCount = block.subBlocks.length;

  return (
    <div className="dispatch-card" data-done={done ? "true" : undefined} data-error={block.isError ? "true" : undefined} ref={cardRef}>
      <button
        type="button"
        className="dispatch-head"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
      >
        <span className="dispatch-dot" style={{ background: block.agentColor }} aria-hidden />
        <span className="dispatch-agent">{block.agentName}</span>
        <span className="dispatch-task" title={block.task}>{clipTask(block.task)}</span>
        <span className="dispatch-state">
          {done ? (
            <>
              {block.isError ? "✗ 失败" : "✓ 完成"}
              {block.usageTokens ? <span className="dispatch-tokens mono">{block.usageTokens} tk</span> : null}
            </>
          ) : (
            <>
              <span className="mset-spinner" aria-hidden />
              执行中
            </>
          )}
        </span>
        {subCount > 0 && (
          <svg
            className={"dispatch-chev" + (expanded ? " open" : "")}
            width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden
          >
            <path d="m6 9 6 6 6-6" />
          </svg>
        )}
      </button>

      {done && block.result && (
        <div className="dispatch-result">
          <Markdown text={block.result} />
        </div>
      )}

      {subCount > 0 && expanded && (
        <div className="dispatch-body" ref={bodyRef}>
          {block.subBlocks.map((sub) => (
            <Block key={sub.uid} block={sub} onConfirm={onConfirm} />
          ))}
        </div>
      )}
    </div>
  );
}

function clipTask(task: string): string {
  const one = task.replace(/\n+/g, " ").trim();
  const r = [...one];
  if (r.length <= 60) return one;
  return r.slice(0, 60).join("") + "…";
}
