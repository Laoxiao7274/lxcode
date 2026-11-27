// 对话完成/失败系统通知（2026-12）：主轮完成或失败且窗口失焦时弹系统通知——
// 用户切到别的窗口干活也能知道一轮跑完了/跑挂了。判定全在渲染层（唯一知道焦点
// 与开关的地方），壳侧只管弹（preload 的 __LX__.notify → main.ts 的 Notification）。
//
// 触发规则（shouldNotify 纯函数，测试直测同一份）：
//  - ev.type === "done" 且 dispatchId 为空——子会话轮完成不弹（dispatch 卡归
//    父时间线，不是"你的对话完成了"）；
//  - finishReason === "stop"——白名单制（2026-12 通知误触发修复二段）：
//    后端每轮都发 done，中间轮 finish_reason=tool_calls 表示「模型在等工具
//    结果，对话没完成」；主 Agent 发起派发的那轮（工具 = dispatch）没有
//    dispatchId 却是 tool_calls 轮，只滤 dispatchId 会误弹。黑名单写成白名单
//    是因为 `?? "stop"` 兜不住空串（`??` 不兜 ""）——OpenAI 端点缺省
//    finish_reason 会透传空串，黑名单下空串漏网照弹；白名单下只有明确
//    stop 才弹，unknown/aborted/空串一律静默（aborted = 用户主动取消，
//    通知「任务被取消」是打扰）。stop done 到达时派发必然已收尾（runTools
//    的阻塞语义保证），不会出现重复通知；
//  - 或 ev.type === "error" 且 aborted 非真——一轮失败（后端错误/流断）：
//    用户取消（aborted=true）不是失败，不弹；子会话轮（dispatchId）的错误
//    已经变成派发卡内的呈现（TurnError 转 DispatchEnd），不单独弹；
//  - document.hasFocus() 为 false——正看着的会话完成不弹（弹了是打扰）；
//  - 开关开（localStorage lx.notify.done，默认开）。
//
// 浏览器/demo 模式无桥（getNotifyBridge 返回 null）→ 静默跳过，hook 挂载也不炸。
import { useEffect, useMemo } from "react";
import type { AgentEvent, AgentSource } from "../shared/types";
import { getNotifyBridge } from "../agent/host";

/** localStorage 持久化键（设置面板通用区 ToggleRow 读写）。 */
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

/** 是否该为这条事件弹通知。纯函数：焦点与开关由调用方传（测试不用 stub DOM）。
 *  返回值收窄事件类型（null = 不弹；非空 = kind 对应的事件可访问字段）。 */
export function shouldNotify(
  ev: AgentEvent,
  focused: boolean,
  enabled: boolean,
): { kind: "done"; ev: AgentEvent & { type: "done" } } | { kind: "error"; ev: AgentEvent & { type: "error" } } | null {
  if (focused || !enabled) return null;
  if (ev.type === "done") {
    if (ev.dispatchId || ev.finishReason !== "stop") return null;
    return { kind: "done", ev };
  }
  if (ev.type === "error") {
    // 用户主动取消不是失败；子会话轮的错误上不了主时间线（派发卡内呈现）
    if (ev.aborted || ev.dispatchId) return null;
    return { kind: "error", ev };
  }
  return null;
}

/** 会话标题查不到时的兜底标题。 */
export const NOTIFY_FALLBACK_TITLE_DONE = "任务完成";
export const NOTIFY_FALLBACK_TITLE_ERROR = "任务失败";

export function useDoneNotify({ source }: { source: AgentSource }): void {
  const bridge = useMemo(() => getNotifyBridge(), []);
  useEffect(() => {
    if (!bridge) return;
    return source.subscribe((ev) => {
      // 焦点与开关在事件到达那一刻现读——判定要的是"此刻"的状态，不是挂载时刻的。
      const hit = shouldNotify(ev, document.hasFocus(), notifyDoneEnabled());
      if (!hit) return;
      const title = source.sessions().find((s) => s.id === hit.ev.sessionId)?.title;
      if (hit.kind === "done") {
        bridge.done({
          title: title || NOTIFY_FALLBACK_TITLE_DONE,
          body: hit.ev.content || "本轮已完成",
          sessionId: hit.ev.sessionId,
        });
      } else {
        // error 事件的 message 是人话原因（后端拼好）；空 = 中断无详情。
        bridge.error({
          title: title || NOTIFY_FALLBACK_TITLE_ERROR,
          body: hit.ev.message || "本轮生成失败",
          sessionId: hit.ev.sessionId,
        });
      }
    });
  }, [source, bridge]);
}
