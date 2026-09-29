// 后台任务（jobs）域的前端纯逻辑：wire → JobInfo、状态与文案、时长与输出切片。
//
// 单独一个文件（而不是塞进组件）的理由与 mcp-status.ts 同款：这些判定是
// 「契约改了前端静默走偏」的高发层（字段名、EndedBy 文案、超时识别），
// 纯函数才能被 node:test 直接钉住。组件（blocks/JobCard、components/jobs/）
// 与数据源（shared/jobs-admin.tsx）都从这里取语义。
import type { JobEndedBy, JobInfo, JobStatus } from "./types";

/** 后台任务唤醒通告的文本前缀——与后端 `protocol.JobNoticePrefix` **逐字一致**
 *  （含尾空格）。通告在历史里是**真实 user 角色消息**（模型必须当作用户回合才能
 *  回应），所以前端**只能按文本前缀识别**，不能按角色判断（按角色会把通告当成
 *  用户自己说的话渲染成气泡）。 */
export const JOB_NOTICE_PREFIX = "[后台任务通告] ";

/** 重复调用提醒的文本前缀——与后端 `agent.RepeatNoticePrefix` **逐字一致**（含尾
 *  空格）。它与后台任务通告是同一类东西：在历史里都是**真实 user 角色消息**（模型
 *  要把它当用户回合才会回应），所以识别只能靠文本前缀，见 shared/notices.ts 的表。
 *
 *  常量留在本文件而不是 notices.ts：本文件是协议层通告前缀的历史归属地，而 notices.ts
 *  反向 import 这里的常量——若这里再 import 它取表，两个模块互相 import，NOTICE_KINDS
 *  会在常量初始化之前求值（const 的 TDZ 直接抛错）。 */
export const REPEAT_NOTICE_PREFIX = "[重复调用提醒] ";

/** 这条 user 消息是不是后台任务的唤醒通告（前缀匹配，不看角色）。
 *
 *  刻意不委托给 notices.ts 的 noticeLabel：那会与上面的理由同样成环。行为与泛化前
 *  逐字一致（jobNoticeBody 是它唯一的消费者）。 */
export function isJobNotice(text: string): boolean {
  return text.startsWith(JOB_NOTICE_PREFIX);
}

/** 通告正文（去掉前缀——前缀由渲染层作为标签单独显示）。 */
export function jobNoticeBody(text: string): string {
  return isJobNotice(text) ? text.slice(JOB_NOTICE_PREFIX.length).trim() : text;
}

/** 读一个字符串字段：契约（docs/jobs.md §4）只给了 Go 字段名，没给 JSON tag；
 *  本协议既有惯例是 snake_case（session_id / ended_by / output_tail），所以主
 *  键名按 snake_case 读，同时容忍 camelCase——前后端并行实现时两边对 tag 的
 *  选择可能分叉，而分叉的后果是字段**静默变空串**（不是编译错误）。 */
function str(p: Record<string, unknown>, snake: string, camel: string = snake): string {
  const v = p[snake] ?? p[camel];
  return typeof v === "string" ? v : "";
}

/** wire 载荷 → JobInfo（job.started / job.settled 的载荷就是 JobInfo 本身）。 */
export function jobFromWire(raw: unknown): JobInfo {
  const p = (raw ?? {}) as Record<string, unknown>;
  return {
    id: str(p, "id"),
    kind: str(p, "kind"),
    label: str(p, "label"),
    status: str(p, "status") as JobStatus,
    ended_by: str(p, "ended_by", "endedBy") as JobEndedBy,
    detail: str(p, "detail"),
    session_id: str(p, "session_id", "sessionId"),
    owner_session_id: str(p, "owner_session_id", "ownerSessionId"),
    started_at: str(p, "started_at", "startedAt"),
    finished_at: str(p, "finished_at", "finishedAt"),
    output_tail: str(p, "output_tail", "outputTail"),
    output_path: str(p, "output_path", "outputPath"),
  };
}

/** 还在跑（含已请求取消但进程未收尾）——只有这两个状态可以「结束」。 */
export function isJobActive(job: JobInfo): boolean {
  return job.status === "running" || job.status === "stopping";
}

/** 超时被杀：契约把它归在 EndedBy=self + Detail 里写「超时」（§5），
 *  没有独立状态位——所以只能看 Detail，且必须在「它挂了」之前判。 */
export function isJobTimeout(job: JobInfo): boolean {
  return /超时|timeout|timed out/i.test(job.detail);
}

/** 结束原因文案（契约 §6 的四种 + agent 自查的一种）。
 *
 * 顺序即优先级：用户停的 / 后端重启中断 / agent 停的 / 超时 / 它挂了 /
 * 正常结束——EndedBy 是**归属**，比 Detail 里的现象更权威（用户停掉的进程
 * 也会以非零退出码收尾，那不该显示成「它挂了」）。 */
export function jobOutcomeText(job: JobInfo): string {
  if (job.status === "stopping") return "正在结束…";
  if (job.status === "running") return "运行中";
  if (job.ended_by === "user") return "你停的";
  if (job.ended_by === "backend") return "后端重启中断";
  if (job.ended_by === "agent") return "Agent 停的";
  if (isJobTimeout(job)) return "超时";
  if (job.status === "failed") return "它挂了";
  if (job.status === "completed") return "正常结束";
  // 认不出的状态不冒充「正常结束」（绿色会骗人）——中性地说「已结束」
  return "已结束";
}

/** 状态色档（CSS 的 data-state 用：run 蓝 / ok 绿 / err 红 / off 灰）。 */
export function jobTone(job: JobInfo): "run" | "ok" | "err" | "off" {
  if (job.status === "running" || job.status === "stopping") return "run";
  if (job.ended_by === "user" || job.ended_by === "agent" || job.status === "killed") return "off";
  if (job.status === "failed" || isJobTimeout(job)) return "err";
  if (job.status === "completed") return "ok";
  return "off";
}

/** 已运行时长（毫秒）。时间戳缺失/坏数据 → 0（不编一个数）。 */
export function jobElapsedMs(job: JobInfo, now: number): number {
  const start = Date.parse(job.started_at);
  if (!Number.isFinite(start)) return 0;
  const end = job.finished_at ? Date.parse(job.finished_at) : now;
  if (!Number.isFinite(end)) return 0;
  return Math.max(0, end - start);
}

/** 时长显示：45s / 2m 05s / 1h 20m（秒级精度——后台任务看的是量级）。 */
export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h > 0) return `${h}h ${String(m).padStart(2, "0")}m`;
  if (m > 0) return `${m}m ${String(s).padStart(2, "0")}s`;
  return `${s}s`;
}

/** 取文本末尾 n 行（clipped = 前面还有被丢掉的；total = 总行数，提示用）。
 *  空文本 → 空数组（「无输出」与「有一堆空行」是两件事，不混为一谈）。 */
export function tailOf(text: string, n: number): { lines: string[]; clipped: boolean; total: number } {
  if (!text) return { lines: [], clipped: false, total: 0 };
  const all = text.replace(/\n+$/, "").split("\n");
  if (all.length <= n) return { lines: all, clipped: false, total: all.length };
  return { lines: all.slice(all.length - n), clipped: true, total: all.length };
}

/** 任务列表 upsert（同 id 就替换——事件是后端事实源的增量）。
 *  保位置：已在列表里的任务不因更新而跳到最前（面板顺序稳定=可读）。 */
export function upsertJob(list: JobInfo[], job: JobInfo): JobInfo[] {
  const idx = list.findIndex((j) => j.id === job.id);
  if (idx < 0) return [...list, job];
  const next = list.slice();
  next[idx] = job;
  return next;
}

/** 面板顺序：在跑的在前（按开始时间倒序），结束的在后（按结束时间倒序）。
 *
 * 「常驻任务在别的会话里也想看到」的诉求要求它先出现——让用户一眼看到
 * 该不该去停它，而不是在一堆历史任务里翻。 */
export function sortJobs(list: JobInfo[]): JobInfo[] {
  return list.slice().sort((a, b) => {
    const activeA = isJobActive(a) ? 0 : 1;
    const activeB = isJobActive(b) ? 0 : 1;
    if (activeA !== activeB) return activeA - activeB;
    const ta = Date.parse(a.started_at);
    const tb = Date.parse(b.started_at);
    return (Number.isFinite(tb) ? tb : 0) - (Number.isFinite(ta) ? ta : 0);
  });
}

/** 会话 id 的短标识（面板一行放不下完整 id）。 */
export function shortSessionId(id: string): string {
  return id ? id.slice(0, 8) : "—";
}

/** 运行中任务数（顶栏角标用）。 */
export function activeJobCount(list: JobInfo[]): number {
  return list.filter(isJobActive).length;
}
