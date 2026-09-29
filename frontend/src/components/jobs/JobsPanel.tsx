// 后台任务面板（顶栏角标的弹层内容）：**跨会话**的常驻任务清单。
//
// 为什么要有全局入口：dev server / 长构建是在某个会话里起的，但用户切到别的
// 会话（甚至开着别的页面）时依然要能看到它、能停它——只做时间线卡片的话，
// 你必须在那个会话里才管得着它。
//
// 顺序由 Provider 定（在跑的在前）——这里不再排一次，否则两处会分叉。
import type { JobInfo } from "../../shared/types";
import { formatDuration, isJobActive, jobElapsedMs, jobOutcomeText, jobTone, shortSessionId } from "../../shared/jobs";
import { useJobClock, useJobs } from "../../shared/jobs-admin";
import { JobKillButton } from "./JobKillButton";
import { JobOutput } from "./JobOutput";

export function JobsPanel() {
  const { jobs, live } = useJobs();
  return (
    <>
      <div className="job-pop-head">
        <span className="job-pop-title">后台任务</span>
        <span className="job-pop-sub">
          {jobs.length === 0 ? "无" : `${jobs.length} 个`}
        </span>
      </div>
      {jobs.length === 0 ? (
        <div className="job-pop-empty">
          还没有后台任务。
          <br />
          长命令（构建 / 测试 / dev server）走 <code>run_in_background</code> 起任务后会出现在这里，
          跨会话可见、可停——不必回到起它的那个会话。
        </div>
      ) : (
        <div className="job-pop-list">
          {jobs.map((job) => <JobRow key={job.id} job={job} />)}
        </div>
      )}
      {!live && <div className="job-pop-hint">演示模式：任务与输出由前端脚本生成。</div>}
    </>
  );
}

function JobRow({ job }: { job: JobInfo }) {
  const active = isJobActive(job);
  const now = useJobClock(active);
  const tone = jobTone(job);
  return (
    <div className="job-row" data-state={tone}>
      <div className="job-row-head">
        <span className="job-dot" aria-hidden />
        <span className="job-row-label mono" title={job.label}>{job.label}</span>
        <span className="job-row-state">{jobOutcomeText(job)}</span>
        <span className="job-row-time mono">{formatDuration(jobElapsedMs(job, now))}</span>
        <JobKillButton job={job} />
      </div>
      <div className="job-row-meta">
        <span className="job-row-session mono" title={job.session_id ? `会话 ${job.session_id}` : "无归属会话"}>
          {shortSessionId(job.session_id)}
        </span>
        {job.detail && <span className="job-row-detail mono">{job.detail}</span>}
      </div>
      <JobOutput job={job} />
    </div>
  );
}
