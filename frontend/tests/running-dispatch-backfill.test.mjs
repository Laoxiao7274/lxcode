// 观察端回填（chat.history 的 running_dispatches → 派发卡）的数据面钉子。
//
// 场景（2026-12 安卓端报，桌面同源）：派发由电脑发起，安卓端只是观察者——
// 重进会话走历史回放，回放重建的派发卡**没有子会话 id**（dispatchStart 早于
// 本次连接、结果提示行要派发结束才落库）：
//   ① 点不开子会话（canOpenChild 按 sessionId）；
//   ② 带 dispatch_id 的 todo/子工具事件按卡反查归属——卡上没有 id 就无处可投
//      （「任务进度消失」）。
// 修法：后端在 chat.history 应答带**此刻正在跑**的派发快照（跑完即注销），
// 归约器按 dispatch_id 回填卡（补卡/补 id，绝不动已收尾卡的状态）。
//
// 四条契约：
//   A. 回放窗口里已有这张卡（assistant 的 agent_dispatch 调用重建的「中断」
//      占位卡）→ 只补 id/名字/任务，**不**把状态翻成 running（那是
//      reviveRunningDispatch 按 busy 判定的事——已收尾的卡绝不能被快照翻回）；
//   B. 回放窗口被 tail 截掉、卡不在 → 追加一张运行中卡（后续带归属的事件有
//      归属可投，主时间线看得到正在跑的派发）；
//   C. 已收尾（done）的卡**绝不被翻回运行中**——快照说"在跑"只对活着的派发
//      成立（后端跑完即注销，正常时序不会出现；这是防御性钉子）；
//   D. store 侧：runningDispatches 记进 childLinks 映射（dispatch_id → 子会话
//      id）——todo/子工具事件按它路由，进度不再丢。
import test from 'node:test';
import assert from 'node:assert/strict';
import { handleAgentEvent } from '../src/shared/store.ts';
import { timelineOf } from '../src/shared/blocks.ts';

/** 与 child-reopen.test.mjs 同款的可变 controller。 */
function makeController() {
  return {
    currentId: 'parent-1',
    sessionStates: { 'parent-1': { blocks: [], busy: false, pending: null, todos: [], currentId: 'parent-1', operationError: null, context: null, historyReady: false } },
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

/** 主会话历史快照：含一次派发调用（回放重建的就是「中断」占位卡）。 */
const parentSnapshot = (overrides = {}, messages) => ({
  sessionId: 'parent-1',
  busy: false,
  pending: null,
  todos: [],
  checkpoints: [],
  messages: messages ?? [
    { role: 'user', content: '把测试跑了' },
    { role: 'assistant', content: '', tool_calls: [{ id: 'd1', function: { name: 'agent_dispatch', arguments: '{"agent":"coder","task":"跑测试"}' } }] },
  ],
  ...overrides,
});

const runningDispatches = [{ dispatchId: 'd1', childSessionId: 'child-9', agentName: '代码 Agent', task: '跑测试' }];

test('A. 回放已有占位卡：补 id/名字/任务，占位态被快照翻回 running（直接证据）', () => {
  const c = makeController();
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: parentSnapshot({ runningDispatches }),
  }, c);
  const cards = c.sessionStates['parent-1'].blocks.filter((b) => b.kind === 'dispatch');
  assert.equal(cards.length, 1, '不该重复建卡');
  const card = cards[0];
  assert.equal(card.sessionId, 'child-9', '子会话 id 应被回填（点开子会话的钥匙）');
  assert.equal(card.agentName, '代码 Agent', 'Agent 名应被回填（回放没有注册表）');
  assert.equal(card.status, 'running', '快照是「正在跑」的直接证据：占位卡翻回 running（比 reviveRunningDispatch 的 busy 启发式强）');
  assert.ok(!card.isError, '占位态的错误位清掉');
});

test('B. 回放窗口被截、卡不在：按快照追加运行中卡（进度事件有归属可投）', () => {
  const c = makeController();
  // 尾部窗口只回放到了结论轮——派发的 agent_dispatch 调用在更早的历史里
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: parentSnapshot(
      { runningDispatches },
      [{ role: 'user', content: '把测试跑了' }],
    ),
  }, c);
  const cards = c.sessionStates['parent-1'].blocks.filter((b) => b.kind === 'dispatch');
  assert.equal(cards.length, 1);
  assert.equal(cards[0].status, 'running', '窗口外的活派发补一张运行中卡');
  assert.equal(cards[0].sessionId, 'child-9');
  // 卡在了，后续带归属的 todo 事件就能按卡路由（进度不再丢）
  handleAgentEvent({
    type: 'todoUpdated', sessionId: 'parent-1', dispatchId: 'd1',
    items: [{ content: '跑测试', status: 'active' }],
  }, c);
  const after = c.sessionStates['parent-1'].blocks.find((b) => b.kind === 'dispatch');
  assert.ok(after.todos, '带归属的 todo 事件应归并进回填的卡（「任务进度消失」的修复）');
});

test('C. 已收尾的卡绝不被快照翻回 running', () => {
  const c = makeController();
  const doneMessages = [
    { role: 'user', content: '把测试跑了' },
    { role: 'assistant', content: '', tool_calls: [{ id: 'd1', function: { name: 'agent_dispatch', arguments: '{"agent":"coder","task":"跑测试"}' } }] },
    { role: 'tool', content: '结论 + [子会话 id: child-old]', tool_call_id: 'd1' },
    { role: 'assistant', content: '已验收。' },
  ];
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: parentSnapshot({ runningDispatches }, doneMessages),
  }, c);
  const card = c.sessionStates['parent-1'].blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.status, 'done', '收尾卡不被翻回 running（回放快照携带的是防御性余量）');
  assert.equal(card.sessionId, 'child-old', '回放权威值优先（结果提示行的 id 不被快照覆盖）');
});

test('D. store：runningDispatches 记进 childLinks（事件路由的数据源）', () => {
  const c = makeController();
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: parentSnapshot({ runningDispatches }),
  }, c);
  assert.equal(c.childLinks.dispatchChild['d1'], 'child-9', 'dispatch_id → 子会话 id 应进映射');
  assert.equal(c.childLinks.childParents['child-9'], 'parent-1', '子→父映射同批建立');
  // 快照里 childSessionId 为空的条目（纯内存子会话）不记映射
  const c2 = makeController();
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'parent-1',
    history: parentSnapshot({ runningDispatches: [{ dispatchId: 'd2', childSessionId: '', agentName: 'x', task: 'y' }] }),
  }, c2);
  assert.equal(c2.childLinks.dispatchChild['d2'], undefined, '无子会话 id 的条目不进映射（寻址不到它）');
});

test('E. 无 runningDispatches（老后端/空闲）：行为与从前逐字节一致', () => {
  const c = makeController();
  handleAgentEvent({ type: 'historyLoaded', sessionId: 'parent-1', history: parentSnapshot() }, c);
  const blocks = timelineOf(c.sessionStates['parent-1']);
  assert.equal(blocks.filter((b) => b.kind === 'dispatch').length, 1, '回放照常建占位卡');
});
