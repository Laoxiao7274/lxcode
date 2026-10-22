// update-dialog-model.ts —— 更新弹窗的纯展示逻辑（不 import React/electron，
// node:test 直接断言）。口径只在这里写一份：组件只负责画，测试与组件共用。
//
// 为什么单拎出来：进度文案有两个易错点——① 总量未知（manifest 没写 size 且
// 响应无 content-length）时不能编一个分母；② 下载完成后还有整包 sha256 +
// 解压 + 逐文件校验（数秒），进度条 100% 但仍在忙，必须换成「校验中」，
// 否则用户以为卡死。两处都由纯函数给，避免组件里散落条件判断。
import { humanBytes } from "../../shared/connections";
import type { UpdatePhase } from "../../shared/update";

/** 弹窗标题：每个 phase 一句，不共用模糊文案。 */
export function dialogTitle(phase: UpdatePhase): string {
  switch (phase) {
    case "checking": return "正在检查更新";
    case "latest": return "已是最新版本";
    case "available": return "发现新版本";
    case "downloading": return "正在下载更新";
    case "ready": return "更新已就绪";
    case "installing": return "正在安装更新";
    default: return "软件更新";
  }
}

/** 阶段一句话（进度区上方）。verifying = 已下完、在校验解压。 */
export function stageText(stage: "downloading" | "verifying", percent: number): string {
  if (stage === "verifying") return "下载完成，正在校验安装包…";
  if (percent >= 100) return "下载完成，正在校验安装包…";
  return "正在下载更新包…";
}

/** 已下载 / 总量。总量未知（0）时只给已下载，不编分母。 */
export function bytesText(downloaded: number, total: number): string {
  if (!(total > 0)) return downloaded > 0 ? humanBytes(downloaded) : "";
  return `${humanBytes(Math.min(downloaded, total))} / ${humanBytes(total)}`;
}

/** 百分比（0-100 整数）。总量未知时按 0 处理（进度条不动，不假装有进度）。 */
export function percentOf(downloaded: number, total: number, reported: number): number {
  if (total > 0) return Math.max(0, Math.min(100, Math.round((downloaded / total) * 100)));
  return Math.max(0, Math.min(100, Math.round(reported)));
}

/** 弹窗能否关闭：安装已排程时不可关（应用即将重启，关掉只会让用户以为
 *  更新没生效）。下载中可关——后台继续，完成时会重新打开引导安装。 */
export function canClose(phase: UpdatePhase): boolean {
  return phase !== "installing";
}

/** 关闭按钮的语义说明（下载中关闭 ≠ 取消）。 */
export function closeHint(phase: UpdatePhase): string {
  if (phase === "downloading") return "关闭弹窗不会中断下载——完成后会再次提醒你安装";
  if (phase === "ready") return "稍后安装也行——更新已下载校验完成，随时可在设置里继续";
  if (phase === "available") return "稍后更新也行——可在设置 · 软件更新里继续";
  return "关闭";
}

/** 失败步骤 → 重试按钮文案。 */
export function retryLabel(action: "check" | "download" | "install" | null): string {
  if (action === "download") return "重新下载";
  if (action === "install") return "重新安装";
  return "重试";
}
