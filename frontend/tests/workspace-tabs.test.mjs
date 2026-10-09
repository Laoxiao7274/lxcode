import test from 'node:test';
import assert from 'node:assert/strict';
import {
  childTabHint,
  closeWorkspacePage,
  createWorkspaceTabs,
  focusChatTab,
  focusWorkspacePage,
  splitWorkspaceTabs,
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

// ---------- 2026-10-09：标签条分流（子会话标签迁到会话标签条） ----------

// TabBar 按 splitWorkspaceTabs 的两个桶决定渲染位置：pages 进左 strip（聊天 + 页面页签），
// children 进右 strip（主会话标签之后）。这条测试钉住「子会话标签出现在会话标签条」的
// 判定源头——桶分错了，TabBar 渲染位置跟着错。
test('splitWorkspaceTabs 把子会话标签分进 children、固定页签留在 pages（顺序保持）', () => {
  const { pages, children } = splitWorkspaceTabs([
    'agents',
    'child:abc123',
    'git',
    'child:def456',
  ]);

  assert.deepEqual(pages, ['agents', 'git'], '固定页签留在工作区标签条，相对顺序不变');
  assert.deepEqual(children, ['child:abc123', 'child:def456'], '子会话标签归会话标签条，相对顺序不变');
});

test('splitWorkspaceTabs 对空表/纯页面/纯子会话都不炸', () => {
  assert.deepEqual(splitWorkspaceTabs([]), { pages: [], children: [] });
  assert.deepEqual(splitWorkspaceTabs(['catalog']), { pages: ['catalog'], children: [] });
  assert.deepEqual(splitWorkspaceTabs(['child:x1']), { pages: [], children: ['child:x1'] });
});

// 子会话标签的 hover 提示：项目名 · 主会话「标题」 · 子标签名；未分组少一节。
test('childTabHint 组出「项目 · 主会话 · 子标签」三层定位，缺项目就少一节', () => {
  assert.equal(
    childTabHint('lxcode', '修标签页布局', 'researcher · 通读 internal/agent'),
    'lxcode · 主会话「修标签页布局」 · researcher · 通读 internal/agent',
  );
  assert.equal(
    childTabHint(undefined, '未分组会话', 'writer · 起草公告'),
    '主会话「未分组会话」 · writer · 起草公告',
    '未分组会话没有项目名，不许伪装成有归属（不许出现「未分组 ·」）',
  );
  // 占位不许空白：空节在提示里就是一处看不懂的「· ·」
  assert.equal(childTabHint('p', '', ''), 'p · 主会话「未命名」 · 子会话');
});
