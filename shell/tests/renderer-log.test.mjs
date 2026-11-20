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
  fatalMainLine,
  nextBootCount,
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

// ===== 主进程死亡取证（2026-12：0.3.60 两次整壳静默死亡）=====

test("nextBootCount：空日志首启 = boot #1", () => {
  assert.equal(nextBootCount([]), 1);
});

test("nextBootCount：取上一次 boot 行 +1（含 renderer 层杂行不误判）", () => {
  const lines = [
    "[2026-12-01T00:00:00.000Z] [info] [main] boot #7 — 主进程启动（minidump 目录：X）",
    "[2026-12-01T00:05:00.000Z] [info] [main] graceful quit（will-quit 正常收尾）",
  ];
  assert.equal(nextBootCount(lines), 8);
});

test("nextBootCount：两代日志（.old + .log）里取最大值，序号只求单调不求连续", () => {
  const lines = [
    "[main] boot #3",
    "[main] boot #12",
    "[main] boot #4",
  ];
  assert.equal(nextBootCount(lines), 13);
});

test("nextBootCount：非 boot 的数字行 / 损坏序号不误判", () => {
  assert.equal(nextBootCount(["[main] boot #abc", "[main] boot #-1", "boot #999 无 [main] 前缀"]), 1);
  assert.equal(nextBootCount(["[main] boot #2", "[main] boot #oops"]), 3);
});

test("fatalMainLine：Error 带 name/message/stack；非 Error 字符串化", () => {
  const err = new TypeError("boom");
  err.stack = "TypeError: boom\n    at x";
  const line = fatalMainLine(err, "uncaughtException");
  assert.match(line, /^FATAL main: uncaughtException TypeError: boom\n/);
  assert.match(line, /    at x$/);
  assert.equal(fatalMainLine("plain", "unhandledRejection"), "FATAL main: unhandledRejection plain");
});
