// screen-params.test.mjs —— 投屏参数映射纯函数的单测（screen-params.ts，
// 与 screen.ts 的 ffmpegArgs/reconcile 消费同一份判定，测试与生产零漂移）。
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  FPS_OPTIONS, FPS_DEFAULT, normalizeFps,
  QUALITY_QP, QUALITY_OPTIONS, normalizeQuality,
  gopSize, h264Level, isUltra, backpressureBytes,
} from "../src/screen-params.ts";

test("normalizeFps: 白名单命中原样返回", () => {
  for (const f of FPS_OPTIONS) assert.equal(normalizeFps(f), f);
});

test("normalizeFps: 未知值/缺席回落默认 30", () => {
  assert.equal(normalizeFps(0), FPS_DEFAULT);
  assert.equal(normalizeFps(90), FPS_DEFAULT);
  assert.equal(normalizeFps(24), FPS_DEFAULT);
  assert.equal(normalizeFps(undefined), FPS_DEFAULT);
  // 缺席（老后端不带 fps 键）= 0 → 默认 30：行为与帧率可配置前一致
  assert.equal(FPS_DEFAULT, 30);
});

test("QUALITY_QP: 四档映射（ultra = qp 14）", () => {
  assert.equal(QUALITY_QP.low, "26");
  assert.equal(QUALITY_QP.medium, "22");
  assert.equal(QUALITY_QP.high, "18");
  assert.equal(QUALITY_QP.ultra, "14");
  assert.deepEqual([...QUALITY_OPTIONS], ["low", "medium", "high", "ultra"]);
});

test("normalizeQuality: ultra 保留、未知回落 medium", () => {
  assert.equal(normalizeQuality("ultra"), "ultra");
  assert.equal(normalizeQuality("high"), "high");
  assert.equal(normalizeQuality("best"), "medium");
  assert.equal(normalizeQuality(undefined), "medium");
});

test("gopSize: 2s GOP 按帧率换算", () => {
  assert.equal(gopSize(30), 60);
  assert.equal(gopSize(60), 120);
  assert.equal(gopSize(120), 240);
  assert.equal(gopSize(144), 288);
  assert.equal(gopSize(0), 60); // 未知按默认 30fps
});

test("h264Level: 1080p@≤60fps = 4.2", () => {
  // 1080p = 8160 MB/帧；60fps = 489,600 MB/s ≤ 522,240（4.2 MaxMBPS）
  assert.equal(h264Level(30, 1920, 1080), "4.2");
  assert.equal(h264Level(60, 1920, 1080), "4.2");
});

test("h264Level: 1080p@120 = 5.1（983k 上限内）", () => {
  // 120×8160 = 979,200 ≤ 983,040 → 5.1
  assert.equal(h264Level(120, 1920, 1080), "5.1");
});

test("h264Level: 1080p@144 = 5.2（超 5.1 上限）", () => {
  // 144×8160 = 1,175,040 > 983,040 → 必须 5.2（钉住 level 自适应的核心断言）
  assert.equal(h264Level(144, 1920, 1080), "5.2");
});

test("h264Level: 4K@30 超出 4.2（分辨率维度生效，不只看 fps）", () => {
  // 3840×2160 = 32400 MB/帧 ×30 = 972,000 MB/s → 5.1
  assert.equal(h264Level(30, 3840, 2160), "5.1");
});

test("h264Level: 未知分辨率按 1080p 基准", () => {
  assert.equal(h264Level(30, 0, 0), "4.2");
  assert.equal(h264Level(144, 0, 0), "5.2");
});

test("isUltra: ultra 档判定", () => {
  assert.equal(isUltra("ultra"), true);
  assert.equal(isUltra("high"), false);
});

test("backpressureBytes: 按帧率线性放大、有上限", () => {
  assert.equal(backpressureBytes(30), 400_000); // 基准不变
  assert.equal(backpressureBytes(0), 400_000); // 未知按 30
  assert.equal(backpressureBytes(144), 1_200_000); // 400k×144/30=1.92M → 上限 1.2M
  assert.equal(backpressureBytes(60), 800_000);
});
