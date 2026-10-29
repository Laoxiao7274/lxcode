// project-entry.test.mjs —— 点击侧栏项目行切换会话的语义钉子（2026-12 体验修复）：
//
// 需求：点项目 → 过滤到该项目 + 当前标签页切到该项目**最上面的会话**（侧栏
// 第一条 = 后端 session.list 的 updated_at DESC——前端不重排，两处永远一致）。
//
// 判定抽在 shared/project-entry.ts（纯函数，不 import React）——不需要 DOM 就能
// 逐条钉住边界。接线（Sidebar 的项目行点击 → onOpenProject）用源码文本钉住——
// 组件渲染无法在 node 单测里跑交互（同 child-tab-view.test.mjs 的做法）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { projectEntryTarget, openProject, LOOSE } from '../src/shared/project-entry.ts';

const here = dirname(fileURLToPath(import.meta.url));
const sidebarSrc = readFileSync(join(here, '../src/components/sidebar/Sidebar.tsx'), 'utf8');
const navSrc = readFileSync(join(here, '../src/app-hooks/use-workspace-nav.ts'), 'utf8');
const appSrc = readFileSync(join(here, '../src/App.tsx'), 'utf8');

/** SessionMeta 造数（updatedAt 顺序即数组顺序——口径由后端排序保证，前端不重排）。 */
const s = (id, workspace) => ({ id, title: `会话 ${id}`, updatedAt: '12:00', messages: 1, workspace });

// ---------- ① projectEntryTarget：切到第一条 ----------

test('① 点项目 → 目标是该列表第一条会话（侧栏顶 = 最新）', () => {
  assert.equal(projectEntryTarget([s('a', 'p1'), s('b', 'p1')], 'zzz'), 'a');
});

test('①b 目标与过滤解耦：项目行点击不依赖 filter 当前值', () => {
  // 从另一个项目切过来：目标只由 list 决定（调用方已按 workspace 过滤）
  assert.equal(projectEntryTarget([s('a', 'p1')], 'x'), 'a');
});

// ---------- ② 边界：项目没有会话 → 不切换（空串） ----------

test('② 空项目 → 空串（不切换当前会话；过滤照常——侧栏如实显示空态）', () => {
  assert.equal(projectEntryTarget([], 'current'), '');
});

// ---------- ③ 边界：最上面就是当前会话 → null（无操作） ----------

test('③ 第一条就是当前会话 → null（不重触发 resume——它不是免费的）', () => {
  assert.equal(projectEntryTarget([s('a', 'p1'), s('b', 'p1')], 'a'), null);
});

// ---------- ④ openProject：副作用按回调注入，边界零副作用 ----------

test('④a 有目标：setFilter + resume + 回聊天，且只 resume 目标一条', () => {
  const calls = [];
  openProject({
    workspace: 'p1',
    list: [s('a', 'p1'), s('b', 'p1')],
    currentId: 'zzz',
    setFilter: (f) => calls.push(`filter:${f}`),
    resumeSession: (id) => calls.push(`resume:${id}`),
    onOpenChat: () => calls.push('chat'),
  });
  assert.deepEqual(calls, ['filter:p1', 'chat', 'resume:a']);
});

test('④b 空项目：过滤到空列表（如实显示空态）但**不切会话**（currentId 不动）', () => {
  const calls = [];
  openProject({
    workspace: 'p1',
    list: [],
    currentId: 'current',
    setFilter: (f) => calls.push(`filter:${f}`),
    resumeSession: (id) => calls.push(`resume:${id}`),
    onOpenChat: () => calls.push('chat'),
  });
  // 过滤照常（用户点项目就是要看这个项目的列表——空也如实给空态），
  // 但绝不能 resume 任何会话（没有可切的目标）
  assert.deepEqual(calls, ['filter:p1', 'chat']);
});

test('④c 目标就是当前会话：无操作（连 setFilter 都不动——幂等）', () => {
  const calls = [];
  openProject({
    workspace: 'p1',
    list: [s('a', 'p1')],
    currentId: 'a',
    setFilter: (f) => calls.push(`filter:${f}`),
    resumeSession: (id) => calls.push(`resume:${id}`),
    onOpenChat: () => calls.push('chat'),
  });
  assert.deepEqual(calls, []);
});

test('④d 连点/双击同一项目：第二次目标已是当前会话 → 零副作用（幂等）', () => {
  const calls = [];
  const run = () => openProject({
    workspace: 'p1',
    list: [s('a', 'p1'), s('b', 'p1')],
    currentId: calls.some((c) => c.startsWith('resume:')) ? 'a' : 'zzz',
    setFilter: (f) => calls.push(`filter:${f}`),
    resumeSession: (id) => calls.push(`resume:${id}`),
    onOpenChat: () => calls.push('chat'),
  });
  run(); // 第一次：切到 a
  run(); // 第二次：a 已是当前 → 无操作
  assert.deepEqual(calls, ['filter:p1', 'chat', 'resume:a']);
});

test('④e 快速连点两个不同项目：各自独立判定，第二次 resume 第二个项目的第一条', () => {
  const calls = [];
  openProject({
    workspace: 'p1', list: [s('a', 'p1'), s('b', 'p1')], currentId: 'zzz',
    setFilter: (f) => calls.push(`filter:${f}`),
    resumeSession: (id) => calls.push(`resume:${id}`),
    onOpenChat: () => calls.push('chat'),
  });
  openProject({
    workspace: 'p2', list: [s('c', 'p2')], currentId: 'a',
    setFilter: (f) => calls.push(`filter:${f}`),
    resumeSession: (id) => calls.push(`resume:${id}`),
    onOpenChat: () => calls.push('chat'),
  });
  assert.deepEqual(calls, ['filter:p1', 'chat', 'resume:a', 'filter:p2', 'chat', 'resume:c']);
});

// ---------- ⑤ 未分组：同一函数同一行为（用户拍板：恒有范围） ----------

test('⑤ 未分组（workspace=""）行为一致：切到未分组列表第一条；空未分组不切', () => {
  assert.equal(projectEntryTarget([s('u1', ''), s('u2', '')], 'zzz'), 'u1');
  assert.equal(projectEntryTarget([], 'zzz'), '');
  assert.equal(LOOSE, '');
  const calls = [];
  openProject({
    workspace: LOOSE, list: [s('u1', '')], currentId: 'zzz',
    setFilter: (f) => calls.push(`filter:${f}`),
    resumeSession: (id) => calls.push(`resume:${id}`),
    onOpenChat: () => calls.push('chat'),
  });
  assert.deepEqual(calls, ['filter:', 'chat', 'resume:u1']);
});

// ---------- ⑥ 接线钉子（源码文本）：项目行/未分组行走 onOpenProject ----------

test('⑥a Sidebar 项目行与未分组行的点击/Enter 都交给 onOpenProject（不再自己 setFilter）', () => {
  // 项目行：onClick / onKeyDown 都走 onOpenProject(p.id, projList)
  assert.match(sidebarSrc, /onClick=\{\(\) => \{ onOpenProject\(p\.id, projList\); \}\}/);
  assert.match(sidebarSrc, /onKeyDown=\{\(e\) => \{ if \(e\.key === "Enter"\) onOpenProject\(p\.id, projList\); \}\}/);
  // 未分组行：同一收口（LOOSE）
  assert.match(sidebarSrc, /onOpenProject\(LOOSE, loose\)/);
  // 旧行为（行内自己 setFilter + onOpenChat）必须已移除——否则两条路径并存，
  // 点项目偶尔只过滤不切会话
  assert.ok(!/if \(!active\) setFilter\(p\.id\)/.test(sidebarSrc), '项目行不得再行内 setFilter（走 onOpenProject）');
  assert.ok(!/if \(filter !== LOOSE\) setFilter\(LOOSE\)/.test(sidebarSrc), '未分组行不得再行内 setFilter（走 onOpenProject）');
});

test('⑥b App 传 onOpenProject={openProject}，openProject 复用 resumeSession + backToChat（不造新轮子）', () => {
  assert.match(appSrc, /onOpenProject=\{openProject\}/);
  assert.match(navSrc, /openProjectEntry\(\{/);
  assert.match(navSrc, /source\.resumeSession\(id\)/, '切换必须复用既有 resume 链路');
  assert.match(navSrc, /onOpenChat: backToChat/, '视图收口复用 backToChat');
});

test('⑥c project-entry 不重排列表（口径对齐后端 session.list：updated_at DESC）', () => {
  const src = readFileSync(join(here, '../src/shared/project-entry.ts'), 'utf8');
  assert.ok(!src.includes('.sort('), '判定不许在前端重排——侧栏第一条就是切换目标，两处必须一致');
});
