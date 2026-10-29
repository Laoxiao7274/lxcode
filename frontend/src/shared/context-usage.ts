// 上下文指示器的展示口径（纯函数——组件与 node:test 共用同一份判定，不各写一遍）。
//
// 为什么要单独抽出来：**估算值与真实用量必须在 UI 上分得开**。后端的 used 有两个来源：
//   - provider 回报的真实 prompt_tokens（准确，可直接拿来做预算判断）；
//   - 按固定密度折算的估算（端点不回报 usage；或库里没有真实测量、按已加载的历史回落
//     ——老会话/后端重启前跑过的会话）。按字节算对中文是**低估**的（一个汉字 3 字节
//     ≈ 0.75 token），拿它当真实用量会让人以为还有富余。
// 后端用 estimated 位如实标注了来源，前端就必须把它显示出来——看不出区别的数字等于假数据。
// 显示方式（2026-09-30 用户拍板）：**可见记号只有 `~`**（对齐 DSH 的 ContextMeter），
// 「这份数字是估的、不是真实测量」写在悬停说明与无障碍标签里。
import type { ContextUsage } from "./types";
import { kfmtTokens } from "./format";

/** ContextUsageDisplay 是指示器要显示的一组值。 */
export interface ContextUsageDisplay {
  /** used > 0 且窗口已知——只有这时才画百分比环形（缺窗口就算不出占比）。 */
  known: boolean;
  /** 已用百分比（known 为假时是 0，调用方不该显示它）。 */
  pct: number;
  /** 这个数字是估算的（后端 estimated 位）——UI 必须标注（悬停说明与无障碍标签）。 */
  estimated: boolean;
  /** 环形/chip 里的百分比文字。**可见记号只有 `~`**（对齐 DSH：DSH 的 ContextMeter 也
   *  只在读数上带 ~，百分比本身是纯数字）——估算与真实的区别走悬停说明，
   *  不在百分比后面挂「估」字（那会让 chip 变宽、且 DSH 没有这个记号）。 */
  pctText: string;
  /** 无障碍/悬停说明（估算与真实的措辞不同——这是「这个数字是估的」的可见出口）。 */
  title: string;
}

/** contextUsageDisplay 把后端测量折算成 UI 的显示口径。
 *
 * usage 为 null/undefined = 未知（后端刚重启且库里没有测量、会话还没跑过主轮）——
 * 显示中性态「—」，**不编数字**（0% 会被读成"上下文是空的"，比没有更坏）。
 *
 * estimated 只在 known 时才有意义：窗口未知时连百分比都不显示，标注估算无从谈起。 */
export function contextUsageDisplay(usage: ContextUsage | null | undefined): ContextUsageDisplay {
  const used = usage?.used ?? 0;
  const total = usage?.window ?? 0;
  const known = usage != null && used > 0 && total > 0;
  const pct = known ? Math.min(100, Math.round((used / total) * 100)) : 0;
  const estimated = known && usage?.estimated === true;
  return {
    known,
    pct,
    estimated,
    pctText: known ? `${pct}%` : "—",
    title: !known
      ? "上下文用量未知"
      : estimated
        ? `上下文已用约 ${pct}%（估算值，不是真实用量）`
        : `上下文已用 ${pct}%`,
  };
}

/** 弹层头部的「~used / window」读数（未知或没窗口时 null = 不显示这一行）。
 *
 *  为什么数字带 `~`：总量取 provider 回报的真实用量、分类是估算拆分，两者都**不是
 *  精确到个位的读数**——DSH 的 ContextMeter 同样带这个记号。少了它，用户会把一个
 *  估算值当成精确值做预算判断（这是界面上**唯一**的近似记号：2026-09-30 按用户决定
 *  去掉了百分比后面的「估」字，与 DSH 一致；"这份数字是估的"走悬停说明与无障碍标签）。
 *
 *  判定放在纯函数里而不是埋在 JSX：组件与 node:test 共用同一份口径，测试不必拉起
 *  渲染层就能钉住它（静态渲染看不到弹层里的字）。 */
export function contextFiguresText(usage: ContextUsage | null | undefined): string | null {
  const used = usage?.used ?? 0;
  const total = usage?.window ?? 0;
  if (used <= 0 || total <= 0) return null;
  return `~${kfmtTokens(used)} / ${kfmtTokens(total)}`;
}

/** 占比条与图例的一段：分类键 + 展示名 + 估算 token 数 + 配色。 */
export interface ContextSegment {
  key: "system" | "tools" | "tool_results" | "messages" | "reasoning";
  label: string;
  tokens: number;
  color: string;
}

/** 分类占比的展示口径（**顺序与配色都在这里**，组件只负责画）。
 *
 *  为什么工具声明单列一类：它从 system 里拆出来（后端也拆了）——"工具占了窗口多少"
 *  是用户最想知道的其中一件事，混在系统提示里就答不了这个问题（DSH 的 ContextMeter
 *  同样是 system / tools / messages 三类）。
 *
 *  零值分类**不出现在结果里**（不是画一段宽度为 0 的色块）：图例只列有占用的部分，
 *  否则用户会看到一排"0"的行。 */
export function contextSegments(usage: ContextUsage | null | undefined): ContextSegment[] {
  const all: ContextSegment[] = [
    { key: "system", label: "系统提示", tokens: usage?.system ?? 0, color: "#0d0d0d" },
    { key: "tools", label: "工具声明", tokens: usage?.tools ?? 0, color: "#7c6cf0" },
    { key: "tool_results", label: "工具结果", tokens: usage?.tool_results ?? 0, color: "#6e6e80" },
    { key: "messages", label: "对话消息", tokens: usage?.messages ?? 0, color: "#a9a9b5" },
    { key: "reasoning", label: "思考链", tokens: usage?.reasoning ?? 0, color: "#d4d4d8" },
  ];
  return all.filter((s) => s.tokens > 0);
}
