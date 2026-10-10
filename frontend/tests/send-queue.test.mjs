// 发送缓冲区（体验修复批次 5）：busy 时提交的消息排队，会话空闲后逐条自动发出。
//
// 被钉住的行为（全部驱动真实现——纯函数与 handleAgentEvent，不复刻副本）：
// ① submit 门（routeSubmit）：空闲直接发送、busy/sending 入队、锁定与斜杠命令不发；
// ② 自动发送（handleAgentEvent 的 busy 分支 + autoSendPick）：只在 busy true→false
//    的轮次边界触发、只发当前活跃会话的队首、逐条（发出即移除，下一条等下一个边界）；
// ③ 「直接发送」按钮（queueSendAction）：空闲立即发、忙时置顶；
// ④ 队列操作：编辑取出 / 删除 / 置顶；
// ⑤ 切会话队列隔离 + 切回空闲会话接着发（sessionFocused 分支）；
// ⑥ send 被拒：队首已移除、剩余队列保留、自动发送停止（不再有轮次边界）。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  autoSendPick,
  enqueueSend,
  moveQueuedToFront,
  newQueueId,
  queuedHead,
  removeQueuedSend,
  routeSubmit,
  queueSendAction,
} from '../src/shared/send-queue.ts';
import { drainQueueHead, handleAgentEvent } from '../src/shared/store.ts';
import { initial } from '../src/shared/blocks.ts';

const item = (id, text) => ({ id, text });

// ---------- ① submit 门（Composer 驱动同一份判定） ----------

test('① routeSubmit：空闲直接发送；busy/sending 入队；锁定与斜杠命令不发', () => {
  const base = { text: '帮我看看', busy: false, sending: false, locked: false, hasQueue: true };
  assert.equal(routeSubmit(base), 'send', '空闲 → 正常发送路径（不入队）');
  assert.equal(routeSubmit({ ...base, busy: true }), 'queue', 'busy → 入队');
  assert.equal(routeSubmit({ ...base, sending: true }), 'queue', 'sending → 入队（后端同样会拒收第二条）');
  assert.equal(routeSubmit({ ...base, busy: true, hasQueue: false }), 'none', '未接缓冲区的调用方保持旧语义：忙时不发');
  assert.equal(routeSubmit({ ...base, busy: true, locked: true }), 'none', '锁定视图（子会话只读）不发');
  assert.equal(routeSubmit({ ...base, busy: true, text: '' }), 'none', '空文本不发');
  assert.equal(routeSubmit({ ...base, busy: true, text: '/compact' }), 'none', '斜杠命令由面板接管，不进对话流');
  assert.equal(routeSubmit({ ...base, busy: true, text: '中间的/不算命令' }), 'queue', '行中斜杠不是命令');
});

// ---------- 队列操作 ----------

test('队列操作：入队追加、删除移除、编辑取出（removeQueuedSend）、置顶', () => {
  let q = [];
  q = enqueueSend(q, 'a', '第一条');
  q = enqueueSend(q, 'b', '第二条');
  assert.deepEqual(q, [item('a', '第一条'), item('b', '第二条')], '先提交的先发（追加到尾部）');
  // 删除
  q = removeQueuedSend(q, 'a');
  assert.deepEqual(q, [item('b', '第二条')]);
  // 删除不存在的 id：原样返回（不产生无谓的新数组）
  assert.equal(removeQueuedSend(q, 'nope'), q);
  // 置顶：把 b 挪到已有队列的队首
  let q2 = enqueueSend(enqueueSend([], 'x', 'X'), 'y', 'Y');
  q2 = moveQueuedToFront(q2, 'y');
  assert.deepEqual(q2, [item('y', 'Y'), item('x', 'X')], '忙时点「直接发送」= 本轮结束后第一个发它');
  assert.equal(moveQueuedToFront(q2, 'nope'), q2, '置顶不存在的 id 原样返回');
  // 队首
  assert.deepEqual(queuedHead(q2), item('y', 'Y'));
  assert.equal(queuedHead([]), null);
  assert.equal(queuedHead(undefined), null);
  // id 唯一
  assert.notEqual(newQueueId(), newQueueId());
});

// ---------- ② 自动发送（驱动 handleAgentEvent） ----------

function makeController() {
  return {
    currentId: '',
    sessionStates: {},
    childLinks: { childParents: {}, dispatchChild: {} },
    sendQueues: {},
    prevBusy: {},
    revisions: 0,
    sent: [],
    operationError: null,
    getCurrentId() { return this.currentId; },
    getSessionStates() { return this.sessionStates; },
    getChildLinks() { return this.childLinks; },
    getSendQueues() { return this.sendQueues; },
    getPrevBusy() { return this.prevBusy; },
    setCurrentId(id) { this.currentId = id; },
    setSessionStates(next) { this.sessionStates = next; },
    setOperationError(message) { this.operationError = message; },
    bumpRevision() { this.revisions += 1; },
    setChildLinks(next) { this.childLinks = next; },
    setSendQueues(next) { this.sendQueues = next; },
    setPrevBusy(next) { this.prevBusy = next; },
    autoSend(sessionId, text) { this.sent.push({ sessionId, text }); },
    reset() {},
  };
}

const busyEv = (sessionId, busy) => ({ type: 'busy', sessionId, busy });

test('② 自动发送：busy true→false 触发、发队首并移除、逐条（下一条等下一个边界）', () => {
  const c = makeController();
  c.currentId = 's1';
  c.sendQueues = { s1: [item('a', '第一条'), item('b', '第二条')] };
  // 首次 busy=true：开始生成，不触发
  handleAgentEvent(busyEv('s1', true), c);
  assert.deepEqual(c.sent, []);
  // 轮次结束（true→false）：发出队首 a，队列只剩 b
  handleAgentEvent(busyEv('s1', false), c);
  assert.deepEqual(c.sent, [{ sessionId: 's1', text: '第一条' }]);
  assert.deepEqual(c.sendQueues.s1, [item('b', '第二条')], '发出即移除，剩余队列保留');
  // 第二轮：b 接着发，直至队空
  handleAgentEvent(busyEv('s1', true), c);
  handleAgentEvent(busyEv('s1', false), c);
  assert.deepEqual(c.sent, [{ sessionId: 's1', text: '第一条' }, { sessionId: 's1', text: '第二条' }]);
  assert.deepEqual(c.sendQueues.s1, [], '队空为止');
  // 队空后再来的边界：无副作用（幂等）
  handleAgentEvent(busyEv('s1', true), c);
  handleAgentEvent(busyEv('s1', false), c);
  assert.equal(c.sent.length, 2);
});

test('② 幂等：done/error 与 busy=false 同 tick 的重复边界、false→false 重播都不重发', () => {
  const c = makeController();
  c.currentId = 's1';
  c.sendQueues = { s1: [item('a', '唯一一条')] };
  handleAgentEvent(busyEv('s1', true), c);
  handleAgentEvent(busyEv('s1', false), c);
  handleAgentEvent(busyEv('s1', false), c); // 同 tick 重播 / 别的客户端同步
  assert.equal(c.sent.length, 1, '第二次 false→false 不是轮次边界');
  assert.deepEqual(c.sendQueues.s1, []);
  // 没经过 true 直接来 false（应用刚起的初次同步）：不触发
  const c2 = makeController();
  c2.currentId = 's1';
  c2.sendQueues = { s1: [item('a', '唯一一条')] };
  handleAgentEvent(busyEv('s1', false), c2);
  assert.deepEqual(c2.sent, [], '没有 true→false 翻转就不发');
  // autoSendPick 自身的判定面（store 分支的同一份判定）
  assert.equal(autoSendPick(undefined, false, 's1', 's1', [item('a', 'x')]), null);
  assert.equal(autoSendPick(false, false, 's1', 's1', [item('a', 'x')]), null);
  assert.equal(autoSendPick(true, true, 's1', 's1', [item('a', 'x')]), null);
  assert.equal(autoSendPick(true, false, 's1', 's2', [item('a', 'x')]), null, '非当前会话不触发');
  assert.equal(autoSendPick(true, false, 's1', 's1', []), null, '空队列不触发');
  assert.deepEqual(autoSendPick(true, false, 's1', 's1', [item('a', 'x')]), item('a', 'x'));
});

test('② 非活跃会话的队列不抢发；切回（sessionFocused）且空闲才接着发', () => {
  const c = makeController();
  c.currentId = 's2';
  c.sendQueues = { s1: [item('a', 's1 的排队消息')] };
  handleAgentEvent(busyEv('s1', true), c);
  handleAgentEvent(busyEv('s1', false), c);
  assert.deepEqual(c.sent, [], 's1 不是当前活跃会话：边界到了也不发（口径：挂起等选中）');
  assert.deepEqual(c.sendQueues.s1, [item('a', 's1 的排队消息')], '队列原样保留');
  // 切到 s1：state 不存在（忙闲未知）→ 保守挂起
  c.sessionStates = {};
  handleAgentEvent({ type: 'sessionFocused', id: 's1' }, c);
  assert.deepEqual(c.sent, [], '忙闲未知不发（后端可能正在跑）');
  // state 已存在且报空闲 → 接着发
  c.sessionStates = { s1: { ...initial, currentId: 's1', busy: false, sending: false } };
  handleAgentEvent({ type: 'sessionFocused', id: 's1' }, c);
  assert.deepEqual(c.sent, [{ sessionId: 's1', text: 's1 的排队消息' }]);
  // state 报忙碌（切走时它还在跑）→ 不发，等 busy false 边界（那时 currentId 已是它）
  const c3 = makeController();
  c3.currentId = 's1';
  c3.sendQueues = { s1: [item('a', 'busy 会话的排队消息')] };
  c3.sessionStates = { s1: { ...initial, currentId: 's1', busy: true } };
  handleAgentEvent(busyEv('s1', true), c3); // 切走前它已经在跑（记下 busy=true 基线）
  handleAgentEvent({ type: 'sessionFocused', id: 's1' }, c3);
  assert.deepEqual(c3.sent, []);
  handleAgentEvent(busyEv('s1', false), c3);
  assert.deepEqual(c3.sent, [{ sessionId: 's1', text: 'busy 会话的排队消息' }], '本轮结束的边界接上');
});

test('⑤ 切会话队列隔离：一条会话的边界不碰另一条的队列', () => {
  const c = makeController();
  c.currentId = 's1';
  c.sendQueues = {
    s1: [item('a1', 's1-A'), item('a2', 's1-B')],
    s2: [item('b1', 's2-A')],
  };
  handleAgentEvent(busyEv('s1', true), c);
  handleAgentEvent(busyEv('s1', false), c);
  assert.deepEqual(c.sent, [{ sessionId: 's1', text: 's1-A' }]);
  assert.deepEqual(c.sendQueues.s1, [item('a2', 's1-B')], '只动 s1 的队首');
  assert.deepEqual(c.sendQueues.s2, [item('b1', 's2-A')], 's2 的队列原封不动');
});

test('⑥ send 被拒：队首已移除、剩余队列保留、自动发送停止（等用户手动处理）', () => {
  const c = makeController();
  c.currentId = 's1';
  c.sendQueues = { s1: [item('a', '会被拒的一条'), item('b', '留下的一条')] };
  // 后端拒收 = autoSend 走了但会话不再进入 busy（busy 维持 false）
  handleAgentEvent(busyEv('s1', true), c);
  handleAgentEvent(busyEv('s1', false), c); // 发队首 a → 被拒
  assert.deepEqual(c.sent, [{ sessionId: 's1', text: '会被拒的一条' }]);
  assert.deepEqual(c.sendQueues.s1, [item('b', '留下的一条')], '剩余队列保留（队首已移除）');
  // 之后没有任何轮次边界（busy 一直 false）→ 自动发送停止：
  // 用户手动点「直接发送」走 drainQueueHead 同一条路径
  drainQueueHead(c, 's1');
  assert.deepEqual(c.sent, [
    { sessionId: 's1', text: '会被拒的一条' },
    { sessionId: 's1', text: '留下的一条' },
  ]);
  assert.deepEqual(c.sendQueues.s1, []);
  // 空队列再 drain：无副作用
  drainQueueHead(c, 's1');
  assert.equal(c.sent.length, 2);
});

// ---------- ③ 直接发送按钮（App 驱动同一份判定） ----------

test('③ queueSendAction：空闲立即发；busy/sending 置顶', () => {
  assert.equal(queueSendAction({ busy: false, sending: false }), 'send-now');
  assert.equal(queueSendAction({ busy: true, sending: false }), 'to-front');
  assert.equal(queueSendAction({ busy: false, sending: true }), 'to-front', 'sending 期间后端同样拒收');
});
