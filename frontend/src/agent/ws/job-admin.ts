// JobAdminSource 域（后台任务——job.*）：请求编排 + 缓存。
// applyJobList/applyJob 双路：握手并行组的 job.list 全量对齐（onInitList 注册）
// 与 job.started/settled 事件的副作用（onEventSideEffect 注册——事件载荷即最新
// 快照，直接 upsert，不再往返 job.list：与 search.changed 同款）。
import type { JobAdminSource, JobInfo, JobLogResult } from "../../shared/types";
import { jobFromWire, sortJobs, upsertJob } from "../../shared/jobs";
import type { WsConnection } from "./connection";

export class JobAdminDomain implements JobAdminSource {
  /** 后台任务快照（job.list 结果 + job.started/settled 增量；顶栏面板的数据源）。 */
  private jobsCache: JobInfo[] = [];
  private listeners = new Set<() => void>();

  constructor(private readonly conn: WsConnection) {
    conn.onInitList("job.list", (r) => this.applyJobList(r));
    conn.onEventSideEffect((method, _params, ev) => {
      if (method !== "job.started" && method !== "job.settled") return;
      // 任务事件同时是面板缓存的增量：事件载荷即最新快照，直接 upsert
      //（不再往返 job.list——后端广播时已经把它算好了）
      if (ev?.type === "jobStarted" || ev?.type === "jobSettled") this.applyJob(ev.job);
    });
  }

  jobs(): JobInfo[] {
    return this.jobsCache;
  }

  onJobsChanged(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  async listJobs(sessionId?: string): Promise<JobInfo[]> {
    const r = await this.conn.call("job.list", sessionId ? { session_id: sessionId } : undefined);
    const list = (Array.isArray(r) ? r : (r as { jobs?: unknown[] } | null)?.jobs ?? []).map(jobFromWire);
    // 按会话过滤的请求不覆盖全局缓存（顶栏面板看的是跨会话全量）。
    if (!sessionId) this.applyJobList({ jobs: list });
    return list;
  }

  async killJob(id: string): Promise<JobInfo> {
    const r = (await this.conn.call("job.kill", { id })) as { job?: unknown } | null;
    // 应答里的 JobInfo 是请求后的即时快照（stopping）；真正的 settle 随后由
    // job.settled 事件补上——两条路都 upsert，重复更新是幂等的。
    const job = jobFromWire(r?.job ?? r);
    this.applyJob(job);
    return job;
  }

  async readJobLog(id: string): Promise<JobLogResult> {
    const r = (await this.conn.call("job.log", { id })) as { data?: string; truncated?: boolean } | null;
    return { data: r?.data ?? "", truncated: Boolean(r?.truncated) };
  }

  /** job.list 结果 → 缓存 + 通知（重连后的全量对齐）。 */
  private applyJobList(result: unknown) {
    const list = Array.isArray(result) ? result : (result as { jobs?: unknown[] } | null)?.jobs;
    if (!Array.isArray(list)) return;
    this.jobsCache = sortJobs(list.map(jobFromWire));
    this.listeners.forEach((l) => l());
  }

  /** 单个任务快照 → 缓存 + 通知（job.started/settled 与 kill 的应答共用）。 */
  private applyJob(job: JobInfo) {
    if (!job.id) return;
    this.jobsCache = sortJobs(upsertJob(this.jobsCache, job));
    this.listeners.forEach((l) => l());
  }
}
