// 后台任务（jobs.*）与后端日志（logs.tail）的 wire 形态与能力接口。

/** 后台任务的运行状态（对齐 protocol/jobs.Status）。 */
export type JobStatus = "running" | "stopping" | "completed" | "killed" | "failed";

/** 「谁结束的」——空 = 还在跑。用户点「结束」与 agent 自己 kill 在这里分叉：
 *  前者要唤醒 agent 并告诉它「不要重启」，后者是同一轮内的已知动作。 */
export type JobEndedBy = "" | "self" | "user" | "agent" | "backend";

/** 后台任务快照（对齐 protocol.JobInfo，见 docs/jobs.md §4）。
 *
 * 字段名逐字对齐契约（wire 命名按本协议惯例是 snake_case：session_id /
 * ended_by / output_tail）；这是**对外快照**，UI 直接消费它，不再另立一份
 * 视图模型——多一层映射只会让「契约改了前端不知道」多一个静默点。 */
export interface JobInfo {
  id: string;
  /** 任务种类（当前只有 "bash"）。 */
  kind: string;
  /** 一行摘要（命令截断——UI 与通告都用它）。 */
  label: string;
  status: JobStatus;
  ended_by: JobEndedBy;
  /** 退出码 / 信号 / 超时 / 重启（人话，直接显示）。 */
  detail: string;
  /** 执行会话（谁起的任务；空 = 不属于任何会话，只进全局面板）。 */
  session_id: string;
  /** **时间线归属**（顶层会话）：子 Agent 起的任务挂在父会话上，前端据此把它
   *  放进**父会话**的时间线——子会话不进侧栏，按执行会话上卡等于用户在主对话里
   *  什么都看不到。空 = 回落 session_id。 */
  owner_session_id: string;
  /** 任务起时所属的**项目 id**（后端 Start 时从执行会话的 ctx 焊进 Spec，之后
   *  不变）。面板据此按项目分组渲染；空 = 未分组会话（收进「未分组」组）。
   *  omitempty：老后端没有这个键。 */
  workspace?: string;
  /** RFC3339；finished_at 空 = 未结束。 */
  started_at: string;
  finished_at: string;
  /** 最近 64KB 输出（面板直接显示，不必往返 job.log）。 */
  output_tail: string;
  /** 落盘日志路径（恒有——任务结束后仍可读全量）。 */
  output_path: string;
}

/** job.log 的结果：全量输出（后端可能截断，truncated 标明）。 */
export interface JobLogResult {
  data: string;
  truncated: boolean;
}

/** JobAdminSource：后台任务的查看与结束（后端 job.* 直通）。
 *
 * 与 ModelAdminSource / SearchAdminSource 同模式：UI 依赖能力接口而非具体
 * WSAgent；Demo 也实现（演示模式无后端也要能全量跑 UI——起任务、看输出、
 * 点结束）。事件（job.started / job.settled）与 job.list 共用同一份缓存。 */
export interface JobAdminSource {
  /** 当前任务快照（job.list 结果 + job.started/settled 增量，按开始时间倒序）。 */
  jobs(): JobInfo[];
  /** 订阅任务变化（连接建立 / job.started / job.settled；返回退订）。 */
  onJobsChanged(listener: () => void): () => void;
  /** 拉取任务清单（sessionId 为空 = 全部）；结果写回缓存。 */
  listJobs(sessionId?: string): Promise<JobInfo[]>;
  /** **用户点「结束」**——后端走 Manager.Kill(id, EndedUser)，与 agent 的
   *  job_kill 同一条路径（只是 by 不同，两条路径行为永远一致）。 */
  killJob(id: string): Promise<JobInfo>;
  /** 读全量输出（落盘日志——任务结束后仍可读）。 */
  readJobLog(id: string): Promise<JobLogResult>;
}

/** 后端日志尾（logs.tail——桌面错误日志查看器，2026-12）。content 是文件
 *  尾部的字节（可能跨行截断，原样展示）；truncated = 文件比请求的字节数大
 *  （读的只是尾部）；path 是当前日志文件路径（展示用）。 */
export interface LogsTailResult {
  path: string;
  content: string;
  truncated: boolean;
}

/** logs.tail 的请求参数（对齐 protocol.LogsTailParams）：bytes = 要读的尾部
 *  字节数，0/缺省 = 后端缺省 16KB。 */
export interface LogsTailParams {
  bytes?: number;
}

/** LogsSource：后端日志查看（logs.tail 直通）。
 *
 * 与 JobAdminSource 同模式：UI 依赖能力接口而非具体 WSAgent；缺省（demo）
 * 时设置面板「日志」分区回自解释文案（演示模式没有后端日志可看）。 */
export interface LogsSource {
  /** 读后端当前日志文件的尾部。bytes 缺省 = 16KB；失败向上抛（文案照后端）。 */
  tail(bytes?: number): Promise<LogsTailResult>;
}
