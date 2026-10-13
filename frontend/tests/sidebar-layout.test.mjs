// sidebar-layout.test.mjs —— 侧栏布局策略钉子（2026-12 体验修复）：
//  旧布局 .sidebar 整列 overflow-y:auto——项目/会话一多，底部设置入口被挤出
//  视口。新布局：侧栏 = 固定区（导航/搜索/设置入口，flex:none）+ 两个独立
//  滚动区（proj-scroll / sess-scroll），设置入口永远钉在底部。
//
// 断言两层：
//  ① 结构（react-dom/server 静态渲染，与 sidebar-busy.test.mjs 同一模式）：
//     两个滚动区真实存在、设置入口不在任何未闭合的滚动容器里、会话列表不再
//     截断前 8 条（scroll 区接管长列表——旧的「Show more」是死 UI，已删）。
//  ② 样式（源码文本断言——CSS 无法在无 DOM 环境渲染断言，源码钉子是这里
//     唯一可行的接线断言）：.sidebar overflow:hidden；两个滚动区 overflow-y:auto
//     + min-height:0；固定区 flex:none；proj-name / session 标题 ellipsis。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

globalThis.window = { __LX__: undefined };

const { Sidebar } = await import('../src/components/sidebar/Sidebar.tsx');
const { UpdateProvider } = await import('../src/shared/update.tsx');

const meta = (id, extra = {}) => ({
  id, title: `会话 ${id}`, updatedAt: '12:00', messages: 1, archived: false, ...extra,
});

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

const renderSidebar = (sessions, projects) => renderToStaticMarkup(
  createElement(UpdateProvider, null, createElement(Sidebar, {
    source: fakeSource(sessions, projects),
    currentId: '',
    busyBySession: {},
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

// ---------- ① 结构 ----------

test('Sidebar：项目区与会话区各自包在独立滚动容器里（proj-scroll / sess-scroll）', () => {
  const html = renderSidebar(
    [meta('a'), meta('b')],
    [{ id: 'p1', name: '项目一', path: '/p1' }],
  );
  assert.ok(html.includes('class="proj-scroll"'), html);
  assert.ok(html.includes('class="sess-scroll"'), html);
  // 「项目」与「对话」分组标题行在滚动容器外（恒可见）
  const projLabel = html.indexOf('project-head');
  const projScroll = html.indexOf('class="proj-scroll"');
  const sessLabel = html.indexOf('group-head');
  const sessScroll = html.indexOf('class="sess-scroll"');
  assert.ok(projLabel > -1 && projScroll > projLabel, '项目标题行必须在 proj-scroll 之前（容器外）');
  assert.ok(sessLabel > -1 && sessScroll > sessLabel, '对话标题行必须在 sess-scroll 之前（容器外）');
});

test('Sidebar：设置入口不在滚动容器内——footer 之前的所有 div 都已闭合', () => {
  const projects = Array.from({ length: 12 }, (_, i) => ({ id: `p${i}`, name: `项目${i}`, path: `/p${i}` }));
  const sessions = Array.from({ length: 30 }, (_, i) => meta(`s${i}`, { workspace: 'p0' }));
  const html = renderSidebar(sessions, projects);
  const footerAt = html.indexOf('<div class="sidebar-footer"');
  assert.ok(footerAt > 0, '必须有 sidebar-footer');
  const prefix = html.slice(0, footerAt);
  const opens = (prefix.match(/<div/g) ?? []).length;
  const closes = (prefix.match(/<\/div>/g) ?? []).length;
  assert.equal(opens, closes,
    `footer 之前 div 必须全部闭合（设置入口不在滚动容器内）：<div=${opens} </div=${closes}`);
  // footer 在两个滚动区之后（布局顺序：列表区 → 固定底部）
  assert.ok(html.lastIndexOf('sess-scroll') < footerAt, '设置入口必须在会话滚动区之后（钉底）');
});

test('Sidebar：会话列表不再截断前 8 条（滚动区接管；死 UI「Show more」已删）', () => {
  const sessions = Array.from({ length: 12 }, (_, i) => meta(`s${i}`));
  const html = renderSidebar(sessions, []);
  const rows = (html.match(/data-sid=/g) ?? []).length;
  assert.equal(rows, 12, `12 条会话必须全部渲染（实际 ${rows}）`);
  assert.ok(!html.includes('ws-more'), '不再有截断提示（列表滚动代替）');
  assert.ok(!html.includes('Show more'), html);
});

test('Sidebar：长标题行有原生 title 提示 + 标题文本在 ellipsis 类元素里', () => {
  const longName = '特别特别长的项目名称'.repeat(8);
  const longTitle = '一段长得会被单行截断的会话标题'.repeat(8);
  const html = renderSidebar(
    [meta('s1', { title: longTitle })],
    [{ id: 'p1', name: longName, path: 'C:\\长\\路径\\项目' }],
  );
  // 项目行：title 属性带路径（截断后悬停可见），名字在 .proj-name 里
  const p1 = html.split('proj-row').find((seg) => seg.includes(longName)) ?? '';
  assert.ok(p1.includes('title='), '项目行必须有 title 提示');
  assert.ok(p1.includes('class="proj-name"'), '项目名必须在 proj-name（ellipsis 类）里');
  // 会话行：行级 title 提示 + 标题在 .title 里（.session-item .title 有 ellipsis）
  const row = html.split('session-item').find((seg) => seg.includes(longTitle)) ?? '';
  assert.ok(row.includes('title='), '会话行必须有 title 提示');
  assert.ok(row.includes('class="title"'), '会话标题必须在 .title（ellipsis 类）里');
});

// ---------- ② 样式（源码钉子） ----------

const sidebarCss = readFileSync('src/styles/sidebar.css', 'utf8');
const composerCss = readFileSync('src/styles/composer.css', 'utf8');
const threadCss = readFileSync('src/styles/thread.css', 'utf8');

/** 取选择器的规则体（到下一个顶层 `}` 为止）。 */
function rule(css, selector) {
  const at = css.indexOf(selector);
  if (at < 0) return null;
  const start = css.indexOf('{', at);
  const end = css.indexOf('}', start);
  return css.slice(start + 1, end);
}

test('CSS：.sidebar 整列不滚动（overflow:hidden），滚动职责下沉到两个列表区', () => {
  const s = rule(sidebarCss, '.sidebar {');
  assert.ok(s, 'sidebar.css 必须有 .sidebar 规则');
  assert.ok(s.includes('overflow: hidden'), s);
  assert.ok(!s.includes('overflow-y: auto'), '侧栏本身不能再是滚动容器: ' + s);
  const proj = rule(sidebarCss, '.proj-scroll {');
  const sess = rule(sidebarCss, '.sess-scroll {');
  assert.ok(proj?.includes('overflow-y: auto') && proj.includes('min-height: 0'), proj ?? '');
  assert.ok(sess?.includes('overflow-y: auto') && sess.includes('min-height: 0'), sess ?? '');
});

test('CSS：固定区（导航/搜索/设置入口）flex:none——不在滚动容器内也不被压缩', () => {
  const fixed = rule(sidebarCss, '.nav-list,\n.sidebar-pad,\n.sidebar-footer {');
  assert.ok(fixed?.includes('flex: none'), '三个固定区必须一起 flex:none: ' + (fixed ?? '缺失'));
  const footer = rule(threadCss, '.sidebar-footer {');
  assert.ok(footer?.includes('margin-top: auto'), '设置入口钉底语义保留: ' + (footer ?? ''));
});

test('CSS：长标题 ellipsis——项目名与会话标题都是单行截断 + title 由行级补', () => {
  const projName = rule(composerCss, '.proj-name {');
  assert.ok(projName?.includes('white-space: nowrap'), projName ?? '');
  assert.ok(projName?.includes('overflow: hidden'), projName ?? '');
  assert.ok(projName?.includes('text-overflow: ellipsis'), projName ?? '');
  const title = rule(sidebarCss, '.session-item .title {');
  assert.ok(title?.includes('white-space: nowrap'), title ?? '');
  assert.ok(title?.includes('text-overflow: ellipsis'), title ?? '');
});
