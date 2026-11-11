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
import { readLastProject, writeLastProject, readSidebarSections, writeSidebarSections } from '../src/shared/sidebar-persist.ts';

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
  // 搜索结果列表由 trimmedQuery 驱动，不带任何 assistant 数据源。
  // 切片边界 = 搜索三元分支（到「: (」的 else 分支为止）——2026-12 分层重构后
  // 助手小节移到了 sess-scroll 之下，固定 4000 字符的切片会把它扫进来（那段
  // 引用 assistantTasks 是合法的：小节本来就在搜索态之外渲染）。
  assert.match(sidebarSrc, /const searchActive = trimmedQuery\.length > 0;/);
  const searchStart = sidebarSrc.indexOf('searchActive ? (');
  const searchEnd = sidebarSrc.indexOf(': (', searchStart);
  const searchBlock = sidebarSrc.slice(searchStart, searchEnd);
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

// ---------- ④ 分节折叠 + 分层重构（2026-12） ----------

/** 取选择器的规则体（到下一个顶层 `}` 为止）——sidebar-layout.test.mjs 同款。 */
function rule(css, selector) {
  const at = css.indexOf(selector);
  if (at < 0) return null;
  const start = css.indexOf('{', at);
  const end = css.indexOf('}', start);
  return css.slice(start + 1, end);
}

test('④ 持久化：readSidebarSections/writeSidebarSections 读写 + 容错（坏值/缺键 = 展开）', () => {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, String(v)),
  };
  try {
    assert.deepEqual(readSidebarSections(), { projects: false, assistants: false }, '无记忆 = 两节都展开');
    writeSidebarSections({ projects: true, assistants: false });
    assert.deepEqual(readSidebarSections(), { projects: true, assistants: false }, '写后读到');
    assert.equal(store.get('lxcode.ui.sidebarSections'), '{"projects":true,"assistants":false}', '固定持久化键');
    store.set('lxcode.ui.sidebarSections', 'not-json');
    assert.deepEqual(readSidebarSections(), { projects: false, assistants: false }, '坏 JSON = 展开');
    store.set('lxcode.ui.sidebarSections', '{"projects":"yes"}');
    const r = readSidebarSections();
    assert.equal(r.projects, false, '非布尔值按展开处理（不编「记住收起」）');
    globalThis.localStorage = {
      getItem: () => { throw new Error('denied'); },
      setItem: () => { throw new Error('denied'); },
    };
    assert.doesNotThrow(() => writeSidebarSections({ projects: true, assistants: true }), '写失败不抛');
    assert.deepEqual(readSidebarSections(), { projects: false, assistants: false }, '读失败 = 展开');
  } finally {
    delete globalThis.localStorage;
  }
});

test('④ Sidebar：两节折叠头接线——section-toggle 钮 + collapsed 条件卸载 + 记忆落盘', () => {
  // 折叠 state 初始化从持久化读；toggleSection 落盘（同 setFilter 的纪律）
  assert.match(sidebarSrc, /useState<SidebarSectionsCollapsed>\(\(\) => readSidebarSections\(\)\)/, '启动恢复折叠态');
  assert.match(sidebarSrc, /writeSidebarSections\(next\)/, '切换即落盘');
  // 项目节头：section-toggle + aria-expanded；折叠时 proj-scroll 条件卸载。
  // 切片锚点用 JSX 里的 className（文件前部的注释里也会出现 proj-scroll 字样）。
  const projHeadAt = sidebarSrc.indexOf('<div className="sidebar-label project-head">');
  const projHead = sidebarSrc.slice(projHeadAt, sidebarSrc.indexOf('"proj-scroll"', projHeadAt));
  assert.match(projHead, /className="section-toggle"/, '项目节头有折叠钮');
  assert.match(projHead, /aria-expanded=\{!collapsed\.projects\}/, 'aria-expanded 表达状态');
  assert.match(sidebarSrc, /collapsed\.projects \? null : \(/, '项目列表折叠 = 条件卸载');
  // 助手节头同款；折叠时行列表条件卸载
  const asstHead = sidebarSrc.slice(sidebarSrc.indexOf('assistant-tasks-label'), sidebarSrc.indexOf('assistantTasks.map'));
  assert.match(asstHead, /className="section-toggle"/, '助手节头有折叠钮');
  assert.match(sidebarSrc, /collapsed\.assistants \? null : assistantTasks\.map/, '助手行折叠 = 条件卸载');
  // 节名：项目区改「项目」（原「对话」）
  const groupHead = sidebarSrc.slice(sidebarSrc.indexOf('sidebar-label group-head'));
  assert.ok(groupHead.includes('\n        项目\n'), '会话节名 =「项目」');
  assert.ok(!groupHead.includes('\n        对话\n'), '不再叫「对话」');
});

test('④ CSS：分节开关钮 + 高度分配（项目区 0 1 auto / 55% 封顶，会话区 flex:1 为主）', () => {
  const toggle = rule(sidebarCss, '.section-toggle {');
  assert.ok(toggle?.includes('cursor: pointer'), toggle ?? '缺失');
  const proj = rule(sidebarCss, '.proj-scroll {');
  assert.ok(proj?.includes('flex: 0 1 auto'), '项目区内容自适应可压缩: ' + (proj ?? ''));
  assert.ok(proj?.includes('max-height: 55%'), '项目区封顶放宽到 55%: ' + (proj ?? ''));
  const sess = rule(sidebarCss, '.sess-scroll {');
  assert.ok(sess?.includes('flex: 1 1 0'), '会话区恒吃剩余空间（对话优先）: ' + (sess ?? ''));
  // 徽标行首：min-width 定宽对齐
  const pill = rule(sidebarCss, '.assistant-src-pill {');
  assert.ok(pill?.includes('min-width: 30px'), '徽标定宽（行首整齐）: ' + (pill ?? ''));
});
