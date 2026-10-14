// 子会话**临时视图**：把子 Agent 的独立会话（AGENTS.md §2.3）当临时视图打开的状态模型钉子。
//
// 2026-12 用户拍板：标签栏不再出现子会话——点主时间线的派发卡打开的是**临时视图**
// （focusChildView 只切 active、**不进 tabs**），页头「返回主会话」关闭它，返回后视图
// 消失。这里逐条钉住：
// ① 打开不改变 tabs（标签栏长度不变、不许长出子会话标签）；
// ② 重复打开/多张卡来回点不产生重复视图（active 指向同一个视图键）；
// ③ 返回（focusChatTab）回到原视图；再次点同一张卡视图还在、标签栏长度仍不变
//    （store 层数据不清理——重新打开由 ChildSessionPage 重新装载，见 child-history.test.mjs）。
// 这条纪律一旦丢掉，表现就是「点卡长出一个子会话标签」或「连点长出重复视图」。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  childTabKey,
  childTabSession,
  childTabTitle,
  closeWorkspacePage,
  createWorkspaceTabs,
  focusChatTab,
  focusChildView,
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
  for (const fixed of ['agents', 'catalog', 'catalog', 'chat']) {
    assert.equal(childTabSession(fixed), null, fixed + ' 不该被当成子会话键');
  }
  // 前缀必须整段匹配：childish: 不是 child:
  assert.equal(childTabSession('childish:x'), null);
  // 空 id 的坏键 → null（不是空串）：否则视图标题会回落成「子会话 未知」的空页
  assert.equal(childTabSession('child:'), null);
});

// ---------- ② 打开临时视图：只切 active，不进 tabs ----------

test('② focusChildView 打开并聚焦临时视图；重复打开回到同一个视图（去重）', () => {
  let state = focusChildView(createWorkspaceTabs(), 'sess-1234567890');
  assert.deepEqual(state.tabs, [], '临时视图不进 tabs——标签栏不许出现子会话');
  assert.equal(state.active, 'child:sess-1234567890', '打开即聚焦');
  assert.deepEqual(state.history, ['chat'], '从聊天过来：返回目标是聊天');

  // 同一张卡上再点一次 → 回到同一个视图（不是长出第二个）
  state = focusChildView(state, 'sess-1234567890');
  assert.deepEqual(state.tabs, [], '重复打开也不进 tabs');
  assert.equal(state.active, 'child:sess-1234567890');

  // 返回聊天再点同一张卡：仍然只有那一个视图
  state = focusChatTab(state);
  state = focusChildView(state, 'sess-1234567890');
  assert.deepEqual(state.tabs, []);
  assert.equal(state.active, 'child:sess-1234567890');

  // 空 id 不开视图（no-op，返回原对象）
  const empty = createWorkspaceTabs();
  assert.equal(focusChildView(empty, ''), empty);
});

// ---------- ③ 钉子：点派发卡 → 视图打开且标签栏长度不变；返回；再点数据仍在 ----------

test('③ 点派发卡：视图打开、标签栏长度不变；返回后回到原会话；再点同一张卡视图还在', () => {
  // 有一个工作区标签 + 聊天视图的初始态（标签栏非空才量得出「长度不变」）
  let state = focusWorkspacePage(createWorkspaceTabs(), 'catalog');
  state = focusChatTab(state);
  const tabsBefore = state.tabs.length;
  assert.ok(tabsBefore > 0);

  // 点派发卡 → 子会话视图打开，标签栏长度与内容都不变
  state = focusChildView(state, 'sess-1234567890');
  assert.equal(state.tabs.length, tabsBefore, '打开子会话视图不许改变标签栏长度');
  assert.equal(state.tabs.includes(childTabKey('sess-1234567890')), false, '标签栏不许出现子会话');
  assert.equal(state.active, childTabKey('sess-1234567890'), '视图立即覆盖当前会话区');

  // 返回（页头「返回主会话」→ focusChatTab）→ 回到原会话视图，标签栏原样
  state = focusChatTab(state);
  assert.equal(state.active, 'chat', '返回后回到原会话');
  assert.equal(state.tabs.length, tabsBefore);

  // 再次点同一张卡 → 同一个视图键（store 里的子会话 state 不受导航影响，
  // 数据仍在——重新打开只是重新装载，不重置）
  state = focusChildView(state, 'sess-1234567890');
  assert.equal(state.active, childTabKey('sess-1234567890'));
  assert.equal(state.tabs.length, tabsBefore, '再次打开也不改变标签栏');
});

// ---------- ④ 多张卡来回点：不产生重复视图，标签栏不动 ----------

test('④ 两个不同子会话来回点：各自聚焦互不干扰，也不长出标签', () => {
  let state = focusChildView(createWorkspaceTabs(), 'sess-aaaaaaaa');
  state = focusChildView(state, 'sess-bbbbbbbb');
  assert.deepEqual(state.tabs, [], '来回点也不进 tabs');
  assert.equal(state.active, childTabKey('sess-bbbbbbbb'));

  // 点回第一张卡：焦点切过去，依旧没有标签
  state = focusChildView(state, 'sess-aaaaaaaa');
  assert.equal(state.active, childTabKey('sess-aaaaaaaa'));
  assert.deepEqual(state.tabs, []);

  // 两个键互不相等（不会误伤）
  assert.notEqual(childTabKey('sess-aaaaaaaa'), childTabKey('sess-bbbbbbbb'));

  // 临时视图与固定页签互不干扰：固定页签照常进 tabs
  let mixed = focusWorkspacePage(createWorkspaceTabs(), 'catalog');
  mixed = focusChildView(mixed, 'sess-aaaaaaaa');
  assert.deepEqual(mixed.tabs, ['catalog'], '打开子会话视图不动既有工作区标签');
  assert.equal(mixed.active, childTabKey('sess-aaaaaaaa'));
});

// ---------- ⑤ 临时视图不适用「关闭标签」语义 ----------

test('⑤ closeWorkspacePage 对子会话视图键是 no-op（返回走 focusChatTab，不是关标签）', () => {
  let state = focusChildView(createWorkspaceTabs(), 'sess-1234567890');
  assert.equal(closeWorkspacePage(state, childTabKey('sess-1234567890')), state,
    '临时视图不在 tabs 里，关闭标签语义对它不适用');
});

// ---------- ⑥ 标题：Agent 名 + 任务摘要；找不到就回落，不许空白 ----------

test('⑥ 标题取主时间线里那张 dispatch 块（Agent 名 + 任务摘要）', () => {
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

test('⑥ 找不到对应 dispatch 块时回落成「子会话 <id 前 8 位>」，不空白', () => {
  const fallback = '子会话 sess-123';
  // 主时间线是空的（卡还没落进当前时间线 / 会话被撤回）
  assert.equal(childTabTitle([], 'sess-1234567890'), fallback);
  // 空 id 这种坏键也不能给出空标题
  assert.equal(childTabTitle([], ''), '子会话 未知');
  // 块在、但名字与任务都是空的（坏历史）→ 仍然回落，不是空串
  assert.equal(childTabTitle([dispatchBlock({ agentName: '', agentId: '', task: '' })], 'sess-1234567890'), fallback);
  // 任何一条路径都不许返回空白标题（视图页头一个认不出的空位）
  for (const title of [
    childTabTitle([], 'sess-1234567890'),
    childTabTitle([dispatchBlock({ agentName: '', agentId: '', task: '' })], 'sess-1234567890'),
    childTabTitle([dispatchBlock()], 'sess-1234567890'),
  ]) {
    assert.ok(title.trim().length > 0, '标题不许空白');
  }
});

test('⑥ 任务摘要折成单行并截断（换行/超长不许把顶栏标题撑坏）', () => {
  const multiline = [dispatchBlock({ task: '第一行\n\n第二行' })];
  assert.equal(childTabTitle(multiline, 'sess-1234567890'), 'researcher · 第一行 第二行');
  const long = childTabTitle([dispatchBlock({ task: 'x'.repeat(60) })], 'sess-1234567890');
  assert.ok(long.endsWith('…'), '超长要截断（标题放不下整段任务）');
  assert.ok(long.length < 60, '截断后要比原文短');
});
