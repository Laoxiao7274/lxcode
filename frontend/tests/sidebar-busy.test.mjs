// 侧栏「运行中」可见性（体验修复批次 4）：
//  ① 会话行 busy 加强态（SessionRow）——加强 class + 标题旁 spinner + aria「正在运行」；
//     busy 结束不残留。
//  ② 项目行 / 未分组行角标（Sidebar + ProjectRunBadge）——该项目下有 busy 会话时
//     渲染 spinner（>1 带 ×N），无 busy 不渲染。
//  ③ busy 汇总 selector（shared/busy-summary.ts）的口径：只算顶层会话（子会话
//     busy 不上 wire，无法可靠归属——shared/busy-summary.ts 头注）。
//
// 渲染断言用 react-dom/server 静态渲染（测试环境没有 DOM；useEffect 不执行，
// 渲染路径只读 props——与 context-indicator.test.mjs 同一模式）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { busyCountsByWorkspace } from '../src/shared/busy-summary.ts';

// Sidebar 的依赖链里有模块顶层读 window.__LX__（AddProjectDialog 的壳桥）——
// node 没有 window，先给最小桩再加载组件（只影响本测试文件的加载顺序）。
globalThis.window = { __LX__: undefined };

const { SessionRow } = await import('../src/components/sidebar/SessionRow.tsx');
const { Sidebar, ProjectRunBadge } = await import('../src/components/sidebar/Sidebar.tsx');
const { UpdateProvider } = await import('../src/shared/update.tsx');

const meta = (id, extra = {}) => ({
  id, title: `会话 ${id}`, updatedAt: '12:00', messages: 1, archived: false, ...extra,
});

// ---------- ① busy 汇总 selector（纯函数口径） ----------

test('busyCountsByWorkspace：busy 会话按 workspace 聚合，未分组归 "" 键', () => {
  const sessions = [
    meta('a', { workspace: 'p1' }),
    meta('b', { workspace: 'p1' }),
    meta('c', { workspace: 'p2' }),
    meta('d'), // 未分组（无 workspace）
  ];
  // c 不 busy → p2 键不出现（不编 0）
  const counts = busyCountsByWorkspace(sessions, { a: true, b: true, d: true });
  assert.deepEqual(counts, { p1: 2, '': 1 });
});

test('busyCountsByWorkspace：无 busy 会话返回空对象（不编 0 键）', () => {
  const sessions = [meta('a', { workspace: 'p1' }), meta('b')];
  assert.deepEqual(busyCountsByWorkspace(sessions, {}), {});
  assert.deepEqual(busyCountsByWorkspace(sessions, { a: false, b: false }), {});
});

test('busyCountsByWorkspace：workspace 缺省与会话未列出都安全（不猜）', () => {
  // busy id 不在会话列表里（子会话 id——不上 wire 的那种）→ 无处归属，不计
  const counts = busyCountsByWorkspace([meta('a')], { a: true, ghost: true });
  assert.deepEqual(counts, { '': 1 });
});

// ---------- ② 会话行 busy 加强态（SessionRow） ----------

const rowProps = (busy) => ({
  session: meta('s1', { workspace: 'p1' }),
  current: false,
  busy,
  renaming: false,
  menuOpen: false,
  onOpenMenu: () => {},
  onCloseMenu: () => {},
  onStartRename: () => {},
  onRename: () => {},
  onArchive: async () => ({}),
  onReleaseWorktree: async () => {},
  onResume: () => {},
  enterRow: () => {},
});

test('SessionRow：busy 时渲染加强 class + 标题旁 spinner + aria「正在运行」', () => {
  const html = renderToStaticMarkup(createElement(SessionRow, rowProps(true)));
  assert.ok(html.includes('session-item busy'), '行上必须有 busy class: ' + html);
  assert.ok(html.includes('session-spinner'), '标题旁必须有 mset-spinner: ' + html);
  assert.ok(html.includes('（正在运行）'), 'aria-label 必须说明正在运行: ' + html);
  assert.ok(html.includes('s-dot live'), '运行蓝点仍在: ' + html);
});

test('SessionRow：busy 结束恢复普通态（不残留 spinner / busy class / aria）', () => {
  const html = renderToStaticMarkup(createElement(SessionRow, rowProps(false)));
  assert.ok(!html.includes('session-item busy'), html);
  assert.ok(!html.includes('session-spinner'), html);
  assert.ok(!html.includes('正在运行'), html);
});

// ---------- ③ 项目行 / 未分组行角标 ----------

test('ProjectRunBadge：count<=0 不渲染；>1 带 ×N；title/aria 说明 N 个会话正在运行', () => {
  assert.equal(renderToStaticMarkup(createElement(ProjectRunBadge, { count: 0 })), '');
  assert.equal(renderToStaticMarkup(createElement(ProjectRunBadge, { count: -1 })), '');
  const one = renderToStaticMarkup(createElement(ProjectRunBadge, { count: 1 }));
  assert.ok(one.includes('proj-run'), one);
  assert.ok(one.includes('proj-run-spinner'), one);
  assert.ok(one.includes('1 个会话正在运行'), one);
  assert.ok(!one.includes('×'), '单个不该带 ×N: ' + one);
  const two = renderToStaticMarkup(createElement(ProjectRunBadge, { count: 3 }));
  assert.ok(two.includes('×3'), two);
});

// 整个 Sidebar 的接线断言：busy 汇总真的喂到了项目行（不是只有组件本身正确）。
function fakeSource(sessions, projects) {
  return {
    sessions: () => sessions,
    projects: () => projects,
    subscribe: () => () => {},
    newSession: () => {},
    renameSession: () => {},
    archiveSession: () => {},
    releaseWorktree: () => {},
    resumeSession: () => {},
  };
}

const renderSidebar = (sessions, projects, busyBySession) => renderToStaticMarkup(
  createElement(UpdateProvider, null, createElement(Sidebar, {
    source: fakeSource(sessions, projects),
    currentId: '',
    busyBySession,
    filter: '',
    setFilter: () => {},
    onOpenSettings: () => {},
    agentsActive: false,
    onOpenAgents: () => {},
    onOpenChat: () => {},
    catalogActive: false,
    onOpenCatalog: () => {},
    gitActive: false,
    onOpenGit: () => {},
    remoteActive: false,
    onOpenRemote: () => {},
  })),
);

test('Sidebar：项目下有 busy 会话 → 项目行渲染角标（×N）；无 busy 的项目不渲染', () => {
  const projects = [
    { id: 'p1', name: '项目一', path: '/p1' },
    { id: 'p2', name: '项目二', path: '/p2' },
  ];
  const sessions = [
    meta('a', { workspace: 'p1' }),
    meta('b', { workspace: 'p1' }),
    meta('c', { workspace: 'p2' }),
  ];
  const html = renderSidebar(sessions, projects, { a: true, b: true });
  // p1：两个 busy → 角标带 ×2
  const p1Row = html.split('proj-row').find((seg) => seg.includes('项目一')) ?? '';
  assert.ok(p1Row.includes('proj-run'), 'p1 行必须有运行角标: ' + p1Row);
  assert.ok(p1Row.includes('×2'), 'p1 角标必须带 ×2: ' + p1Row);
  assert.ok(p1Row.includes('2 个会话正在运行'), p1Row);
  // p2：无 busy → 无角标
  const p2Row = html.split('proj-row').find((seg) => seg.includes('项目二')) ?? '';
  assert.ok(!p2Row.includes('proj-run'), 'p2 行不该有角标: ' + p2Row);
  // hover title 补充
  assert.ok(p1Row.includes('（2 个会话正在运行）'), p1Row);
});

test('Sidebar：未分组行的角标同口径（busy 未分组会话计入 "" 键）', () => {
  const sessions = [meta('a'), meta('b')];
  const html = renderSidebar(sessions, [], { b: true });
  const looseRow = html.split('proj-row').find((seg) => seg.includes('未分组')) ?? '';
  assert.ok(looseRow.includes('proj-run'), '未分组行必须有角标: ' + looseRow);
  assert.ok(!looseRow.includes('×'), '单个 busy 不带 ×N: ' + looseRow);
});

test('Sidebar：全无 busy → 任何项目行/未分组行都没有角标', () => {
  const projects = [{ id: 'p1', name: '项目一', path: '/p1' }];
  const sessions = [meta('a', { workspace: 'p1' }), meta('b')];
  const html = renderSidebar(sessions, projects, {});
  assert.ok(!html.includes('proj-run'), html);
});
