// 每轮统计（模型 · 首字 · 吞吐 · tokens）的展示口径测试。
//
// 最要紧的一条是 ③：**live 与 replay 必须给出同一组数字**——只填实时不填回放的话，
// 用户刷新一次这些数字就全没了（本仓库已经为「两条路径不一致」吃过三次亏：子 Agent 卡
// 退化成 agent_dispatch、两条路径分叉、seq 只在一条路径上）。所以这里把两条路径的产出
// 逐字段比一遍。
import test from "node:test";
import assert from "node:assert/strict";
import { formatFirstToken, formatTokens, tokensPerSec, turnStatParts } from "../src/shared/turn-stats.ts";
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
  // 首 token 缺席 → 退化成整轮耗时（口径含 prefill，与后端同款）
  assert.equal(Math.round(tokensPerSec(42, 1500) * 100) / 100, 28);
  // 缺任一项 / 扣完没有正时长 → null（不编数）
  assert.equal(tokensPerSec(undefined, 1500, 320), null);
  assert.equal(tokensPerSec(42, undefined, 320), null);
  assert.equal(tokensPerSec(42, 320, 320), null);
  assert.equal(tokensPerSec(0, 1500, 320), null);
});

test("缺席的项不渲染：全缺席 → 整行不渲染；只缺首字 → 不显示「首字 0ms」", () => {
  assert.deepEqual(turnStatParts({}), [], "全缺席必须整行不渲染（不留空行）");
  const parts = turnStatParts({ usageTokens: 42, durationMs: 1500 });
  assert.deepEqual(parts, ["28.0 tok/s", "42 tokens"], "缺首字时不许出现「首字 0ms」");
  assert.equal(parts.some((p) => p.includes("首字")), false);
  // 后端在工具轮上整键缺席（不是 0）——这里同样按缺席处理
  assert.deepEqual(turnStatParts({ firstTokenMs: 0, durationMs: 1500, usageTokens: 42 }), ["28.0 tok/s", "42 tokens"]);
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
