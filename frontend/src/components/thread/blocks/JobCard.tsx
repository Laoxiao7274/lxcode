// 后台任务卡（本次对话起的任务——时间线里的渲染形态）。
//
// 一行摘要（命令截断）+ 状态/结束原因 + 计时 + 输出 tail + 「结束」。
// 结束后：状态与 EndedBy 文案（你停的 / 它挂了 / 超时 / 后端重启中断）+
// 「查看输出」（job.log 读全量，落盘日志任务结束后仍可读）。
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

export function JobCard({ block, "data-uid": dataUid }: { block: Extract<ThreadBlock, { kind: "job" }>; "data-uid"?: number }) {
  const job = block.job;
  const active = isJobActive(job);
  const now = useJobClock(active);
  const cardRef = useEnterRef<HTMLDivElement>();

  return (
    <div className="job-card" data-uid={dataUid} data-state={jobTone(job)} ref={cardRef}>
      <div className="job-head">
        <span className="job-dot" aria-hidden />
        <span className="job-label mono" title={job.label}>{job.label}</span>
        <span className="job-state">{jobOutcomeText(job)}</span>
        {job.detail && <span className="job-detail mono">{job.detail}</span>}
        <span className="job-time mono">{formatDuration(jobElapsedMs(job, now))}</span>
        <JobKillButton job={job} />
      </div>
      <JobOutput job={job} />
    </div>
  );
}
