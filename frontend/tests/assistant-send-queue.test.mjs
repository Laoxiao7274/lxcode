// 助手域发送缓冲区（2026-12 接入——与主域共用 send-queue 纯函数与 sendQueues
// 键域无关的 map）：
// ① store 的 handleAssistantEvent：busy true→false 边沿触发 drainQueueHead 出口
//    分流到 autoSendAssistant（绝不走主域 autoSend——chat.send 寻址助手会话串域）；
//    带 dispatchId（子会话忙闲）不触发父会话补发（与主域同一条纪律）；
// ② 排队条抽成共享组件 SendQueueBar：主域 Composer 与助手域 AssistantInput
//    渲染同一份（源码断言——组件交互层 node 测不了，照 assistant-attachments 的
//    源码断言模式）；
// ③ routeSubmit 的助手路径：AssistantInput 的 submit 门直测同一份纯函数
//    （hasQueue = 接了 onQueue；busy 时入队而不是无效）。
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

import { handleAgentEvent } from '../src/shared/store.ts';
import { routeSubmit } from '../src/shared/send-queue.ts';

// ---------- 测试桩（照 assistant-domain.test.mjs 的 newController 形状 + 助手槽） ----------

const base = { blocks: [], streaming: null, busy: false, pending: null, todos: [], currentId: 's1', operationError: null, context: null, stats: null };

function newController(states, assistantStates) {
  return {
    currentId: 's1',
    sessionStates: states,
    assistantStates: assistantStates ?? {},
    childLinks: { childParents: {}, dispatchChild: {} },
    sendQueues: {},
    prevBusy: {},
    prevAssistantBusy: {},
    revisions: 0,
    sent: [],
    assistantSent: [],
    getCurrentId() { return this.currentId; },
    getSessionStates() { return this.sessionStates; },
    getAssistantStates() { return this.assistantStates; },
    getChildLinks() { return this.childLinks; },
    getSendQueues() { return this.sendQueues; },
    getPrevBusy() { return this.prevBusy; },
    getPrevAssistantBusy() { return this.prevAssistantBusy; },
    setCurrentId(id) { this.currentId = id; },
    setSessionStates(next) { this.sessionStates = next; },
    setAssistantStates(next) { this.assistantStates = next; },
    setOperationError() {},
    bumpRevision() { this.revisions += 1; },
    setChildLinks(next) { this.childLinks = next; },
    setSendQueues(next) { this.sendQueues = next; },
    setPrevBusy(next) { this.prevBusy = next; },
    setPrevAssistantBusy(next) { this.prevAssistantBusy = next; },
    getPrevCompacting() { return {}; },
    setPrevCompacting() {},
    autoSend(sessionId, text) { this.sent.push({ sessionId, text }); },
    autoSendAssistant(sessionId, text) { this.assistantSent.push({ sessionId, text }); },
    sendFailed() {},
    reset() {},
  };
}

// ---------- ① 助手域 busy 边沿触发 drain + 出口分流 ----------

test('① 助手 busy true→false 触发队首发出，且走 autoSendAssistant 不走主域 autoSend', () => {
  const c = newController(
    { s1: { ...base } },
    { a1: { ...base, busy: true } },
  );
  c.sendQueues['a1'] = [{ id: 'q1', text: '排队的话' }];
  // busy=true 先到：记基线，不触发（false→… 无边沿）
  handleAgentEvent({ type: 'busy', sessionId: 'a1', busy: true, space: 'assistant' }, c);
  assert.equal(c.assistantSent.length, 0, 'busy=true 不是边界');
  assert.equal(c.sendQueues['a1'].length, 1);
  // busy=false：true→false 边沿 → 队首发出
  handleAgentEvent({ type: 'busy', sessionId: 'a1', busy: false, space: 'assistant' }, c);
  assert.equal(c.assistantSent.length, 1, '轮次边界补发队首');
  assert.deepEqual(c.assistantSent[0], { sessionId: 'a1', text: '排队的话' });
  assert.equal(c.sent.length, 0, '绝不走主域 autoSend（chat.send 寻址助手会话会串域报错）');
  // 发出即移除：重复的 busy=false（重播）空队列幂等
  handleAgentEvent({ type: 'busy', sessionId: 'a1', busy: false, space: 'assistant' }, c);
  assert.equal(c.assistantSent.length, 1, '重复触发空队列无副作用');
});

test('① 助手 false→false（重播/未知基线）不触发——state 不存在时不瞎发', () => {
  const c = newController({ s1: { ...base } }, {});
  c.sendQueues['a1'] = [{ id: 'q1', text: 'x' }];
  handleAgentEvent({ type: 'busy', sessionId: 'a1', busy: false, space: 'assistant' }, c);
  assert.equal(c.assistantSent.length, 0, 'prevBusy 未知（undefined）→ false 不构成 true→false 边沿');
});

test('① 子会话的 busy（带 dispatchId）不触发助手会话补发', () => {
  const c = newController(
    { s1: { ...base } },
    { a1: { ...base, busy: true } },
  );
  c.prevAssistantBusy['a1'] = true;
  c.sendQueues['a1'] = [{ id: 'q1', text: 'x' }];
  handleAgentEvent({ type: 'busy', sessionId: 'a1', busy: false, dispatchId: 'd1', space: 'assistant' }, c);
  assert.equal(c.assistantSent.length, 0, 'run_task 子会话的忙闲不触发父会话补发');
  assert.equal(c.sendQueues['a1'].length, 1, '队列保留');
});

test('① 助手 busy 边沿不影响主域队列的自动发送路径（主域同事件独立判定）', () => {
  const c = newController(
    { m1: { ...base, busy: true } },
    { a1: { ...base, busy: true } },
  );
  c.sendQueues['m1'] = [{ id: 'q1', text: '主域' }];
  c.sendQueues['a1'] = [{ id: 'q2', text: '助手' }];
  // 各自的 busy=true 先记基线（false→false 不构成边沿）
  handleAgentEvent({ type: 'busy', sessionId: 'm1', busy: true }, c);
  handleAgentEvent({ type: 'busy', sessionId: 'a1', busy: true, space: 'assistant' }, c);
  // 主域会话的 busy 边沿
  handleAgentEvent({ type: 'busy', sessionId: 'm1', busy: false }, c);
  assert.deepEqual(c.sent, [{ sessionId: 'm1', text: '主域' }]);
  assert.equal(c.assistantSent.length, 0);
  // 助手会话的 busy 边沿（同一 tick）
  handleAgentEvent({ type: 'busy', sessionId: 'a1', busy: false, space: 'assistant' }, c);
  assert.deepEqual(c.assistantSent, [{ sessionId: 'a1', text: '助手' }]);
  assert.equal(c.sent.length, 1, '主域只发它自己的那条');
});

// ---------- ② SendQueueBar 共享组件钉子 ----------

const composerSrc = fs.readFileSync(
  new URL('../src/components/composer/Composer.tsx', import.meta.url), 'utf8');
const assistantInputSrc = fs.readFileSync(
  new URL('../src/components/assistant/AssistantInput.tsx', import.meta.url), 'utf8');
const barSrc = fs.readFileSync(
  new URL('../src/components/composer/SendQueueBar.tsx', import.meta.url), 'utf8');

test('② 排队条抽成 SendQueueBar：Composer 与 AssistantInput 渲染同一份', () => {
  assert.ok(barSrc.includes('aria-label="发送缓冲区"'), 'SendQueueBar 持有排队条 JSX');
  assert.ok(!composerSrc.includes('send-queue-item'), 'Composer 不再内联排队条（抽走了）');
  assert.ok(composerSrc.includes('<SendQueueBar'), 'Composer 渲染共享组件');
  assert.ok(assistantInputSrc.includes('<SendQueueBar'), 'AssistantInput 渲染共享组件（两域同一份 JSX）');
});

// ---------- ③ routeSubmit 的助手路径 ----------

test('③ AssistantInput 的 submit 门走 routeSubmit：busy 时接了 onQueue 就入队', () => {
  const base = { text: '查个天气', busy: false, sending: false, locked: false, hasQueue: true };
  assert.equal(routeSubmit(base), 'send', '空闲 → 正常发送');
  assert.equal(routeSubmit({ ...base, busy: true }), 'queue', 'busy → 入队（AssistantInput 接了缓冲区）');
  assert.equal(routeSubmit({ ...base, busy: true, hasQueue: false }), 'none', '未接缓冲区保持旧语义：忙时不发');
  assert.equal(routeSubmit({ ...base, busy: true, compacting: true }), 'queue', '压缩期间入队（busy 边沿补发——store 的 shortcut）');
  // 源码钉子：submit 判定确实驱动 routeSubmit（不复刻副本）
  assert.ok(assistantInputSrc.includes('routeSubmit({'), 'AssistantInput 的 submit 门驱动 routeSubmit 纯函数');
  assert.ok(assistantInputSrc.includes('hasQueue: Boolean(onQueue)'), 'hasQueue 按是否接了 onQueue 判定');
});
