// update-poll.test.mjs —— 更新周期检查纯逻辑的单测（shell/src/update-poll.ts）。
//
// 钉的是三条纪律：
//   ① 定时与抖动：延迟恒在 [6h, 6h+30min) 区间；rand=0 → 恰为基线；
//      rand 逼近 1 → 抖动上界（不含）；每次排程现取 nextDelay（逐次独立）。
//   ② 检查器循环：发现新版本回调 onAvailable；失败静默但**照排下一次**
//      （一次网络抖动不能让周期检查永久停摆）；stop 取消已排程的一次。
//   ③ 手动检查三态：有新版 / 已是最新 / 失败（失败保留原因——手动检查不许静默）。
import test from "node:test";
import assert from "node:assert/strict";
import {
  UPDATE_CHECK_INTERVAL_MS,
  UPDATE_CHECK_JITTER_MS,
  createUpdatePoller,
  manualCheckOutcome,
  nextCheckDelayMs,
} from "../src/update-poll.ts";

// ---------- ① 定时与抖动 ----------

test("nextCheckDelayMs：基线 6h + [0, 30min) 抖动，边界由 rand 决定", () => {
  assert.equal(UPDATE_CHECK_INTERVAL_MS, 6 * 3600_000);
  assert.equal(UPDATE_CHECK_JITTER_MS, 30 * 60_000);
  // rand=0 → 恰为基线（抖动下界，含）
  assert.equal(nextCheckDelayMs(() => 0), UPDATE_CHECK_INTERVAL_MS);
  // rand 逼近 1 → 基线+抖动窗-1（上界不含——Math.floor(rand*JITTER) 最大 JITTER-1）
  assert.equal(nextCheckDelayMs(() => 0.9999999), UPDATE_CHECK_INTERVAL_MS + UPDATE_CHECK_JITTER_MS - 1);
  assert.equal(nextCheckDelayMs(() => 0.5), UPDATE_CHECK_INTERVAL_MS + Math.floor(0.5 * UPDATE_CHECK_JITTER_MS));
});

test("nextCheckDelayMs：随机样本全部落在 [基线, 基线+抖动) 区间", () => {
  for (let i = 0; i < 200; i++) {
    const d = nextCheckDelayMs();
    assert.ok(d >= UPDATE_CHECK_INTERVAL_MS, `低于基线: ${d}`);
    assert.ok(d < UPDATE_CHECK_INTERVAL_MS + UPDATE_CHECK_JITTER_MS, `超出抖动上界: ${d}`);
  }
});

// ---------- ② 检查器循环 ----------

/** 手动触发的时间桩：setTimer 只记录（不真等），用例显式 fire。 */
function fakeTimers() {
  const pending = [];
  return {
    pending,
    setTimer: (fn, ms) => { const h = { fn, ms }; pending.push(h); return h; },
    clearTimer: (h) => { const i = pending.indexOf(h); if (i >= 0) pending.splice(i, 1); },
    /** 触发最早排程的一次（模拟时间到）。 */
    fire() {
      const h = pending.shift();
      if (!h) throw new Error("没有待触发的定时器");
      h.fn();
      return h.ms;
    },
  };
}

function makePoller(t, { available = false, fail = false } = {}) {
  const calls = { checks: 0, available: 0, errors: 0 };
  let delayCalls = 0;
  const deps = {
    check: async () => {
      calls.checks++;
      if (fail) throw new Error("网络断了");
      return { available };
    },
    nextDelay: () => 1000 + ++delayCalls, // 每次排程延迟都不同 → 钉「逐次现取」
    onAvailable: () => calls.available++,
    setTimer: t.setTimer,
    clearTimer: t.clearTimer,
  };
  return { deps, calls };
}

test("poller：首次检查延迟 nextDelay()；发现新版本回调 onAvailable 并继续排程", async () => {
  const t = fakeTimers();
  const { deps, calls } = makePoller(t, { available: true });
  const poller = createUpdatePoller(deps);
  poller.start();
  assert.equal(t.pending.length, 1, "start 后应恰有一次排程");
  assert.equal(t.pending[0].ms, 1001, "首次延迟 = 第一次 check 前的 nextDelay()");
  t.fire();
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); // 让 async 链走完
  assert.equal(calls.checks, 1);
  assert.equal(calls.available, 1, "available=true 必须回调 onAvailable");
  assert.equal(t.pending.length, 1, "检查后必须排下一次");
  assert.equal(t.pending[0].ms, 1002, "下一次延迟逐次现取（不是启动时定死）");
  poller.stop();
});

test("poller：检查失败静默（不回调、不抛出定时器）但照排下一次", async () => {
  const t = fakeTimers();
  const { deps, calls } = makePoller(t, { fail: true });
  const poller = createUpdatePoller(deps);
  poller.start();
  t.fire();
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
  assert.equal(calls.checks, 1);
  assert.equal(calls.available, 0, "失败不回调 onAvailable");
  assert.equal(t.pending.length, 1, "失败后仍要排下一次——一次网络抖动不能停摆");
  poller.stop();
});

test("poller：无新版不回调；stop 取消已排程的一次且不再排程", async () => {
  const t = fakeTimers();
  const { deps, calls } = makePoller(t, { available: false });
  const poller = createUpdatePoller(deps);
  poller.start();
  t.fire();
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
  assert.equal(calls.available, 0, "无新版不回调");
  poller.stop();
  assert.equal(t.pending.length, 0, "stop 取消已排程的一次");
  assert.throws(() => t.fire(), "stop 后不该有任何可触发的定时器");
  assert.equal(calls.checks, 1, "stop 后不再产生新检查");
});

// ---------- ③ 手动检查三态 ----------

test("manualCheckOutcome：available+manifest → available（带版本号 + manifest 透传）", async () => {
  const manifest = { version: "0.3.0", size: 12_000_000, notes: ["a"] };
  const r = await manualCheckOutcome(async () => ({ available: true, manifest }));
  assert.deepEqual(r, { kind: "available", version: "0.3.0", manifest });
});

test("manualCheckOutcome：无新版 → latest（manifest 缺席/available=false 同样）", async () => {
  assert.deepEqual(
    await manualCheckOutcome(async () => ({ available: false, manifest: null })),
    { kind: "latest" },
  );
  assert.deepEqual(
    await manualCheckOutcome(async () => ({ available: false, manifest: { version: "0.2.5" } })),
    { kind: "latest" },
  );
  // available=true 但 manifest 异常缺席（后端契约破坏）→ 不编版本号，落 latest
  assert.deepEqual(
    await manualCheckOutcome(async () => ({ available: true, manifest: null })),
    { kind: "latest" },
  );
});

test("manualCheckOutcome：失败 → error 且保留人话原因（手动检查不许静默）", async () => {
  const r = await manualCheckOutcome(async () => { throw new Error("manifest 拉取失败：HTTP 503"); });
  assert.deepEqual(r, { kind: "error", message: "manifest 拉取失败：HTTP 503" });
  // 非 Error 抛出物也要能转成可展示的消息
  const r2 = await manualCheckOutcome(async () => { throw "boom"; });
  assert.deepEqual(r2, { kind: "error", message: "boom" });
});
