import test from 'node:test';
import assert from 'node:assert/strict';
import {
  closeWorkspacePage,
  createWorkspaceTabs,
  focusChatTab,
  focusWorkspacePage,
} from '../src/shared/workspace-tabs.ts';

test('opening a workspace page adds one tab and focusing it does not reorder tabs', () => {
  let state = createWorkspaceTabs();
  state = focusWorkspacePage(state, 'catalog');
  state = focusWorkspacePage(state, 'git');
  state = focusWorkspacePage(state, 'catalog');

  assert.deepEqual(state.tabs, ['catalog', 'git']);
  assert.equal(state.active, 'catalog');
  assert.deepEqual(state.history, ['git', 'chat']);
});

test('closing the active page returns to the most recently focused open page', () => {
  let state = createWorkspaceTabs();
  state = focusWorkspacePage(state, 'agents');
  state = focusWorkspacePage(state, 'git');
  state = closeWorkspacePage(state, 'git');

  assert.deepEqual(state.tabs, ['agents']);
  assert.equal(state.active, 'agents');
  assert.deepEqual(state.history, ['chat']);
});

test('closing the final workspace page returns to chat and retains no stale history', () => {
  let state = focusWorkspacePage(createWorkspaceTabs(), 'git');
  state = closeWorkspacePage(state, 'git');

  assert.deepEqual(state, { tabs: [], active: 'chat', history: [] });
});

test('closing an inactive page leaves focus unchanged and removes it from history', () => {
  let state = createWorkspaceTabs();
  state = focusWorkspacePage(state, 'agents');
  state = focusWorkspacePage(state, 'catalog');
  state = closeWorkspacePage(state, 'agents');

  assert.deepEqual(state.tabs, ['catalog']);
  assert.equal(state.active, 'catalog');
  assert.deepEqual(state.history, ['chat']);
});

test('focusing chat keeps workspace tabs open for later reuse', () => {
  let state = focusWorkspacePage(createWorkspaceTabs(), 'git');
  state = focusChatTab(state);

  assert.deepEqual(state.tabs, ['git']);
  assert.equal(state.active, 'chat');
  assert.deepEqual(state.history, ['git']);
});

test('closing an unknown page is a no-op', () => {
  const state = createWorkspaceTabs();
  assert.equal(closeWorkspacePage(state, 'catalog'), state);
});
