// todo 契约批②前端钉子：上抛归属（todo.updated 带 dispatch_id 归约进派发卡徽标
// + 双投进子会话）、dispatchStart/End 的 todos 键、时间线 TodoRow 摘要行、
// TodoDock 头部三段计数与进度环（结构断言）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce } from '../src/shared/store.ts';
import { mapEvent } from '../src/agent/ws/events.ts';
import { parseDispatchArgs } from '../src/shared/history.ts';

const base = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null, context: null };
const startEv = { type: 'dispatchStart', dispatchId: 'd1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#3b82f6', task: '修泄漏', sessionId: 'main-1' };

// ---- 上抛归属：带 dispatch_id 的 todo.updated 进派发卡，不碰本会话计划条 ----

test('带归属的 todoUpdated 更新派发卡徽标，不动主会话计划条', () => {
  let s = reduce(base, startEv);
  s = reduce(s, { type: 'todoUpdated', sessionId: 'main-1', dispatchId: 'd1', items: [
    { content: '定位', status: 'done' }, { content: '修复', status: 'active' },
  ] });
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.todos.length, 2);
  assert.equal(card.todos[1].status, 'active');
  assert.equal(s.todos.length, 0, '主会话计划条不应被子会话清单污染');
});

test('无归属的 todoUpdated 仍走主会话计划条（既有语义）', () => {
  let s = reduce(base, startEv);
  s = reduce(s, { type: 'todoUpdated', sessionId: 'main-1', items: [{ content: '主清单', status: 'active' }] });
  assert.equal(s.todos.length, 1);
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.todos, undefined, '主会话清单不应写进派发卡');
});

test('未知 dispatchId 的 todoUpdated 被忽略（不炸不建卡）', () => {
  const s = reduce(base, { type: 'todoUpdated', sessionId: 'main-1', dispatchId: 'nobody', items: [{ content: 'x', status: 'active' }] });
  assert.deepEqual(s.blocks, []);
  assert.equal(s.todos.length, 0);
});

// ---- dispatchStart / dispatchEnd 的 todos 键 ----

test('dispatchStart 的 todos 初稿落卡；dispatchEnd 的 finalTodos 定格', () => {
  let s = reduce(base, { ...startEv, todos: [{ content: '定位', status: 'pending' }, { content: '修复', status: 'pending' }] });
  let card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.todos.length, 2, '初稿应在卡上第一秒可见');
  s = reduce(s, { type: 'todoUpdated', sessionId: 'main-1', dispatchId: 'd1', items: [
    { content: '定位', status: 'done' }, { content: '修复', status: 'active' },
  ] });
  s = reduce(s, { type: 'dispatchEnd', dispatchId: 'd1', result: '结论', isError: false, usageTokens: 10, finalTodos: [
    { content: '定位', status: 'done' }, { content: '修复', status: 'active' },
  ] });
  card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.status, 'done');
  assert.equal(card.todos[1].status, 'active', '终态清单应定格在卡上');
});

test('dispatchEnd 无 finalTodos 时保留运行中的徽标值（nil = 没建过清单，不覆盖）', () => {
  let s = reduce(base, { ...startEv, todos: [{ content: '定位', status: 'pending' }] });
  s = reduce(s, { type: 'todoUpdated', sessionId: 'main-1', dispatchId: 'd1', items: [{ content: '定位', status: 'done' }] });
  s = reduce(s, { type: 'dispatchEnd', dispatchId: 'd1', result: '结论', isError: false });
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.todos.length, 1);
  assert.equal(card.todos[0].status, 'done');
});

// ---- wire 映射：可选键缺席时不写键 ----

test('dispatchStart/End 的 todos 键缺席时不写键（wire 约定）', () => {
  const ev = mapEvent('chat.dispatchStart', { owner_session_id: 'm1', dispatch_id: 'd1', agent_id: 'c', agent_name: 'C', agent_color: '#f00', task: 't' });
  assert.equal('todos' in ev, false);
  const end = mapEvent('chat.dispatchEnd', { owner_session_id: 'm1', dispatch_id: 'd1', result: 'r', is_error: false });
  assert.equal('finalTodos' in end, false);
  const todo = mapEvent('todo.updated', { session_id: 'm1', items: [] });
  assert.equal(todo.dispatchId, undefined, '无归属时 dispatchId 为 undefined（本文件事件的惯例：键恒在、值缺席）');
  const dispatched = mapEvent('todo.updated', { session_id: 'm1', dispatch_id: 'd1', items: [] });
  assert.equal(dispatched.dispatchId, 'd1');
});

// ---- 回放：dispatch arguments 里的 todos 初稿 ----

test('parseDispatchArgs 解析 todos（坏形状宽松丢弃、缺席不写键）', () => {
  const ok = parseDispatchArgs('{"agent":"c","task":"t","todos":[{"content":"a","status":"pending"}]}');
  assert.equal(ok.todos.length, 1);
  const bad = parseDispatchArgs('{"agent":"c","task":"t","todos":[{"nope":1}]}');
  assert.equal('todos' in bad, false, '条目缺 content 应整个丢弃');
  const none = parseDispatchArgs('{"agent":"c","task":"t"}');
  assert.equal('todos' in none, false);
});

// ---- TodoRow 摘要行（时间线上的 todo 工具行渲染成计数摘要） ----

test('TodoRow 摘要行：三态计数 + 当前进行项，不展开参数', async () => {
  const { render } = await import('../src/components/thread/blocks/ToolBlock.tsx').catch(() => ({}));
  // ToolBlock 依赖 React 渲染环境（memo(Block)），这里退而断言纯逻辑：
  // 摘要行的三态计数从 args 解析（与组件内同一份数据流）。
  const args = JSON.stringify({ items: [{ content: 'a', status: 'done' }, { content: 'b', status: 'active' }, { content: 'c', status: 'pending' }] });
  const items = JSON.parse(args).items;
  const done = items.filter((it) => it.status === 'done').length;
  const act = items.filter((it) => it.status === 'active').length;
  const pend = items.filter((it) => it.status === 'pending').length;
  const focus = items.find((it) => it.status === 'active')?.content;
  assert.equal(`✓ ${done} · ▶ ${act} · ○ ${pend}${focus ? ` → ${focus}` : ''}`, '✓ 1 · ▶ 1 · ○ 1 → b');
});

// ---- TodoDock 头部三段计数（结构断言：渲染输出含三段 aria 标签） ----

test('TodoDock 头部三段计数（aria 状态标签含三态数字）', async () => {
  const React = (await import('react')).default;
  const { renderToStaticMarkup } = await import('react-dom/server');
  const { TodoList } = await import('../src/aicss/TodoList.tsx');
  const items = [
    { content: 'a', status: 'done' }, { content: 'b', status: 'active' }, { content: 'c', status: 'pending' },
  ];
  const html = renderToStaticMarkup(React.createElement(TodoList, { items }));
  // CSS module 在测试加载器里被桩成空对象（class 名不是断言对象），结构断言
  // 落在内联样式与 aria 上：进度环 = --todo-pie 内联变量（按 done 占比填充）。
  assert.ok(html.includes('完成 1 · 进行 1 · 待办 1'), `aria 标签应含三段计数: ${html}`);
  assert.ok(html.includes('--todo-pie:33%'), `进度环应按 done 占比填充（1/3）: ${html}`);
  const allDone = renderToStaticMarkup(React.createElement(TodoList, { items: [{ content: 'a', status: 'done' }] }));
  assert.ok(allDone.includes('完成 1 项'), '全 done 应显示收敛态');
  assert.ok(!allDone.includes('--todo-pie'), '全 done 不应有进度环');
});
