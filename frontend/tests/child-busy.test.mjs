// 子会话 busy 上抛的前端契约（2026-10-10）：
//   ① 带归属（dispatchId）的 busy 双投进子会话自己的 state——子会话页的
//      「生成中」行与停止钮靠它出现（busyBySession 由 App 从全部 sessionStates
//      派生，子会话 state.busy 为真即自动包含，无额外过滤）；
//   ② 主会话的 busy/sending/pending 不被子会话的 busy 翻动；
//   ③ 发送缓冲区的自动发送边界（busy true→false）跳过带归属的 busy——
//      子会话跑完不代表主会话空闲，误触发会把队首发出去被后端拒收；
//   ④ 无归属的 busy 行为逐字节不变（边界照常触发）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce, reduceSessionStates, handleAgentEvent } from '../src/shared/store.ts';
import { mapEvent } from '../src/agent/ws/events.ts';

const base = { blocks: [], busy: false, sending: false, pending: null, todos: [], currentId: 's1', operationError: null, context: null };

test('① 子会话 busy 双投进子会话 state，主会话 busy 不被翻动', () => {
  let all = { s1: { ...base }, 'child-1': { ...base, currentId: 'child-1' } };
  const dispatchChild = { d1: 'child-1' };
  // 子会话开始跑（带归属 busy=true，sessionId 是父会话——后端由父 emitter 广播）
  all = reduceSessionStates(all, { type: 'busy', sessionId: 's1', busy: true, dispatchId: 'd1' }, dispatchChild);
  assert.equal(all['child-1'].busy, true, '子会话自己的 state 应翻成 busy（标签页「生成中」靠它）');
  assert.equal(all.s1.busy, false, '主会话 busy 不被子会话翻动');
  assert.equal(all.s1.sending, false);
  // 子会话跑完
  all = reduceSessionStates(all, { type: 'busy', sessionId: 's1', busy: false, dispatchId: 'd1' }, dispatchChild);
  assert.equal(all['child-1'].busy, false);
  assert.equal(all.s1.busy, false);
});

test('② 带归属的 busy 原样返回（不动主会话的 pending/sending）', () => {
  const s = { ...base, sending: true, pending: { id: 'c1' } };
  const next = reduce(s, { type: 'busy', sessionId: 's1', busy: false, dispatchId: 'd1' });
  assert.equal(next, s, '带归属的 busy 应原样返回主会话 state');
});

test('③ 自动发送边界跳过带归属的 busy（子会话跑完不触发队首发送）', () => {
  const c = {
    currentId: 's1',
    sessionStates: { s1: { ...base } },
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
  // 队列里有一条待发 + dispatchStart 记下 d1→child-1 映射
  c.sendQueues = { s1: [{ id: 'q1', text: '排队的话' }] };
  handleAgentEvent({ type: 'dispatchStart', sessionId: 's1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: 't' }, c);
  // 子会话 busy 一真一假：都不该触发自动发送、不该记主会话的 prevBusy
  handleAgentEvent({ type: 'busy', sessionId: 's1', busy: true, dispatchId: 'd1' }, c);
  handleAgentEvent({ type: 'busy', sessionId: 's1', busy: false, dispatchId: 'd1' }, c);
  assert.deepEqual(c.sent, [], '子会话的忙闲边界不触发发送缓冲区');
  assert.equal(c.prevBusy.s1, undefined, '带归属的 busy 不记主会话的 prevBusy');
  assert.deepEqual(c.sendQueues.s1, [{ id: 'q1', text: '排队的话' }], '队列原样保留');
  // 主会话自己的边界照常触发（无归属 busy 行为不变）
  handleAgentEvent({ type: 'busy', sessionId: 's1', busy: true }, c);
  handleAgentEvent({ type: 'busy', sessionId: 's1', busy: false }, c);
  assert.deepEqual(c.sent, [{ sessionId: 's1', text: '排队的话' }]);
  assert.deepEqual(c.sendQueues.s1, []);
});

test('④ 无归属 busy 的归约语义逐字节不变', () => {
  const s = { ...base, sending: true, pending: { id: 'c1' } };
  const next = reduce(s, { type: 'busy', sessionId: 's1', busy: true });
  assert.equal(next.busy, true);
  assert.equal(next.sending, false, 'busy=true 交接发送中态');
  assert.equal(next.pending.id, 'c1', 'busy=true 保留 pending');
  const done = reduce(next, { type: 'busy', sessionId: 's1', busy: false });
  assert.equal(done.busy, false);
  assert.equal(done.pending, null, 'busy=false 收尾 pending');
});

test('⑤ wire 映射：chat.busy 带/不带 dispatch_id', () => {
  const withId = mapEvent('chat.busy', { session_id: 's1', busy: true, dispatch_id: 'd1' });
  assert.equal(withId.type, 'busy');
  assert.equal(withId.dispatchId, 'd1', '子会话 busy 带归属');
  const plain = mapEvent('chat.busy', { session_id: 's1', busy: true });
  assert.equal(plain.dispatchId, undefined, '主会话 busy 无归属键');
});
