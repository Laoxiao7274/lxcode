// sidebar-persist.test.mjs —— 侧栏状态持久化 + 全局搜索/Ctrl+K 的接线钉子（第一步四件套）：
//
//  ① 持久化：readLastProject/writeLastProject 的读写、键、容错（localStorage
//     手工桩，theme/assistant-domain 同款）；未分组 "" 也是有效记忆。
//  ② 接线（源码文本钉，同 project-entry.test.mjs 的做法——组件渲染无法在
//     node 单测里跑交互）：use-workspace-nav 初始值从存储读 + setFilter 落盘；
//     Sidebar 全局搜索双列表接管 + Ctrl+K 全局监听 + search-kbd 角标。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readLastProject, writeLastProject } from '../src/shared/sidebar-persist.ts';

const here = dirname(fileURLToPath(import.meta.url));
const navSrc = readFileSync(join(here, '../src/app-hooks/use-workspace-nav.ts'), 'utf8');
const sidebarSrc = readFileSync(join(here, '../src/components/sidebar/Sidebar.tsx'), 'utf8');
const sidebarCss = readFileSync(join(here, '../src/styles/sidebar.css'), 'utf8');

// ---------- ① readLastProject / writeLastProject ----------

test('① 写后读到；固定持久化键 lxcode.ui.lastProject；未分组 "" 也是有效记忆', () => {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, String(v)),
  };
  try {
    assert.equal(readLastProject(), '', '无记忆 = 空串（LOOSE，启动默认）');
    writeLastProject('p1');
    assert.equal(readLastProject(), 'p1', '写后读到');
    assert.equal(store.get('lxcode.ui.lastProject'), 'p1', '固定持久化键');
    writeLastProject('');
    assert.equal(store.get('lxcode.ui.lastProject'), '', '未分组也持久化（不 removeItem——空串是有效范围）');
    assert.equal(readLastProject(), '');
  } finally {
    delete globalThis.localStorage;
  }
});

test('①b localStorage 抛异常：读 = 没有记忆，写 = 不抛不落（provider-expanded 同款纪律）', () => {
  globalThis.localStorage = {
    getItem: () => { throw new Error('denied'); },
    setItem: () => { throw new Error('denied'); },
  };
  try {
    assert.equal(readLastProject(), '');
    writeLastProject('p1'); // 不抛
  } finally {
    delete globalThis.localStorage;
  }
});

// ---------- ② 接线（源码钉） ----------

test('② use-workspace-nav：filter 初始值从持久化读，setFilter 切换落盘', () => {
  assert.match(navSrc, /useState<string>\(readLastProject\(\)\)/, '启动恢复（不再恒 LOOSE）');
  assert.match(navSrc, /setFilterState\(f\);\s*\n\s*writeLastProject\(f\);/, '每次切换落盘');
  assert.match(navSrc, /import \{ readLastProject, writeLastProject \} from "\.\.\/shared\/sidebar-persist";/);
});

test('②b Sidebar：全局搜索——查询命中跨所有项目会话 + 项目行；结果替换双列表', () => {
  assert.match(sidebarSrc, /const globalHits = useMemo\(/, '跨项目会话命中');
  assert.match(sidebarSrc, /all\.filter\(\(s\) => s\.title\.toLowerCase\(\)\.includes\(trimmedQuery\)\)/, '数据源 = 全量未归档会话');
  assert.match(sidebarSrc, /const projectHits = useMemo\(/, '项目名命中行');
  assert.match(sidebarSrc, /projects\.filter\(\(p\) => p\.name\.toLowerCase\(\)\.includes\(trimmedQuery\)\)/);
  assert.match(sidebarSrc, /openSearchHit\(ws, s\.id\)/, '会话行点击 = 切项目 + 开该会话');
  assert.match(sidebarSrc, /`\$\{wsName\} · \$\{s\.title\}`/, '「项目名 · 会话名」两级行展示');
});

test('②c Sidebar：助手域不进全局搜索（searchActive 只看 query；助手任务小节仍在 query 空时渲染）', () => {
  // 搜索结果列表由 trimmedQuery 驱动，不带任何 assistant 数据源
  assert.match(sidebarSrc, /const searchActive = trimmedQuery\.length > 0;/);
  const searchBlock = sidebarSrc.slice(sidebarSrc.indexOf('searchActive ? ('), sidebarSrc.indexOf('searchActive ? (') + 4000);
  assert.ok(!searchBlock.includes('assistant'), '搜索态渲染块不引用 assistant 域数据');
});

test('②d Sidebar：Ctrl/Cmd+K 全局监听 → 聚焦+全选；Escape 清查询+失焦（既有行为保留）', () => {
  assert.match(sidebarSrc, /document\.addEventListener\("keydown", onKey\)/, '全局 keydown（document 级）');
  assert.match(sidebarSrc, /\(e\.ctrlKey \|\| e\.metaKey\) && \(e\.key === "k" \|\| e\.key === "K"\)/);
  assert.match(sidebarSrc, /searchRef\.current\?\.select\(\)/, '聚焦后全选');
  assert.match(sidebarSrc, /if \(e\.key === "Escape"\) \{\s*\n\s*setQuery\(""\);\s*\n\s*e\.currentTarget\.blur\(\);/);
});

test('②e search-kbd 角标保留（现在有真实功能了）', () => {
  assert.match(sidebarSrc, /<span className="search-kbd">Ctrl K<\/span>/);
});

// ---------- ③ 细滚动条（CSS 源码钉——样式断言只能在文本层做） ----------

test('③ proj-scroll / sess-scroll 细滚动条：--scrollbar-thumb + hover 加深 + Firefox thin', () => {
  assert.match(sidebarCss, /\.proj-scroll,\s*\n\.sess-scroll \{\s*\n\s*scrollbar-width: thin;/);
  assert.match(sidebarCss, /\.proj-scroll::-webkit-scrollbar,\s*\n\.sess-scroll::-webkit-scrollbar \{\s*\n\s*width: 5px;/);
  assert.match(sidebarCss, /\.proj-scroll::-webkit-scrollbar-thumb,\s*\n\.sess-scroll::-webkit-scrollbar-thumb \{\s*\n\s*background: var\(--scrollbar-thumb\);/);
  assert.match(sidebarCss, /::-webkit-scrollbar-thumb:hover \{\s*\n\s*background: color-mix\(in srgb, var\(--scrollbar-thumb\) 75%, var\(--fg\)\);/);
});
