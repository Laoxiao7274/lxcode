// AssistantWorkspace 侧栏的 UI 钉子（2026-12 助手模式视觉对齐主域）：
//   ① 静态渲染冒烟：侧栏骨架走主域类（.sidebar / .nav-item / .search-box /
//      .sidebar-label group-head / .sess-scroll / .session-item / .sidebar-footer），
//      会话行用手写 DOM + 主域行类（无 ⋯ 菜单——assistant.* 无 rename/archive）；
//   ② 源码断言：旧的助手域自创类（.assistant-ws-brand / .assistant-ws-session /
//      .assistant-ws-side-head）不回潮；侧栏外壳用 .sidebar 类。
// 用 react-dom/server 静态渲染（无 DOM）；CSS 导入由 ts-loader 桩掉。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

globalThis.window = { __LX__: undefined, matchMedia: () => ({ matches: false }) };

const { AssistantSessionList } = await import('../src/components/assistant/AssistantSessionList.tsx');

const meta = (id, extra = {}) => ({
  id, title: `对话 ${id}`, updatedAt: '2026-12-08 12:00', messages: 1, archived: false, ...extra,
});

const noop = () => {};

// ---------- ① AssistantSessionList：主域行类 ----------

test('AssistantSessionList：行用主域 .session-item 系类（active/busy 态 + 全量 updatedAt）', () => {
  const sessions = [meta('a1', { running: true }), meta('a2')];
  const html = renderToStaticMarkup(
    createElement(AssistantSessionList, { sessions, activeId: 'a2', onSelect: noop }),
  );
  assert.ok(html.includes('sess-scroll'), '滚动容器用主域 .sess-scroll');
  assert.ok(html.includes('session-item'), '会话行用主域 .session-item');
  assert.ok(html.includes('session-item active'), '选中态走主域 .active（中性灰）');
  assert.ok(html.includes('busy'), '运行中行走主域 .busy 加强态');
  assert.ok(html.includes('session-line'), '行结构用主域 .session-line');
  assert.ok(html.includes('s-dot live'), '运行蓝点用主域 .s-dot.live');
  assert.ok(html.includes('session-spinner'), '运行中标题旁 spinner（主域同款）');
  assert.ok(html.includes('2026-12-08 12:00'), '时间是全量 updatedAt（不截断，对齐主域）');
  assert.ok(!html.includes('session-more'), '助手域无 ⋯ 菜单（路 A：assistant.* 无 rename/archive）');
});

// ---------- ② 源码断言：自创类不回潮、外壳用主域 .sidebar ----------

test('② 侧栏骨架：AssistantWorkspace 用主域 .sidebar 类 + 搜索/分组/设置行齐全', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.ok(/className="sidebar assistant-ws-side"/.test(src), '外壳直接用主域 .sidebar 类');
  assert.ok(/className="nav-item"/.test(src), '「新对话」用主域 nav-item 形态');
  assert.ok(/className="search-box"/.test(src), '搜索框用主域 search-box');
  assert.ok(/sidebar-label group-head/.test(src), '分组标题用主域 sidebar-label group-head');
  assert.ok(/className="sidebar-footer"/.test(src), '底部设置行用主域 sidebar-footer');
});

test('② 自创类不回潮：旧 brand/侧栏头/紫行类已删（assistant-mode.css 与源码都不再有）', () => {
  const wsSrc = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  const listSrc = fs.readFileSync(new URL('../src/components/assistant/AssistantSessionList.tsx', import.meta.url), 'utf8');
  const css = fs.readFileSync(new URL('../src/styles/assistant-mode.css', import.meta.url), 'utf8');
  assert.ok(!/assistant-ws-brand/.test(wsSrc + css), '.assistant-ws-brand 头已删');
  assert.ok(!/assistant-ws-side-head/.test(wsSrc + css), '.assistant-ws-side-head 已删');
  assert.ok(!/assistant-ws-new/.test(wsSrc + css), '.assistant-ws-new 按钮已删');
  assert.ok(!/assistant-ws-session[^s-]/.test(listSrc + css), '.assistant-ws-session 紫行全套已删');
  assert.ok(!/assistant-input-btn|assistant-input-box/.test(fs.readFileSync(new URL('../src/components/assistant/AssistantInput.tsx', import.meta.url), 'utf8')), 'AssistantInput 不再用自创视觉类（容器定位类 .assistant-input 保留）');
  // 侧栏宽度对齐主域 252px
  assert.ok(/252px/.test(css), '--asst-side-w 对齐主域列宽 252px');
});

// ---------- ④ 二级视图（2026-12 用户拍板：助手的配置和拓展在助手模式内
// 独立呈现，不跳回项目模式；助手配置独立成页，不经 CatalogPage）----------
// 二级视图 secondaryView（单 state 枚举，形态照抄 childViewId）：「助手配置」
// nav 开 assistant-config 独立页（返回头 + AssistantConfigTab 直接挂载）、
// 「拓展」nav 开 catalog 视图（内嵌 CatalogPage），返回按钮回对话视图；三个
// 取值天然互斥，与会话/子会话视图互斥（点会话行/新对话/派发卡清一切）；
// git/remote 两个跨模式 nav 已摘除（2026-12 用户拍板：模式切换入口唯一化——
// 只有 Topbar 的模式切换控件）。

test('④ 「助手配置」点击开独立页：assistant-config 视图，不经 CatalogPage、不触发 onOpenProjectTab', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  const m = src.match(/data-nav="assistant-config"[\s\S]*?<\/button>/);
  assert.ok(m, '找到「助手配置」nav 按钮');
  assert.ok(m[0].includes('setSecondaryView("assistant-config")'), '开助手配置独立页视图');
  assert.ok(!m[0].includes('setCatalogTabPref'), '不再预设 CatalogPage 页签（独立页不借道拓展页）');
  assert.ok(!m[0].includes('onOpenProjectTab'), '跨模式跳转调用消失（不离开助手模式）');
});

test('④ 助手配置独立页主区挂 AssistantConfigTab（不挂 CatalogPage）+ 返回头 + 管理入口接线', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.ok(src.includes('<AssistantConfigTab'), '主区挂 AssistantConfigTab 自包含组件');
  const cfgBranch = src.slice(src.indexOf('secondaryView === "assistant-config"'));
  assert.ok(!cfgBranch.slice(0, cfgBranch.indexOf('secondaryView === "catalog"')).includes('<CatalogPage />'), '助手配置分支不挂 CatalogPage');
  assert.match(cfgBranch, /assistant-ws-child-head[\s\S]*?onClick=\{\(\) => setSecondaryView\(null\)\}[\s\S]*?← 返回/, '返回头复用 child-head 类，点击回对话视图');
  const css = fs.readFileSync(new URL('../src/styles/assistant-mode.css', import.meta.url), 'utf8');
  assert.ok(/\.assistant-config-page/.test(css), '独立页滚动容器样式（对齐 .cg-page 内边距）');
  // 「管理技能 / 管理 MCP 服务器」入口 → 拓展页（setCatalogTabPref + secondaryView）
  assert.match(cfgBranch, /onOpenCatalog=\{\(tab\) => \{[\s\S]*?setCatalogTabPref\(tab\);[\s\S]*?setSecondaryView\("catalog"\);/, '配置页管理入口 → 预设拓展页签 + 切 catalog 视图');
});

test('④ 「拓展」点击开内嵌 catalog 视图且不带页签偏好（默认 tools 页签语义不变）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  const m = src.match(/data-nav="catalog"[\s\S]*?<\/button>/);
  assert.ok(m, '找到「拓展」nav 按钮');
  assert.ok(m[0].includes('setSecondaryView("catalog")'), '开助手模式内嵌的 catalog 视图');
  assert.ok(!m[0].includes('setCatalogTabPref') && !m[0].includes('onOpenProjectTab'), '不带页签偏好、不跨模式');
});

test('④ git/remote 跨模式 nav 已摘除（模式切换入口唯一化：只有 Topbar 切换控件）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.ok(!src.includes('data-nav="git"'), '助手侧栏无 Git 管理 nav');
  assert.ok(!src.includes('data-nav="remote"'), '助手侧栏无远程访问 nav');
  assert.ok(!src.includes('onOpenProjectTab'), 'onOpenProjectTab 链路已清（无跨模式跳转 prop）');
  // 保留的 nav 组齐全：助手配置/拓展/自动化占位/新对话。
  assert.ok(src.includes('data-nav="assistant-config"'), '助手配置 nav 保留');
  assert.ok(src.includes('data-nav="catalog"'), '拓展 nav 保留');
  assert.ok(/自动化/.test(src), '自动化占位保留');
  assert.ok(/新对话/.test(src), '新对话 nav 保留');
});

test('④ 拓展 catalog 视图主区仍挂 CatalogPage（拓展页签保留 assistant 页——主域语义不变）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.match(src, /secondaryView === "catalog" \? \(\s*<main className="assistant-ws-main">[\s\S]*?<CatalogPage \/>/, 'catalog 分支主区挂 CatalogPage');
});

test('④ 二级视图与会话/子会话视图互斥：点会话行/新对话/子会话卡清一切', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  // 开二级视图 → 清子会话；点会话行/新对话/子会话卡 → 清二级视图。
  assert.ok(/setChildViewId\(null\);\s*\n\s*setSecondaryView\(/.test(src), '开二级视图清掉子会话视图');
  const clears = src.match(/setSecondaryView\(null\)/g) ?? [];
  assert.ok(clears.length >= 5, `清二级视图的出口齐全（两处返回头/会话行/新对话/子会话卡，现 ${clears.length} 处）`);
  // 出口分别挂在两处返回头 / handleSelect / handleNewSession / onOpenChild。
  assert.match(src, /handleSelect = [\s\S]*?setSecondaryView\(null\)/, '点会话行清二级视图');
  assert.match(src, /handleNewSession = [\s\S]*?setSecondaryView\(null\)/, '新对话清二级视图');
  assert.match(src, /onOpenChild=\{\(id\) => \{ setChildViewId\(id\); setSecondaryView\(null\); \}\}/, '点子会话卡清二级视图');
});

// ---------- ③ 会话切换与恢复（2026-12 修复批）----------
// 背景：AssistantPage 的 boot 闸门是「每组件实例一次」语义，旧代码渲染
// AssistantPage 无 key——切会话时 isBooted() 挡住，assistant.history 永不再调
//（点回会话无内容的根因）；且进入助手模式自动 assistant.new 建空会话、列表无
// 初始装载、无 lastAssistantSession 记忆。钉住修复后的四条接线：

test('③ 同一实例切 sessionId → AssistantPage key 变化（重挂→重装载）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  // key = sessionId：key 变化 → React 强制重挂 → bootRef 随实例新建 →
  // isBooted 闸门不挡，boot 重新走 assistant.history（装载历史）。
  assert.ok(/<AssistantPage\s*\n\s*key=\{sessionId\}/.test(src), 'AssistantPage 按 sessionId 强制重挂（key）');
  // 空 sessionId 渲染中性态而不是 AssistantPage（空 key 会误触发 boot 新建的
  // 风险由条件分支挡住——key 只在 sessionId 非空分支出现）。
  assert.ok(/\) : sessionId \? \(/.test(src), 'sessionId 为空走中性态分支（不渲染 AssistantPage，避免空 key）');
  assert.ok(/选择左侧会话继续/.test(src), '中性态文案（不自动新建）');
});

test('③ 列表初始装载 + session.changed 订阅（挂载时拉一次、共享状态变化重拉）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.match(src, /useEffect\(\(\) => \{[\s\S]*?void refreshList\(\);[\s\S]*?source\.subscribe/, '挂载时 refreshList + 订阅 source.subscribe');
  assert.match(src, /ev\.type === "sessionChanged"[\s\S]*?void refreshList\(\)/, 'session.changed → refreshList（后端全局广播两域共用）');
});

test('③ lastAssistantSession 记忆：进入恢复、选中/新建写回', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.match(src, /useState\(\(\) => readLastAssistantSession\(\)\)/, '进入助手模式恢复上次会话记忆');
  assert.match(src, /handleSelect = [\s\S]*?writeLastAssistantSession\(id\)/, '点会话行 → 记忆更新');
  assert.match(src, /handleNewSession = [\s\S]*?writeLastAssistantSession\(id\)/, '新建会话 → 记忆更新');
  assert.ok(!/newSession\(\)[\s\S]*?setSessionId\(id\)[\s\S]*?useState\(""\)/.test(src) && !/useState\(""\)\; \/\/ 起始/.test(src), '不再以空 sessionId 起始自动新建');
});

// ---------- ⑤ 子会话视图回调稳定（2026-12 闪烁修复钉子）----------
//
// 背景：AssistantChildPage 曾把 historyLoader/onConfirm/onAnswer 以内联箭头传给
// ChildSessionPage——装载 effect 的 useCallback 依赖每帧换身份 → assistant.
// childHistory 自激循环 + Block key 全换 → 整条时间线反复卸载重挂（用户报
// 「点开子会话一直闪」）。源码断言钉住「稳定化模式不许回潮」。

test('⑤ AssistantChildPage：historyLoader/cancelSession 是 useCallback 包裹（不是内联箭头）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantChildPage.tsx', import.meta.url), 'utf8');
  // 稳定化模式存在：useCallback 依赖 [assistant]
  assert.match(src, /const historyLoader = useCallback\(/, 'historyLoader 用 useCallback 包裹');
  assert.match(src, /const cancelSession = useCallback\(/, 'cancelSession 用 useCallback 包裹');
  assert.ok(/\[assistant\]/.test(src), '依赖数组是 [assistant]（source.assistant 引用稳定）');
  // 内联回潮防御：ChildSessionPage 的 props 上不得再出现内联箭头的装载/取消口
  const jsx = src.slice(src.indexOf('<ChildSessionPage'));
  assert.ok(!/historyLoader=\{\(/.test(jsx), 'historyLoader prop 不得内联箭头');
  assert.ok(!/cancelSession=\{\(/.test(jsx), 'cancelSession prop 不得内联箭头');
});

test('⑤ AssistantChildPage：onConfirm/onAnswer 经 ref 转发（调用方回调不稳定也不自激）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantChildPage.tsx', import.meta.url), 'utf8');
  assert.match(src, /confirmRef = useRef\(/, 'onConfirm 进 ref');
  assert.match(src, /answerRef = useRef\(/, 'onAnswer 进 ref');
  // 转发回调本身是 useCallback([])（零依赖——读 ref.current，不依赖调用方身份）
  assert.match(src, /handleConfirm = useCallback\(/, 'handleConfirm 用 useCallback');
  assert.match(src, /handleAnswer = useCallback\(/, 'handleAnswer 用 useCallback');
  // 内联回潮防御：props 上不得内联箭头传 onConfirm/onAnswer
  const jsx = src.slice(src.indexOf('<ChildSessionPage'));
  assert.ok(!/onConfirm=\{\(id, allow\) =>/.test(jsx), 'onConfirm prop 不得内联箭头');
  assert.ok(!/onAnswer=\{\(id, text\) =>/.test(jsx), 'onAnswer prop 不得内联箭头');
});

test('⑤ AssistantWorkspace：子会话视图的 *partial* 包装是 useCallback（每帧不换身份）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
  assert.match(src, /const childConfirmOf = useCallback\(/, 'childConfirmOf 用 useCallback');
  assert.match(src, /const childAnswerOf = useCallback\(/, 'childAnswerOf 用 useCallback');
  // 内联回潮防御：<AssistantChildPage> 的 onConfirm/onAnswer 不得内联箭头
  // （切片到它的闭合 /> 为止——AssistantPage 的内联包装是另一处 props 面，
  // 不属于子会话视图的装载路径，不在本钉子范围）。
  const start = src.indexOf('<AssistantChildPage');
  const end = src.indexOf('/>', start);
  const jsx = src.slice(start, end);
  assert.ok(!/onConfirm=\{\(id, allow\)/.test(jsx), 'AssistantChildPage 的 onConfirm 不得内联箭头');
  assert.ok(!/onAnswer=\{\(id, text\)/.test(jsx), 'AssistantChildPage 的 onAnswer 不得内联箭头');
  assert.match(jsx, /onConfirm=\{childConfirmOf\(childId\)\}/, 'onConfirm 走稳定化的 childConfirmOf');
  assert.match(jsx, /onAnswer=\{childAnswerOf\(childId\)\}/, 'onAnswer 走稳定化的 childAnswerOf');
  // hooks 纪律：early return（assistant 缺席）在全部 hooks 之后（hooks 规则）
  const acSrc = fs.readFileSync(new URL('../src/components/assistant/AssistantChildPage.tsx', import.meta.url), 'utf8');
  const hooksEnd = Math.max(acSrc.lastIndexOf('useCallback('), acSrc.lastIndexOf('useRef('));
  const earlyReturn = acSrc.indexOf('if (!assistant) return null;');
  assert.ok(earlyReturn > hooksEnd, 'early return 必须在全部 hooks 之后');
});
