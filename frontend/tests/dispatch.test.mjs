// dispatch 块的归约测试：子上下文隔离（归属路由）+ 状态定格 + 主时间线不受污染。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce } from '../src/shared/store.ts';
const base = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null };

test('dispatchStart 开卡 → 子事件按 dispatchId 挂进 subBlocks', () => {
  let s = reduce(base, { type: 'dispatchStart', dispatchId: 'd1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#3b82f6', task: '跑测试' });
  s = reduce(s, { type: 'delta', kind: 'text', text: '子回复', dispatchId: 'd1' });
  s = reduce(s, { type: 'toolCall', id: 'd-c1', name: 'bash', arguments: '{}', dispatchId: 'd1' });
  s = reduce(s, { type: 'toolResult', id: 'd-c1', name: 'bash', content: 'PASS', isError: false, dispatchId: 'd1' });
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.kind, 'dispatch');
  assert.equal(card.status, 'running');
  assert.equal(card.subBlocks.length, 2, '子块两个（assistant + tool）');
  assert.equal(card.subBlocks[0].content, '子回复');
  assert.equal(card.subBlocks[1].result, 'PASS');
  // 主时间线没有子内容（隔离）
  assert.equal(s.blocks.filter((b) => b.kind === 'assistant').length, 0);
  assert.equal(s.blocks.filter((b) => b.kind === 'tool').length, 0);
});

test('dispatchEnd 定格 + 结果回填（done 状态 + result）', () => {
  let s = reduce(base, { type: 'dispatchStart', dispatchId: 'd1', agentId: 'coder', agentName: '代码', agentColor: '#123456', task: 't' });
  s = reduce(s, { type: 'delta', kind: 'text', text: '部分', dispatchId: 'd1' });
  s = reduce(s, { type: 'done', usageTokens: 100, finishReason: 'stop', dispatchId: 'd1' });
  s = reduce(s, { type: 'dispatchEnd', dispatchId: 'd1', result: '最终结论', isError: false, usageTokens: 100 });
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.status, 'done');
  assert.equal(card.result, '最终结论');
  // 子时间线的 assistant 已定格带 usage
  const sub = card.subBlocks.find((b) => b.kind === 'assistant');
  assert.equal(sub.streaming, false);
  assert.equal(sub.usageTokens, 100);
});

test('未知 dispatchId 的子事件被忽略（不炸不挂错）', () => {
  const s = reduce(base, { type: 'delta', kind: 'text', text: '孤儿', dispatchId: 'nobody' });
  assert.deepEqual(s.blocks, []);
});

test('子事件不干扰主时间线的流式块（并行场景的归属正确性）', () => {
  let s = reduce(base, { type: 'dispatchStart', dispatchId: 'd1', agentId: 'coder', agentName: 'c', agentColor: '#000', task: 't' });
  s = reduce(s, { type: 'delta', kind: 'text', text: '主时间线' });
  s = reduce(s, { type: 'delta', kind: 'text', text: '子时间线', dispatchId: 'd1' });
  const main = s.blocks.find((b) => b.kind === 'assistant');
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(main.content, '主时间线');
  assert.equal(card.subBlocks[0].content, '子时间线');
});
