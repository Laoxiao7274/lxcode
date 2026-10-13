// 子会话「任务」块的契约（2026-10-13 用户报的两个呈现问题）：
//   问题 1：派发进子 Agent 的任务说明书（user 角色、带 dispatch_id 归属）在子会话
//           标签页渲染成和用户手打消息一模一样的气泡——用户分不清是 AI 传进去的
//           任务还是自己发的。修复后 = 独立的「任务」块（kind:"task"），且**两条
//           来源同一样式**：实时（UserMsgEvent 双投）与回放（childHistory 快照）。
//   问题 2：再次调用子 Agent 变成「需要同意」的小卡片。根因 = 渲染错位（B）：
//           0.2.3（用户在用的版本，打包于 15:43）早于修复提交 2e9257f（16:46）——
//           那一版的双投没有 isCardLifecycle 过滤，第二次派发的 chat.dispatchStart/
//           dispatchEnd 被投进子会话自己的 state，reduceDispatchStart 在子会话
//           时间线里画了一张「调用子 Agent · 执行中」的可点小卡；子会话自己的高危
//           工具确认（bash 等，取严档位下）再叠一张「执行此命令？」卡——「得同意」。
//           agent_dispatch 本体是 RiskLow（internal/tools/dispatch.go:62），不经过
//           确认门（internal/agent/tools.go:80 只对 registry.Confirm 返回非空的
//           高危工具挂门）——「派发要审批」从来不是真审批。isCardLifecycle 已在
//           master 修掉卡的部分；本文件用两轮派发的完整事件序列把它钉住：
//           第二轮的呈现必须与第一轮一致（任务块 + 摘要卡），且全程零确认块。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce, reduceSessionStates, handleAgentEvent } from '../src/shared/store.ts';
import { historyBlocks, reduceHistory } from '../src/shared/history.ts';
import { mapEvent } from '../src/agent/ws/events.ts';
import { initial } from '../src/shared/blocks.ts';

/** 一次派发的完整 wire 事件序（与后端 emit 顺序一致：dispatchStart → busy →
 *  任务消息 → delta → done → dispatchEnd）。ownerSessionId = 父会话。 */
function dispatchRound({ dispatchId, childId, owner, task, reply }) {
  return [
    ['chat.dispatchStart', { owner_session_id: owner, session_id: childId, dispatch_id: dispatchId, agent_id: 'coder', agent_name: '代码 Agent', agent_color: '#3b82f6', task }],
    ['chat.busy', { session_id: owner, busy: true, dispatch_id: dispatchId }],
    ['chat.userMessage', { session_id: owner, message: { content: task }, dispatch_id: dispatchId }],
    ['chat.delta', { session_id: owner, kind: 'reasoning', text: '看看现状…', dispatch_id: dispatchId }],
    ['chat.delta', { session_id: owner, kind: 'text', text: reply, dispatch_id: dispatchId }],
    ['chat.done', { session_id: owner, usage_tokens: 12, finish_reason: 'stop', dispatch_id: dispatchId }],
    ['chat.dispatchEnd', { owner_session_id: owner, session_id: childId, dispatch_id: dispatchId, result: reply, is_error: false, usage_tokens: 12 }],
  ];
}

/** 驱动一轮完整事件（store.handleAgentEvent = 真实接线，含 dispatchChild 映射记录）。 */
function drive(controller, wire) {
  for (const [method, params] of wire) {
    const ev = mapEvent(method, params);
    if (ev) handleAgentEvent(ev, controller);
  }
}

function newController(states) {
  const c = {
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
    setPrevBusy(next) { this.prevBusy = next; },
    autoSend(sessionId, text) { this.sent.push({ sessionId, text }); },
    sendFailed() {},
    reset() {},
  };
  return c;
}

test('问题1（实时路径）：任务消息双投进子会话 state 渲染成任务块，不是用户气泡', () => {
  const child0 = { ...initial, currentId: 'child-1', historyReady: true };
  const all = { s1: { ...initial, currentId: 's1' }, 'child-1': child0 };
  const c = newController(all);
  drive(c, dispatchRound({ dispatchId: 'd1', childId: 'child-1', owner: 's1', task: '修复下载页的措辞', reply: '改完了' }));

  const child = c.sessionStates['child-1'];
  const kinds = child.blocks.map((b) => b.kind);
  assert.deepEqual(kinds, ['task', 'assistant'], '子会话时间线 = 任务块 + 助手块，任务消息不再画成用户气泡');
  assert.equal(child.blocks[0].text, '修复下载页的措辞');
  assert.equal(child.blocks[0].seq, undefined, '任务块不带撤回锚点（它不是用户消息，撤回语义不适用）');

  // 主时间线不画任务消息（dispatch 卡 = 一行摘要），也不长出确认卡
  const parent = c.sessionStates.s1;
  assert.deepEqual(parent.blocks.map((b) => b.kind), ['dispatch']);
  assert.equal(parent.pending, null);
});

test('问题2（回归钉子）：第二轮派发与第一轮呈现一致——任务块照画，全程零确认块、零 dispatch 卡进子会话', () => {
  const all = { s1: { ...initial, currentId: 's1' }, 'child-1': { ...initial, currentId: 'child-1', historyReady: true } };
  const c = newController(all);
  // 第一轮 + 第二轮（新 dispatch_id、同一子会话——续跑形态）
  drive(c, dispatchRound({ dispatchId: 'd1', childId: 'child-1', owner: 's1', task: '第一轮任务', reply: '第一轮结论' }));
  drive(c, dispatchRound({ dispatchId: 'd2', childId: 'child-1', owner: 's1', task: '第二轮任务（继续）', reply: '第二轮结论' }));

  const child = c.sessionStates['child-1'];
  assert.deepEqual(child.blocks.map((b) => b.kind), ['task', 'assistant', 'task', 'assistant'],
    '两轮派发的子会话时间线形态完全一致：每轮一条任务块 + 一段助手输出');
  assert.equal(child.blocks[2].text, '第二轮任务（继续）');
  assert.equal(child.pending, null, '派发不产生挂起确认');
  assert.equal(child.blocks.some((b) => b.kind === 'confirm'), false, '子会话时间线零确认卡');
  assert.equal(child.blocks.some((b) => b.kind === 'dispatch'), false,
    '子会话时间线零 dispatch 卡（isCardLifecycle 把 dispatchStart/End 拦在双投之外）');

  const parent = c.sessionStates.s1;
  assert.deepEqual(parent.blocks.map((b) => b.kind), ['dispatch', 'dispatch'], '主时间线每轮一张一行摘要卡');
  assert.equal(parent.pending, null, '主时间线同样不需要用户同意');
  assert.equal(parent.blocks.every((b) => b.status === 'done'), true);
});

test('问题1（回放路径）：childHistory 快照（child 标记）把非通告 user 消息渲染成任务块', () => {
  const snapshot = {
    sessionId: 'child-1',
    messages: [
      { role: 'user', content: '第一轮任务说明书' },
      { role: 'assistant', content: '第一轮结论', reasoning_content: '', },
      { role: 'user', content: '第二轮任务说明书（续跑）' },
      { role: 'assistant', content: '第二轮结论', reasoning_content: '' },
    ],
    busy: false,
    pending: null,
    todos: [],
    child: true,
  };
  const blocks = historyBlocks(snapshot);
  assert.deepEqual(blocks.map((b) => b.kind), ['task', 'assistant', 'task', 'assistant'],
    '回放路径与实时路径给出同一个块种类（每轮任务 = 任务块）');
  assert.equal(blocks[0].text, '第一轮任务说明书');
  assert.equal(blocks[2].text, '第二轮任务说明书（续跑）', '续跑的第二轮任务同样渲染成任务块');
  assert.equal(blocks.some((b) => b.kind === 'user'), false, '子会话历史里不再出现用户气泡');

  // 同一段历史走主会话回放（child 键缺席）：user 消息照旧是用户气泡——分流只认标记
  const mainBlocks = historyBlocks({ ...snapshot, child: undefined });
  assert.deepEqual(mainBlocks.map((b) => b.kind), ['user', 'assistant', 'user', 'assistant']);
});

test('问题1（回放路径）：reduceHistory 对子会话快照整段生效（装载入口同款判定）', () => {
  const snapshot = {
    sessionId: 'child-1',
    messages: [{ role: 'user', content: '任务说明书' }],
    busy: false, pending: null, todos: [], child: true,
  };
  const st = reduceHistory({ ...initial, currentId: 'child-1' }, snapshot);
  assert.deepEqual(st.blocks.map((b) => b.kind), ['task']);
});

test('任务块不吞通告：子会话里的注入通告照旧走 notice / hidden 判定', () => {
  // 注入通告（如「用户中断了这次生成」）同样带 dispatch_id 上抛——它不是任务，
  // 前缀表判定必须优先于 task 标记。
  let all = { s1: { ...initial, currentId: 's1' }, 'child-1': { ...initial, currentId: 'child-1', historyReady: true } };
  const c = newController(all);
  drive(c, dispatchRound({ dispatchId: 'd1', childId: 'child-1', owner: 's1', task: '任务', reply: '结论' }));
  // 子会话轮边界的 hidden 注记（系统通告）：不产生任何块
  c.sessionStates = reduceSessionStates(c.sessionStates, {
    type: 'userMessage', sessionId: 's1', text: '[系统通告] 用户中断了这次生成', dispatchId: 'd1',
  }, c.childLinks.dispatchChild);
  // 非隐藏的通告种类：渲染成提示条而不是任务块
  c.sessionStates = reduceSessionStates(c.sessionStates, {
    type: 'userMessage', sessionId: 's1', text: '[重复调用提醒] 检测到重复调用', dispatchId: 'd1',
  }, c.childLinks.dispatchChild);
  const child = c.sessionStates['child-1'];
  assert.deepEqual(child.blocks.map((b) => b.kind), ['task', 'assistant', 'notice'],
    'hidden 注记不产块；带前缀的通告渲染成提示条——都不许画成任务块或用户气泡');
  assert.equal(child.blocks[2].label, '重复调用提醒');
});

test('wire 映射不变：chat.userMessage 的形状与旧后端逐字节一致（task 标记由归约层打，不进 wire）', () => {
  const ev = mapEvent('chat.userMessage', { session_id: 's1', message: { content: '任务', seq: 3 }, dispatch_id: 'd1' });
  assert.deepEqual(ev, { type: 'userMessage', sessionId: 's1', text: '任务', seq: 3, dispatchId: 'd1' },
    'ws 映射层不携带 task 键——它是 store 双投时才打的内部标记');
});
