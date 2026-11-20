// OOM 三件套 S3 的钉子：states 表的 LRU 淘汰（LRU + 数量上限 + 时长上限）与
// 配套映射清理（childLinks 两张映射 + prevBusy / prevCompacting）。
//
// 纯函数层（shared/state-retention.ts）+ 归约/事件层（store.handleAgentEvent
// 的 evictStaleStates 收口——驱动真实现，驱动方式与 store.test.mjs 的手搭
// controller 同款）。
//
// 恢复路径契约（钉在注释里、不驱动 WS）：主域淘汰时 forgetLoaded 会清连接层的
// loadedSessions 记忆（重进走全量装载）；助手域 AssistantPage 的 boot 闸门
// （historyReady 缺失）与子会话视图装载 effect 本就按 state 缺失重拉。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import {
  SESSION_STATES_MAX,
  SESSION_STATES_TTL_MS,
  evictableSessionIds,
  pruneActivity,
  pruneChildLinksFor,
  pruneSessionKeys,
  touchActivity,
  touchActivityMany,
  isBusyState,
} from '../src/shared/state-retention.ts';
import { handleAgentEvent } from '../src/shared/store.ts';
import { SESSIONS } from '../src/agent/demo/data.ts';

const HOUR = 60 * 60 * 1000;
/** 造一批不忙的占位 state（evictableSessionIds 只读忙闲位）。 */
const idle = () => ({ busy: false, pending: null, streaming: null, sending: false });
const busy = () => ({ busy: true, pending: null, streaming: null, sending: false });

// ---------- ① 常量契约 ----------

test('淘汰参数：24 个上限 / 24h 时长上限（LRU + 时长的三参数形态）', () => {
  assert.equal(SESSION_STATES_MAX, 24);
  assert.equal(SESSION_STATES_TTL_MS, 24 * HOUR);
});

// ---------- ② 时长淘汰（超 TTL 全删，豁免/忙着的除外） ----------

test('超 TTL 的条目淘汰；keep 豁免与忙着的会话不淘汰', () => {
  const now = 1_000_000_000;
  const states = {
    stale: idle(),          // 30h 前活跃 → 淘汰
    kept: idle(),           // 30h 前活跃但豁免 → 留
    busy: busy(),           // 30h 前活跃但忙着 → 留
    fresh: idle(),          // 1h 前活跃 → 留
  };
  const active = { stale: now - 30 * HOUR, kept: now - 30 * HOUR, busy: now - 30 * HOUR, fresh: now - 1 * HOUR };
  const evicted = evictableSessionIds(states, active, {
    now, keep: new Set(['kept']), ttlMs: SESSION_STATES_TTL_MS,
  });
  assert.deepEqual(evicted.sort(), ['stale'], '只淘汰超 TTL 的非豁免非忙条目');
});

test('没有活跃记录的条目不按时长淘汰（交给数量上限兜底——不猜时间）', () => {
  const states = { mystery: idle(), fresh: idle() };
  const evicted = evictableSessionIds(states, { fresh: Date.now() }, { now: Date.now() });
  assert.deepEqual(evicted, [], '无记录 ≠ 超时——编时间是错的');
});

// ---------- ③ 数量上限（LRU——最久未活跃先淘汰） ----------

test('超上限：从最久未活跃开始淘汰，豁免与忙着的永不动', () => {
  const now = 1_000_000_000;
  // 5 个非豁免条目 + 1 个豁免 + 1 个忙；上限 3 → 非豁免的 5 个超出 2 → 淘汰
  // 最旧的 2 个（a、b）。上限是「可淘汰条目数」的口径：豁免/忙着的条目不占额度。
  const states = {
    a: idle(), b: idle(), c: idle(), d: idle(), e: idle(), keep: idle(), busyOne: busy(),
  };
  const active = {
    a: now - 5 * HOUR, b: now - 4 * HOUR, c: now - 3 * HOUR, d: now - 2 * HOUR,
    e: now - 1 * HOUR, keep: now - 6 * HOUR, busyOne: now - 7 * HOUR,
  };
  const evicted = evictableSessionIds(states, active, {
    now, keep: new Set(['keep']), max: 3, ttlMs: SESSION_STATES_TTL_MS,
  });
  assert.deepEqual(evicted.sort(), ['a', 'b'], 'LRU：最久未活跃先淘汰（豁免/忙着的永不参与）');
});

test('上限内不产生任何淘汰（空数组——调用方据此跳过配套清理）', () => {
  const now = Date.now();
  const states = Object.fromEntries(
    Array.from({ length: SESSION_STATES_MAX }, (_, i) => [`s${i}`, idle()]),
  );
  const active = Object.fromEntries(
    Array.from({ length: SESSION_STATES_MAX }, (_, i) => [`s${i}`, now - i * 1000]),
  );
  assert.deepEqual(evictableSessionIds(states, active, { now }), []);
});

// ---------- ④ 活跃表维护 ----------

test('touchActivity：刷新、幂等返回原引用；时间戳单调化（同毫秒也能分先后）', () => {
  const active = { a: 1, b: 2 };
  assert.equal(touchActivity(active, '', 9), active, '空 id 不动');
  const touched = touchActivity(active, 'a', 1);
  assert.deepEqual(touched, { a: 2, b: 2 }, '同毫秒刷新 → 单调化到上次+1');
  // 单调化保证严格递增——刚活跃的会话在 LRU 排序里恒最新，不会被同毫秒平局挤出
  assert.ok(touchActivity(touched, 'a', 2).a === 3, '再刷再+1');
  const pruned = pruneActivity({ a: 1, b: 2, c: 3 }, ['a', 'c']);
  assert.deepEqual(pruned, { b: 2 });
  const single = { a: 1 };
  assert.ok(pruneActivity(single, []) === single, '无淘汰返回原引用');
});

test('touchActivityMany：批量保留 = 批量刷新活跃（挂载层喂豁免时顺手续命）', () => {
  const active = { a: 1 };
  const same = touchActivityMany(active, ['a'], 1);
  assert.equal(same, active, '全同值幂等');
  const next = touchActivityMany(active, ['a', 'b'], 5);
  assert.deepEqual(next, { a: 5, b: 5 });
});

// ---------- ⑤ 配套清理：childLinks 两张映射 ----------

test('pruneChildLinksFor：子被淘汰 / 父被淘汰的条目都清；dispatchChild 按子端清', () => {
  const links = {
    childParents: { c1: 'p1', c2: 'p2', c3: 'p3' },
    dispatchChild: { d1: 'c1', d2: 'c2' },
  };
  // 淘汰 p1（父）与 c2（子）：c1→p1（父没了）清、c2→p2（子没了）清、c3→p3 留；
  // dispatchChild 里 d1（子=c1，父没了）按子端在父淘汰时也清吗？——c1 没被淘汰，
  // d1 留；d2（子=c2 被淘汰）清。
  const next = pruneChildLinksFor(links, ['p1', 'c2']);
  assert.deepEqual(next.childParents, { c3: 'p3' });
  assert.deepEqual(next.dispatchChild, { d1: 'c1' });
});

test('pruneChildLinksFor：无淘汰返回原引用', () => {
  const links = { childParents: { c: 'p' }, dispatchChild: {} };
  assert.equal(pruneChildLinksFor(links, []), links);
});

// ---------- ⑥ per-session 键值表的通用清理 ----------

test('pruneSessionKeys：prevBusy/prevCompacting/sendQueues 同款清理', () => {
  const table = { a: 1, b: 2, c: 3 };
  assert.deepEqual(pruneSessionKeys(table, ['b']), { a: 1, c: 3 });
  assert.equal(pruneSessionKeys(table, []), table, '无淘汰返回原引用');
});

test('isBusyState：busy/pending/streaming/sending 任一为真都不淘汰', () => {
  assert.equal(isBusyState(undefined), false);
  assert.equal(isBusyState({}), false);
  assert.equal(isBusyState({ busy: true }), true);
  assert.equal(isBusyState({ pending: { id: 'x' } }), true);
  assert.equal(isBusyState({ streaming: {} }), true);
  assert.equal(isBusyState({ sending: true }), true);
});

// ---------- ⑦ 事件层收口：handleAgentEvent 驱动真实现 ----------

/** 手搭 controller（store.test.mjs 同款可变对象直填 + 新增的保留策略槽）。 */
function makeController() {
  const state = {
    currentId: '',
    sessionStates: {},
    assistantStates: {},
    childLinks: { childParents: {}, dispatchChild: {} },
    sendQueues: {},
    prevBusy: {},
    prevCompacting: {},
    active: { project: {}, assistant: {} },
    keep: { project: new Set(), assistant: new Set() },
    forgotten: [],
  };
  const c = {
    getCurrentId: () => state.currentId,
    getSessionStates: () => state.sessionStates,
    getAssistantStates: () => state.assistantStates,
    getChildLinks: () => state.childLinks,
    getSendQueues: () => state.sendQueues,
    getPrevBusy: () => state.prevBusy,
    getPrevCompacting: () => state.prevCompacting,
    setCurrentId: (id) => { state.currentId = id; },
    setSessionStates: (next) => { state.sessionStates = next; },
    setAssistantStates: (next) => { state.assistantStates = next; },
    setOperationError: () => {},
    getOperationErrors: () => [],
    bumpRevision: () => {},
    setChildLinks: (next) => { state.childLinks = next; },
    setSendQueues: (next) => { state.sendQueues = next; },
    setPrevBusy: (next) => { state.prevBusy = next; },
    setPrevCompacting: (next) => { state.prevCompacting = next; },
    autoSend: () => {},
    sendFailed: () => {},
    reset: () => {},
    getActive: (space) => state.active[space],
    setActive: (space, next) => { state.active[space] = next; },
    getKeep: (space) => state.keep[space],
    setKeep: (space, ids) => { state.keep[space] = new Set(ids); },
    forgetLoaded: (ids) => { state.forgotten.push(...ids); },
  };
  return { c, state };
}

test('事件层：keep 集合 + 忙碌的会话不会被淘汰（每事件收口一次）', () => {
  const { c, state } = makeController();
  // 预置两个不忙的会话 state（直接塞，绕过归约）+ 一个挂豁免
  state.sessionStates = {
    open: { ...idle(), blocks: [] },
    closed: { ...idle(), blocks: [] },
  };
  state.active.project = { open: Date.now(), closed: Date.now() };
  c.setKeep('project', ['open']);
  handleAgentEvent({ type: 'ready' }, c);
  handleAgentEvent({ type: 'userMessage', sessionId: 'closed', text: 'x' }, c);
  // closed 未被淘汰（active 刚刷新）……但 keep 里只有 open——closed 不在 keep、
  // 不忙、不超 TTL → 数量上限（24）远未到 → 留着。
  assert.ok(state.sessionStates.closed, '上限内不淘汰');
  // 超上限驱动淘汰：闭集外补 25 个非豁免会话 → closed（最旧）被淘汰
  const now = Date.now();
  for (let i = 0; i < 25; i++) {
    state.sessionStates[`f${i}`] = { ...idle(), blocks: [] };
  }
  state.active.project = { open: now, closed: now - 10_000 };
  for (let i = 0; i < 25; i++) state.active.project[`f${i}`] = now;
  handleAgentEvent({ type: 'userMessage', sessionId: 'f0', text: 'x' }, c);
  assert.ok(state.sessionStates.open, 'keep 豁免不被淘汰');
  assert.ok(!state.sessionStates.closed, '最久未活跃的被淘汰（LRU）');
  assert.ok(state.sessionStates.f0, '刚活跃的留下');
});

test('事件层：主域淘汰同步清 childLinks / prevBusy / prevCompacting 并 forgetLoaded', () => {
  const { c, state } = makeController();
  state.sessionStates = {
    gone: { ...idle(), blocks: [] },
    stay: { ...idle(), blocks: [] },
  };
  state.childLinks = {
    childParents: { gone: 'stay', other: 'stay' },
    dispatchChild: { d1: 'gone', d2: 'stay' },
  };
  state.prevBusy = { gone: false, stay: true };
  state.prevCompacting = { gone: false };
  const now = Date.now();
  state.active.project = { gone: now, stay: now };
  c.setKeep('project', ['stay']);
  // 25 个新会话把 gone 挤出去（gone 最旧）
  for (let i = 0; i < 25; i++) {
    state.sessionStates[`x${i}`] = { ...idle(), blocks: [] };
    state.active.project[`x${i}`] = now;
  }
  handleAgentEvent({ type: 'userMessage', sessionId: 'x0', text: 'x' }, c);
  assert.ok(!state.sessionStates.gone, 'gone 被淘汰');
  assert.deepEqual(state.childLinks.childParents, { other: 'stay' }, 'childParents：父端被淘汰的条目清');
  assert.deepEqual(state.childLinks.dispatchChild, { d2: 'stay' }, 'dispatchChild：子端被淘汰的条目清');
  assert.deepEqual(state.prevBusy, { stay: true }, 'prevBusy 配套清');
  assert.deepEqual(state.prevCompacting, {}, 'prevCompacting 配套清');
  assert.deepEqual(state.forgotten.sort(), ['gone', 'x1'], '淘汰通知连接层（重进走全量装载）');
});

test('事件层：忙着的会话永不淘汰（增量流需要落点）', () => {
  const { c, state } = makeController();
  const now = Date.now();
  state.sessionStates = { busyOne: { ...busy(), blocks: [] } };
  state.active.project = { busyOne: now };
  for (let i = 0; i < 30; i++) {
    state.sessionStates[`f${i}`] = { ...idle(), blocks: [] };
    state.active.project[`f${i}`] = now;
  }
  handleAgentEvent({ type: 'userMessage', sessionId: 'f0', text: 'x' }, c);
  assert.ok(state.sessionStates.busyOne, '忙碌的会话豁免于数量上限');
  // 超时长也不淘汰
  state.active.project.busyOne = now - 48 * HOUR;
  handleAgentEvent({ type: 'userMessage', sessionId: 'f0', text: 'x' }, c);
  assert.ok(state.sessionStates.busyOne, '忙碌的会话豁免于时长上限');
});

test('事件层：非空发送队列的会话不淘汰（缓冲的文本不能跟着丢）', () => {
  const { c, state } = makeController();
  const now = Date.now();
  state.sessionStates = { queued: { ...idle(), blocks: [] } };
  state.active.project = { queued: now };
  state.sendQueues = { queued: [{ id: 'q1', text: '等忙完再发' }] };
  for (let i = 0; i < 30; i++) {
    state.sessionStates[`f${i}`] = { ...idle(), blocks: [] };
    state.active.project[`f${i}`] = now;
  }
  handleAgentEvent({ type: 'userMessage', sessionId: 'f0', text: 'x' }, c);
  assert.ok(state.sessionStates.queued, '有缓冲消息的会话豁免');
});

test('事件层：助手域淘汰独立收口（assistant 事件归约后跑 assistantStates 的淘汰）', () => {
  const { c, state } = makeController();
  const now = Date.now();
  state.assistantStates = { old: { ...idle(), blocks: [] } };
  state.active.assistant = { old: now };
  // 25 个新助手会话
  for (let i = 0; i < 25; i++) {
    state.assistantStates[`a${i}`] = { ...idle(), blocks: [] };
    state.active.assistant[`a${i}`] = now;
  }
  state.childLinks = { childParents: { old: 'x' }, dispatchChild: {} };
  // 一条助手域事件（space 标记）驱动 assistantStates 的归约 + 淘汰
  handleAgentEvent({ type: 'userMessage', sessionId: 'a0', text: 'x', space: 'assistant' }, c);
  assert.ok(!state.assistantStates.old, '最旧的助手会话被淘汰（LRU）');
  assert.ok(state.assistantStates.a0, '刚活跃的留下');
  assert.deepEqual(state.childLinks.childParents, {}, '助手域淘汰同样清 childLinks');
  assert.deepEqual(state.forgotten, [], '助手域不通知 forgetLoaded（AssistantPage boot 按 state 缺失重拉）');
});

test('事件层：sessionFocused 刷新活跃表（聚焦即续命）', () => {
  const { c, state } = makeController();
  handleAgentEvent({ type: 'sessionFocused', id: 's1' }, c);
  assert.ok(state.active.project.s1 > 0, '聚焦刷新活跃时间戳');
  assert.equal(state.currentId, 's1');
});

// ---------- ⑤ forgetLoaded 的 source 桥（2026-12 审查修复） ----------

// 闭包捕获首渲染 source 的热换 bug：controller 只建一次，旧实现把首渲染的
// source 捕进 ref 里的闭包——热换 source 后淘汰通知仍打到旧源（旧源的
// loadedSessions 记忆没清，新源又没记装载 → 被淘汰会话重进秒回空 state）。
// 修复 = ref 存 source 本体、每渲染同步、调用时解引用（与 autoSendRef 同款）。
// hook 内部状态驱动不了（useAgent 是 React hook，测试基建没有渲染期副作用
// 的执行器）——钉子钉源码形状：ref 必须存 source 本体且每渲染同步，调用点
// 必须经 ref 解引用；旧写法（ref 里存捕获 source 的闭包）会让这两条断言红。
test('forgetLoaded 桥：ref 存 source 本体、每渲染同步、调用时解引用（热换不指向旧源）', () => {
  const src = fs.readFileSync(new URL('../src/shared/store.ts', import.meta.url), 'utf8');
  assert.match(src, /const forgetLoadedSourceRef = useRef\(source\);/,
    'ref 初始值必须是 source 本体（不是捕获 source 的闭包）');
  assert.match(src, /forgetLoadedSourceRef\.current = source;/,
    '每渲染同步 ref——热换 source 后读到当前源');
  assert.match(src, /forgetLoaded: \(ids\) => forgetLoadedSourceRef\.current\?\.forgetLoadedSessions\?\.\(ids\)/,
    'controller 调用时经 ref 解引用到当前 source');
  assert.ok(!src.includes('forgetLoadedRef.current(ids)'),
    '旧写法（ref 里存闭包、调用时 blind 转发）必须移除');
});

// ---------- ⑥ demo 源的淘汰恢复（2026-12 审查修复） ----------

// S3 淘汰在 demo 模式的下场：demo 没有连接层 loadedSessions 记忆（forgetLoadedSessions
// 对它 no-op），resumeSession 又只发 sessionFocused 不重拉历史 → state 被淘汰后重进
// 永远空白。修复 = demo 自录主会话历史 + resumeSession 在重进时重放（state 还在的
// 会话不发 historyLoaded——重放会把正在跑的时间线重建一遍）。
test('demo 源：forgetLoadedSessions 空实现 + resumeSession 重放录制的历史', async () => {
  const { DemoAgent } = await import('../src/agent/demo/index.ts');
  const demo = new DemoAgent();
  assert.equal(typeof demo.forgetLoadedSessions, 'function',
    '接口对齐：AgentSource.forgetLoadedSessions 可选能力要有实现（淘汰通知可达）');
  // 空实现语义：对任意 id 调用不炸、不改会话状态
  demo.forgetLoadedSessions(['s1', 's2']);
  assert.equal(demo.currentId, SESSIONS[0].id, 'forgetLoadedSessions 是 no-op（demo 没有 loadedSessions 记忆）');

  // 重放契约：send 一轮 → 录制历史；resumeSession 重进 → 发 historyLoaded（重建 blocks）
  const events = [];
  const off = demo.subscribe((ev) => { if (ev.type !== 'ready') events.push(ev); });
  // 空会话：send 一轮（不等播完——录制随事件序同步发生）
  const sid = '20260911-test-demo-01';
  demo.send(sid, '修一个 bug');
  assert.ok(events.some((ev) => ev.type === 'userMessage' && ev.sessionId === sid), 'send 的 userMessage 已发出');
  // 主会话录制的两处写点随事件序同步发生：todoUpdated（t+300）与派发文本
  //（t+900）落地后，user + assistant(tool_calls) + assistant 正文就都在录制里
  // 了（不等整轮播完——验收 delta 在 6s 以后；思考段 4 行 × ~0.5s + 300ms ≈ 3s）。
  await new Promise((r) => setTimeout(r, 4500));
  // 模拟 store 淘汰：state 表清掉该会话 + forgetLoaded 通知 demo（no-op）
  demo.forgetLoadedSessions([sid]);
  // 重进：resumeSession 必须重放录制的历史（historyLoaded 先到、sessionFocused 后切）
  events.length = 0;
  await demo.resumeSession(sid);
  const loaded = events.filter((ev) => ev.type === 'historyLoaded' && ev.sessionId === sid);
  assert.equal(loaded.length, 1, '重进发一次 historyLoaded（淘汰后重进恢复时间线的依据）');
  const msgs = loaded[0].history.messages;
  assert.equal(msgs[0]?.role, 'user', '录制历史首条是用户消息');
  assert.ok(msgs.some((m) => m.role === 'assistant' && m.tool_calls?.[0]?.function?.name === 'agent_dispatch'),
    '主会话的工具调用（agent_dispatch）也在录制里——dispatch 卡可重建');
  assert.ok(msgs.every((m) => m.role !== 'tool'),
    'demo 的主会话不录 tool 行（agent_dispatch 的结果由 dispatchEnd 呈现，store 不建工具行）');
  const focusIdx = events.findIndex((ev) => ev.type === 'sessionFocused');
  const loadIdx = events.findIndex((ev) => ev.type === 'historyLoaded');
  assert.ok(loadIdx !== -1 && focusIdx !== -1 && loadIdx < focusIdx, '历史先到、焦点后切（resumeSession 帧序纪律）');
  off();
});

test('demo 源：没有录制历史的会话重进不发 historyLoaded（不编空快照抹时间线）', async () => {
  const { DemoAgent } = await import('../src/agent/demo/index.ts');
  const demo = new DemoAgent();
  const events = [];
  const off = demo.subscribe((ev) => { if (ev.type !== 'ready') events.push(ev); });
  const sid = '20260911-test-demo-02';
  await demo.resumeSession(sid);
  assert.ok(!events.some((ev) => ev.type === 'historyLoaded'),
    '没有录制（未发过消息/新会话）不发 historyLoaded——空快照会把演示对话流抹掉');
  assert.ok(events.some((ev) => ev.type === 'sessionFocused'), '焦点照常切换');
  off();
});

