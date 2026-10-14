// 主会话标签的「视图态」钉子：子会话临时视图打开期间，TabBar 里**父会话**的标签要
// 明确反映「正在查看其子会话」（↳ 子标题 + 描边角标），点击 = 返回主会话。
//
// 2026-12 用户需求：临时视图打开时主标签原来是普通态——用户看不出当前正在看子会话、
// 看的是哪个子会话。规则抽在 workspace-tabs.ts（parentTabChildView / sessionTabAction），
// TabBar 只管渲染；组件渲染无法在 node 单测里跑，接线（类名/data 属性/点击分派）
// 用源码文本钉住——同 theme.test.mjs 的做法。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  childTabTitle,
  focusChatTab,
  focusChildView,
  focusWorkspacePage,
  createWorkspaceTabs,
  parentTabChildView,
  sessionTabAction,
} from '../src/shared/workspace-tabs.ts';

const here = dirname(fileURLToPath(import.meta.url));
const tabbarSrc = readFileSync(join(here, '../src/components/topbar/TabBar.tsx'), 'utf8');
const baseCss = readFileSync(join(here, '../src/styles/base.css'), 'utf8');
const appSrc = readFileSync(join(here, '../src/App.tsx'), 'utf8');

/** 主时间线里那张 dispatch 块（agentName 空 = 回放形态，展示名查注册表）。 */
const dispatchBlock = (overrides = {}) => ({
  kind: 'dispatch', uid: 1, id: 'c1',
  sessionId: 'sess-1234567890', agentId: 'researcher', agentName: '',
  agentColor: '#888', task: '通读 internal/agent 的压缩实现', status: 'done', result: '', subBlocks: [],
  ...overrides,
});
const childTitleOf = (id) => childTabTitle([dispatchBlock()], id, (agentId) => agentId === 'researcher' ? 'researcher' : '');

// ---------- ① 视图打开 → 父标签拿到子会话信息；关闭 → 恢复普通态 ----------

test('① 视图打开：parentTabChildView 给出子会话 id 与标题；返回聊天后为 null（标签恢复普通态）', () => {
  let ws = focusChildView(createWorkspaceTabs(), 'sess-1234567890');
  const view = parentTabChildView(ws.active, 'sess-1234567890', () => 'sess-1234567890', childTitleOf);
  assert.ok(view, '视图打开时必须给出呈现信息');
  assert.equal(view.sessionId, 'sess-1234567890');
  assert.equal(view.title, 'researcher · 通读 internal/agent 的压缩实现', '标题 = Agent 名 + 任务摘要（与视图页头同一份 childTabTitle）');

  // 关闭（返回主会话）→ null：标签退回普通态
  ws = focusChatTab(ws);
  assert.equal(parentTabChildView(ws.active, 'sess-1234567890', () => 'sess-1234567890', childTitleOf), null);
  // 非子会话视图（固定页签/聊天）也恒为 null
  const page = focusWorkspacePage(createWorkspaceTabs(), 'catalog');
  assert.equal(parentTabChildView(page.active, 'x', () => 'x', childTitleOf), null);
  assert.equal(parentTabChildView('chat', 'x', () => 'x', childTitleOf), null);
});

// ---------- ② 切换子会话：标签内容跟随（多个子会话来回切） ----------

test('② 切换子会话：sessionId/title 跟随当前视图键（来回切不残留上一个）', () => {
  const blocks = [
    dispatchBlock({ uid: 1, id: 'c1', sessionId: 'sess-aaaaaaaa01', task: '任务甲' }),
    dispatchBlock({ uid: 2, id: 'c2', sessionId: 'sess-bbbbbbbb02', task: '任务乙' }),
  ];
  const titleOf = (id) => childTabTitle(blocks, id, () => 'researcher');
  const parents = { 'sess-aaaaaaaa01': 'parent-1', 'sess-bbbbbbbb02': 'parent-1' };
  let ws = focusChildView(createWorkspaceTabs(), 'sess-aaaaaaaa01');
  let view = parentTabChildView(ws.active, 'parent-1', (id) => parents[id], titleOf);
  assert.equal(view.sessionId, 'sess-aaaaaaaa01');
  assert.equal(view.title, 'researcher · 任务甲');

  // 切到第二个子会话
  ws = focusChildView(ws, 'sess-bbbbbbbb02');
  view = parentTabChildView(ws.active, 'parent-1', (id) => parents[id], titleOf);
  assert.equal(view.sessionId, 'sess-bbbbbbbb02');
  assert.equal(view.title, 'researcher · 任务乙');

  // 切回来
  ws = focusChildView(ws, 'sess-aaaaaaaa01');
  view = parentTabChildView(ws.active, 'parent-1', (id) => parents[id], titleOf);
  assert.equal(view.title, 'researcher · 任务甲');
});

// ---------- ③ 防御：子会话不属于这个主会话时不显示（不许标签说别人家孩子） ----------

test('③ 映射指向别的父 → null；映射缺失（视图刚开的窗口期）→ 回落信任 currentId', () => {
  const ws = focusChildView(createWorkspaceTabs(), 'sess-1234567890');
  // 映射已建立且指向别的会话：不显示（正常链路走不到，防御浏览器 popstate 恢复条目）
  assert.equal(
    parentTabChildView(ws.active, 'other-parent', () => 'another-parent', childTitleOf),
    null,
  );
  // 映射缺失（父历史还没回放）：放行，回落 currentId
  const view = parentTabChildView(ws.active, 'sess-1234567890', () => undefined, childTitleOf);
  assert.ok(view);
  // currentId 为空（没有当前会话）不显示
  assert.equal(parentTabChildView(ws.active, '', () => 'p', childTitleOf), null);
});

// ---------- ④ 点击语义：视图态 = 返回主会话；激活 = no-op；其余 = 聚焦 ----------

test('④ sessionTabAction：viewing=back（关临时视图）、active=none、其余=focus', () => {
  assert.equal(sessionTabAction(true, false), 'back', '视图态点击 = 返回主会话（关闭临时视图）');
  assert.equal(sessionTabAction(false, true), 'none', '已激活标签点击 = no-op');
  assert.equal(sessionTabAction(false, false), 'focus', '其余 = 聚焦该会话');
});

// ---------- ⑤ TabBar 接线钉住（渲染层无法在 node 单测跑，用源码文本钉） ----------

test('⑤ TabBar/App/base.css：视图态类名、角标、data 标记、点击分派与样式规则都在', () => {
  // TabBar：viewing 只落在父会话（currentId）标签上
  assert.match(tabbarSrc, /const viewing = childView !== null && id === currentId/, 'viewing 判定必须锚在 currentId（父会话）标签上');
  // 类名与 data 标记（视图态样式钩子）
  assert.match(tabbarSrc, /viewing \? " viewing"/, '要有 .viewing 类');
  assert.match(tabbarSrc, /data-child-view=\{viewing \? "true" : undefined\}/, '要有 data-child-view 标记');
  // 角标内容：↳ + 子标题
  assert.match(tabbarSrc, /tab-child-crumb/, '要渲染 ↳ 角标');
  assert.match(tabbarSrc, /tab-child-title/, '要渲染子会话标题');
  // 点击分派走 sessionTabAction（back → onFocusChat = 关临时视图）
  assert.match(tabbarSrc, /sessionTabAction\(viewing, on\)/, '点击语义必须走 sessionTabAction（单一判定处）');
  assert.match(tabbarSrc, /action === "back"\) onFocusChat\(\)/, 'back 动作 = onFocusChat（返回主会话）');

  // App：childView 与顶栏标题同一份 childTabTitleOf，传给 TabBar
  assert.match(appSrc, /parentTabChildView\(view, currentId/, 'App 要用 parentTabChildView 算 childView');
  assert.match(appSrc, /childView=\{childView\}/, 'childView 要传给 TabBar');

  // base.css：视图态样式（区别于普通激活态）+ 截断省略
  assert.match(baseCss, /\.tab\.viewing \{/, '要有 .tab.viewing 规则');
  assert.match(baseCss, /\.tab-child-title \{[\s\S]*?text-overflow: ellipsis/, '子标题要截断省略（不撑破标签）');
});
