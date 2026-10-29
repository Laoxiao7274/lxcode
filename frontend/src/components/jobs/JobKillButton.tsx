// 「结束」按钮（时间线卡片与顶栏面板共用）。
//
// **不二次确认**（用户拍板）：低风险、可恢复——任务随时能重起，而且用户点的
// 就是「现在停」，多一步确认只是噪音。与 agent 的 job_kill 走同一条后端路径
// （Manager.Kill(id, EndedUser)），只是 by 不同：用户停的会唤醒 agent 并明确
// 告诉它「不要重启」。
import { useState } from "react";
import { isJobActive } from "../../shared/jobs";
import type { JobInfo } from "../../shared/types";
import { useJobs } from "../../shared/jobs-admin";
import { Button } from "../form";

export function JobKillButton({ job }: { job: JobInfo }) {
  const { kill } = useJobs();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const active = isJobActive(job);

  const onKill = () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    kill(job.id)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  };

  if (!active && !error) return null;
  return (
    <span className="job-kill-wrap">
      {active && (
        <Button
          variant="ghost"
          className="job-kill"
          onClick={onKill}
          disabled={busy || job.status === "stopping"}
          title="请求结束这个后台任务（可恢复——需要时随时能重起）"
        >
          {job.status === "stopping" || busy ? "结束中…" : "结束"}
        </Button>
      )}
      {error && <span className="job-kill-err">{error}</span>}
    </span>
  );
}
