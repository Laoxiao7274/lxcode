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
