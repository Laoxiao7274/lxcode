import test from 'node:test';
import assert from 'node:assert/strict';
import {
  astChildSession,
  astChildTabKey,
  childTabSession,
  closeWorkspacePage,
  createWorkspaceTabs,
  focusAstChildView,
  focusChatTab,
  focusHistoryView,
  focusWorkspacePage,
} from '../src/shared/workspace-tabs.ts';

test('opening a workspace page adds one tab and focusing it does not reorder tabs', () => {
  let state = createWorkspaceTabs();
  state = focusWorkspacePage(state, 'catalog');
  state = focusWorkspacePage(state, 'agents');
  state = focusWorkspacePage(state, 'catalog');

  assert.deepEqual(state.tabs, ['catalog', 'agents']);
  assert.equal(state.active, 'catalog');
  assert.deepEqual(state.history, ['agents', 'chat']);
});

test('closing the active page returns to the most recently focused open page', () => {
  let state = createWorkspaceTabs();
  state = focusWorkspacePage(state, 'agents');
  state = focusWorkspacePage(state, 'catalog');
  state = closeWorkspacePage(state, 'catalog');

  assert.deepEqual(state.tabs, ['agents']);
  assert.equal(state.active, 'agents');
  assert.deepEqual(state.history, ['chat']);
});

test('closing the final workspace page returns to chat and retains no stale history', () => {
  let state = focusWorkspacePage(createWorkspaceTabs(), 'catalog');
  state = closeWorkspacePage(state, 'catalog');

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
  let state = focusWorkspacePage(createWorkspaceTabs(), 'catalog');
  state = focusChatTab(state);

  assert.deepEqual(state.tabs, ['catalog']);
  assert.equal(state.active, 'chat');
  assert.deepEqual(state.history, ['catalog']);
});

test('closing an unknown page is a no-op', () => {
  const state = createWorkspaceTabs();
  assert.equal(closeWorkspacePage(state, 'catalog'), state);
});

// ---------- 2026-12：子会话不再进标签栏（临时视图，见 child-view.test.mjs） ----------
//
// 原先这里的 splitWorkspaceTabs（标签条分流）与 childTabHint（子会话标签 hover 提示）
// 已随「标签栏不出现子会话」一起退役：固定页签是标签栏唯一的工作区标签，
// 子会话以临时视图打开（focusChildView 只切 active、不进 tabs）。

// ---------- 2026-12 方案 A：助手子会话临时视图（astchild:*） ----------
//
// 项目模式侧栏「来自助手」行点击开**临时视图**：与主域 child:* 同语义（只切
// active、不进 tabs），但前缀刻意区分——两类临时视图的渲染分流与裁决域口
//（主域 chat.confirm vs 助手域 assistant.confirm）完全不同，混用前缀 = 串域实害。

test('A① astChildTabKey/astChildSession 往返一致；child:/固定页签/chat 不被误认', () => {
  assert.equal(astChildTabKey('sess-1'), 'astchild:sess-1');
  assert.equal(astChildSession(astChildTabKey('sess-1')), 'sess-1');
  assert.equal(astChildSession(astChildTabKey('a:b')), 'a:b', 'id 里带冒号照样往返');
  // 与主域 child: 前缀互不误认
  assert.equal(astChildSession('child:sess-1'), null, 'child: 不是 astchild 键');
  assert.equal(childTabSession('astchild:sess-1'), null, 'astchild: 不是 child 键');
  for (const fixed of ['agents', 'catalog', 'git', 'chat']) {
    assert.equal(astChildSession(fixed), null, fixed + ' 不该被当成 astchild 键');
  }
  assert.equal(astChildSession('astchild:'), null, '空 id 坏键 → null');
});

test('A② focusAstChildView：只切 active 不进 tabs；重复打开同一视图；空 id no-op', () => {
  let state = focusWorkspacePage(createWorkspaceTabs(), 'git');
  state = focusChatTab(state);
  const tabsBefore = state.tabs.length;

  state = focusAstChildView(state, 'task-1234');
  assert.deepEqual(state.tabs, ['git'], '临时视图不进 tabs（标签栏原样）');
  assert.equal(state.tabs.length, tabsBefore, '打开 astchild 视图不改变标签栏长度');
  assert.equal(state.active, 'astchild:task-1234');

  state = focusAstChildView(state, 'task-1234');
  assert.equal(state.active, 'astchild:task-1234', '重复打开回到同一个视图');

  const empty = createWorkspaceTabs();
  assert.equal(focusAstChildView(empty, ''), empty, '空 id no-op');
});

test('A③ focusHistoryView：astchild 条目恢复（popstate/lastProjectView 不静默 no-op）', () => {
  const opened = focusAstChildView(createWorkspaceTabs(), 'task-1234');
  // 离开（去 chat）→ 模拟「切回项目模式恢复 lastProjectView」/「popstate 回退」
  const left = focusChatTab(opened);
  assert.equal(left.active, 'chat');
  const restored = focusHistoryView(left, 'astchild:task-1234');
  assert.equal(restored.active, 'astchild:task-1234', '恢复到 astchild 临时视图（不再回落 child 分支 no-op）');
  // focusChatTab/focusWorkspacePage 分支不受影响
  assert.equal(focusHistoryView(left, 'chat').active, 'chat');
  assert.equal(focusHistoryView(left, 'git').active, 'git');
});
