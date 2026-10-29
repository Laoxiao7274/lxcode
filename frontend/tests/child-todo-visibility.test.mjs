// 子 Agent 任务清单（todo）「有时不显示」的钉子（2026-12 用户报）。
//
// 三条独立根因，每条都能单独让清单消失：
//   ① **空快照覆盖本地清单**：chat.history 的 `todos` 是 omitempty（Go 侧空切片
//      不上 wire，前端 history-snapshot 归一成 []）。子会话重开视图（每次挂载都
//      childHistory）拿到的快照若还没带上清单（落库时序/现建运行时），旧归约
//      `todos: h.todos ?? []` 直接把本地已显示的清单抹成空 → 计划条整块不渲染
//      （Composer 的 `todos.length > 0` 条件）。
//   ② **回放占位卡丢弃 todo.updated**：历史里没有配对结果的调度被 history.ts
//      先按「中断」定格（status=done + isError + DISPATCH_INTERRUPTED）。会话仍在跑
//      时那张卡其实是执行中，而 applyDispatchTodos 的迟到守卫按 `status === "done"`
//      一律丢弃 → 卡上的 N/M 永远停在初稿（甚至没有徽标）。
//   ③ **dispatchStart 无条件追加卡**：刷新后 historyLoaded 已建过同 id 的卡，
//      dispatchStart 再追加一张 → 两张同 id 的卡，todo 更新只落在新卡上，用户
//      看到/点开的是历史那张（旧卡）。
//
// 覆盖：事件先到/后到、历史重拉后恢复、完成后状态（终态定格不被迟到事件覆盖）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { handleAgentEvent, reduce, reduceSessionStates } from '../src/shared/store.ts';
import { DISPATCH_INTERRUPTED } from '../src/shared/history.ts';

function makeController() {
  return {
    currentId: 'parent-1',
    sessionStates: { 'parent-1': { blocks: [], busy: true, pending: null, todos: [], currentId: 'parent-1', operationError: null, context: null, historyReady: false } },
    childLinks: { childParents: {}, dispatchChild: {} },
    sendQueues: {}, prevBusy: {}, revisions: 0,
    getCurrentId() { return this.currentId; },
    getSessionStates() { return this.sessionStates; },
    getChildLinks() { return this.childLinks; },
    getSendQueues() { return this.sendQueues; },
    getPrevBusy() { return this.prevBusy; },
    setCurrentId(id) { this.currentId = id; },
    setSessionStates(next) { this.sessionStates = next; },
    setOperationError() {}, bumpRevision() { this.revisions += 1; },
    setChildLinks(next) { this.childLinks = next; },
    setSendQueues(next) { this.sendQueues = next; },
    setPrevBusy(next) { this.prevBusy = next; },
    autoSend() {}, sendFailed() {}, reset() {},
  };
}

const snapshot = (sessionId, messages, overrides = {}) => ({
  sessionId, busy: false, pending: null, todos: [], checkpoints: [], messages, ...overrides,
});

const TODOS = [
  { content: '定位问题', status: 'done' },
  { content: '修复', status: 'active' },
];
const FINAL = [
  { content: '定位问题', status: 'done' },
  { content: '修复', status: 'done' },
];

const cardOf = (state, id) => state.blocks.find((b) => b.kind === 'dispatch' && b.id === id);

// ---------- ① 历史重拉不抹掉本地清单 ----------

test('① 子会话重开（空 todos 快照）不抹掉已显示的清单', () => {
  const c = makeController();
  handleAgentEvent({
    type: 'dispatchStart', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码', agentColor: '#000', task: '改 bug',
  }, c);
  handleAgentEvent({ type: 'historyLoaded', sessionId: 'child-1', history: snapshot('child-1', [{ role: 'user', content: '任务说明书' }]) }, c);
  // 子 Agent 建清单（todo.updated 带归属 → 双投进子会话 state）
  handleAgentEvent({ type: 'todoUpdated', sessionId: 'parent-1', dispatchId: 'd1', items: TODOS }, c);
  assert.deepEqual(c.sessionStates['child-1'].todos, TODOS);
  // 重开视图：快照还没带上清单（omitempty → 空数组）
  handleAgentEvent({ type: 'historyLoaded', sessionId: 'child-1', history: snapshot('child-1', [{ role: 'user', content: '任务说明书' }]) }, c);
  assert.deepEqual(c.sessionStates['child-1'].todos, TODOS,
    '空 todos 快照 = 未知，不许抹掉本地已显示的清单（否则计划条整块消失）');
});

test('①b 快照带了清单 → 以快照为准（后端是事实源）', () => {
  const c = makeController();
  handleAgentEvent({
    type: 'dispatchStart', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码', agentColor: '#000', task: '改 bug',
  }, c);
  handleAgentEvent({ type: 'historyLoaded', sessionId: 'child-1', history: snapshot('child-1', [{ role: 'user', content: '任务说明书' }]) }, c);
  handleAgentEvent({ type: 'todoUpdated', sessionId: 'parent-1', dispatchId: 'd1', items: TODOS }, c);
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [{ role: 'user', content: '任务说明书' }], { todos: FINAL }),
  }, c);
  assert.deepEqual(c.sessionStates['child-1'].todos, FINAL, '快照带清单时以它为准（重启恢复靠它）');
});

// ---------- ② 回放占位卡不再吞掉 todo.updated ----------

test('② 回放占位卡（中断态）+ 会话仍在跑 → todo.updated 照常上卡，且卡恢复运行中', () => {
  const c = makeController();
  c.sessionStates['parent-1'] = { ...c.sessionStates['parent-1'], historyReady: false, busy: false };
  // 刷新/冷恢复：历史里有 assistant 的 agent_dispatch 调用但没有配对结果
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: snapshot('parent-1', [
      { role: 'user', content: '干活' },
      { role: 'assistant', content: '', tool_calls: [{ id: 'd9', function: { name: 'agent_dispatch', arguments: '{"agent":"coder","task":"跑测试"}' } }] },
    ], { busy: true }),
  }, c);
  const card0 = cardOf(c.sessionStates['parent-1'], 'd9');
  assert.equal(card0.status, 'running', '会话仍在跑 → 最后一张占位卡恢复成执行中（不许红着说失败）');
  assert.equal(card0.isError, undefined);
  assert.equal(card0.result, undefined);
  // todo.updated 到达：不再被「卡已 done」的迟到守卫丢弃
  handleAgentEvent({ type: 'todoUpdated', sessionId: 'parent-1', dispatchId: 'd9', items: TODOS }, c);
  const card1 = cardOf(c.sessionStates['parent-1'], 'd9');
  assert.deepEqual(card1.todos, TODOS, '占位卡不是终态，清单必须能写上去');
});

test('②b 回放占位卡 + 会话空闲（历史里真断掉的派发）→ 保持中断定格，清单仍可补', () => {
  const c = makeController();
  c.sessionStates['parent-1'] = { ...c.sessionStates['parent-1'], historyReady: false, busy: false };
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: snapshot('parent-1', [
      { role: 'user', content: '干活' },
      { role: 'assistant', content: '', tool_calls: [{ id: 'd9', function: { name: 'agent_dispatch', arguments: '{"agent":"coder","task":"跑测试"}' } }] },
    ], { busy: false }),
  }, c);
  const card = cardOf(c.sessionStates['parent-1'], 'd9');
  assert.equal(card.status, 'done', '会话空闲 → 那次派发确实在结束前断了，如实定格');
  assert.equal(card.isError, true);
  assert.equal(card.result, DISPATCH_INTERRUPTED);
  // 清单是「状态」不是「结论」：卡定格之后迟到的清单快照仍如实补上（不当成翻回运行中）
  handleAgentEvent({ type: 'todoUpdated', sessionId: 'parent-1', dispatchId: 'd9', items: TODOS }, c);
  const after = cardOf(c.sessionStates['parent-1'], 'd9');
  assert.deepEqual(after.todos, TODOS, '占位卡的清单进度照常更新');
  assert.equal(after.status, 'done', '但不许把「已中断」改成「运行中」');
});

// ---------- ③ dispatchStart 复用回放卡（不长两张同 id 的卡） ----------

test('③ 刷新后 dispatchStart 复用历史卡（不并排长两张同 id 的卡）', () => {
  let s = { blocks: [], busy: false, pending: null, todos: [], currentId: 'main-1', operationError: null, context: null, historyReady: false };
  // 历史回放建卡（中断占位）
  s = reduce(s, {
    type: 'historyLoaded', sessionId: 'main-1',
    history: snapshot('main-1', [
      { role: 'user', content: '干活' },
      { role: 'assistant', content: '', tool_calls: [{ id: 'd1', function: { name: 'agent_dispatch', arguments: '{"agent":"coder","task":"跑测试"}' } }] },
    ], { busy: true }),
  });
  assert.equal(s.blocks.filter((b) => b.kind === 'dispatch').length, 1);
  // 子 Agent 还在跑 → dispatchStart 到
  s = reduce(s, {
    type: 'dispatchStart', sessionId: 'main-1', dispatchId: 'd1', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: '跑测试', todos: TODOS,
  });
  const cards = s.blocks.filter((b) => b.kind === 'dispatch');
  assert.equal(cards.length, 1, '同 dispatch_id 只许有一张卡（两张会让 todo 落到用户看不见的那张）');
  assert.equal(cards[0].status, 'running');
  assert.equal(cards[0].sessionId, 'child-1');
  assert.deepEqual(cards[0].todos, TODOS, '初稿落在同一张卡上');
  // 后续 todo.updated 也落在它上面
  s = reduce(s, { type: 'todoUpdated', sessionId: 'main-1', dispatchId: 'd1', items: FINAL });
  assert.deepEqual(cardOf(s, 'd1').todos, FINAL);
});

test('③b 续跑（同 id 再派一次）：状态回到 running，旧结论清掉，清单初稿覆盖', () => {
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: 'main-1', operationError: null, context: null, historyReady: true };
  s = reduce(s, { type: 'dispatchStart', sessionId: 'main-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码', agentColor: '#000', task: '第一轮', todos: TODOS });
  s = reduce(s, { type: 'dispatchEnd', sessionId: 'main-1', dispatchId: 'd1', result: '第一轮结论', isError: false, usageTokens: 10, finalTodos: FINAL });
  assert.equal(cardOf(s, 'd1').status, 'done');
  s = reduce(s, { type: 'dispatchStart', sessionId: 'main-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码', agentColor: '#000', task: '第二轮', todos: TODOS });
  const card = cardOf(s, 'd1');
  assert.equal(card.status, 'running');
  assert.equal(card.result, undefined, '旧结论清掉（不然用户看到上一轮的结论挂在运行中的卡上）');
  assert.equal(card.usageTokens, undefined);
  assert.equal(card.task, '第二轮');
  assert.deepEqual(card.todos, TODOS);
  assert.equal(s.blocks.filter((b) => b.kind === 'dispatch').length, 1);
});

test('③c dispatchStart 不带初稿时不抹掉卡上已有的清单', () => {
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: 'main-1', operationError: null, context: null, historyReady: true };
  s = reduce(s, { type: 'dispatchStart', sessionId: 'main-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码', agentColor: '#000', task: 't', todos: TODOS });
  s = reduce(s, { type: 'todoUpdated', sessionId: 'main-1', dispatchId: 'd1', items: FINAL });
  // 续跑时主 Agent 没带 todos（nil = 未知）
  s = reduce(s, { type: 'dispatchStart', sessionId: 'main-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码', agentColor: '#000', task: 't2' });
  assert.deepEqual(cardOf(s, 'd1').todos, FINAL, '键缺席 = 未知，不猜（别把已有清单抹成空）');
});

// ---------- ④ 竞态：todo 事件先于 dispatchStart 到达 ----------

test('④ todo 先于 dispatchStart（卡还不存在）→ 不炸；卡建好后 dispatchStart 的初稿补上', () => {
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: 'main-1', operationError: null, context: null, historyReady: true };
  // 无卡时到达：静默忽略（徽标是附属于卡的呈现，不是独立状态）
  s = reduce(s, { type: 'todoUpdated', sessionId: 'main-1', dispatchId: 'd1', items: TODOS });
  assert.deepEqual(s.blocks, []);
  assert.deepEqual(s.todos, [], '带归属的清单绝不写进主会话计划条');
  // 卡建好后（初稿非空）清单立刻可见
  s = reduce(s, { type: 'dispatchStart', sessionId: 'main-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码', agentColor: '#000', task: 't', todos: TODOS });
  assert.deepEqual(cardOf(s, 'd1').todos, TODOS);
});

test('④b todo 先到且子会话 state 已存在 → 双投仍然生效（不依赖 dispatchStart）', () => {
  // 子会话 state 由 childHistory 先建好（用户已打开过视图），dispatchStart 早已过去
  let states = {
    'parent-1': { blocks: [{ kind: 'dispatch', uid: 1, id: 'd1', sessionId: 'child-1', agentId: 'coder', agentName: '代码', agentColor: '#000', task: 't', status: 'running', subBlocks: [] }], busy: true, pending: null, todos: [], currentId: 'parent-1', operationError: null, context: null, historyReady: true },
    'child-1': { blocks: [], busy: true, pending: null, todos: [], currentId: 'child-1', operationError: null, context: null, historyReady: true },
  };
  states = reduceSessionStates(states, { type: 'todoUpdated', sessionId: 'parent-1', dispatchId: 'd1', items: TODOS }, { d1: 'child-1' });
  assert.deepEqual(states['child-1'].todos, TODOS, '双投：子会话计划条拿到清单');
  assert.deepEqual(cardOf(states['parent-1'], 'd1').todos, TODOS, '卡徽标同步');
  assert.deepEqual(states['parent-1'].todos, [], '主会话计划条不被污染');
});

// ---------- ⑤ 完成后状态：终态定格，迟到事件不覆盖 ----------

test('⑤ 真终态（成功）→ 迟到的 todo.updated 不许覆盖 finalTodos', () => {
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: 'main-1', operationError: null, context: null, historyReady: true };
  s = reduce(s, { type: 'dispatchStart', sessionId: 'main-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码', agentColor: '#000', task: 't', todos: TODOS });
  s = reduce(s, { type: 'dispatchEnd', sessionId: 'main-1', dispatchId: 'd1', result: '结论', isError: false, usageTokens: 10, finalTodos: FINAL });
  s = reduce(s, { type: 'todoUpdated', sessionId: 'main-1', dispatchId: 'd1', items: TODOS });
  assert.deepEqual(cardOf(s, 'd1').todos, FINAL, '成功收尾后清单定格，迟到快照不许改回去');
});

test('⑤b 子会话计划条在派发结束后定格到 finalTodos（完成后仍显示，不清空）', () => {
  let states = { 'child-1': { blocks: [], busy: true, pending: null, todos: TODOS, currentId: 'child-1', operationError: null, context: null, historyReady: true } };
  states = reduceSessionStates(states, {
    type: 'dispatchEnd', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1',
    result: '结论', isError: false, finalTodos: FINAL,
  }, { d1: 'child-1' });
  assert.deepEqual(states['child-1'].todos, FINAL,
    '完成后保留显示（docs/todo-contract.md：子会话清单定格归档，不清空）');
});

test('⑤c finalTodos 缺席（老后端）→ 保持运行中的清单，不猜', () => {
  let states = { 'child-1': { blocks: [], busy: true, pending: null, todos: TODOS, currentId: 'child-1', operationError: null, context: null, historyReady: true } };
  states = reduceSessionStates(states, {
    type: 'dispatchEnd', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1',
    result: '结论', isError: false,
  }, { d1: 'child-1' });
  assert.deepEqual(states['child-1'].todos, TODOS);
});
