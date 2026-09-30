// 子会话标签：把子 Agent 的**独立会话**（AGENTS.md §2.3）当工作区标签打开的状态模型钉子。
//
// 为什么单独一个文件：标签模型是**字符串键**（child:<sessionId>）而不是对象，靠的就是
// includes / filter / === 这些按值比较——去重、关闭回退、两个子会话互不干扰全靠它。
// 去重一旦丢掉，表现是「重复打开同一个子会话长出两个一模一样的标签，关掉一个还剩一个」
// （用户会以为关不掉），而界面上看起来一切正常。所以这里逐条钉住。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  childTabKey,
  childTabSession,
  childTabTitle,
  closeWorkspacePage,
  createWorkspaceTabs,
  focusChatTab,
  focusChildTab,
  focusWorkspacePage,
} from '../src/shared/workspace-tabs.ts';

/** 主时间线里那张 dispatch 块（回放形态：agentName 常是空串，展示名要查注册表）。 */
const dispatchBlock = (overrides = {}) => ({
  kind: 'dispatch', uid: 1, id: 'c1',
  sessionId: 'sess-1234567890', agentId: 'researcher', agentName: 'researcher',
  agentColor: '#888', task: '通读 internal/agent', status: 'done', result: '读完', subBlocks: [],
  ...overrides,
});

// ---------- ① 键的往返与边界 ----------

test('① childTabKey/childTabSession 往返一致，固定页签键不被误认成子会话键', () => {
  assert.equal(childTabKey('sess-1234567890'), 'child:sess-1234567890');
  assert.equal(childTabSession(childTabKey('sess-1234567890')), 'sess-1234567890');
  // id 里带冒号也照样往返（id 是后端给的，别假设它的字符集）
  assert.equal(childTabSession(childTabKey('a:b:c')), 'a:b:c');
  // 固定页签与 chat 都不是子会话键——否则标签栏会把「Agent」页签当成子会话去读历史
  for (const fixed of ['agents', 'catalog', 'git', 'chat']) {
    assert.equal(childTabSession(fixed), null, fixed + ' 不该被当成子会话键');
  }
  // 前缀必须整段匹配：childish: 不是 child:
  assert.equal(childTabSession('childish:x'), null);
  // 空 id 的坏键 → null（不是空串）：否则标签标题会回落成「子会话 未知」的空白标签
  assert.equal(childTabSession('child:'), null);
});

// ---------- ② 打开 + 去重（本文件的核心钉子） ----------

test('② focusChildTab 打开并聚焦；重复打开同一个子会话不产生第二个标签（去重）', () => {
  let state = focusChildTab(createWorkspaceTabs(), 'sess-1234567890');
  assert.deepEqual(state.tabs, ['child:sess-1234567890']);
  assert.equal(state.active, 'child:sess-1234567890', '打开即聚焦');
  assert.deepEqual(state.history, ['chat'], '从聊天过来：回退目标是聊天');

  // 同一张卡上再点一次「打开子会话」→ 回到同一个标签（不是并排两个）
  state = focusChildTab(state, 'sess-1234567890');
  assert.deepEqual(state.tabs, ['child:sess-1234567890'], '重复打开必须去重');
  assert.equal(state.active, 'child:sess-1234567890');

  // 切回聊天再打开同一个子会话：仍然只有那一个标签
  state = focusChatTab(state);
  state = focusChildTab(state, 'sess-1234567890');
  assert.deepEqual(state.tabs, ['child:sess-1234567890']);
  assert.equal(state.active, 'child:sess-1234567890');

  // 空 id 不开标签（no-op，返回原对象）
  const empty = createWorkspaceTabs();
  assert.equal(focusChildTab(empty, ''), empty);
});

// ---------- ③ 关闭：既有语义（关当前页回退最近打开的页） ----------

test('③ 关闭子会话标签：关当前页回退到最近仍打开的页；关非当前页不影响 active', () => {
  // 关**当前**子会话标签 → 回退到最近打开的固定页签
  let state = focusWorkspacePage(createWorkspaceTabs(), 'agents');
  state = focusChildTab(state, 'sess-1234567890');
  state = closeWorkspacePage(state, childTabKey('sess-1234567890'));
  assert.deepEqual(state.tabs, ['agents']);
  assert.equal(state.active, 'agents', '关掉当前页要回退，不能停在已关闭的页上');

  // 关**非当前**子会话标签 → 焦点一动不动，只从 tabs 摘掉
  let other = focusChildTab(createWorkspaceTabs(), 'sess-aaaaaaaa');
  other = focusChildTab(other, 'sess-bbbbbbbb');
  other = closeWorkspacePage(other, childTabKey('sess-aaaaaaaa'));
  assert.deepEqual(other.tabs, ['child:sess-bbbbbbbb']);
  assert.equal(other.active, 'child:sess-bbbbbbbb', '关非当前页不许改 active');

  // 关掉最后一个标签且没有别的可回退 → 回聊天
  let last = focusChildTab(createWorkspaceTabs(), 'sess-1234567890');
  last = closeWorkspacePage(last, childTabKey('sess-1234567890'));
  assert.deepEqual(last, { tabs: [], active: 'chat', history: [] });

  // 关一个没开过的子会话标签是 no-op（返回原对象）
  const none = createWorkspaceTabs();
  assert.equal(closeWorkspacePage(none, childTabKey('sess-missing')), none);
});

// ---------- ④ 两个子会话各自一个标签，互不干扰 ----------

test('④ 两个不同子会话各自一个标签，互不干扰', () => {
  let state = focusChildTab(createWorkspaceTabs(), 'sess-aaaaaaaa');
  state = focusChildTab(state, 'sess-bbbbbbbb');
  assert.deepEqual(state.tabs, ['child:sess-aaaaaaaa', 'child:sess-bbbbbbbb'], '按打开顺序排列');
  assert.equal(state.active, 'child:sess-bbbbbbbb');

  // 聚焦第一个不重排（浏览器语义：点标签只切焦点）
  state = focusChildTab(state, 'sess-aaaaaaaa');
  assert.deepEqual(state.tabs, ['child:sess-aaaaaaaa', 'child:sess-bbbbbbbb']);
  assert.equal(state.active, 'child:sess-aaaaaaaa');

  // 关掉其中一个，另一个原样还在（两个键互不相等，不会误伤）
  assert.notEqual(childTabKey('sess-aaaaaaaa'), childTabKey('sess-bbbbbbbb'));
  const closed = closeWorkspacePage(state, childTabKey('sess-aaaaaaaa'));
  assert.deepEqual(closed.tabs, ['child:sess-bbbbbbbb']);
  assert.equal(closed.active, 'child:sess-bbbbbbbb', '关当前页 → 回退到另一个子会话标签');

  // 子会话标签与固定页签共用同一套打开顺序
  let mixed = focusWorkspacePage(createWorkspaceTabs(), 'git');
  mixed = focusChildTab(mixed, 'sess-aaaaaaaa');
  assert.deepEqual(mixed.tabs, ['git', 'child:sess-aaaaaaaa']);
  assert.equal(mixed.active, 'child:sess-aaaaaaaa');
});

// ---------- ⑤ 标题：Agent 名 + 任务摘要；找不到就回落，不许空白 ----------

test('⑤ 标签标题取主时间线里那张 dispatch 块（Agent 名 + 任务摘要）', () => {
  const blocks = [dispatchBlock()];
  assert.equal(childTabTitle(blocks, 'sess-1234567890'), 'researcher · 通读 internal/agent');
  // 回放块的 agentName 是空串（展示名是注册表的知识）→ 按 agentId 回落，再不行问 nameOf
  const replay = [dispatchBlock({ agentName: '' })];
  assert.equal(childTabTitle(replay, 'sess-1234567890'), 'researcher · 通读 internal/agent');
  assert.equal(childTabTitle(replay, 'sess-1234567890', () => '调研员'), '调研员 · 通读 internal/agent');
  // 命中要**按 sessionId**：同一张时间线里别的子会话的卡不能顶替
  const other = [dispatchBlock({ sessionId: 'sess-other', agentName: 'coder', task: '改代码' })];
  assert.equal(childTabTitle(other, 'sess-1234567890'), '子会话 sess-123', 'sessionId 不命中就是找不到');
  // 非 dispatch 块（工具行）不算数
  assert.equal(childTabTitle([{ kind: 'tool', uid: 2, id: 't1', name: 'read_file', arguments: '{}' }], 'sess-1234567890'), '子会话 sess-123');
});

test('⑤ 找不到对应 dispatch 块时回落成「子会话 <id 前 8 位>」，不空白', () => {
  const fallback = '子会话 sess-123';
  // 主时间线是空的（卡还没落进当前时间线 / 会话被撤回）
  assert.equal(childTabTitle([], 'sess-1234567890'), fallback);
  // 空 id 这种坏键也不能给出空标题
  assert.equal(childTabTitle([], ''), '子会话 未知');
  // 块在、但名字与任务都是空的（坏历史）→ 仍然回落，不是空串
  assert.equal(childTabTitle([dispatchBlock({ agentName: '', agentId: '', task: '' })], 'sess-1234567890'), fallback);
  // 任何一条路径都不许返回空白标题（标签栏上一个认不出的色块）
  for (const title of [
    childTabTitle([], 'sess-1234567890'),
    childTabTitle([dispatchBlock({ agentName: '', agentId: '', task: '' })], 'sess-1234567890'),
    childTabTitle([dispatchBlock()], 'sess-1234567890'),
  ]) {
    assert.ok(title.trim().length > 0, '标题不许空白');
  }
});

test('⑤ 任务摘要折成单行并截断（换行/超长不许把标签撑坏）', () => {
  const multiline = [dispatchBlock({ task: '第一行\n\n第二行' })];
  assert.equal(childTabTitle(multiline, 'sess-1234567890'), 'researcher · 第一行 第二行');
  const long = childTabTitle([dispatchBlock({ task: 'x'.repeat(60) })], 'sess-1234567890');
  assert.ok(long.endsWith('…'), '超长要截断（标签栏放不下整段任务）');
  assert.ok(long.length < 60, '截断后要比原文短');
});
