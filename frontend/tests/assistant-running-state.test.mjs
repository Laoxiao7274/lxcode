// 助手域会话运行状态显示修复（2026-12）的行为钉子：
//   P1 列表 running 位的事件驱动对账（AssistantWorkspace 订阅接线，源码文本钉
//      ——React effect 在 node 单测里跑不了，按仓库惯例源码钉 + 纯函数直测）；
//   P2 冷恢复 busy 保留（reduce 直测——工具执行间隙重开视图 busy 不被快照抹掉
//      + 轮末 busy=false 事件正常翻回）；
//   P3 busyMismatchRef 按 sessionId 重置（源码钉）；
//   P4 busy 快照补拉失败可见（源码钉——console.error 不静默）；
//   P5 侧栏「来自助手」小节失败保留旧值 + connectionChanged 重拉（源码钉）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { handleAgentEvent, reduceSessionStates } from '../src/shared/store.ts';
import { initial } from '../src/shared/blocks.ts';
import { reduce } from '../src/shared/reduce.ts';

const here = new URL('.', import.meta.url);
const read = (p) => readFileSync(new URL(p, here), 'utf8');

const wsSrc = read('../src/components/assistant/AssistantWorkspace.tsx');
const pageSrc = read('../src/components/assistant/AssistantPage.tsx');
const indexSrc = read('../src/agent/ws/index.ts');
const sidebarSrc = read('../src/components/sidebar/Sidebar.tsx');

// ---------- P1：列表 running 的事件驱动对账 ----------

test('P1a: AssistantWorkspace 听 busy 事件（space=assistant、无 dispatchId）就地更新列表行 running', () => {
  assert.match(wsSrc, /ev\.type === "busy" && !ev\.dispatchId && \(ev as \{ space\?: string \}\)\.space === "assistant"/,
    'busy 翻转 = 对账信号（子会话忙闲与主域事件被显式判掉）');
  assert.match(wsSrc, /running: ev\.busy/, '就地写 running 位');
  assert.match(wsSrc, /if \(ev\.type === "sessionChanged" \|\| ev\.type === "sessionsChanged"\) \{\s*\n\s*void refreshList\(\);/,
    'P1b: sessionsChanged（hello busy_sessions 快照）触发列表重拉');
});

test('P1: busy 事件驱动列表 running 的数据面（reduce + store 直测——事件带 space 标记进助手域）', () => {
  // store 层对 busy 事件的归约路径直测：assistant 域 busy=true 置真、busy=false 翻回。
  function makeController() {
    return {
      currentId: '',
      sessionStates: {},
      assistantStates: {},
      childLinks: { childParents: {}, dispatchChild: {} },
      sendQueues: {},
      prevBusy: {},
      revisions: 0,
      getCurrentId() { return this.currentId; },
      getSessionStates() { return this.sessionStates; },
      getAssistantStates() { return this.assistantStates; },
      getChildLinks() { return this.childLinks; },
      getSendQueues() { return this.sendQueues; },
      getPrevBusy() { return this.prevBusy; },
      getPrevAssistantBusy() { return {}; },
      setCurrentId() {},
      setSessionStates() {},
      setAssistantStates(next) { this.assistantStates = next; },
      setOperationError() {},
      bumpRevision() { this.revisions += 1; },
      setChildLinks() {},
      setSendQueues() {},
      setPrevBusy() {},
      setPrevAssistantBusy() {},
      autoSend() {},
      autoSendAssistant() {},
      sendFailed() {},
      reset() {},
    };
  }
  const c = makeController();
  // 先用一条带 space 的 historyLoaded 建助手域 state（busy=false 的空会话）。
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'asst-9', space: 'assistant',
    history: { sessionId: 'asst-9', busy: false, blocks: [], messages: [], todos: [], historyReady: true, currentId: 'asst-9', operationError: null, context: null, runningDispatches: [] },
  }, c);
  // 轮开始：assistant.busy → chat.busy 同构事件（space 标记）置真。
  handleAgentEvent({ type: 'busy', sessionId: 'asst-9', busy: true, space: 'assistant' }, c);
  assert.equal(c.assistantStates['asst-9'].busy, true, 'busy=true 进助手域 state（列表行 running 的同源信号）');
  // 轮结束：busy=false 翻回。
  handleAgentEvent({ type: 'busy', sessionId: 'asst-9', busy: false, space: 'assistant' }, c);
  assert.equal(c.assistantStates['asst-9'].busy, false, '轮末 busy=false 正常翻回');
  // 带 dispatchId 的子会话忙闲不翻助手会话自己的 state（双投进子会话）。
  handleAgentEvent({ type: 'busy', sessionId: 'asst-9', busy: true, dispatchId: 'd1', space: 'assistant' }, c);
  assert.equal(c.assistantStates['asst-9'].busy, false, '子会话忙闲不翻父助手会话的 busy');
});

// ---------- P2：冷恢复 busy 保留 ----------

test('P2: 冷恢复重建不把本地 busy=true 抹成 false（工具执行间隙重开视图 busy 保留）', () => {
  // 前置：子会话在跑（busy=true 已进子会话 state——CHILD_BUSY_HINTS 兜底或
  // 轮首 busy=true 事件；此处用轮首事件构造，语义相同）、无 live 流式块。
  let st = { ...initial, currentId: 'child-1' };
  st = reduce(st, { type: 'toolCall', sessionId: 'child-1', id: 't1', name: 'read_file', arguments: '{}' });
  st = reduce(st, { type: 'busy', sessionId: 'child-1', busy: true });
  assert.equal(st.busy, true, 'busy=true 进子会话 state（正在跑）');
  // 重开/重拉视图：子会话快照 busy 恒 false（现建运行时 AttachTo 的静态事实）、
  // 无实时尾巴——旧的显式写 false 会把忙点抹掉，新的保留语义写回 true。
  const rebuilt = reduce(st, {
    type: 'historyLoaded', sessionId: 'child-1',
    history: {
      sessionId: 'child-1', busy: false, blocks: [
        { role: 'user', content: '任务说明书', seq: 1 },
      ], messages: [{ role: 'user', content: '任务说明书', seq: 1 }], todos: [],
      historyReady: true, currentId: 'child-1', operationError: null, context: null,
      runningDispatches: [],
    },
  });
  assert.equal(rebuilt.busy, true, '快照说闲但本地在跑 → 保留本地真值');
  // 轮末 busy=false 事件正常翻回（保留语义不影响正常收尾）。
  const ended = reduce(rebuilt, { type: 'busy', sessionId: 'child-1', busy: false });
  assert.equal(ended.busy, false, '轮末 busy=false 翻回（busy 事件无条件发，不会漏）');
  // 反向不变量：本地闲 + 快照闲 → 仍闲（不引入假忙）。
  const idle = reduce({ ...initial, currentId: 'child-2' }, {
    type: 'historyLoaded', sessionId: 'child-2',
    history: { sessionId: 'child-2', busy: false, blocks: [], messages: [], todos: [], historyReady: true, currentId: 'child-2', operationError: null, context: null, runningDispatches: [] },
  });
  assert.equal(idle.busy, false, '本来闲的会话重开仍闲');
  // 快照说忙（重连补拉）→ 置真（服务端事实优先）。
  const busySnap = reduce({ ...initial, currentId: 'child-3' }, {
    type: 'historyLoaded', sessionId: 'child-3',
    history: { sessionId: 'child-3', busy: true, blocks: [], messages: [], todos: [], historyReady: true, currentId: 'child-3', operationError: null, context: null, runningDispatches: [] },
  });
  assert.equal(busySnap.busy, true, '快照 busy=true 照旧置真');
});

test('P2: 注释与代码一致（busy 保留语义的注释修正）', () => {
  const reduceSrc = read('../src/shared/reduce.ts');
  assert.match(reduceSrc, /busy: busySnapshot \|\| ev\.history\.busy \|\| live\.length > 0 \|\| state\.busy/,
    'busy 表达式带保留项（state.busy）');
  assert.match(reduceSrc, /反向（快照说闲）不显式写 false，保留本地真值/,
    '注释写明为什么 busy 用保留语义');
});

// ---------- P3：重拉闸基线随会话重置 ----------

test('P3: busyMismatchRef 在进入 effect 时按 sessionId 重置（跨会话残留清除）', () => {
  assert.match(pageSrc, /useEffect\(\(\) => \{\s*\n\s*busyMismatchRef\.current = false;/,
    'effect 首行清基线（切会话不残留上一会话的判定）');
});

// ---------- P4：busy 快照补拉失败可见 ----------

test('P4: busy 快照补拉失败不静默（console.error 落 renderer.log，不弹 UI）', () => {
  assert.match(indexSrc, /console\.error\(`\[busy-snapshot\] 助手会话 \$\{id\} 补拉历史失败:/,
    '助手域分流重拉失败可见');
  assert.match(indexSrc, /console\.error\(`\[busy-snapshot\] 会话 \$\{id\} 补拉历史失败:/,
    '主域补拉失败同样可见');
});

// ---------- P5：侧栏「来自助手」小节恢复 ----------

test('P5: 小节拉取失败保留旧值不清空 + connectionChanged 恢复重拉', () => {
  assert.match(sidebarSrc, /console\.error\("\[sidebar\] assistant\.tasks 拉取失败（保留旧列表）"\)/,
    '失败路径不再 setAssistantTasks(EMPTY_SESSIONS)');
  // 失败回调里不再有清空调用
  const fetchFn = sidebarSrc.slice(sidebarSrc.indexOf('const asstFetch = useCallback'));
  const fetchEnd = fetchFn.indexOf('const asstTasksRef');
  assert.ok(!/setAssistantTasks\(EMPTY_SESSIONS\)/.test(fetchFn.slice(0, fetchEnd).split('() => {').pop()),
    '失败分支无清空调用');
  assert.match(sidebarSrc, /if \(ev\.type === "connectionChanged"\) \{\s*\n\s*if \(!ev\.connected\) return;/,
    '订阅 connectionChanged：恢复（connected 翻 true）即重拉');
});
