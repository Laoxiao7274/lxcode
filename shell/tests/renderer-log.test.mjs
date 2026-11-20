// renderer-log.test.mjs —— 渲染层日志落盘与崩溃自动恢复纯函数的单测。
//
// 钉住的根因（2026-12 用户「跑一段时间全白屏」）：白屏 = 渲染进程崩溃/OOM 或
// React 渲染期未捕获异常，原先两者都无恢复、无日志。main.ts 只做事件接线，
// 判定逻辑全部收在 ../src/renderer-log.ts（Electron 依赖进不了 node:test——
// 与 cert-allow.test.mjs 同一条纪律）。
import test from "node:test";
import assert from "node:assert/strict";
import { join } from "node:path";
import {
  RENDERER_LOG_MAX_BYTES,
  MAX_CRASH_RECOVERIES,
  consoleLevelName,
  crashRecovery,
  rendererLogLine,
  rendererLogPaths,
  shouldRotate,
} from "../src/renderer-log.ts";

test("rendererLogPaths：.log 与 .log.old 都落在 userData 目录", () => {
  const p = rendererLogPaths("C:\\Users\\x\\AppData\\Roaming\\lxcode");
  assert.equal(p.log, join("C:\\Users\\x\\AppData\\Roaming\\lxcode", "renderer.log"));
  assert.equal(p.old, join("C:\\Users\\x\\AppData\\Roaming\\lxcode", "renderer.log.old"));
});

test("rendererLogLine：ISO 时间戳 + 级别前缀 + 换行结尾（appendFile 逐行追加）", () => {
  const line = rendererLogLine(new Date("2026-12-01T00:00:00.000Z"), "error", "boom");
  assert.equal(line, "[2026-12-01T00:00:00.000Z] [error] boom\n");
});

test("consoleLevelName：Electron 0..3 → verbose/info/warning/error；越界回落 info", () => {
  assert.equal(consoleLevelName(0), "verbose");
  assert.equal(consoleLevelName(1), "info");
  assert.equal(consoleLevelName(2), "warning");
  assert.equal(consoleLevelName(3), "error");
  assert.equal(consoleLevelName(99), "info");
});

test("consoleLevelName：Electron 40 新式字符串级别直通，debug 视作 verbose", () => {
  assert.equal(consoleLevelName("info"), "info");
  assert.equal(consoleLevelName("warning"), "warning");
  assert.equal(consoleLevelName("error"), "error");
  assert.equal(consoleLevelName("debug"), "verbose");
});

test("shouldRotate：达到/超过 5MB 轮转，未到不轮转", () => {
  assert.equal(shouldRotate(RENDERER_LOG_MAX_BYTES - 1), false);
  assert.equal(shouldRotate(RENDERER_LOG_MAX_BYTES), true);
  assert.equal(shouldRotate(RENDERER_LOG_MAX_BYTES + 1024), true);
});

test("crashRecovery：退避 5s/15s/30s（第 1~3 次连崩）", () => {
  assert.deepEqual(crashRecovery(1), { recover: true, waitMs: 5_000 });
  assert.deepEqual(crashRecovery(2), { recover: true, waitMs: 15_000 });
  assert.deepEqual(crashRecovery(3), { recover: true, waitMs: 30_000 });
});

test(`crashRecovery：第 ${MAX_CRASH_RECOVERIES + 1} 次连崩停止自动恢复（防崩溃循环刷爆日志/CPU）`, () => {
  assert.deepEqual(crashRecovery(MAX_CRASH_RECOVERIES + 1), { recover: false, waitMs: 0 });
  assert.deepEqual(crashRecovery(100), { recover: false, waitMs: 0 });
});
