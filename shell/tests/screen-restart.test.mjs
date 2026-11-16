// screen-restart.test.mjs —— ffmpeg 重启连败熔断纯逻辑的单测（screen-restart.ts，
// 与 screen.ts 的 reconcile/close 处理消费同一份判定，测试与生产零漂移）。
//
// 钉的是 scrcpy MAX_CONSECUTIVE_ERRORS 同款策略：
//   ① 上限 = 3；② want=false 时不熔断；③ 成功一次清零；④ 达上限后停止重启
//   且上报可读错误（err_kind=reason 走既有 shell state 上报机型）。
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  MAX_CONSECUTIVE_ERRORS,
  FFMPEG_MIN_ALIVE_MS,
  restartTripped,
  afterRestartAttempt,
  attemptSucceeded,
  newStartIntent,
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

// P0-1 钉子：spawn 同步成功不算成功——按存活时长判定。存活 < 2s（1.9s，锁屏/
// 驱动坏的常态）= 失败 → 计数累加、熔断可达；存活 ≥ 2s（2.1s）= 成功 → 清零。
// 实撞形态：ffmpeg 异步立即退出，旧行为（spawn 成功即清零）让计数 0↔1 震荡。
test("P0-1 存活时长判定：1.9s 失败累加、2.1s 成功清零", () => {
  assert.equal(FFMPEG_MIN_ALIVE_MS, 2000, "存活门槛 2s");
  assert.equal(attemptSucceeded(0), false, "spawn 即退（0ms）= 失败");
  assert.equal(attemptSucceeded(1999), false);
  assert.equal(attemptSucceeded(FFMPEG_MIN_ALIVE_MS), true, "恰好 2s = 成功");
  assert.equal(attemptSucceeded(2100), true, "存活 2.1s = 成功");
  // 震荡链钉死：spawn 成功（未清零）→ 1.9s 后退出 = 失败累加 → 三连败熔断可达
  let n = 0;
  n = afterRestartAttempt(attemptSucceeded(1900), n);
  n = afterRestartAttempt(attemptSucceeded(1900), n);
  n = afterRestartAttempt(attemptSucceeded(1900), n);
  assert.equal(n, 3, "三次短命尝试连败累加——熔断计数不被 spawn 成功复位");
  assert.equal(restartTripped(true, n), true, "锁屏/驱动坏常态下熔断必须可达");
  // 存活过门槛后退出：成功 → 清零
  n = afterRestartAttempt(attemptSucceeded(2100), n);
  assert.equal(n, 0, "存活 ≥ 2s 的尝试清零连败计数");
});

test("P1-5 显式重试判定：epoch 变化 = 新的开始意图；老后端（无 epoch）恒否", () => {
  // 熔断态（want 保持 true、电平不变）：用户点「重新开始投屏」→ 后端递增 epoch
  assert.equal(newStartIntent(3, 4), true, "epoch 变化 = 显式重试（熔断恢复路径）");
  assert.equal(newStartIntent(3, 3), false, "epoch 未变（普通轮询）= 不重试");
  // 老后端不下发 start_epoch：undefined === undefined 恒 false（行为与旧版一致）
  assert.equal(newStartIntent(undefined, undefined), false);
  // 首见 epoch（壳启动后第一次轮询 / 后端重启 epoch 归档）：返回 true——
  // 此时壳侧连败计数必为 0（计数是壳本地状态，壳启动即归零），清零无副作用；
  // 后端重启 epoch 归 1 的「误触发」同理无害（触发的前提是 restartFailures>0，
  // 后端重启本身就让壳重走 stop→start，清零符合「重新开始」语义）。
  assert.equal(newStartIntent(undefined, 1), true, "首见 epoch = 新的开始意图（此时计数为 0，无副作用）");
});

// close 回调实例一致性闸门（screen.ts 接线层，纯函数层表达不了进程身份）：
// 旧进程被 stop/替换后它的 close 回调迟到，必须不计任何成败、不碰新实例引用。
// 用源码钉子（updater-copytree.test.mjs 同款先例）断言接线形态：
//   ① close 回调先比 thisSpawn 再计数；② 计数语句必须位于闸门 return 之后。
test("源码钉子：close 回调先判实例一致性（thisSpawn）再做连败计数", () => {
  const src = readFileSync(join(import.meta.dirname, "..", "src", "screen.ts"), "utf8");
  const closeFn = src.match(/ffmpeg\.on\("close",[\s\S]*?\n  \}\);/)?.[0];
  assert.ok(closeFn, "screen.ts 应有 ffmpeg close 回调");
  const gateIdx = closeFn.indexOf("ffmpeg !== thisSpawn");
  const countIdx = closeFn.indexOf("attemptSucceeded");
  assert.ok(gateIdx >= 0, "close 回调必须先判 ffmpeg !== thisSpawn（旧实例退出不计成败）");
  assert.ok(countIdx > gateIdx, "存活时长计数必须在实例闸门之后（旧实例退出不走计数）");
  // stopFfmpeg kill 后置 null——旧实例 close 迟到时 ffmpeg 已是 null 或新实例，
  // 闸门统一用「!== thisSpawn」覆盖两种形态（null !== thisSpawn 也为真）。
});

test("熔断上报文案：停止重启的原因对手机端可读，指向重新开始投屏重试", () => {
  const msg = tripMessage(3);
  assert.ok(msg.includes("3"), "应带连败次数");
  assert.ok(msg.includes("重新开始投屏"), "应指向手机端重新开始投屏（P1-5 的恢复路径）");
  assert.ok(msg.includes("重试"), "点重新开始即可重试（显式重试语义，P1-5）");
  assert.notEqual(msg, "", "错误原因不能为空——手机端无从排查");
});
