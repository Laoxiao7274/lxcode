// 项目模式侧栏「来自助手」小节（2026-12）：
//   ① 渲染断言：当前项目有助手任务时小节出现（小标题 + SessionRow 骨架行 +
//      busy 点）；未分组/空列表不渲染小节（空分组不显示）。
//   ② 接线钉子（源码文本）：行点击/Enter 交给 onOpenAssistantChild（不接
//      chat.* 历史装载——域纪律：进的是助手模式的 AssistantChildPage，走
//      assistant.childHistory）；App 侧 openAssistantChild = 先落一次性偏好
//      再切模式（顺序不能反）；AssistantWorkspace 挂载时消费偏好。
//
// 渲染断言用 react-dom/server 静态渲染（无 DOM；useEffect 不执行，数据由
// state 初值兜底——初值恒空数组，所以渲染断言只能钉「空态不渲染」；有数据的
// 形态走源码文本钉子，与 project-entry.test.mjs 的接线钉子同一模式）。
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
    resumeSession: () => {},
    // 助手域能力缺席（演示源形态）：小节不渲染、回调不被触发
  };
}

const renderSidebar = (sessions, projects, extra = {}) => renderToStaticMarkup(
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
    onOpenAssistantChild: () => {},
    ...extra,
  })),
);

// ---------- ① 渲染：空态不渲染 ----------

test('① 助手域能力缺席 / 未分组 / 无任务：小节不渲染（空分组不显示）', () => {
  const html = renderSidebar([meta('a')], [{ id: 'p1', name: '项目一', path: '/p1' }]);
  assert.ok(!html.includes('assistant-tasks-head'), '助手域能力缺席（source.assistant 为空）不渲染小节');
  assert.ok(!html.includes('assistant-tasks-label'), '不出现「助手」分节标题');
});

// ---------- ② 接线钉子（源码文本） ----------

const sidebarSrc = readFileSync(new URL('../src/components/sidebar/Sidebar.tsx', import.meta.url), 'utf8');
const appSrc = readFileSync(new URL('../src/App.tsx', import.meta.url), 'utf8');
const wsSrc = readFileSync(new URL('../src/components/assistant/AssistantWorkspace.tsx', import.meta.url), 'utf8');
const prefSrc = readFileSync(new URL('../src/components/assistant/assistant-child-pref.ts', import.meta.url), 'utf8');

test('② Sidebar：小节数据源 = source.assistant.tasks(filter)，行点击/Enter 交给 onOpenAssistantChild', () => {
  assert.match(sidebarSrc, /assistant\.tasks\(filter\)/, '数据源是 assistant 域的 tasks（assistant.* 寻址，不接 chat.*）');
  // 行点击与键盘都走 onOpenAssistantChild（跨模式跳转的唯一收口）
  assert.match(sidebarSrc, /onClick=\{\(\) => onOpenAssistantChild\(t\.id\)\}/);
  assert.match(sidebarSrc, /onOpenAssistantChild\(t\.id\); \}/, 'Enter/Space 同一收口');
  // 行是「看/跳」入口：小节行不接 chat.* 装载（无 resumeSession/chatHistory 调用）
  const section = sidebarSrc.slice(sidebarSrc.indexOf('assistantTasks.length > 0'));
  const end = section.indexOf('sess-scroll');
  const seg = section.slice(0, end);
  assert.ok(!/resumeSession|loadHistory|childHistory/.test(seg), '小节行不接历史装载（域纪律）');
  // 未分组不拉取（LOOSE 分支显式清空）
  assert.match(sidebarSrc, /filter === LOOSE \|\| filter === ""/, '未分组不拉 assistant.tasks');
});

test('② 小节标题缩短为「助手」+ 行首来源徽标 assistant-src-pill（2026-12 侧栏区分 + ④整齐化）', () => {
  const section = sidebarSrc.slice(sidebarSrc.indexOf('assistantTasks.length > 0'));
  const end = section.indexOf('sidebar-footer');
  const seg = section.slice(0, end);
  assert.ok(seg.includes('>助手<'), '分节标题文案 =「助手」（与「项目」同密度）');
  assert.ok(!seg.includes('>来自助手<'), '不再用「来自助手」长标题（aria 语义文案不受限）');
  assert.match(seg, /assistant-src-pill/, '任务行带来源徽标 pill');
  // ④整齐化：徽标统一在**行首**（title 之前）——与会话行的 s-dot 同位
  assert.ok(seg.indexOf('assistant-src-pill') < seg.indexOf('className="title"'), 'pill 在标题之前（行首图标位）');
});

test('② App：onOpenAssistantChild = 留在项目模式开 astchild 临时视图（openAstChildView，不切模式）', () => {
  assert.match(appSrc, /onOpenAssistantChild=\{openAssistantChild\}/, 'Sidebar props 接线');
  const fn = appSrc.slice(appSrc.indexOf('const openAssistantChild = useCallback'));
  const seg = fn.slice(0, fn.indexOf('}, [openAstChildView]'));
  // 方案 A（2026-12）：不再 setAssistantChildPref + 切助手模式——与主域子会话
  // 同语义：留在项目模式开 astchild 临时视图（pushState 进浏览器历史）。
  assert.match(seg, /openAstChildView\(childId\)/, '点击 = openAstChildView（项目模式内临时视图）');
  assert.ok(!seg.includes('setAssistantChildPref'), '不再落一次性偏好（跨模式跳转已退役）');
  assert.ok(!seg.includes('handleModeChange'), '不再切助手模式（不跳模式）');
});

test('② App：renderView 有 astchild 分支（AssistantChildPage + 返回头，state 源 = assistantStates）', () => {
  // 漏加分支 = astchild 键落到「坏键 return null」渲染空白（测试必红）。
  const fn = appSrc.slice(appSrc.indexOf('const renderView = (activeView'));
  const seg = fn.slice(0, fn.indexOf('const childId = childTabSession(activeView)'));
  assert.match(seg, /astChildSession\(activeView\)/, 'astchild 键在主域 child 分支之前解析');
  assert.match(seg, /<AssistantChildPage/, '渲染 AssistantChildPage（域口钉死助手域）');
  assert.match(seg, /state=\{assistantStates\[astId\]\}/, 'state 源 = assistantStates（不是主域 sessionStates）');
  assert.match(seg, /onClick=\{backToChat\}/, '返回头 = backToChat（回聊天视图）');
  assert.match(seg, /childConfirmOf\(astId\)/, '裁决走助手域口（useAssistantConfirm 的 partial 包装）');
  // 主域子会话分支仍走 ChildSessionPage + 主域口（两类临时视图不混）
  const mainSeg = fn.slice(fn.indexOf('const childId = childTabSession(activeView)'));
  assert.match(mainSeg, /<ChildSessionPage/, '主域 child:<id> 仍渲染 ChildSessionPage');
});

test('② App：astchild 裁决走助手域口（useAssistantConfirm），不接主域 chat.confirm', () => {
  // 串域实害防御：astchild 视图渲染点附近的 onConfirm 必须来自 useAssistantConfirm。
  assert.match(appSrc, /useAssistantConfirm\(\{[\s\S]*?assistantStates[\s\S]*?resolveAssistant/, 'App 挂 useAssistantConfirm（数据源 assistantStates）');
  const astSeg = appSrc.slice(appSrc.indexOf('astChildSession(activeView)')).slice(0, appSrc.slice(appSrc.indexOf('astChildSession(activeView)')).indexOf('ChildSessionPage'));
  assert.ok(!/handleChildConfirm/.test(astSeg), 'astchild 分支不接主域 handleChildConfirm（chat.confirm 串域）');
  assert.ok(!/handleChildAnswer/.test(astSeg), 'astchild 分支不接主域 handleChildAnswer');
});

test('② AssistantWorkspace：子会话裁决走公共 hook（useAssistantConfirm），数据源 assistantStates', () => {
  // 方案 A：裁决桥抽成 app-hooks/use-assistant-confirm.ts——助手模式内与项目
  // 模式 astchild 视图消费同一个 hook；AssistantWorkspace 不再自持 owner 反查组。
  assert.match(wsSrc, /useAssistantConfirm\(\{/, '消费 useAssistantConfirm（单一裁决实现）');
  assert.ok(!/resolveAssistantChildOwner = useCallback/.test(wsSrc), '自持 owner 反查组已迁出（不回潮）');
  // partial 包装仍从 hook 返回（身份稳定纪律不动）
  assert.match(wsSrc, /onConfirm=\{childConfirmOf\(childId\)\}/, 'onConfirm 走稳定化的 childConfirmOf');
  assert.match(wsSrc, /onAnswer=\{childAnswerOf\(childId\)\}/, 'onAnswer 走稳定化的 childAnswerOf');
});

test('② AssistantWorkspace：挂载时 take 偏好直接落子会话视图；偏好模块 take 即清', () => {
  assert.match(wsSrc, /useState<string \| null>\(\(\) => takeAssistantChildPref\(\)\)/, 'childViewId 初始化消费一次性偏好');
  assert.match(prefSrc, /pref = null;\s*\n\s*return p;/s, 'take 即清（读走即消费）');
  assert.match(prefSrc, /export function setAssistantChildPref/, 'set 口存在');
});

test('② AssistantSource.tasks 接口与两域实现：WS 走 assistant.tasks，演示源如实回空', () => {
  const typesSrc = readFileSync(new URL('../src/shared/types.ts', import.meta.url), 'utf8');
  assert.match(typesSrc, /tasks\(projectId: string\): Promise<SessionMeta\[\]>;/, 'AssistantSource.tasks 接口声明');
  const wsSrc2 = readFileSync(new URL('../src/agent/ws/assistant.ts', import.meta.url), 'utf8');
  assert.match(wsSrc2, /call\("assistant\.tasks", \{ project_id: projectId \}\)/, 'WS 域走 assistant.tasks');
  const demoSrc = readFileSync(new URL('../src/agent/demo/index.ts', import.meta.url), 'utf8');
  assert.match(demoSrc, /async assistantTasks\(/, '演示源实现 tasks（如实回空数组）');
});
