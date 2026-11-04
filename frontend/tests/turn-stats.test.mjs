// 每轮统计（模型 · 首字 · 吞吐 · tokens）的展示口径测试。
//
// 最要紧的一条是 ③：**live 与 replay 必须给出同一组数字**——只填实时不填回放的话，
// 用户刷新一次这些数字就全没了（本仓库已经为「两条路径不一致」吃过三次亏：子 Agent 卡
// 退化成 agent_dispatch、两条路径分叉、seq 只在一条路径上）。所以这里把两条路径的产出
// 逐字段比一遍。
import test from "node:test";
import assert from "node:assert/strict";
import { formatFirstToken, formatTokens, tokensPerSec, turnStatParts, MIN_DECODE_WINDOW_MS } from "../src/shared/turn-stats.ts";
import { historyBlocks } from "../src/shared/history.ts";
import { reduce } from "../src/shared/reduce.ts";
import { initial } from "../src/shared/blocks.ts";

/** 后端 messages 表落的那四列（chat.history 的每条 message）。 */
const wireAssistant = (extra = {}) => ({
  role: "assistant", content: "答", seq: 2,
  first_token_ms: 320, duration_ms: 1500, model: "m1", usage_tokens: 42,
  ...extra,
});

test("tokensPerSec：分母扣掉首 token 延迟（首字是 prefill/排队，不是吐字速度）", () => {
  // 42 tok / (1500-320)ms = 42*1000/1180 = 35.59
  assert.equal(Math.round(tokensPerSec(42, 1500, 320) * 100) / 100, 35.59);
  // 首 token 缺席 → **不出速度**（2026-12 口径修订，对齐 DSH 的 token-meter：
  // 旧口径退化成整轮耗时，算出来的数字含 prefill——是另一口径的速度，宁可不显示）
  assert.equal(tokensPerSec(42, 1500), null);
  // 缺任一项 / 扣完没有正时长 → null（不编数）
  assert.equal(tokensPerSec(undefined, 1500, 320), null);
  assert.equal(tokensPerSec(42, undefined, 320), null);
  assert.equal(tokensPerSec(42, 320, 320), null);
  assert.equal(tokensPerSec(0, 1500, 320), null);
});

test("短窗护栏：解码窗 < 300ms 时速度不可信（计时误差主导），返回 null", () => {
  // 299ms 的窗：1ms 的首字/收尾计时误差就能让速度抖一倍——样本过小是噪声
  assert.equal(tokensPerSec(10, 360, 61), null, "gen=299ms < 300ms → null");
  // 恰好 300ms 算正样本（护栏是 < 300ms，不是 <=）
  assert.ok(tokensPerSec(10, 360, 60) > 0, "gen=300ms 应照常计算");
  // 两个纯函数层持同一阈值——轮级与会话级口径必须一致（改一处必改另一处）
  assert.equal(MIN_DECODE_WINDOW_MS, 300);
});

test("缺席的项不渲染：全缺席 → 整行不渲染；只缺首字 → 速度也不显示", () => {
  assert.deepEqual(turnStatParts({}), [], "全缺席必须整行不渲染（不留空行）");
  // 2026-12 口径修订：首字缺席 → 不出速度（旧口径退化成整轮耗时已废），
  // 所以只剩 tokens 那一项；也绝不出现「首字 0ms」
  const parts = turnStatParts({ usageTokens: 42, durationMs: 1500 });
  assert.deepEqual(parts, ["42 tokens"], "缺首字时速度整项缺席（不再编含 prefill 的假速度）");
  assert.equal(parts.some((p) => p.includes("首字")), false);
  assert.equal(parts.some((p) => p.includes("tok/s")), false);
  // 后端在工具轮上整键缺席（不是 0）——这里同样按缺席处理
  assert.deepEqual(turnStatParts({ firstTokenMs: 0, durationMs: 1500, usageTokens: 42 }), ["42 tokens"]);
  // 显式 320ms 首字 + 短窗（gen=0）→ 速度缺席，首字与 tokens 照常
  assert.deepEqual(turnStatParts({ firstTokenMs: 320, durationMs: 320, usageTokens: 42 }),
    ["首字 320ms", "42 tokens"]);
});

test("live 与 replay 给出同一组数字：reduce(done) 与 historyBlocks 逐字段一致", () => {
  const ev = {
    type: "done", sessionId: "s1", usageTokens: 42, finishReason: "stop",
    firstTokenMs: 320, durationMs: 1500, model: "m1",
  };
  const live = reduce({ ...initial, blocks: [{ kind: "assistant", uid: 1, content: "答", reasoning: "", streaming: true }] }, ev);
  const liveBlock = live.blocks.find((b) => b.kind === "assistant");
  const replayBlock = historyBlocks({
    sessionId: "s1", messages: [{ role: "user", content: "问" }, wireAssistant()],
    busy: false, pending: null, todos: [],
  }).find((b) => b.kind === "assistant");
  assert.ok(liveBlock && replayBlock);
  for (const k of ["usageTokens", "firstTokenMs", "durationMs", "model"]) {
    assert.equal(liveBlock[k], replayBlock[k], `两条路径的 ${k} 必须一致（刷新后数字不能变）`);
  }
  assert.equal(liveBlock.model, "m1");
  assert.equal(liveBlock.firstTokenMs, 320);
});

test("formatTokens / formatFirstToken：人类可读，且 0/负数/NaN 一律当缺席", () => {
  assert.equal(formatTokens(412), "412");
  assert.equal(formatTokens(1234), "1.2k");
  assert.equal(formatTokens(12345), "12k");
  assert.equal(formatTokens(0), null);
  assert.equal(formatTokens(-5), null);
  assert.equal(formatTokens(Number.NaN), null);
  assert.equal(formatFirstToken(320), "320ms");
  assert.equal(formatFirstToken(1500), "1.5s");
  assert.equal(formatFirstToken(0), null, "0 是「没测到」而不是「0ms 首字」");
  assert.equal(formatFirstToken(undefined), null);
});

test("坏数据（负数/NaN/字符串）不崩，且一律当缺席", () => {
  assert.deepEqual(turnStatParts({ model: "", firstTokenMs: -1, durationMs: Number.NaN, usageTokens: -3 }), []);
  assert.equal(tokensPerSec(-3, 1500, 320), null);
  assert.equal(tokensPerSec(Number.NaN, 1500, 320), null);
  assert.equal(formatFirstToken(Number.NaN), null);
  assert.equal(formatTokens(Number.POSITIVE_INFINITY), null);
});

test("子会话的 model 与 context 取自**子会话快照**（不是主会话）", () => {
  const snapshot = {
    sessionId: "child-1", messages: [wireAssistant({ model: "researcher-m" })],
    busy: false, pending: null, todos: [], model: "researcher-m",
    context: { used: 1234, window: 32768 },
  };
  assert.equal(snapshot.model, "researcher-m");
  const block = historyBlocks(snapshot).find((b) => b.kind === "assistant");
  assert.equal(block.model, "researcher-m", "每轮模型取自该子会话的消息");
  assert.equal(snapshot.context.used, 1234, "上下文取自该子会话的快照");
});
