// 后台任务卡（本次对话起的任务——时间线里的渲染形态）。
//
// **默认收成一行**（状态点 + 任务 label + 状态词 + 计时 +（运行中）结束按钮）
// ——N 个任务 ≈ 20+ 行输出把对话淹掉，用户报的「铺满界面」就是它。输出预览
// 与落盘路径行收进**展开区**：点卡头展开/收起，默认收起；运行中例外保留
// **一行**实时 tail（最后一行非空输出——仍是一行，不是 4 行预览）。
//
// 完整形态（4 行预览 + 「查看输出」读全量 + 输出路径行）在展开区里，与
// JobsPanel 共用 JobOutput——面板里维持完整形态（面板本来就是专门看任务的
// 地方，不需要再收）。
//
// 计时只在运行中跳（useJobClock 没有运行中的任务就不起表）——已结束的任务
// 时长是定值，让它每秒重渲染是白烧。
import { useState } from "react";
import type { ThreadBlock } from "../../../shared/store";
import { useEnterRef } from "../../../shared/anim";
import { formatDuration, isJobActive, jobElapsedMs, jobOutcomeText, jobTone } from "../../../shared/jobs";
import { useJobClock } from "../../../shared/jobs-admin";
import { JobKillButton } from "../../jobs/JobKillButton";
import { JobOutput } from "../../jobs/JobOutput";

/** 收起态的实时 tail：输出末尾最后一个非空行（一行，不带滚动区）。 */
function lastLine(text: string): string {
  const lines = text.split("\n");
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i].trim();
    if (line) return line;
  }
  return "";
}

export function JobCard({ block, "data-uid": dataUid }: { block: Extract<ThreadBlock, { kind: "job" }>; "data-uid"?: number }) {
  const job = block.job;
  const active = isJobActive(job);
  const now = useJobClock(active);
  const [open, setOpen] = useState(false);
  const cardRef = useEnterRef<HTMLDivElement>();
  const tail = active ? lastLine(job.output_tail) : "";

  const toggle = () => setOpen((v) => !v);

  return (
    <div className="job-card" data-uid={dataUid} data-state={jobTone(job)} ref={cardRef}>
      {/* 卡头可点开/收起（键盘可达）；「结束」按钮 stopPropagation——停任务不该顺带翻转展开态 */}
      <div
        className="job-head"
        role="button"
        tabIndex={0}
        aria-expanded={open}
        title={open ? "收起输出" : "展开输出"}
        onClick={toggle}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            toggle();
          }
        }}
      >
        <span className="job-dot" aria-hidden />
        <span className="job-label mono" title={job.label}>{job.label}</span>
        <span className="job-state">{jobOutcomeText(job)}</span>
        {job.detail && <span className="job-detail mono">{job.detail}</span>}
        <span className="job-time mono">{formatDuration(jobElapsedMs(job, now))}</span>
        <span className="job-caret" aria-hidden>{open ? "▾" : "▸"}</span>
        <span className="job-head-kill" onClick={(e) => e.stopPropagation()}>
          <JobKillButton job={job} />
        </span>
      </div>
      {open ? (
        <JobOutput job={job} />
      ) : (
        tail && <div className="job-tail mono" title={tail}>{tail}</div>
      )}
    </div>
  );
}
