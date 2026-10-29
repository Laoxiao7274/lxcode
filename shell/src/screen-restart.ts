// screen-restart.ts —— ffmpeg 重启连败熔断的纯逻辑（不 import electron，
// node:test 直测；screen.ts 的 reconcile 消费同一份判定，测试与生产零漂移）。
//
// 背景：ffmpeg 崩溃时 reconcile 轮询环每秒自动重启一次——编码器坏/驱动崩时
// 无限循环（手机端只见「正在自动重启」闪烁）。scrcpy 的 MAX_CONSECUTIVE_ERRORS
// 同款策略：连续失败达上限停止重启并上报错误；用户重新 start 清零重试。

/** 连败上限（scrcpy MAX_CONSECUTIVE_ERRORS = 3 同值）。 */
export const MAX_CONSECUTIVE_ERRORS = 3;

/** 熔断判定：命令仍要求运行（want）且连败达上限 → 停止自动重启。
 *  want=false 时不熔断（停止路径本来就不再重启，计数另行清零）。 */
export function restartTripped(want: boolean, consecutiveFailures: number): boolean {
  return want && consecutiveFailures >= MAX_CONSECUTIVE_ERRORS;
}

/** 一次重启尝试后的新计数：进程起来了 → 清零（连败中断）；
 *  没起来（spawn 失败/无编码器/立即退出）→ +1。 */
export function afterRestartAttempt(started: boolean, consecutiveFailures: number): number {
  return started ? 0 : consecutiveFailures + 1;
}

/** 熔断上报文案（err_kind=reason，随 shell state 轮询上报——手机端可读）。 */
export function tripMessage(consecutiveFailures: number): string {
  return `ffmpeg 连续 ${consecutiveFailures} 次异常退出，已停止自动重启——请在手机端重新开始投屏`;
}
