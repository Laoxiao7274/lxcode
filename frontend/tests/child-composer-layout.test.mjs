// 子会话临时视图的两个 UI 钉子（2026-12 用户截图报的问题）：
//   问题 1：子会话的清单卡渲染在时间线顶部（消息流内）而不是输入框上方。
//     修复 = 挂载点收拢到 Composer 的 todos prop（.pi 白卡顶部 .pi-plan 区块，
//     与主会话同一位置同一视觉）；ChildSessionPage 不再自己挂页头下的
//     TodoList。归约层 todo.updated 不产生时间线块（reduce.ts 既有纪律），
//     子会话双投路径同一条——时间线里永不出现清单卡。
//   问题 2：只读提示文字「只读视图——回到主会话才能发消息」贴死左缘、视觉上
//     挤进 piBar 工具行。根因 = 第 74 轮（c138695）把 .piInput 的左内距让给
//     「+」按钮（padding-left:0），而只读模式不渲染「+」，textarea 成为首元素。
//     修复 = :disabled 态补回左内距（styles/composer.css）。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { reduce, reduceSessionStates, handleAgentEvent } from '../src/shared/store.ts';
import { mapEvent } from '../src/agent/ws/events.ts';

const base = { blocks: [], busy: false, pending: null, todos: [], currentId: 's1', operationError: null, context: null };

function newController(states) {
  return {
    currentId: 's1',
    sessionStates: states,
    childLinks: { childParents: {}, dispatchChild: {} },
    sendQueues: {},
    prevBusy: {},
    revisions: 0,
    sent: [],
    getCurrentId() { return this.currentId; },
    getSessionStates() { return this.sessionStates; },
    getChildLinks() { return this.childLinks; },
    getSendQueues() { return this.sendQueues; },
    getPrevBusy() { return this.prevBusy; },
    setCurrentId(id) { this.currentId = id; },
    setSessionStates(next) { this.sessionStates = next; },
    setOperationError() {},
    bumpRevision() { this.revisions += 1; },
    setChildLinks(next) { this.childLinks = next; },
    setSendQueues(next) { this.sendQueues = next; },
    setPrevBusy(next) { this.prevBusy = next; },
    autoSend(sessionId, text) { this.sent.push({ sessionId, text }); },
    sendFailed() {},
    reset() {},
  };
}

// ---- 问题 1：归约断言——子会话时间线不产生 todo 块 ----

test('子会话的 todo.updated 双投只进 state.todos，时间线零 todo 块', () => {
  const all = { s1: { ...base }, 'child-1': { ...base, currentId: 'child-1' } };
  const c = newController(all);
  // dispatchStart 记下 d1→child-1 映射（真实接线）
  handleAgentEvent({
    type: 'dispatchStart', sessionId: 's1', dispatchId: 'd1', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: 't',
  }, c);
  // 子会话更新自己的清单（带归属，父 emitter 广播）
  const next = reduceSessionStates(c.sessionStates, {
    type: 'todoUpdated', sessionId: 's1', dispatchId: 'd1',
    items: [{ content: '定位', status: 'done' }, { content: '修复', status: 'active' }],
  }, c.childLinks.dispatchChild);
  const child = next['child-1'];
  assert.deepEqual(child.todos, [
    { content: '定位', status: 'done' }, { content: '修复', status: 'active' },
  ], '子会话 state 应持有自己的清单（ChildSessionPage 传给 Composer 的数据源）');
  assert.deepEqual(child.blocks, [], '时间线不许出现清单卡（todo.updated 不产生块——主会话纪律对子会话同样生效）');
});

// ---- 问题 1：挂载断言——ChildSessionPage 不再自挂页头下 TodoList ----

test('ChildSessionPage 的清单走 Composer 的 todos prop（页头下不再自挂 TodoList）', async () => {
  const src = fs.readFileSync(new URL('../src/components/panels/ChildSessionPage.tsx', import.meta.url), 'utf8');
  assert.ok(!src.includes('child-session-todos'),
    '页头下自挂的计划条容器已删除（那是清单落到时间线顶部的根因）');
  assert.ok(!src.includes('<TodoList'),
    'ChildSessionPage 不再自己渲染 TodoList——呈现收拢到 Composer 的 .pi-plan');
  assert.ok(/<Composer[\s\S]*?todos=\{todos\}/.test(src),
    '子会话清单经 Composer 的 todos prop 进输入白卡顶部（与主会话同一挂载点）');
});

// ---- 问题 2：CSS 钉子——锁定态输入框有左内距（提示不贴缘） ----

test('只读态 piInput 补回左内距（composer.css :disabled 规则）', () => {
  const css = fs.readFileSync(new URL('../src/styles/composer.css', import.meta.url), 'utf8');
  assert.ok(/\.pi textarea\.piInput:disabled\s*\{[^}]*padding-left:\s*16px/.test(css),
    ':disabled 规则必须带 padding-left:16px——否则只读提示贴死左缘（基态 0 是给「+」让位的）');
});

// ---- 问题 2：结构钉子——只读提示是 textarea 的 placeholder（pi 行内、独立于 piBar） ----

test('只读提示是 pi-row 里 textarea 的 placeholder，不在 piBar 工具行内', async () => {
  const src = fs.readFileSync(new URL('../src/components/composer/Composer.tsx', import.meta.url), 'utf8');
  // placeholder 挂在 textarea 上（.pi-row 内）——与 piBar（工具行）是两个兄弟区块
  const taIdx = src.indexOf('只读视图——回到主会话才能发消息');
  assert.ok(taIdx > 0, '只读提示文案存在');
  const ta = src.lastIndexOf('<textarea', taIdx);
  const piRow = src.lastIndexOf('className="pi-row"', taIdx);
  const piBar = src.indexOf('className="piBar"', taIdx);
  assert.ok(piRow > 0 && piRow < ta && ta < piBar,
    '只读提示应落在 .pi-row 的 textarea（placeholder）内——piBar 工具行在它之后');
});
