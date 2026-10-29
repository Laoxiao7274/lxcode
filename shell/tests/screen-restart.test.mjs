// screen-restart.test.mjs —— ffmpeg 重启连败熔断纯逻辑的单测（screen-restart.ts，
// 与 screen.ts 的 reconcile/close 处理消费同一份判定，测试与生产零漂移）。
//
// 钉的是 scrcpy MAX_CONSECUTIVE_ERRORS 同款策略：
//   ① 上限 = 3；② want=false 时不熔断；③ 成功一次清零；④ 达上限后停止重启
//   且上报可读错误（err_kind=reason 走既有 shell state 上报机型）。
import test from "node:test";
import assert from "node:assert/strict";
import {
  MAX_CONSECUTIVE_ERRORS,
  restartTripped,
  afterRestartAttempt,
  tripMessage,
} from "../src/screen-restart.ts";

test("连败计数：3 次失败后熔断（scrcpy 同值），成功一次即清零", () => {
  assert.equal(MAX_CONSECUTIVE_ERRORS, 3, "上限与 scrcpy MAX_CONSECUTIVE_ERRORS 同值");
  let n = 0;
  // 三连败
  n = afterRestartAttempt(false, n);
  n = afterRestartAttempt(false, n);
  assert.equal(n, 2);
  assert.equal(restartTripped(true, n), false, "第 2 次失败还不熔断");
  n = afterRestartAttempt(false, n);
  assert.equal(n, 3);
  assert.equal(restartTripped(true, n), true, "连败 3 次必须熔断，停止自动重启");
  // 第 4 次尝试不允许发生（熔断态 reconcile 不再调 startFfmpeg——计数不再增长）
  // 成功一次即清零
  n = afterRestartAttempt(true, 0);
  assert.equal(n, 0);
  assert.equal(restartTripped(true, n), false);
});

test("want=false 时不熔断（停止路径本来就不重启）", () => {
  assert.equal(restartTripped(false, 5), false);
  assert.equal(restartTripped(false, MAX_CONSECUTIVE_ERRORS), false);
  assert.equal(restartTripped(true, MAX_CONSECUTIVE_ERRORS - 1), false, "未达上限不熔断");
});

test("熔断上报文案：停止重启的原因对手机端可读", () => {
  const msg = tripMessage(3);
  assert.ok(msg.includes("3"), "应带连败次数");
  assert.ok(msg.includes("重新开始"), "应提示用户重新开始投屏");
  assert.notEqual(msg, "", "错误原因不能为空——手机端无从排查");
});
