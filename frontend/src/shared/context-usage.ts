// 上下文指示器的展示口径（纯函数——组件与 node:test 共用同一份判定，不各写一遍）。
//
// 为什么要单独抽出来：**估算值与真实用量必须在 UI 上分得开**。后端的 used 有两个来源：
//   - provider 回报的真实 prompt_tokens（准确，可直接拿来做预算判断）；
//   - 按固定密度折算的估算（端点不回报 usage；或库里没有真实测量、按已加载的历史回落
//     ——老会话/后端重启前跑过的会话）。按字节算对中文是**低估**的（一个汉字 3 字节
//     ≈ 0.75 token），拿它当真实用量会让人以为还有富余。
// 后端用 estimated 位如实标注了来源，前端就必须把它显示出来——看不出区别的数字等于假数据。
import type { ContextUsage } from "./types";

/** ContextUsageDisplay 是指示器要显示的一组值。 */
export interface ContextUsageDisplay {
  /** used > 0 且窗口已知——只有这时才画百分比环形（缺窗口就算不出占比）。 */
  known: boolean;
  /** 已用百分比（known 为假时是 0，调用方不该显示它）。 */
  pct: number;
  /** 这个数字是估算的（后端 estimated 位）——UI 必须标注。 */
  estimated: boolean;
  /** 环形/chip 里的百分比文字：估算时带「估」后缀，真实用量就是纯百分比。 */
  pctText: string;
  /** 无障碍/悬停说明（估算与真实的措辞不同）。 */
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
    pctText: !known ? "—" : estimated ? `${pct}%估` : `${pct}%`,
    title: !known
      ? "上下文用量未知"
      : estimated
        ? `上下文已用约 ${pct}%（估算值，不是真实用量）`
        : `上下文已用 ${pct}%`,
  };
}
