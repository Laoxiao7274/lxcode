// todo 契约批③的定格测试：子会话结束后的清单终态要**如实且一致**地落到三处——
// ① 主时间线派发卡徽标（dispatchEnd.finalTodos）；② 子会话视图的计划条（双投
// 路径吃 dispatchEnd 的 finalTodos——「最后一项没显示完成」的根因修复）；
// ③ 迟到的旧 todo.updated 不许把定格终态覆盖回去。
//
// 复现用例覆盖两种事件序列：
//   A. 模型做完工作没再调 todo（最后一次 todo.updated 缺最后一项）→ dispatchEnd
//      的 finalTodos 带全 done 终态——三处都应显示完成；
//   B. finalTodos 正常（无残留）→ 与 A 同形，钉住不回归。
// 以及「确实未收敛」的序列：finalTodos 有残留 → 卡如实显示未完成，不假装完成。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce, reduceSessionStates } from '../src/shared/store.ts';

const base = { blocks: [], busy: true, pending: null, todos: [], currentId: 's1', operationError: null, context: null };

/** 子会话执行中的最后一次清单快照（模型做完事但没把最后一项标 done）。 */
const STALE_TODOS = [
  { content: '定位问题', status: 'done' },
  { content: '修复', status: 'done' },
  { content: '补测试', status: 'active' },  // ← 症状：这一项挂着未完成
];
/** 派发收尾的定格终态（后端按子会话清单现算——全 done）。 */
const FINAL_TODOS = [
  { content: '定位问题', status: 'done' },
  { content: '修复', status: 'done' },
  { content: '补测试', status: 'done' },
];

const start = (s) => reduce(s, {
  type: 'dispatchStart', sessionId: 's1', dispatchId: 'd1', childSessionId: 'child-1',
  agentId: 'coder', agentName: '代码', agentColor: '#000', task: '修 bug',
  todos: STALE_TODOS.map((it) => ({ ...it, status: 'pending' })),
});
const runUpdate = (s) => reduce(s, {
  type: 'todoUpdated', sessionId: 's1', dispatchId: 'd1', items: STALE_TODOS,
});
const end = (s, finalTodos) => reduce(s, {
  type: 'dispatchEnd', sessionId: 's1', dispatchId: 'd1', childSessionId: 'child-1',
  result: '结论', isError: false, usageTokens: 100,
  ...(finalTodos ? { finalTodos } : {}),
});

const badge = (s) => s.blocks.find((b) => b.kind === 'dispatch')?.todos;

test('A. dispatchEnd.finalTodos 定格卡徽标：最后一项显示完成', () => {
  let s = start(base);
  s = runUpdate(s);
  assert.equal(badge(s).at(-1).status, 'active', '运行中徽标随 todo.updated 实时走（最后一项进行中）');
  s = end(s, FINAL_TODOS);
  assert.equal(badge(s).at(-1).status, 'done', '结束后定格为终态——最后一项不再是未完成');
  assert.deepEqual(badge(s), FINAL_TODOS);
});

test('A2. finalTodos 缺席（老后端）不动运行中徽标（不猜）', () => {
  let s = start(base);
  s = runUpdate(s);
  s = end(s, undefined);
  assert.deepEqual(badge(s), STALE_TODOS, '没有终态键就不覆盖——键缺席 = 未知');
});

test('B. 双投：finalTodos 归约进子会话自己的 state（计划条定格）', () => {
  // 子会话 state 已存在（用户开着子会话视图/实时流建过）——计划条停在最后一次
  // todo.updated，最后一项 active；dispatchEnd 到达后必须定格为终态。
  // dispatchChild 是 store 维护的 dispatch→子会话映射（生产恒有——dispatchStart
  // 先于一切子事件到达时记上）。
  let states = { 'child-1': { ...base, currentId: 'child-1', todos: STALE_TODOS } };
  states = reduceSessionStates(states, {
    type: 'dispatchEnd', sessionId: 's1', dispatchId: 'd1', childSessionId: 'child-1',
    result: '结论', isError: false, finalTodos: FINAL_TODOS,
  }, { d1: 'child-1' });
  assert.deepEqual(states['child-1'].todos, FINAL_TODOS,
    '子会话计划条从最后一次 todo.updated 定格到终态——根因修复点');
  // 子会话时间线不因此长出卡（dispatchEnd 仍是卡生命周期事件，不整投）
  assert.deepEqual(states['child-1'].blocks, []);
});

test('B2. 双投：finalTodos 缺席时子会话 state 原样（不猜）', () => {
  let states = { 'child-1': { ...base, currentId: 'child-1', todos: STALE_TODOS } };
  states = reduceSessionStates(states, {
    type: 'dispatchEnd', sessionId: 's1', dispatchId: 'd1', childSessionId: 'child-1',
    result: '结论', isError: false,
  }, { d1: 'child-1' });
  assert.deepEqual(states['child-1'].todos, STALE_TODOS);
});

test('C. 时序竞态：卡定格后迟到的 todo.updated 不覆盖终态', () => {
  let s = start(base);
  s = runUpdate(s);
  s = end(s, FINAL_TODOS);
  s = reduce(s, { type: 'todoUpdated', sessionId: 's1', dispatchId: 'd1', items: STALE_TODOS });
  assert.deepEqual(badge(s), FINAL_TODOS, '迟到的旧快照不许把定格终态覆盖回 active');
});

test('C2. 续跑 = 新的 dispatch（新 tool call id）→ 更新照常生效', () => {
  // 真实链路的续跑是主 Agent 发起**新的** agent_dispatch 调用（新 id = 新卡），
  // 旧卡保持定格；新卡的更新不被迟到守卫挡住。
  let s = start(base);
  s = end(s, FINAL_TODOS);
  s = reduce(s, {
    type: 'dispatchStart', sessionId: 's1', dispatchId: 'd2', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码', agentColor: '#000', task: '继续',
    todos: STALE_TODOS,
  });
  s = reduce(s, { type: 'todoUpdated', sessionId: 's1', dispatchId: 'd2', items: STALE_TODOS });
  const newCard = s.blocks.filter((b) => b.kind === 'dispatch').find((b) => b.id === 'd2');
  assert.deepEqual(newCard?.todos, STALE_TODOS, '新一轮的实时更新不被迟到守卫挡住');
  const oldCard = s.blocks.filter((b) => b.kind === 'dispatch').find((b) => b.id === 'd1');
  assert.deepEqual(oldCard?.todos, FINAL_TODOS, '旧卡的定格终态不受新卡事件影响');
});

test('D. 确实未收敛：finalTodos 带残留 → 如实显示未完成（不假装完成）', () => {
  const unfinished = [
    { content: '定位问题', status: 'done' },
    { content: '修复', status: 'active' },
    { content: '补测试', status: 'pending' },
  ];
  let s = start(base);
  s = end(s, unfinished);
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.deepEqual(card.todos, unfinished, '残留项原样呈现——徽标 ✓1/3，不代模型标 done');
});
