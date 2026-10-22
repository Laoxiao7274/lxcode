// sakura-nodes.test.mjs —— 樱花frp 节点选择领域逻辑的单测。
//
// 钉住四条纪律：
//   ① 候选判定只有一份（flag bit2/bit9 + vip 等级 + 地址）：列表与自动选共用，
//      UI 能点的节点创建时一定不会被拒；
//   ② 不可用节点**照样进列表**（带 reason）——用户要能看见"为什么点不了"；
//   ③ 延迟失败返回 null 而不是 0（0ms 会冒充"最快"）；超时/拒绝/解析失败都算测不出；
//   ④ 探测有并发上限与 5 分钟缓存（面板反复开不刷屏式 connect）。
import test from "node:test";
import assert from "node:assert/strict";
import {
  AUTO_HTTPS_EXTRA,
  PROBE_CACHE_TTL_MS,
  PROBE_CONCURRENCY,
  PROBE_TIMEOUT_MS,
  autoHttpsConfigured,
  autoNodeChoice,
  candidateNodes,
  createLatencyProber,
  measureLatency,
  nodeUnusableReason,
  parseExtra,
  resolveNodeChoice,
  withAutoHttps,
} from "../src/sakura-nodes.ts";

const ALLOW = 1 << 2; // 允许创建
const OFFLINE = 1 << 9; // 离线

/** 一个可用节点的原始形状（按需覆盖字段）。 */
const ok = (over = {}) => ({ name: "节点", host: "frp-a.com", vip: 0, flag: ALLOW, load: 10, ...over });

// ---------- ① 判定 ----------

test("nodeUnusableReason：bit9 置位 = 离线（即使 bit2 允许创建）", () => {
  assert.equal(nodeUnusableReason(ok({ flag: ALLOW | OFFLINE }), 0), "离线");
});

test("nodeUnusableReason：bit2 清零 = 不可创建隧道", () => {
  assert.equal(nodeUnusableReason(ok({ flag: 0 }), 0), "不可创建隧道");
});

test("nodeUnusableReason：vip 高于用户组等级 = 需更高等级；等于可以", () => {
  assert.equal(nodeUnusableReason(ok({ vip: 3 }), 0), "需更高等级");
  assert.equal(nodeUnusableReason(ok({ vip: 3 }), 3), null);
  assert.equal(nodeUnusableReason(ok({ vip: 3 }), 4), null);
});

test("nodeUnusableReason：缺 host = 地址缺失（没地址既建不了也测不了延迟）", () => {
  assert.equal(nodeUnusableReason(ok({ host: "" }), 0), "节点地址缺失");
  assert.equal(nodeUnusableReason(ok({ host: undefined }), 0), "节点地址缺失");
});

test("nodeUnusableReason：flag 缺失 = 状态未知（不猜可用）", () => {
  assert.equal(nodeUnusableReason(ok({ flag: undefined }), 0), "节点状态未知");
});

// ---------- ② 列表 ----------

test("candidateNodes：不可用节点照样进列表，带 usable=false + reason", () => {
  const cands = candidateNodes([
    [1, ok({ name: "可用", load: 5 })],
    [2, ok({ name: "离线", flag: ALLOW | OFFLINE })],
    [3, ok({ name: "VIP", vip: 4 })],
    [4, ok({ name: "无地址", host: "" })],
  ], 0);
  assert.equal(cands.length, 4, "四台都要在列表里（UI 要按原因置灰）");
  assert.deepEqual(cands.map((c) => [c.name, c.usable, c.reason]), [
    ["可用", true, null],
    ["离线", false, "离线"],
    ["VIP", false, "需更高等级"],
    ["无地址", false, "节点地址缺失"],
  ]);
});

test("candidateNodes：排序 = 可用优先 → 负载升序 → id 升序（未知负载排最后）", () => {
  const cands = candidateNodes([
    [9, ok({ name: "不可用", flag: 0, load: 0 })],
    [5, ok({ name: "负载未知", load: null })],
    [3, ok({ name: "负载30", load: 30 })],
    [7, ok({ name: "负载10b", load: 10 })],
    [2, ok({ name: "负载10a", load: 10 })],
  ], 0);
  assert.deepEqual(cands.map((c) => c.name), ["负载10a", "负载10b", "负载30", "负载未知", "不可用"]);
});

test("candidateNodes：load 缺失保持 null（不强转 0——0% 是「负载为零」不是「未知」）", () => {
  const [c] = candidateNodes([[1, ok({ load: undefined })]], 0);
  assert.equal(c.load, null);
});

// ---------- ③ 选择 ----------

test("autoNodeChoice：可用节点里负载最低者；全不可用报人话错误", () => {
  const cands = candidateNodes([[1, ok({ load: 40 })], [2, ok({ load: 3 })]], 0);
  const pick = autoNodeChoice(cands);
  assert.equal(pick.ok, true);
  assert.equal(pick.node.id, 2);

  const none = autoNodeChoice(candidateNodes([[1, ok({ flag: 0 })]], 0));
  assert.equal(none.ok, false);
  assert.match(none.error, /没有可用的节点/);
});

test("resolveNodeChoice：不传 nodeId = 自动选（行为与旧逻辑一致）", () => {
  const cands = candidateNodes([[1, ok({ load: 40 })], [2, ok({ load: 3 })]], 0);
  for (const v of [undefined, null, Number.NaN]) {
    const pick = resolveNodeChoice(cands, v);
    assert.equal(pick.ok, true);
    assert.equal(pick.node.id, 2, `nodeId=${String(v)} 应走自动选`);
  }
});

test("resolveNodeChoice：显式 nodeId 命中可用节点", () => {
  const cands = candidateNodes([[1, ok({ load: 40 })], [2, ok({ load: 3 })]], 0);
  const pick = resolveNodeChoice(cands, 1); // 手动挑负载高的那台
  assert.equal(pick.ok, true);
  assert.equal(pick.node.id, 1);
});

test("resolveNodeChoice：nodeId 不在候选里 → 拒绝（防过期/伪造）", () => {
  const cands = candidateNodes([[1, ok()]], 0);
  const pick = resolveNodeChoice(cands, 999);
  assert.equal(pick.ok, false);
  assert.match(pick.error, /已不在可用列表/);
});

test("resolveNodeChoice：nodeId 指向不可用节点 → 拒绝并说明原因", () => {
  const cands = candidateNodes([[1, ok({ name: "东京", vip: 4 })]], 0);
  const pick = resolveNodeChoice(cands, 1);
  assert.equal(pick.ok, false);
  assert.match(pick.error, /东京/);
  assert.match(pick.error, /需更高等级/);
});

// ---------- ④ 延迟探测 ----------

/** 假 socket：手动触发 connect / error / 超时。 */
function fakeSocket() {
  const handlers = {};
  let timeoutCb = null;
  const sock = {
    timeouts: [],
    destroyed: 0,
    setTimeout(ms, cb) { sock.timeouts.push(ms); timeoutCb = cb; },
    on(ev, cb) { handlers[ev] = cb; },
    destroy() { sock.destroyed++; },
  };
  return {
    sock,
    connect: () => sock,
    fireConnect: () => handlers.connect?.(),
    fireError: () => handlers.error?.(),
    fireTimeout: () => timeoutCb?.(),
  };
}

test("measureLatency：connect 成功 → 返回 RTT 毫秒，且按 7000 端口探测", async () => {
  const f = fakeSocket();
  const ports = [];
  let t = 1000;
  const p = measureLatency("frp-a.com", {
    now: () => t,
    connect: (o) => { ports.push(o.port); return f.connect(); },
  });
  t = 1042; // 42ms 后连上
  f.fireConnect();
  assert.equal(await p, 42);
  assert.deepEqual(ports, [7000], "樱花 frps 控制端口是 7000（见 sakura-nodes.ts 头注）");
  assert.deepEqual(f.sock.timeouts, [PROBE_TIMEOUT_MS]);
  assert.equal(f.sock.destroyed, 1, "无论成败都要关 socket");
});

test("measureLatency：超时 → null（不是 0，也不是 -1）", async () => {
  const f = fakeSocket();
  const p = measureLatency("frp-a.com", { connect: () => f.connect() });
  f.fireTimeout();
  assert.equal(await p, null);
});

test("measureLatency：连接被拒/解析失败 → null", async () => {
  const f = fakeSocket();
  const p = measureLatency("frp-a.com", { connect: () => f.connect() });
  f.fireError();
  assert.equal(await p, null);
});

test("measureLatency：connect 同步抛（非法 host）→ null 不炸", async () => {
  const ms = await measureLatency("bad host", { connect: () => { throw new Error("getaddrinfo ENOTFOUND"); } });
  assert.equal(ms, null);
});

test("measureLatency：空 host 不探测直接 null", async () => {
  let called = 0;
  const ms = await measureLatency("", { connect: () => { called++; throw new Error("不该被调用"); } });
  assert.equal(ms, null);
  assert.equal(called, 0);
});

test("measureLatency：先 connect 后 error 不重复结算（第一次结果说了算）", async () => {
  const f = fakeSocket();
  let t = 0;
  const p = measureLatency("frp-a.com", { now: () => t, connect: () => f.connect() });
  t = 30;
  f.fireConnect();
  f.fireError(); // 连接后的 RST 不该把结果改成 null
  assert.equal(await p, 30);
  assert.equal(f.sock.destroyed, 1);
});

// ---------- ⑤ 探测器的缓存与并发 ----------

test("createLatencyProber：并发上限（PROBE_CONCURRENCY=6）且同 host 只探一次", async () => {
  let inflight = 0;
  let peak = 0;
  const prober = createLatencyProber({
    concurrency: PROBE_CONCURRENCY,
    connect: () => {
      inflight++;
      peak = Math.max(peak, inflight);
      const handlers = {};
      const sock = { setTimeout() {}, on(ev, cb) { handlers[ev] = cb; }, destroy() {} };
      // 用宏任务模拟"稍后连上"：让并发窗口真的重叠
      setTimeout(() => { inflight--; handlers.connect?.(); }, 1);
      return sock;
    },
  });
  const hosts = Array.from({ length: 20 }, (_, i) => `h${i}.com`);
  const seen = [];
  const p = prober.probeAll([...hosts, ...hosts], (h, ms) => seen.push([h, ms]));
  assert.equal(seen.length, 0, "probeAll 是异步的，结果随后逐个回调");
  await p;
  assert.equal(seen.length, 20, "重复 host 只探一次、只回调一次");
  assert.ok(peak <= PROBE_CONCURRENCY, `并发超上限: ${peak}`);
  assert.ok(peak > 1, `探测应并发进行，实际峰值 ${peak}`);
});

test("createLatencyProber：5 分钟缓存（TTL 内不重复 connect，过期后重测）", async () => {
  let connects = 0;
  let clock = 0;
  const prober = createLatencyProber({
    now: () => clock,
    connect: () => {
      connects++;
      const handlers = {};
      const sock = { setTimeout() {}, on(ev, cb) { handlers[ev] = cb; }, destroy() {} };
      queueMicrotask(() => handlers.connect?.());
      return sock;
    },
  });
  assert.deepEqual(prober.peek("a.com"), { known: false, ms: null });
  const first = await prober.probe("a.com");
  assert.equal(typeof first, "number", "假 socket 立即连上，应得到一个 RTT 数值");
  assert.deepEqual(prober.peek("a.com"), { known: true, ms: first });
  assert.equal(connects, 1);

  clock += PROBE_CACHE_TTL_MS - 1; // TTL 内
  assert.equal(await prober.probe("a.com"), first);
  assert.equal(connects, 1, "缓存命中不该再 connect");

  clock += 2; // 过期
  assert.deepEqual(prober.peek("a.com"), { known: false, ms: null });
  await prober.probe("a.com");
  assert.equal(connects, 2);
});

test("createLatencyProber：失败结果也进缓存（避免对不可达节点反复打）", async () => {
  let connects = 0;
  const prober = createLatencyProber({
    connect: () => {
      connects++;
      const handlers = {};
      const sock = { setTimeout() {}, on(ev, cb) { handlers[ev] = cb; }, destroy() {} };
      queueMicrotask(() => handlers.error?.());
      return sock;
    },
  });
  assert.equal(await prober.probe("down.com"), null);
  assert.equal(await prober.probe("down.com"), null);
  assert.equal(connects, 1);
});

// ---------- ⑥ 接线（源码级钉子：壳侧必须走同一份判定） ----------
//
// sakura.ts 依赖 electron，无法整模块加载；这里只钉三件接线事实：
//   · listNodes / chooseNode 都用 candidateNodes（列表与自动选同一份判定）；
//   · createTunnel 接收 nodeId 并交给 resolveNodeChoice（越权/过期的唯一闸门）；
//   · load 保持 number | null（曾经用强转绕成 number，前端于是显示 "null%"）。
import { readFileSync } from "node:fs";

const sakuraSrc = readFileSync(new URL("../src/sakura.ts", import.meta.url), "utf8");

test("sakura.ts：列表与创建共用 candidateNodes（判定不漂移）", () => {
  const calls = sakuraSrc.match(/candidateNodes\(/g) ?? [];
  assert.ok(calls.length >= 2, `listNodes 与 chooseNode 都要用 candidateNodes，实际 ${calls.length} 处`);
  assert.ok(!/\.filter\(\(\[, n\]\) => typeof n\.flag/.test(sakuraSrc), "旧的就地 flag 过滤必须已删除（只留一份判定）");
});

test("sakura.ts：createTunnel 接收 nodeId 并经 resolveNodeChoice 校验", () => {
  assert.ok(/async function createTunnel\(nodeId\?/.test(sakuraSrc), "createTunnel 要有 nodeId 参数");
  assert.ok(/resolveNodeChoice\(cands, nodeId\)/.test(sakuraSrc), "nodeId 必须过 resolveNodeChoice（防越权/过期）");
  assert.ok(/"sakura:createTunnel": \(_e: unknown, nodeId\?/.test(sakuraSrc), "IPC handler 要把 nodeId 透传下去");
});

test("sakura.ts：隧道负载保持 number | null（不强转 0）", () => {
  assert.ok(/load: number \| null;/.test(sakuraSrc), "SakuraTunnelView.load 必须是 number | null");
  assert.ok(/load: node\?\.load \?\? null/.test(sakuraSrc), "load 缺失要落 null（不是 0）");
});

test("createLatencyProber：进行中的同一 host 复用同一个探测（面板重开不并发自打）", async () => {
  let connects = 0;
  const prober = createLatencyProber({
    connect: () => {
      connects++;
      const handlers = {};
      const sock = { setTimeout() {}, on(ev, cb) { handlers[ev] = cb; }, destroy() {} };
      setTimeout(() => handlers.connect?.(), 1);
      return sock;
    },
  });
  const [a, b] = await Promise.all([prober.probe("same.com"), prober.probe("same.com")]);
  assert.equal(connects, 1, "两次并发 probe 只该开一个连接");
  assert.equal(a, b);
});

// ---- extra / auto_https（内地节点 501 修复）----

test("parseExtra：ini 键值串解析（大小写归一、注释与畸形行丢弃）", () => {
  const m = parseExtra("Auto_Https = auto\n# 注释\n; 也是注释\n\nbadline\n = nokey\nfoo = bar = baz");
  assert.equal(m.get("auto_https"), "auto");
  assert.equal(m.get("foo"), "bar = baz"); // 值里的 = 保留（只按第一个切）
  assert.ok(!m.has("badline"));
  assert.equal(parseExtra(null).size, 0);
  assert.equal(parseExtra(undefined).size, 0);
  assert.equal(parseExtra(42).size, 0);
});

test("autoHttpsConfigured：值非空 = 已配置（域名列表算），缺失/空 = 未配置", () => {
  assert.ok(autoHttpsConfigured("auto_https = auto"));
  assert.ok(autoHttpsConfigured("auto_https=example.com,foo.bar"));
  assert.ok(!autoHttpsConfigured("auto_https = "));
  assert.ok(!autoHttpsConfigured("other_key = x"));
  assert.ok(!autoHttpsConfigured(""));
  assert.ok(!autoHttpsConfigured(null));
});

test("withAutoHttps：补 auto_https 且保留其余键；已有则替换为规范形态（幂等）", () => {
  assert.equal(withAutoHttps(""), AUTO_HTTPS_EXTRA);
  assert.equal(withAutoHttps(null), AUTO_HTTPS_EXTRA);
  assert.equal(withAutoHttps("foo = 1\nbar = 2"), "foo = 1\nbar = 2\n" + AUTO_HTTPS_EXTRA);
  // 已有 auto_https（哪怕是别的值）→ 归一，其余键不丢
  assert.equal(withAutoHttps("auto_https = something\nfoo = 1"), "foo = 1\n" + AUTO_HTTPS_EXTRA);
  // 幂等：再跑一遍结果不变
  const once = withAutoHttps("foo = 1");
  assert.equal(withAutoHttps(once), once);
});

test("sakura.ts：创建带 extra、启动前自愈、repairTunnel handler 都在位", () => {
  const src = readFileSync(new URL("../src/sakura.ts", import.meta.url), "utf8");
  assert.ok(/extra: AUTO_HTTPS_EXTRA/.test(src), "createTunnel 的 form 必须带 extra");
  assert.ok(/ensureAutoHttps\(t, id,/.test(src), "startTunnel spawn 前要走 ensureAutoHttps");
  assert.ok(/"sakura:repairTunnel"/.test(src), "要有手动修复 IPC handler");
  assert.ok(/autoHttps: autoHttpsConfigured\(x\.extra\)/.test(src), "隧道视图要带 autoHttps");
});

// ---- 三态拆分（online / running / starting）----
//
// online 只能来自服务端 x.online（节点端确认）；「本机 frpc 在跑」（runningId）
// 不得合并进 online——否则「启动中」窗口里用户看到「在线」但实际还没连上节点。
// starting = running && !online，QR 入口只认 online。

test("sakura.ts：online 与 running 三态拆分（runningId 不再合并进 online）", () => {
  assert.ok(/online: boolean;/m.test(sakuraSrc), "SakuraTunnelView.online = 节点端确认在线");
  assert.ok(/running: boolean;/m.test(sakuraSrc), "SakuraTunnelView.running = 本机 frpc 在跑");
  assert.ok(/starting\?: boolean;/m.test(sakuraSrc), "SakuraTunnelView.starting = 推导的启动中态");
  assert.ok(
    !/online: Boolean\(x\.online\) \|\| state\.runningId === x\.id/.test(sakuraSrc),
    "旧合并（online = x.online || runningId===id）必须已删除——那是「在线」语义失真的来源",
  );
});

test("sakura.ts：fetchTunnels 视图推导 online/running/starting（纯字段投影，不编造）", () => {
  assert.ok(/const running = state\.runningId === x\.id;/.test(sakuraSrc), "running 直接取 runningId===id");
  assert.ok(/const online = Boolean\(x\.online\);/.test(sakuraSrc), "online 只取服务端 x.online");
  assert.ok(/starting: running && !online,/.test(sakuraSrc), "starting = running && !online（启动中窗口）");
});
