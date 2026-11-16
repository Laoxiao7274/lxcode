// screen-probe.test.mjs —— 编码器探测失败缓存的纯逻辑单测（screen-probe.ts，
// 与 screen.ts 的 probeEncoders/startFfmpeg 消费同一份判定，测试与生产零漂移）。
//
// 钉的是 0.3.56 实撞的误报链：探测全失败带 60s TTL 缓存 → TTL 内重试若只
// 返回 [] 不复述原因，reconcile 每轮 start 前清掉的 lastError 不会重建，
// startFfmpeg 的 `if (!lastError)` 兜底就把「驱动/桌面复制起不来」误报成
// 「缺编码器，请重装 ffmpeg」。修复 = 失败原因与 TTL 时间戳一起缓存，
// TTL 命中复述同一真实原因。
import test from "node:test";
import assert from "node:assert/strict";
import {
  PROBE_FAIL_TTL_MS,
  probeFailureKind,
  probeFailMessage,
  probeFailCacheHit,
} from "../src/screen-probe.ts";


test("失败分类：有候选缺编译 = encoders_missing，全在列起不来 = encoders_unavailable", () => {
  assert.equal(probeFailureKind(["h264_nvenc", "h264_amf"]), "encoders_missing");
  assert.equal(probeFailureKind([]), "encoders_unavailable");
});

test("真实原因文案按 kind 分叉：missing 指向重装，unavailable 指向驱动/桌面复制并附输出", () => {
  const missing = probeFailMessage("encoders_missing", "");
  assert.ok(missing.includes("重新安装"), "缺编译的文案必须指向重装 ffmpeg");
  const unavailable = probeFailMessage("encoders_unavailable", "Cannot load nvcuda.dll");
  assert.ok(unavailable.includes("驱动或桌面复制"), "起不来的文案必须指向驱动/桌面复制");
  assert.ok(unavailable.includes("Cannot load nvcuda.dll"), "必须附 ffmpeg 输出摘要——手机端无从排查的教训");
  assert.ok(!missing.includes("驱动"), "两类文案不能互换");
});

test("TTL 命中复述同一真实原因（0.3.56 误报链的钉子）", () => {
  const memory = {
    kind: "encoders_unavailable",
    message: probeFailMessage("encoders_unavailable", "Cannot load nvcuda.dll"),
    at: 1_000_000,
  };
  // reconcile 下一轮 start（清 lastError 后）TTL 内重试：命中 → 返回记忆，
  // 调用方以 memory.message 重建 lastError → 报的还是驱动/桌面复制，不是兜底。
  const hit = probeFailCacheHit(memory, memory.at + PROBE_FAIL_TTL_MS - 1);
  assert.equal(hit, memory, "TTL 内必须命中且原样返回——复述与首次失败同一文案");
  assert.equal(hit.message, memory.message);
  // TTL 过期：不命中 → 调用方继续真实探测（驱动瞬时故障不把投屏钉死）。
  assert.equal(probeFailCacheHit(memory, memory.at + PROBE_FAIL_TTL_MS), null);
  assert.equal(probeFailCacheHit(memory, memory.at + PROBE_FAIL_TTL_MS + 1), null);
});

test("无记忆（首次/成功后）不命中：不编造原因", () => {
  assert.equal(probeFailCacheHit(null, Date.now()), null);
});

test("成功探测清掉失败记忆后，同刻不再复述旧失败", () => {
  // 模拟 probeEncoders 成功分支的清理：memory = null 之后命中判定恒 null。
  const memory = { kind: "encoders_missing", message: "旧失败", at: Date.now() };
  assert.notEqual(probeFailCacheHit(memory, Date.now()), null);
  assert.equal(probeFailCacheHit(null, Date.now()), null, "成功后清缓存——显卡能力恢复不再复述旧失败");
});
