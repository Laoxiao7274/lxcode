import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce } from '../src/shared/store.ts';
const state = { blocks: [{ kind: 'assistant', uid: 1, content: 'partial', reasoning: '', streaming: true }], busy: true, pending: { id: 'c' }, todos: [{ content: 'work', status: 'active' }] };
test('rename/archive and operation failures preserve current chat, pending and todos', () => {
  for (const reason of ['renamed', 'archived', 'started']) {
    const next = reduce(state, { type: 'sessionChanged', id: 'other', reason });
    assert.deepEqual(next, state);
    assert.equal(next.blocks, state.blocks);
  }
  assert.equal(reduce(state, { type: 'operationError', message: 'failure' }), state);
});
test('new/resumed reset prior conversation and todo state', () => {
  for (const reason of ['new', 'resumed']) {
    assert.deepEqual(reduce(state, { type: 'sessionChanged', id: 'new', reason }), { blocks: [], busy: false, pending: null, todos: [] });
  }
});
