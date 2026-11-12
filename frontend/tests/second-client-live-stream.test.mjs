// 第二客户端（远程连接）冷启动看实时流的数据面钉子（2026-12 修复验收，组件 B+C）。
//
// 场景：用户在公司壳连到家里电脑的后端（远程第二客户端），家里那边一个会话
// 正在跑、里面还有派发的子 Agent。第二客户端切换/重连后的初始化链：
//   ① hello 应答的 busy_sessions（组件 B，ws/index.ts onBusySnapshot）→ 立即
//      sessionsChanged 建 busy 投影 + 对 busy 会话补拉 chat.history；
//   ② 补拉的快照带 busy=true + running_dispatches → store 的 historyLoaded
//      分支建「正在跑」投影（revive 卡）并记 dispatch→子会话映射；
//   ③ 之后的实时事件（delta/toolCall/todo，带 dispatch_id）双投：父的卡 +
//      子会话自己的 state（若已装载）。
// 钉住这条链的前端数据面：busy 快照装载后，实时事件不再无处可投。
import test from 'node:test';
import assert from 'node:assert/strict';
import { handleAgentEvent } from '../src/shared/store.ts';

/** 与 running-dispatch-backfill.test.mjs 同款的可变 controller（补充 busy 字段）。 */
function makeController() {
  return {
    currentId: 'parent-1',
    sessionStates: {
      'parent-1': { blocks: [], busy: false, pending: null, todos: [], currentId: 'parent-1', operationError: null, context: null, historyReady: false },
    },
    childLinks: { childParents: {}, dispatchChild: {} },
    sendQueues: {},
    prevBusy: {},
    revisions: 0,
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
    autoSend() {},
    sendFailed() {},
    reset() {},
  };
}

test('第二客户端冷启动链：busy 快照装载 → 映射建立 → 实时子事件双投进卡', () => {
  const c = makeController();
  // ① 组件 B 补拉回来的快照：busy=true + 运行中派发（历史里派发调用已在库，
  //    卡的占位形态由回放重建；childSessionId 由快照给出）。
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: {
      sessionId: 'parent-1',
      busy: true,
      pending: null,
      todos: [],
      checkpoints: [],
      messages: [
        { role: 'user', content: '把测试跑了' },
        { role: 'assistant', content: '', tool_calls: [{ id: 'd1', function: { name: 'agent_dispatch', arguments: '{"agent":"coder","task":"跑测试"}' } }] },
      ],
      runningDispatches: [{ dispatchId: 'd1', childSessionId: 'child-9', agentName: '代码 Agent', task: '跑测试' }],
    },
  }, c);
  const parent = c.sessionStates['parent-1'];
  assert.equal(parent.busy, true, '快照说在跑：本地投影置真（错过了 busy=true 广播的第二客户端以服务端事实为准）');
  const card = parent.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card?.sessionId, 'child-9', '回填的卡带子会话 id（可点开）');
  assert.equal(card?.status, 'running', '卡是运行中');
  assert.equal(c.childLinks.dispatchChild['d1'], 'child-9', 'dispatch→子会话映射已建立（组件 C 的冷启动）');
  // ② 之后的实时子事件（第二客户端此刻开始收到的）有归属可投：
  //    todo 进卡、delta 进卡的流式块。
  handleAgentEvent({
    type: 'delta', sessionId: 'parent-1', dispatchId: 'd1', kind: 'text', text: '子 Agent 输出',
  }, c);
  const cardAfter = c.sessionStates['parent-1'].blocks.find((b) => b.kind === 'dispatch');
  assert.ok(cardAfter, '卡仍在');
  const streamed = cardAfter.subStreaming?.content ?? '';
  assert.ok(streamed.includes('子 Agent 输出'), '实时 delta 应出现在卡的流式块里（第二客户端看得到执行过程）');
});

test('第二客户端打开正在跑的子会话：childHistory 快照装载后实时 delta 续写同一块', () => {
  const c = makeController();
  // 父会话映射先就位（补拉链完成）
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: {
      sessionId: 'parent-1', busy: true, pending: null, todos: [], checkpoints: [],
      messages: [{ role: 'user', content: '跑' }],
      runningDispatches: [{ dispatchId: 'd1', childSessionId: 'child-9', agentName: '代码 Agent', task: '跑' }],
    },
  }, c);
  // 用户点开子会话临时视图 → childHistory 快照装载（子会话自己的 state 重建）
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-9',
    history: {
      sessionId: 'child-9', busy: false, pending: null, todos: [], checkpoints: [],
      child: true,
      messages: [
        { role: 'user', content: '跑测试（任务说明书）' },
        { role: 'assistant', content: '正在分析…' },
      ],
    },
  }, c);
  assert.ok(c.sessionStates['child-9'], '子会话自己的 state 已建立');
  assert.equal(c.childLinks.childParents['child-9'], 'parent-1', '子→父映射在');
  // 之后子 Agent 的实时事件（按父广播 + dispatch_id 归属）双投进子会话 state：
  // 续写 assistant 块（快照里最后一条 assistant 是『正在分析…』——delta 接在它后面）。
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', dispatchId: 'd1', kind: 'text', text: '继续推理' }, c);
  const child = c.sessionStates['child-9'];
  const tail = child.streaming?.content ?? '';
  assert.ok(
    tail.includes('继续推理'),
    '实时 delta 应续写子会话时间线（打开后实时流可见）',
  );
  assert.equal(child.busy, true, '子会话侧的活动事件把 busy 置真（生成中指示）');
});
