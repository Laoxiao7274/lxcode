import test from 'node:test';
import assert from 'node:assert/strict';
import {
  appendSessionTab,
  pruneSessionTabs,
  seedSessionTabs,
  sessionTabFallback,
  visibleSessionTabs,
} from '../src/shared/session-tabs.ts';

const meta = (id, extra = {}) => ({ id, title: id, workspace: '', archived: false, ...extra });

test('首次铺开：最近在前的后端序反转成"新的在右"', () => {
  const seeded = seedSessionTabs([meta('a'), meta('b'), meta('c')]);
  assert.deepEqual(seeded, ['c', 'b', 'a']);
});

test('铺开时剔除已归档会话', () => {
  const seeded = seedSessionTabs([meta('a'), meta('b', { archived: true }), meta('c')]);
  assert.deepEqual(seeded, ['c', 'a']);
});

test('追加不重排已有标签，超出容量丢最老的', () => {
  let order = ['a', 'b'];
  assert.deepEqual(appendSessionTab(order, 'a'), ['a', 'b']); // 已存在 → 原样
  assert.deepEqual(appendSessionTab(order, 'c'), ['a', 'b', 'c']);
  order = ['a', 'b', 'c'];
  assert.deepEqual(appendSessionTab(order, 'd', 3), ['b', 'c', 'd']); // 容量 3 → 丢 a
});

test('可见标签剔除归档与已关闭', () => {
  const sessions = [meta('a'), meta('b', { archived: true }), meta('c'), meta('d')];
  const closed = new Set(['d']);
  assert.deepEqual(visibleSessionTabs(['a', 'b', 'c', 'd'], sessions, closed, 'a'), ['a', 'c']);
});

test('用户显式关掉的标签立刻消失——当前会话也不例外', () => {
  // 关标签是明确意图，不能为了"标签条始终指着当前会话"把它留到焦点移走为止：
  // 那样会有 ~60ms 的"点了没反应"，就是用户报的"关当前标签卡一下"。
  const sessions = [meta('a'), meta('b')];
  assert.deepEqual(visibleSessionTabs(['a', 'b'], sessions, new Set(['b']), 'b'), ['a']);
});

test('当前会话被容量挤出 order 时仍显示（标签条必须指着它）', () => {
  const sessions = [meta('a'), meta('b')];
  // order 里没有当前会话（容量挤出），但它没被用户关掉 → 必须显示
  assert.deepEqual(visibleSessionTabs(['a'], sessions, new Set(), 'b'), ['a', 'b']);
});

test('关掉当前标签：焦点交给最近打开的另一个标签，而不是新建会话', () => {
  const visible = ['a', 'b', 'c'];
  assert.equal(sessionTabFallback(visible, 'c'), 'b'); // 关最右 → 回退到它左边那个
  assert.equal(sessionTabFallback(visible, 'a'), 'c'); // 关最左 → 回退到最近打开的
  assert.equal(sessionTabFallback(visible, 'b'), 'c');
});

test('只剩一个标签时没有可回退对象：返回空串，调用方保持现状（不新建会话）', () => {
  assert.equal(sessionTabFallback(['only'], 'only'), '');
  assert.equal(sessionTabFallback([], ''), '');
});

test('死标签（已关/已归档）从 order 里剔掉，不占容量', () => {
  const sessions = [meta('a'), meta('b'), meta('c')];
  const closed = new Set(['b']);
  assert.deepEqual(pruneSessionTabs(['a', 'b', 'c'], sessions, closed, 'a'), ['a', 'c']);
  // 无变化时必须返回**原数组引用**（调用方 setState 同引用 → React 跳过重渲染，否则会死循环）
  const stable = ['a', 'c'];
  assert.equal(pruneSessionTabs(stable, sessions, closed, 'a'), stable);
});

test('会话列表还没到时 prune 不动 order（否则会把整条标签栏清空）', () => {
  assert.deepEqual(pruneSessionTabs(['a', 'b'], [], new Set(), 'a'), ['a', 'b']);
});

test('关掉的当前会话也会被剔出 order（不占容量、也不留在标签条上）', () => {
  const sessions = [meta('a'), meta('b')];
  assert.deepEqual(pruneSessionTabs(['a', 'b'], sessions, new Set(['a']), 'a'), ['b']);
});

test('剔除死标签后，追加新标签不再挤掉活标签', () => {
  const sessions = [meta('a'), meta('b')];
  const closed = new Set(['a']);
  const order = pruneSessionTabs(['a', 'b'], sessions, closed, 'b');
  assert.deepEqual(order, ['b']);
  assert.deepEqual(appendSessionTab(order, 'c', 2), ['b', 'c']); // 容量 2：只丢死标签，b 保住
});
