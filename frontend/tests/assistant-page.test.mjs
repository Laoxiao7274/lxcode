// AssistantPage 的 UI 钉子（助手模式 P1）：
//   ① 静态渲染冒烟：页头/时间线容器/Composer 挂载、块渲染走既有 Block 组件；
//   ② 接线断言：数据源 = source.assistant（入口四处之一）、输入走 assistant.send、
//      确认回调透传（复用 Block 的确认卡）——源码文本钉住（接线在 App.tsx）。
// 用 react-dom/server 静态渲染（无 DOM）；CSS 导入由 ts-loader 桩掉。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

globalThis.window = { __LX__: undefined, matchMedia: () => ({ matches: false }) };

const { AssistantPage, createBootRunner } = await import('../src/components/assistant/AssistantPage.tsx');
const { SettingsProvider } = await import('../src/shared/settings.tsx');
const { AgentsProvider } = await import('../src/shared/agents.tsx');

// ---------- ① 静态渲染冒烟 ----------

const noop = () => {};
const sourceStub = {
  subscribe: () => () => {},
  sessions: () => [],
  projects: () => [],
  assistant: {
    newSession: async () => 'a1',
    send: noop,
    history: async () => ({ sessionId: 'a1', messages: [], busy: false, pending: null }),
    childHistory: async () => ({ sessionId: 'c', messages: [], busy: false, pending: null }),
    cancel: noop,
    confirm: async () => {},
    compact: async () => ({ compacted: false }),
  },
};

test('① 空态渲染：页头（助手标题）+ 说明 + 轻量输入挂载', async () => {
  let html = '';
  await new Promise((resolve) => {
    // 静态渲染不跑 effect（boot 不会发请求）——只验证结构
    html = renderToStaticMarkup(createElement(SettingsProvider, { source: sourceStub },
      createElement(AgentsProvider, { source: sourceStub },
      createElement(AssistantPage, {
        source: sourceStub, sessionId: '', state: undefined,
        onSessionId: noop, onConfirm: noop, onAnswer: noop,
      }))));
    resolve();
  });
  assert.ok(html.includes('assistant-page'), '根容器 class: ' + html.slice(0, 200));
  assert.ok(html.includes('assistant-head'), html.slice(0, 300));
  assert.ok(html.includes('助手'), '页头标题');
  // 输入区 = Composer 视觉壳（2026-12 视觉对齐）：DOM 走主域全局类
  assert.ok(html.includes('piInput'), '输入框挂载（.piInput）');
  assert.ok(html.includes('send-btn'), '发送钮挂载（.send-btn）');
  assert.ok(html.includes('piBar'), '工具行挂载（.piBar）');
  // 「新对话」按钮在顶层 AssistantWorkspace 的侧栏（不在页签页头）——见 assistant-workspace 断言
});

test('① 输入区是 Composer 视觉壳：.pi/.pi-row/.piInput/.send-btn + busy-row（钉住视觉对齐不回退）', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantInput.tsx', import.meta.url), 'utf8');
  assert.ok(/className="pi"/.test(src), '白卡用 .pi');
  assert.ok(/className="pi-row"/.test(src), '输入行用 .pi-row');
  assert.ok(/className="piInput"/.test(src), '输入框用 .piInput');
  assert.ok(/className="piBar"/.test(src), '工具行用 .piBar');
  assert.ok(/className="piTips"/.test(src), '提示槽位用 .piTips');
  assert.ok(/send-btn stop/.test(src) && /"send-btn"/.test(src), '发送/停止钮用 .send-btn（stop 态）');
  assert.ok(/"busy-row"/.test(src), '生成中状态行用 .busy-row');
  assert.ok(!/assistant-input-btn|assistant-input-box/.test(src), '旧的助手域自创输入类不得回潮');
  // SVG 沿用主域箭头/方块图标
  assert.ok(/M12 19V5/.test(src), '发送箭头图标与主域同源');
  assert.ok(/rect x="5" y="5" width="14" height="14"/.test(src), '停止方块图标与主域同源');
});

test('① 覆写钉子：助手域有「+」（附件 2026-12）——padding-left:16px 覆写必须已移除', () => {
  const css = fs.readFileSync(new URL('../src/styles/assistant-mode.css', import.meta.url), 'utf8');
  assert.ok(!/\.assistant-ws \.pi-row textarea\.piInput/.test(css), 'piInput 左内距覆写已删（att-add 自带 margin，覆写会双重左距）');
});

test('① 助手附件接线（2026-12）：「+」按钮 + 预览 + 发送带 images', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantInput.tsx', import.meta.url), 'utf8');
  assert.ok(/className="att-add"/.test(src), '附件按钮用主域 .att-add');
  assert.ok(/className="att-preview"/.test(src), '预览行用主域 .att-preview');
  assert.ok(/className="att-thumbs"/.test(src), '缩略图轨道用主域 .att-thumbs');
  assert.ok(/AttachmentThumb/.test(src), '缩略图复用主域 AttachmentThumb（不另写）');
  assert.ok(/attsFromPending\(/.test(src), '发送组装走 shared/attachments 的 wire 组装');
  assert.ok(/stageFile\(/.test(src), '读入走 shared/attachment-read 的暂存通道（路径优先）');
  assert.ok(/visionBlocked/.test(src), 'vision 能力门透传（与主域同一判定链）');
  // 精简版边界：不带文件入口与队列（助手域范围外的两条不变量）
  assert.ok(!/fileInputRef/.test(src), '不带文件入口（图片精简版）');
  assert.ok(!/send-queue|onQueue/.test(src), '不带发送队列');
  const pageSrc = fs.readFileSync(new URL('../src/components/assistant/AssistantPage.tsx', import.meta.url), 'utf8');
  assert.ok(/opts\.images = atts\.images/.test(pageSrc), 'AssistantPage 把附件装进 assistant.send 参数');
  const wsSrc = fs.readFileSync(new URL('../src/agent/ws/assistant.ts', import.meta.url), 'utf8');
  assert.ok(/wireImage/.test(wsSrc), 'WS 域 send 用主域 wireImage 组装（path 剥 file）');
});

test('① 带状态渲染：时间线块复用 Block 渲染（user 气泡 + 正文）', async () => {
  const state = {
    blocks: [
      { kind: 'user', uid: 1, text: '帮我看看今天的日程' },
      { kind: 'assistant', uid: 2, content: '今天有两件事…', reasoning: '', streaming: false },
    ],
    streaming: null,
    busy: false, pending: null, todos: [], currentId: 'a1',
    operationError: null, context: null, stats: null,
  };
  const html = renderToStaticMarkup(createElement(SettingsProvider, { source: sourceStub },
    createElement(AgentsProvider, { source: sourceStub },
    createElement(AssistantPage, {
      source: sourceStub, sessionId: 'a1', state,
      onSessionId: noop, onConfirm: noop, onAnswer: noop,
    }))));
  assert.ok(html.includes('thread'), '复用主时间线容器类');
  assert.ok(html.includes('帮我看看今天的日程'), '用户气泡');
  assert.ok(html.includes('今天有两件事'), 'assistant 正文');
  // todos 为空（[]）不渲染 .pi-plan（与主域 Composer 同一条「无清单不占位」纪律）
  assert.ok(!html.includes('pi-plan'), '空 todos 不渲染 .pi-plan');
});

test('① todos 非空：AssistantInput 白卡顶部渲染 .pi-plan + TodoList（任务清单显示修复）', async () => {
  const state = {
    blocks: [{ kind: 'assistant', uid: 1, content: '好，我来处理。', reasoning: '', streaming: false }],
    streaming: null,
    busy: false, pending: null, currentId: 'a1',
    todos: [
      { content: '查日程', status: 'done' },
      { content: '写周报', status: 'active' },
    ],
    operationError: null, context: null, stats: null,
  };
  const html = renderToStaticMarkup(createElement(SettingsProvider, { source: sourceStub },
    createElement(AgentsProvider, { source: sourceStub },
    createElement(AssistantPage, {
      source: sourceStub, sessionId: 'a1', state,
      onSessionId: noop, onConfirm: noop, onAnswer: noop,
    }))));
  assert.ok(html.includes('pi-plan'), '非空 todos 渲染 .pi-plan 白卡顶部区块');
  assert.ok(html.includes('查日程') && html.includes('写周报'), 'TodoList 清单项渲染');
});

test('① todos 接线钉子：AssistantInput 挂 todos prop、AssistantPage 传 state.todos', () => {
  const inputSrc = fs.readFileSync(new URL('../src/components/assistant/AssistantInput.tsx', import.meta.url), 'utf8');
  assert.ok(/todos = \[\]/.test(inputSrc), 'AssistantInput 有 todos prop（默认空数组）');
  assert.ok(/TodoItem\[\]/.test(inputSrc), 'todos 类型 = TodoItem[]');
  assert.ok(/className="pi-plan"/.test(inputSrc), '白卡顶部区块用主域全局类 .pi-plan');
  assert.ok(/<TodoList items=\{todos\} \/>/.test(inputSrc), '清单渲染复用 TodoList（不另写）');
  // 区块位置：在 .pi 白卡内、att-preview 之前（与主域 Composer 同一形态；
  // 用 JSX 类名匹配，避开文件头注释里的提法）
  assert.ok(
    inputSrc.indexOf('className="pi-plan"') < inputSrc.indexOf('className="att-preview"'),
    '.pi-plan 在 att-preview 之前',
  );
  const pageSrc = fs.readFileSync(new URL('../src/components/assistant/AssistantPage.tsx', import.meta.url), 'utf8');
  assert.ok(/todos=\{state\?\.todos \?\? \[\]\}/.test(pageSrc), 'AssistantPage 传 todos={state?.todos ?? []}');
});

// ---------- ② 接线断言（源码文本——接线在 App.tsx 与组件内） ----------

test('② 接线：输入走 assistant.send、取消走 assistant.cancel、装载走 assistant.history', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantPage.tsx', import.meta.url), 'utf8');
  assert.ok(/assistant\.send\(/.test(src), '发送必须走 assistant.send');
  assert.ok(/assistant\?\.cancel\(|assistant\.cancel\(/.test(src), '停止必须走 assistant.cancel');
  // 2026-12 语义修订：boot 是纯装载——AssistantPage 不再自动 assistant.new
  //（新建只从 AssistantWorkspace 的「新对话」按钮发起）
  assert.ok(!/newSession/.test(src), '页签组件不再自动新建会话（assistant.new 已移出 boot）');
  // 历史装载在 createBootRunner 里走 a.history(id)
  assert.ok(/assistant\.history\(|\.history\(id\)/.test(src), '历史装载必须走 assistant.history');
  // 事件 payload 与 chat 同构 → 块渲染复用 Block（确认卡/派发卡/任务块不另写）
  assert.ok(/import \{ Block \} from/.test(src), '块渲染复用 Block');
});

test('② 接线：助手是顶层模式（AppModeSwitch + AssistantWorkspace），不再走主域页签', () => {
  const appSrc = fs.readFileSync(new URL('../src/App.tsx', import.meta.url), 'utf8');
  assert.ok(!/activeView === "assistant"/.test(appSrc), 'renderView 不再有 assistant 分支（页签已摘除）');
  // 保活渲染（2026-12 卸载竞态防御）：两分支都常驻，appMode 只决定 hidden。
  assert.ok(/appMode !== "assistant"/.test(appSrc), '顶层模式分流：appMode !== "assistant" → hidden（保活常驻）');
  assert.ok(/AssistantWorkspace/.test(appSrc), '顶层容器挂载');
  assert.ok(/AppModeSwitch/.test(appSrc) || /onModeChange=\{handleModeChange\}/.test(appSrc), '模式切换控件已接');
  // 域 state 仍来自 store 双投的 assistantStates（经 AssistantWorkspace 传下去）
  assert.ok(/assistantStates=\{assistantStates\}/.test(appSrc), '助手域 state 从 store 传入 AssistantWorkspace');
  // 侧栏不再有助手入口（顶层模式切换在 Topbar）。onOpenAssistantChild 是
  // 「来自助手」任务行的跨模式跳转（2026-12），不是助手模式入口——放行。
  assert.ok(!/onOpenAssistant(?!Child)/.test(appSrc), '侧栏助手入口已摘除');
  // App 不再持子会话域判定（迁进 AssistantWorkspace）
  assert.ok(!/assistantIdsRef/.test(appSrc), 'App 不再持 assistantIdsRef');
});

// ---------- ③ boot 与 StrictMode 双挂载（死锁回归钉子） ----------
// 背景：bootedRef 布尔闸门 + cleanup 的 alive 吞结果，在 StrictMode 下是死锁
// 组合——第一次 effect 的结果被 cleanup 丢弃、第二次被闸门挡住 → sessionId 永空、
// status 永 loading（"正在打开助手会话…"卡死）。修法 = boot promise 缓存
//（createBootRunner）：第二次 effect await 同一个 promise，结果照常写 state。
// 2026-12 语义修订：boot 是**纯装载**（sessionId 非空 → assistant.history；
// 空 → 静默返回，不自动 assistant.new——进入助手模式自动建空会话已废除，
// 中性态由 AssistantWorkspace 渲染）。测试形态对齐本文件既有风格：不引 DOM
// 渲染栈，直接驱动抽出的 runner + 源码文本钉住接线。

const flush = () => new Promise((resolve) => setImmediate(resolve));

function bootHarness({ startId = '' } = {}) {
  let newCalls = 0;
  let historyCalls = 0;
  // history 挂起期间重挂才构成双挂载场景：手动放行，模拟真实网络时序
  //（holdGate = false 时 history 直接落定——其余测试不需要闸门）
  let releaseHistory;
  const historyGate = new Promise((r) => { releaseHistory = r; });
  let holdGate = false;
  const source = {
    assistant: {
      newSession: async () => { newCalls++; return 'a-new'; },
      history: async () => {
        historyCalls++;
        if (holdGate) await historyGate;
        return { sessionId: 'a1', messages: [], busy: false, pending: null };
      },
      send: () => {}, cancel: () => {}, confirm: async () => {},
      compact: async () => ({ compacted: false }), childHistory: async () => ({ sessionId: 'a1', messages: [], busy: false, pending: null }),
    },
  };
  const gotIds = [];
  const runner = createBootRunner({
    source: () => source,
    sessionId: () => startId,
    onSessionId: (id) => gotIds.push(id),
  });
  return { runner, releaseHistory, setHoldGate: (v) => { holdGate = v; }, counts: () => ({ newCalls, historyCalls }), gotIds, source };
}

test('③ sessionId 非空：boot 走 assistant.history 装载（不调 assistant.new）', async () => {
  const h = bootHarness({ startId: 'a1' });
  await h.runner.run();
  assert.equal(h.counts().historyCalls, 1, '历史装载走 assistant.history');
  assert.equal(h.counts().newCalls, 0, '纯装载语义：boot 不自动新建（assistant.new 不发）');
  assert.deepEqual(h.gotIds, [], '不回报新 id（onSessionId 无调用）');
  assert.equal(h.runner.isBooted(), true);
});

test('③ StrictMode 双挂载：两次 effect 只装载一次历史（in-flight promise 共享）', async () => {
  const h = bootHarness({ startId: 'a1' });
  h.setHoldGate(true);
  // 第一次 effect（首次挂载）：发起 boot，停在 history 挂起
  const p1 = h.runner.run();
  assert.equal(h.counts().historyCalls, 1);
  // StrictMode 重挂：第二次 effect 发现在途 promise → await 同一个（不再发请求）
  const p2 = h.runner.run();
  assert.equal(p2, p1, '第二次调用共享同一个 in-flight promise');
  assert.equal(h.counts().historyCalls, 1, 'assistant.history 只走一次');
  // 请求返回（重挂前奏之后）：两次 effect 的 await 都正常落定（不挂死）
  h.releaseHistory();
  await Promise.all([p1, p2]);
  assert.equal(h.runner.isBooted(), true);
});

test('③ sessionId 空：boot 静默返回（中性态），不自动 assistant.new', async () => {
  const h = bootHarness({ startId: '' });
  await h.runner.run();
  assert.equal(h.counts().newCalls, 0, '空 id 不发 assistant.new（进入助手模式不自动建会话）');
  assert.equal(h.counts().historyCalls, 0, '空 id 也不发 history');
  assert.deepEqual(h.gotIds, []);
  // 中性态由 AssistantWorkspace 渲染（key 空 → 中性态分支）——见 workspace 测试
});

test('③ 失败的 boot 清掉缓存：重试能重新走一遍（reset + run）', async (t) => {
  let failFirst = true;
  const src = {
    assistant: {
      newSession: async () => { throw new Error('纯装载语义下 boot 不应发 assistant.new'); },
      history: async () => { if (failFirst) throw new Error('boom'); return { sessionId: 'a2', messages: [], busy: false, pending: null }; },
      send: () => {}, cancel: () => {}, confirm: async () => {},
      compact: async () => ({ compacted: false }), childHistory: async () => ({ sessionId: 'a2', messages: [], busy: false, pending: null }),
    },
  };
  const runner = createBootRunner({ source: () => src, sessionId: () => 'a2', onSessionId: () => {} });
  await assert.rejects(runner.run(), /boom/, '错误照常向上抛（AssistantPage 写 setStatus("error")）');
  // 失败后缓存已清：isBooted 为假，重试重新走
  assert.equal(runner.isBooted(), false, '失败的 boot 不算数');
  failFirst = false;
  await runner.run();
  assert.equal(runner.isBooted(), true, '重试成功后恢复在途/已完成状态');
});

test('③ 接线：boot promise 进 ref、效果层不吞结果、重试走 runner.reset', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantPage.tsx', import.meta.url), 'utf8');
  assert.ok(/createBootRunner\(/.test(src), 'boot 流程走共享 promise runner');
  assert.ok(/runner\.run\(\)\.then\(/.test(src), 'effect 直接接 runner 的结果（不因 cleanup 吞掉）');
  assert.ok(!/\balive\s*=\s*false\b/.test(src), 'alive 吞结果语义已移除');
  assert.ok(!/bootedRef\b/.test(src.replace(/\/\/.*bootedRef.*/g, '')), '布尔闸门代码已移除（注释叙述除外）');
  assert.ok(/bootRef\.current\?\.reset\(\)/.test(src), '重试按钮走 runner.reset');
});

// ---------- ④ 秒回记忆（2026-12 会话切换优化批 2）----------
// 切走再切回已装载的助手会话：不发 assistant.history（store 双投一直在归约
// assistant.* 事件，切回时增量已经在 state 里——再拉是纯往返 + 整时间线重建）。
// 判定 = 该会话的助手域 state.historyReady；失败重试照旧（失败不会让 historyReady
// 变真，reset + run 能重新走）。
test('④ 已装载的会话（historyReady）boot 不再发 assistant.history（秒回）', async () => {
  let historyCalls = 0;
  let loaded = false;
  const src = {
    assistant: {
      newSession: async () => { throw new Error('boot 不发 assistant.new'); },
      history: async () => { historyCalls++; return { sessionId: 'a1', messages: [], busy: false, pending: null }; },
      send: () => {}, cancel: () => {}, confirm: async () => {},
      compact: async () => ({ compacted: false }), childHistory: async () => ({ sessionId: 'a1', messages: [], busy: false, pending: null }),
    },
  };
  const runner = createBootRunner({ source: () => src, sessionId: () => 'a1', onSessionId: () => {}, isLoaded: () => loaded });
  // 未装载：照发
  await runner.run();
  assert.equal(historyCalls, 1);
  // 模拟「装载完成」：判定翻真（真实链路里是 historyLoaded 归约写的 historyReady）
  loaded = true;
  await runner.run();
  assert.equal(historyCalls, 1, '已装载的会话切回不再发 assistant.history（秒回）');
  assert.equal(runner.isBooted(), true, '跳过装载也算 boot 完成（status 落 ready）');
});

test('④ 接线：AssistantPage 把 state.historyReady 接进 boot runner 的 isLoaded', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantPage.tsx', import.meta.url), 'utf8');
  assert.match(src, /isLoaded: \(\) => Boolean\(stateRef\.current\?\.historyReady\)/, 'isLoaded = 助手域 state.historyReady（ref 读最新值）');
  assert.match(src, /const stateRef = useRef\(state\);/, 'state 进 ref（prop 每帧换，判定读最新值）');
  // 判定在 runner 里：StrictMode 双挂载 / 切会话重挂（key）走同一条闸门
  const runnerSrc = fs.readFileSync(new URL('../src/components/assistant/AssistantPage.tsx', import.meta.url), 'utf8');
  assert.match(runnerSrc, /if \(opts\.isLoaded\?\.\(\)\) return;/, 'boot 判定已装载即跳过（在 runner 内部，所有调用方共用）');
});

// ---------- ⑤ 窗口化渲染 + 上翻哨兵（2026-12 会话切换优化批 2） ----------
// 助手会话与主时间线/子会话页共用同一份窗口机制（useWindowedHistory /
// useLoadOlderHistory / WindowSentinel）——之前的整列表挂载在大会话上是
// 切换卡顿的一块。源码钉子钉接线（hook 语义由 useWindowedHistory 的共用实现承担）。
test('⑤ 接线：AssistantPage 用 useWindowedHistory + 哨兵 + assistant 域分页', () => {
  const src = fs.readFileSync(new URL('../src/components/assistant/AssistantPage.tsx', import.meta.url), 'utf8');
  assert.match(src, /useWindowedHistory\(\{/, '窗口化渲染接进来了');
  assert.match(src, /sessionKey: sessionId,/, '窗口按会话派生（切会话首帧 40 块）');
  assert.match(src, /const visible = hidden > 0 \? sealed\.slice\(hidden\) : sealed;/, '窗口切片渲染（不整列表挂载）');
  assert.match(src, /<WindowSentinel onExpand=\{loadOlder\}/, '顶部哨兵触发上翻');
  assert.match(src, /loadOlderLabel\(Boolean\(state\?\.historyMore\), hidden\)/, '哨兵文案与主时间线同一份');
  // 分页走助手域（域隔离）：load 传 assistant.history（带 before_seq）
  assert.match(src, /assistant\?\.history\(sessionId, beforeSeq\)/, '上翻分页必须走 assistant.history（域隔离）');
  // hooks 齐全（useStickyFollow 的 scrollRef 供锚定补偿用）
  assert.match(src, /const \{ endRef, stickyRef, scrollRef \} = useStickyFollow\(/, 'scrollRef 从跟随状态机取出');
});
