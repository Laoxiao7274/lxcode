// 每轮计时/用量的展示口径（纯函数，不 import React——组件与 node:test 共用同一份判定）。
//
// 为什么要有这一层：tok/s 是**派生值**，后端只发原始值（usage_tokens / duration_ms /
// first_token_ms）。派生值一旦也落库，同一件事就有了两个事实源，而 live 与 replay 必须逐字
// 一致——本仓库已经为「两条路径不一致」吃过三次亏。所以公式只在这里有一份。
//
// 为什么分母扣掉首 token 延迟：首字延迟是 prefill/排队等待，与「每秒吐多少字」不是一回事。
// 算进分母会把「排队久」误报成「吐字慢」（后端 internal/agent/timing.go 同口径）。
// 首 token 缺席（工具轮/非流式回放）时退化成整轮耗时——那时口径含 prefill，这一点如实反映：
// 不假装它也是纯生成速度。

/** 每秒输出 token 数（缺任一项 → null：**不编数**，调用方不显示）。 */
export function tokensPerSec(usageTokens?: number, durationMs?: number, firstTokenMs?: number): number | null {
  const tokens = num(usageTokens);
  const total = num(durationMs);
  if (tokens === null || total === null || tokens <= 0) return null;
  const first = num(firstTokenMs);
  // 首 token 已知就扣掉它（纯生成耗时）；缺席就用整轮耗时（口径含 prefill，注释已说明）
  const gen = first === null ? total : total - first;
  if (gen <= 0) return null; // 扣完没有正时长 = 算不出速度，宁可不显示
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
