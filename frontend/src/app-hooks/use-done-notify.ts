// 对话完成系统通知（2026-12）：主轮完成且窗口失焦时弹系统通知——用户切到别的
// 窗口干活也能知道一轮跑完了。判定全在渲染层（唯一知道焦点与开关的地方），
// 壳侧只管弹（preload 的 __LX__.notify.done → main.ts 的 Notification）。
//
// 触发规则（shouldNotify 纯函数，测试直测同一份）：
//  - ev.type === "done" 且 dispatchId 为空——子会话轮完成不弹（dispatch 卡归
//    父时间线，不是"你的对话完成了"）；
//  - document.hasFocus() 为 false——正看着的会话完成不弹（弹了是打扰）；
//  - 开关开（localStorage lx.notify.done，默认开）。
//
// 浏览器/demo 模式无桥（getNotifyBridge 返回 null）→ 静默跳过，hook 挂载也不炸。
import { useEffect, useMemo } from "react";
import type { AgentEvent, AgentSource } from "../shared/types";
import { getNotifyBridge } from "../agent/host";

/** localStorage 持久化键（设置面板开关 P2 再接 UI，本次只有键与读写）。 */
export const NOTIFY_DONE_KEY = "lx.notify.done";

/** 开关是否打开。默认开；读写失败静默回落默认（theme.ts 同款纪律——隐私模式
 *  等环境下 localStorage 会抛，通知是锦上添花不是关键功能，不因此炸）。 */
export function notifyDoneEnabled(): boolean {
  try {
    return localStorage.getItem(NOTIFY_DONE_KEY) !== "off";
  } catch {
    return true;
  }
}

/** 写开关（值只有 on/off 两种；失败静默——不持久化而已）。 */
export function setNotifyDoneEnabled(on: boolean): void {
  try {
    localStorage.setItem(NOTIFY_DONE_KEY, on ? "on" : "off");
  } catch { /* 隐私模式等——不持久化而已 */ }
}

/** 是否该为这条事件弹通知。纯函数：焦点与开关由调用方传（测试不用 stub DOM）。 */
export function shouldNotify(
  ev: AgentEvent,
  focused: boolean,
  enabled: boolean,
): ev is AgentEvent & { type: "done" } {
  return ev.type === "done" && !ev.dispatchId && !focused && enabled;
}

/** 会话标题查不到时的兜底标题（session.list 缓存尚未就绪/已切走）。 */
export const NOTIFY_FALLBACK_TITLE = "任务完成";

export function useDoneNotify({ source }: { source: AgentSource }): void {
  const bridge = useMemo(() => getNotifyBridge(), []);
  useEffect(() => {
    if (!bridge) return;
    return source.subscribe((ev) => {
      // 焦点与开关在事件到达那一刻现读——判定要的是"此刻"的状态，不是挂载时刻的。
      if (!shouldNotify(ev, document.hasFocus(), notifyDoneEnabled())) return;
      const title = source.sessions().find((s) => s.id === ev.sessionId)?.title || NOTIFY_FALLBACK_TITLE;
      bridge.done({
        title,
        body: ev.content || "本轮已完成",
        sessionId: ev.sessionId,
      });
    });
  }, [source, bridge]);
}
