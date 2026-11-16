// screen-restart.ts —— ffmpeg 重启连败熔断的纯逻辑（不 import electron，
// node:test 直测；screen.ts 的 reconcile 消费同一份判定，测试与生产零漂移）。
//
// 背景：ffmpeg 崩溃时 reconcile 轮询环每秒自动重启一次——编码器坏/驱动崩时
// 无限循环（手机端只见「正在自动重启」闪烁）。scrcpy 的 MAX_CONSECUTIVE_ERRORS
// 同款策略：连续失败达上限停止重启并上报错误；用户重新 start 清零重试。

/** 连败上限（scrcpy MAX_CONSECUTIVE_ERRORS = 3 同值）。 */
export const MAX_CONSECUTIVE_ERRORS = 3;

/** 一次尝试的存活门槛：ffmpeg 起来后存活满 2s 才算「这次成功」（清零连败计数）。
 *  P0-1：spawn 同步成功不算成功——锁屏/驱动坏时 ffmpeg 异步立即退出是常态，
 *  spawn 成功即清零会让计数 0↔1 震荡、熔断永不触发。 */
export const FFMPEG_MIN_ALIVE_MS = 2000;

/** 熔断判定：命令仍要求运行（want）且连败达上限 → 停止自动重启。
 *  want=false 时不熔断（停止路径本来就不再重启，计数另行清零）。 */
export function restartTripped(want: boolean, consecutiveFailures: number): boolean {
  return want && consecutiveFailures >= MAX_CONSECUTIVE_ERRORS;
}

/** 按存活时长判定一次尝试的成败（P0-1）：spawn 后存活 ≥ 2s = 成功；
 *  不足 2s（含 spawn 即退）= 失败。调用方把返回值传给 afterRestartAttempt。 */
export function attemptSucceeded(lifetimeMs: number): boolean {
  return lifetimeMs >= FFMPEG_MIN_ALIVE_MS;
}

/** P1-5：start epoch 变化 = 新的开始意图（用户在手机端显式点了「开始投屏」）。
 *  熔断态下 want 电平不变，壳无法从电平感知「又点了一次」——后端每次
 *  screen.start 递增 start_epoch，壳按 epoch 变化识别显式重试。老后端不下发
 *  该字段（cmdEpoch undefined）恒 false：行为与旧版一致（只在 want=false 清零）。
 *  纯函数：screen.ts 的 reconcile 消费，node:test 直测。 */
export function newStartIntent(lastEpoch: number | undefined, cmdEpoch: number | undefined): boolean {
  return cmdEpoch !== undefined && cmdEpoch !== lastEpoch;
}

/** 一次重启尝试后的新计数：这次尝试成功（存活 ≥ 门槛）→ 清零（连败中断）；
 *  失败（spawn 失败/无编码器/存活 < 2s 退出）→ +1。started 参数保留
 *  （向后兼容：spawn 成功但存活时长未知时传 true）。 */
export function afterRestartAttempt(started: boolean, consecutiveFailures: number): number {
  return started ? 0 : consecutiveFailures + 1;
}

/** 熔断上报文案（err_kind=reason，随 shell state 轮询上报——手机端可读）。 */
export function tripMessage(consecutiveFailures: number): string {
  return `ffmpeg 连续 ${consecutiveFailures} 次异常退出，已停止自动重启——请在手机端点「重新开始投屏」重试`;
}
