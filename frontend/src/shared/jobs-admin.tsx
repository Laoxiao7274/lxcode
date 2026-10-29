// 后台任务域（jobs）的 React 粘合层：任务清单订阅 + 结束/读日志两个动作。
//
// 为什么要有 Provider（而不是把回调一路透传到 Block）：任务卡在时间线深处
// （Block → JobCard），而顶栏角标在完全另一个子树（Topbar → JobsBadge）——
// 两边都要「结束」与「看全量输出」。能力接口（source.jobAdmin）是唯一事实源，
// Provider 只做订阅与去抖，UI 不知道数据来自 WS 还是演示。
//
// 清单顺序在 Provider 里定死（sortJobs：在跑的在前）——两个消费者看到的
// 顺序必须一致，各自排一次就会分叉。
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import type { AgentSource, JobInfo, JobLogResult } from "./types";
import { activeJobCount, sortJobs } from "./jobs";

interface JobsValue {
  /** 任务清单（在跑的在前，其余按开始时间倒序）。 */
  jobs: JobInfo[];
  /** 运行中（含正在结束）的任务数——顶栏角标。 */
  active: number;
  /** live = 接的是真实后端（演示模式的输出是脚本生成的）。 */
  live: boolean;
  /** 用户点「结束」（**不二次确认**：低风险且可恢复——任务随时能重起）。
   *  失败向上抛，由调用方就地提示（面板/卡片各自一行，不互相污染）。 */
  kill(id: string): Promise<void>;
  /** 读全量输出（落盘日志；任务结束后仍可读）。失败向上抛。 */
  log(id: string): Promise<JobLogResult>;
}

const Ctx = createContext<JobsValue | null>(null);

export function JobsProvider({ source, children }: { source: AgentSource; children: ReactNode }) {
  const admin = source.jobAdmin;
  const [jobs, setJobs] = useState<JobInfo[]>(() => sortJobs(admin?.jobs() ?? []));

  useEffect(() => {
    if (!admin) {
      setJobs([]);
      return;
    }
    setJobs(sortJobs(admin.jobs()));
    // 后端是事实源：job.list 的结果与 job.started/settled 的增量共用同一份
    // 缓存（WSAgent/DemoAgent 各自维护），这里只做订阅与排序。
    return admin.onJobsChanged(() => setJobs(sortJobs(admin.jobs())));
  }, [admin]);

  const kill = useCallback(
    async (id: string) => {
      if (!admin) throw new Error("后台任务不可用");
      await admin.killJob(id);
    },
    [admin],
  );

  const log = useCallback(
    async (id: string) => {
      if (!admin) throw new Error("后台任务不可用");
      return admin.readJobLog(id);
    },
    [admin],
  );

  const value = useMemo<JobsValue>(
    () => ({ jobs, active: activeJobCount(jobs), live: Boolean(admin), kill, log }),
    [jobs, admin, kill, log],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useJobs(): JobsValue {
  const v = useContext(Ctx);
  if (!v) throw new Error("useJobs 必须在 JobsProvider 内使用");
  return v;
}

/** 运行中任务的计时时钟（每 1s 一跳；没有运行中的任务就不起表）。
 *
 * 不做成 Provider 的字段：那会让整个 jobs 上下文每秒换新值，把所有消费者
 * （含不显示计时的面板）一起重渲染。计时是卡片自己的事。 */
export function useJobClock(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active]);
  return now;
}
