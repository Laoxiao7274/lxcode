import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce } from '../src/shared/store.ts';
const state = { blocks: [{ kind: 'assistant', uid: 1, content: 'partial', reasoning: '', streaming: true }], busy: true, pending: { id: 'c' }, todos: [{ content: 'work', status: 'active' }], currentId: 's1', operationError: null };
test('rename/archive and operation failures preserve current chat, pending and todos', () => {
  for (const reason of ['renamed', 'archived', 'started']) {
    const next = reduce(state, { type: 'sessionChanged', id: 'other', reason });
    // 会话 id 跟随（started 是懒建行——高亮切过去），其余原样
    assert.deepEqual(next, { ...state, currentId: 'other' });
    assert.equal(next.blocks, state.blocks);
  }
  // operationError 是一次性提示——归约收敛（不再是穿透态）
  const withErr = reduce(state, { type: 'operationError', message: 'failure' });
  assert.equal(withErr.operationError, 'failure');
  assert.equal(withErr.blocks, state.blocks);
});
test('new/resumed reset prior conversation and todo state', () => {
  for (const reason of ['new', 'resumed']) {
    assert.deepEqual(reduce(state, { type: 'sessionChanged', id: 'new', reason }), { blocks: [], busy: false, pending: null, todos: [], currentId: 'new', operationError: null });
  }
});
test('mid-turn usage is cleared when a new assistant block starts (stats only on final answer)', () => {
  // 第一轮 assistant 完成（带 usage）→ 工具行 → 第二轮正文开新块：
  // 中间轮的 usage 应被清——DSH 的 tokens 统计只在整轮末尾显示。
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null };
  s = reduce(s, { type: 'delta', kind: 'text', text: '我先读一下' });
  s = reduce(s, { type: 'done', usageTokens: 2209, finishReason: 'tool_calls' });
  s = reduce(s, { type: 'toolCall', id: 'c1', name: 'read_file', arguments: '{}' });
  s = reduce(s, { type: 'toolResult', id: 'c1', name: 'read_file', content: 'ok', isError: false });
  // 第二轮开新 assistant 块（delta）——中间轮 usage 被清
  s = reduce(s, { type: 'delta', kind: 'text', text: '读完了' });
  const first = s.blocks.find((b) => b.kind === 'assistant');
  assert.equal(first.usageTokens, undefined, '中间轮 usage 应被清');
  // 整轮最终块正常带 usage
  s = reduce(s, { type: 'done', usageTokens: 120, finishReason: 'stop' });
  const last = s.blocks.filter((b) => b.kind === 'assistant').at(-1);
  assert.equal(last.usageTokens, 120, '最终轮 usage 保留');
});
