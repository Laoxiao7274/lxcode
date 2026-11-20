// renderer-log.ts —— 渲染层日志落盘与崩溃自动恢复的**纯逻辑**（shell/src/main.ts 消费）。
//
// 背景（2026-12 用户「壳跑一段时间全白屏」）：白屏 = 渲染进程崩溃/OOM 或 React
// 渲染期未捕获异常，两者都无恢复、无日志——打包壳的 stdout 无处落盘，渲染层
// console 全部蒸发。本模块把两件事拆成可单测的纯函数：
//   * 渲染层 console/unresponsive/crash → appendFile 到 <userData>/renderer.log
//     （两代轮转：超 5MB 改名 .old 覆盖；异步写，不阻塞主进程）；
//   * render-process-gone → 自动 reload，按连崩次数退避 5s/15s/30s，连崩超过
//     MAX_CRASH_RECOVERIES 停止（防止崩溃循环把日志刷爆/把 CPU 吃满）。
// main.ts 只做事件接线与实际 reload/写盘（Electron 依赖没法在 node:test 里引入，
// 判定逻辑全部收在这里——与 cert-allow.ts 同一条纪律）。
import { join } from "node:path";

export const RENDERER_LOG_MAX_BYTES = 5 << 20; // 5MB
/** 连崩上限：超过后停止自动 reload（保留日志，让用户手动重启）。 */
export const MAX_CRASH_RECOVERIES = 3;
/** 退避序列（ms）：第 n 次连崩后的等待。 */
const BACKOFF_MS = [5_000, 15_000, 30_000];

/** 日志文件路径（相对 userData）：renderer.log 与上一代 renderer.log.old。 */
export function rendererLogPaths(userDataDir: string): { log: string; old: string } {
  return { log: join(userDataDir, "renderer.log"), old: join(userDataDir, "renderer.log.old") };
}

/** 追加一行渲染层日志（返回要写的整行——含时间戳与级别前缀）。 */
export function rendererLogLine(now: Date, level: string, message: string): string {
  return `[${now.toISOString()}] [${level}] ${message}\n`;
}

/** console-message 的级别 → 人话。Electron 40 的新式事件参数 level 已是字符串
 *  （'info'|'warning'|'error'|'debug'）；旧式位置参数是数字 0..3（verbose/info/
 *  warning/error）。两种形态都接，越界/未知回落 info。 */
export function consoleLevelName(level: number | string): string {
  if (typeof level === "string") return level === "debug" ? "verbose" : level;
  return ["verbose", "info", "warning", "error"][level] ?? "info";
}

/** 轮转判定：shouldRotate(logSize) — 超 5MB 就轮转（两代：.log → .log.old 覆盖）。 */
export function shouldRotate(logSize: number): boolean {
  return logSize >= RENDERER_LOG_MAX_BYTES;
}

/** 连崩退避状态机的判定：第 n 次（从 1 计）连崩后要不要自动恢复、等多久。
 *
 *  返回 { recover: false } = 连崩超过上限，停止自动 reload（防止崩溃循环）；
 *  连崩窗口在「成功加载」时清零（main.ts 在 did-finish-load 里调 resetCrashStreak
 *  ——恢复成功就算数，不再累计）。 */
export function crashRecovery(streak: number): { recover: boolean; waitMs: number } {
  if (streak > MAX_CRASH_RECOVERIES) return { recover: false, waitMs: 0 };
  return { recover: true, waitMs: BACKOFF_MS[Math.min(streak, BACKOFF_MS.length) - 1] };
}
