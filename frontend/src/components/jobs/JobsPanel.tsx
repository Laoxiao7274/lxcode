// 后台任务面板（顶栏角标的弹层内容）：**跨会话**的常驻任务清单 + 「合并请求」入口。
//
// 为什么要有全局入口：dev server / 长构建是在某个会话里起的，但用户切到别的
// 会话（甚至开着别的页面）时依然要能看到它、能停它——只做时间线卡片的话，
// 你必须在那个会话里才管得着它。
//
// 「合并请求」入口（2026-10 用户拍板）：把本会话分支的改动交给后台合并 Agent
// 汇总到集成分支——与 merge_request 工具同一条后端路径（chat.mergeRequest），
// 只是发起方从模型换成用户。只对项目会话可用（未分组会话禁用并说明原因）。
//
// 顺序由 Provider 定（在跑的在前）——这里不再排一次，否则两处会分叉。
import { useState } from "react";
import type { JobInfo } from "../../shared/types";
import { formatDuration, isJobActive, jobElapsedMs, jobOutcomeText, jobTone, shortSessionId } from "../../shared/jobs";
import { useJobClock, useJobs } from "../../shared/jobs-admin";
import { JobKillButton } from "./JobKillButton";
import { JobOutput } from "./JobOutput";
import { Button, TextInput } from "../form";

/** 合并请求入口的数据（App 按当前会话算好传入——面板自己不知道焦点在哪条会话）。 */
export interface MergeEntry {
  /** 发起合并的会话（当前会话）。 */
  sessionId: string;
  /** 该会话的工作区（项目 id）；空 = 未分组会话——没有可合并的分支，入口禁用。 */
  workspace: string;
  /** 发起合并（targetBranch 空串 = 后端默认 lxcode/integration）。
   *  失败抛错——面板就地显示（后端文案照搬：未分组 / 已有在跑的合并进程等）。 */
  onStart(sessionId: string, targetBranch: string): Promise<void>;
}

/** 合并的默认目标分支（与后端 defaultIntegrationBranch 一致）。 */
const DEFAULT_MERGE_BRANCH = "lxcode/integration";

export function JobsPanel({ merge }: { merge?: MergeEntry }) {
  const { jobs, live } = useJobs();
  return (
    <>
      <div className="job-pop-head">
        <span className="job-pop-title">后台任务</span>
        <span className="job-pop-sub">
          {jobs.length === 0 ? "无" : `${jobs.length} 个`}
        </span>
      </div>
      {merge && <MergeRow merge={merge} />}
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

/** 「合并请求」行：目标分支输入（默认 lxcode/integration）+ 发起按钮。
 *
 *  与 merge_request 工具等价：确认后调 chat.mergeRequest，任务出现在本面板里、
 *  结束后父会话收到通告。未分组会话禁用（title + 行内说明双通道——禁用元素不派发
 *  鼠标事件，只靠 title 提示在多数浏览器里不弹）。 */
function MergeRow({ merge }: { merge: MergeEntry }) {
  const [branch, setBranch] = useState(DEFAULT_MERGE_BRANCH);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const disabled = !merge.workspace || busy;
  const start = async () => {
    setBusy(true);
    setError("");
    try {
      await merge.onStart(merge.sessionId, branch.trim());
    } catch (e) {
      // 后端文案照搬（未分组 / 已有在跑的合并进程……）——就地显示，不吞
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="job-pop-merge" data-off={disabled ? "true" : undefined}>
      <span className="job-pop-merge-label">合并请求</span>
      <TextInput
        className="job-merge-branch mono"
        value={branch}
        onChange={setBranch}
        disabled={disabled}
        placeholder={DEFAULT_MERGE_BRANCH}
        aria-label="合并目标分支"
        title={merge.workspace ? "目标分支（默认 lxcode/integration）" : "当前会话没有独立工作区"}
      />
      <Button
        className="job-merge-go"
        variant="primary"
        disabled={disabled}
        onClick={() => { void start(); }}
        title={merge.workspace ? "起一个后台合并进程（把本会话分支合并到集成分支）" : "当前会话没有独立工作区"}
      >
        {busy ? "发起中…" : "发起合并"}
      </Button>
      {!merge.workspace && <span className="job-merge-hint">当前会话没有独立工作区</span>}
      {error && <span className="job-merge-error" role="alert">{error}</span>}
    </div>
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
