// 会话统计的展示口径（纯函数，不 import React——组件与 node:test 共用同一份判定）。
//
// 为什么要有这一层：**缺席的项不显示，而不是显示 0**。统计的每一项都可能"没有"
//（provider 不回报用量、工具轮没有首字、这个会话一次工具都没跑过），而 "0ms 工具时间"
// 与"没有工具时间"在界面上必须分得开——前者是个假事实（DSH 的统计胶囊同样按
// 有值才渲染那一行）。判定散在 JSX 里就会一处显示 0、另一处不显示。
//
// 数字格式对齐 DSH 的 StatsPills：轮/步是整数、时长按 45.2s / 2m42s、token 按 1.2k/1.2M、
// 速度按 tok/s 一位小数、缓存命中率**不许把部分命中四舍五入成 100%**。
import type { SessionStats } from "./types";

/** 统计胶囊整行可不可见：**一步都没有就不渲染**（新会话/纯内存模式下后端整键缺席，
 *  显示一排 0 比什么都不显示更坏）。
 *
 *  2026-09-30 用户拍板把用量那一半并进「会话用量」弹层之后，这一行只剩时间胶囊，
 *  所以判定就是"有没有步数"（原先还认 token：只有 token 没有步数时留一个空行没有意义）。 */
export function statsVisible(stats: SessionStats | null | undefined): stats is SessionStats {
  return !!stats && stats.steps > 0;
}

/** 有没有 provider 回报的 token（DSH 的 `hasTokens`：prompt 侧三桶 + 输出 > 0）。
 *
 *  为什么单独一个判定：**「会话消耗」那一节按它决定渲不渲染**——老会话（本功能上线前
 *  落库的消息）有步数没 token，显示「0 tok · 缓存命中 0%」是编数字。 */
export function hasTokens(stats: SessionStats): boolean {
  return totalTokens(stats) > 0;
}

/** 计费总量：prompt 侧三桶 + 输出（DSH 的 billedInput + output 同口径）。 */
export function totalTokens(stats: SessionStats): number {
  return stats.input_tokens + stats.cache_read_tokens + stats.cache_write_tokens + stats.output_tokens;
}

/** prompt 侧总量：未缓存输入 + 缓存读 + 缓存写（缓存命中率的分母）。 */
export function promptTokens(stats: SessionStats): number {
  return stats.input_tokens + stats.cache_read_tokens + stats.cache_write_tokens;
}

/** 生成速度（tok/s）：**分母扣掉首字延迟**（首字是 prefill/排队，算进去会把
 *  "排队久"误报成"吐字慢"——口径与后端 agent.OutputTokensPerSec 一致）。
 *  decode_ms/decode_tokens 缺席（没有任何一步同时有首字与用量）→ null，调用方不显示。 */
export function decodeTokensPerSec(stats: SessionStats): number | null {
  if (stats.decode_ms <= 0 || stats.decode_tokens <= 0) return null;
  return (stats.decode_tokens * 1000) / stats.decode_ms;
}

/** 首字延迟均值（ms）：只有有首字可测的步才计入。没有 → null（不显示"0ms"）。 */
export function averageTtftMs(stats: SessionStats): number | null {
  if (stats.ttft_steps <= 0 || stats.ttft_ms <= 0) return null;
  return stats.ttft_ms / stats.ttft_steps;
}

/** token 数的紧凑显示（对齐 DSH 的 formatTokens）：999 → 999；1200 → 1.2k；1200000 → 1.2M。
 *  ≥100 的换算值取整（1234k 显示成 1.2M 而不是 1234.5k——那样读不出量级）。 */
export function formatStatsTokens(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  if (value < 1000) return String(Math.round(value));
  const scaled = (v: number) => (v >= 100 ? String(Math.round(v)) : String(Math.round(v * 10) / 10));
  if (value < 1_000_000) return `${scaled(value / 1000)}k`;
  return `${scaled(value / 1_000_000)}M`;
}

/** 时长：一分钟以内 45.2s，之后 2m42s（DSH 的 formatDuration 同款）。 */
export function formatDuration(ms: number): string {
  const seconds = ms / 1000;
  if (seconds < 60) return `${Math.round(seconds * 10) / 10}s`;
  const whole = Math.round(seconds);
  return `${Math.floor(whole / 60)}m${whole % 60}s`;
}

/** 缓存命中率（百分比文本；null = 没有 prompt 输入，不显示这一项）。
 *
 *  **部分命中不许四舍五入成 100%**：99.6% 显示成 "100%" 会让用户以为"全都命中了"，
 *  而那正是缓存这一栏唯一要说的事。所以逐级加精度直到四舍五入不再把它抬到 100
 *（对齐 DSH 的 formatCacheHitPercent——同一类诚实性要求）。 */
export function cacheHitPercent(stats: SessionStats): string | null {
  const total = promptTokens(stats);
  if (total <= 0) return null;
  const read = stats.cache_read_tokens;
  if (read <= 0) return "0";
  if (read >= total) return "100";
  for (let places = 0; places <= 4; places++) {
    const scale = 10 ** places;
    const rounded = Math.round((read / total) * 100 * scale) / scale;
    if (rounded < 100) return places === 0 ? String(rounded) : rounded.toFixed(places).replace(/0+$/, "").replace(/\.$/, "");
  }
  return "<100"; // 极端的部分命中：宁可显示"不到 100"，也不显示成 100
}

/** 早期记录（本功能上线前落库的消息）的 token 之和：那时的口径是 provider 的
 *  total_tokens（输入+输出），与今天的四桶不同——**不混算**（混算会把速度报得离谱：
 *  实测 687.8 tok/s，真值约 40）。0 = 没有早期记录。 */
export function legacyTokens(stats: SessionStats): number {
  return stats.legacy_tokens ?? 0;
}

/** 统计行的明细（键值对；缺席的项**直接不出现**，不是显示 0）。
 *
 *  DSH 的 TimePill 弹层：LLM 时间 / 工具时间 / 首字（均值）/ 速度；只有有值的才渲染。
 *  早期记录单独一行如实说明（口径不同、不参与速度）——不说的话用户会奇怪"为什么这条
 *  会话没有速度"，而那一行数字（总量）又确实存在。 */
export function timeDialogRows(stats: SessionStats): Array<{ label: string; value: string }> {
  const rows: Array<{ label: string; value: string }> = [];
  if (stats.llm_ms > 0) rows.push({ label: "模型时间", value: formatDuration(stats.llm_ms) });
  if (stats.tool_ms > 0) rows.push({ label: "工具时间", value: formatDuration(stats.tool_ms) });
  const ttft = averageTtftMs(stats);
  if (ttft !== null) rows.push({ label: "首字延迟", value: formatDuration(ttft) });
  const tps = decodeTokensPerSec(stats);
  if (tps !== null) rows.push({ label: "生成速度", value: `${tps.toFixed(1)} tok/s` });
  const legacy = legacyTokens(stats);
  if (legacy > 0) {
    rows.push({ label: "早期记录", value: `${formatStatsTokens(legacy)} tok（输入+输出）` });
  }
  return rows;
}

/** 用量行的明细（DSH 的 UsagePill 弹层：缓存命中 / 未缓存输入 / 缓存读取 / 缓存写入 / 输出）。
 *  没有 token（老会话）时返回空数组——调用方据此不显示这个胶囊（DSH 同款）。
 *  早期记录单独一行如实说明（与上面四桶口径不同，不混算）。 */
export function usageDialogRows(stats: SessionStats): Array<{ label: string; value: string }> {
  const rows: Array<{ label: string; value: string }> = [];
  if (!hasTokens(stats)) return rows;
  const hit = cacheHitPercent(stats);
  if (hit !== null) rows.push({ label: "缓存命中", value: `${hit}%` });
  rows.push({ label: "未缓存输入", value: formatStatsTokens(stats.input_tokens) });
  rows.push({ label: "缓存读取", value: formatStatsTokens(stats.cache_read_tokens) });
  rows.push({ label: "缓存写入", value: formatStatsTokens(stats.cache_write_tokens) });
  rows.push({ label: "输出", value: formatStatsTokens(stats.output_tokens) });
  const legacy = legacyTokens(stats);
  if (legacy > 0) {
    rows.push({ label: "早期记录", value: `${formatStatsTokens(legacy)} tok（输入+输出）` });
  }
  return rows;
}

/** 时间胶囊的标题：轮 · 步 · 速度（速度缺席时不显示那一段，而不是显示 0.0）。 */
export function timePillLabel(stats: SessionStats): string {
  const parts = [`${stats.turns} 轮`, `${stats.steps} 步`];
  const tps = decodeTokensPerSec(stats);
  if (tps !== null) parts.push(`${tps.toFixed(1)} tok/s`);
  return parts.join(" · ");
}

/** 会话消耗的总量标签（「12.5k tok」）——上下文环那个弹层里「会话消耗」一节的头。
 *
 *  这里没有"条上短/悬停长"两套了：2026-09-30 用户拍板把用量胶囊并进「会话用量」弹层，
 *  输入条里只剩时间胶囊——那一行的宽度压力因此消失，缓存命中率也就跟着明细一起回到弹层里。 */
export function usageTotalLabel(stats: SessionStats): string {
  return `${formatStatsTokens(totalTokens(stats))} tok`;
}
