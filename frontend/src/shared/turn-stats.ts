// 每轮计时/用量的展示口径（纯函数，不 import React——组件与 node:test 共用同一份判定）。
//
// 为什么要有这一层：tok/s 是**派生值**，后端只发原始值（usage_tokens / duration_ms /
// first_token_ms）。派生值一旦也落库，同一件事就有了两个事实源，而 live 与 replay 必须逐字
// 一致——本仓库已经为「两条路径不一致」吃过三次亏。所以公式只在这里有一份。
//
// 为什么分母扣掉首 token 延迟：首字延迟是 prefill/排队等待，与「每秒吐多少字」不是一回事。
// 算进分母会把「排队久」误报成「吐字慢」（后端 internal/agent/timing.go 同口径）。
// 首 token 缺席（工具轮/非流式回放）→ **不出速度**（2026-12 口径修订，对齐 DSH 的
// token-meter：首字缺席时 gen 退化成整轮耗时=把 prefill 混进吞吐，是另一口径的数字——
// 宁可不显示，也不显示一个口径不同的假速度）。

/** 短窗护栏：解码窗小于这个毫秒数时速度不可信（几百 ms 的窗里首字计时误差就能占
 *  掉一大截，算出来的 tok/s 抖一个数量级）。两个纯函数层各持同一阈值——轮级与
 *  会话级口径一致，改一处必改另一处（测试钉住同值）。 */
export const MIN_DECODE_WINDOW_MS = 300;

/** 每秒输出 token 数（缺任一项 → null：**不编数**，调用方不显示）。
 *  首 token 缺席也返回 null（不再是"退化成整轮耗时"的旧口径）。 */
export function tokensPerSec(usageTokens?: number, durationMs?: number, firstTokenMs?: number): number | null {
  const tokens = num(usageTokens);
  const total = num(durationMs);
  if (tokens === null || total === null || tokens <= 0) return null;
  const first = num(firstTokenMs);
  // 首 token 缺席（undefined）或 0（后端 wire 上"没测到"的哨兵——omitempty 缺席与
  // 显式 0 同义，agent.OutputTokensPerSec 同口径）都算缺席：不出速度
  if (first === null || first <= 0) return null;
  const gen = total - first;
  if (gen < MIN_DECODE_WINDOW_MS) return null; // 短窗护栏：样本过小，算出来是噪声
  return (tokens * 1000) / gen;
}

/** 有限正数才认（负数/NaN/undefined 一律当缺席——坏数据不许变成假数字）。 */
function num(v: number | undefined): number | null {
  return typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : null;
}

/** token 数的人类可读格式（1234 → 1.2k）。小数值不显示（"0.4k" 不如 "412" 直观）。 */
export function formatTokens(n?: number): string | null {
  const v = num(n);
  if (v === null || v <= 0) return null;
  if (v < 1000) return String(Math.round(v));
  const k = v / 1000;
  return `${k < 10 ? k.toFixed(1) : Math.round(k)}k`;
}

/** 首 token 延迟的显示（毫秒；≥1000 转秒）。缺席 → null（**不显示"0ms"**——那是编数据）。 */
export function formatFirstToken(ms?: number): string | null {
  const v = num(ms);
  if (v === null || v <= 0) return null;
  return v < 1000 ? `${Math.round(v)}ms` : `${(v / 1000).toFixed(1)}s`;
}

/** 一轮的统计行：模型 · 首字 · 吞吐 · tokens。**缺席的项直接不显示**（不是显示 0）。
 *  全缺席 → 返回空数组（调用方据此整行不渲染，不留空行）。 */
export function turnStatParts(input: { model?: string; firstTokenMs?: number; durationMs?: number; usageTokens?: number }): string[] {
  const parts: string[] = [];
  if (input.model) parts.push(input.model);
  const first = formatFirstToken(input.firstTokenMs);
  if (first !== null) parts.push(`首字 ${first}`);
  const tps = tokensPerSec(input.usageTokens, input.durationMs, input.firstTokenMs);
  if (tps !== null) parts.push(`${tps.toFixed(1)} tok/s`);
  const tk = formatTokens(input.usageTokens);
  if (tk !== null) parts.push(`${tk} tokens`);
  return parts;
}
