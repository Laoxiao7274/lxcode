// 子 Agent 正文「输出后消失」的钉子（2026-12 用户报）。
//
// 根因（两条，缺一不可）：
//   ① **assistant 消息由后端整轮结束才落库**（internal/agent/turn.go 的 s.append
//      在 streamRound 返回之后），所以正在流式的那段正文**永远不在 chat.history 里**；
//   ② **子会话的 chat.history 是「现建运行时 + AttachTo」**（internal/server/session_ops.go
//      的 session()：子会话不进运行时缓存，每次现建），AttachTo 出来的 Session
//      busy 恒 false —— 快照的 busy 键对子会话**不携带任何忙闲事实**。
// 旧守卫 `history.busy && state.busy && state.historyReady` 因此对子会话恒不成立，
// 而 ChildSessionPage 每次挂载都 childHistory（StrictMode 下两次），重拉即把流式块
// 整数组换成历史快照 → 用户看到「文字消失」。
//
// 钉住四件事：
//   ① 流式 delta 到达 → 历史重拉（busy=false 快照）→ 文字不丢；
//   ② 快照已含这条回复（append 之后、done 之前重拉）→ 不重复，且仍可续写；
//   ③ 关闭视图期间的实时流 → 重开重拉 → 不丢、不重复（既有 child-reopen 契约不变）；
//   ④ 工具行/已定格的回复照旧由快照重建（不接重复）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { handleAgentEvent, reduce } from '../src/shared/store.ts';

/** 与 child-reopen.test.mjs 同款的可变 controller。 */
function makeController() {
  return {
    currentId: 'parent-1',
    sessionStates: { 'parent-1': { blocks: [], busy: true, pending: null, todos: [], currentId: 'parent-1', operationError: null, context: null, historyReady: false } },
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

const snapshot = (sessionId, messages, overrides = {}) => ({
  sessionId, busy: false, pending: null, todos: [], checkpoints: [], messages, ...overrides,
});

/** 打开子会话视图（dispatchStart + 首次 childHistory 装载）。 */
function openChild(c, messages = [{ role: 'user', content: '任务说明书' }]) {
  handleAgentEvent({
    type: 'dispatchStart', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: '改 bug',
  }, c);
  handleAgentEvent({ type: 'historyLoaded', sessionId: 'child-1', history: snapshot('child-1', messages) }, c);
}

const texts = (blocks) => blocks.map((b) => b.content ?? b.text ?? '');

test('① 流式 delta → 历史重拉（busy=false 快照）→ 已显示的文字不丢', () => {
  const c = makeController();
  openChild(c);
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', kind: 'text', text: '正在写', dispatchId: 'd1' }, c);
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', kind: 'text', text: '结论', dispatchId: 'd1' }, c);
  assert.deepEqual(texts(c.sessionStates['child-1'].blocks), ['任务说明书', '正在写结论']);

  // 重开视图（ChildSessionPage 每次挂载都 childHistory；StrictMode 下还是两次）
  handleAgentEvent({ type: 'historyLoaded', sessionId: 'child-1', history: snapshot('child-1', [{ role: 'user', content: '任务说明书' }]) }, c);
  assert.deepEqual(texts(c.sessionStates['child-1'].blocks), ['任务说明书', '正在写结论'],
    '快照里没有流式中的 assistant（后端整轮结束才落库）——重拉不许把它抹掉');
  // 再拉一次（StrictMode 双调用）同样不丢
  handleAgentEvent({ type: 'historyLoaded', sessionId: 'child-1', history: snapshot('child-1', [{ role: 'user', content: '任务说明书' }]) }, c);
  assert.deepEqual(texts(c.sessionStates['child-1'].blocks), ['任务说明书', '正在写结论']);
  // busy 保真：流式块存在就是「在跑」的证据（子会话快照的 busy 恒 false，不可信）
  assert.equal(c.sessionStates['child-1'].busy, true, '有流式块 = 会话在跑');
});

test('② 快照已含这条回复（append 后、done 前重拉）→ 不重复，且 delta 继续续写同一块', () => {
  const c = makeController();
  openChild(c);
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', kind: 'text', text: '第一段', dispatchId: 'd1' }, c);
  // 后端已 append（整轮结束前不可能，但这是竞态窗口：append 与 done 之间）
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [{ role: 'user', content: '任务说明书' }, { role: 'assistant', content: '第一段' }]),
  }, c);
  assert.deepEqual(texts(c.sessionStates['child-1'].blocks), ['任务说明书', '第一段'],
    '同一条回复不许出现两遍（按正文去重）');
  // 后续 delta 续写同一块（不是另开一块把回复劈成两半）
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', kind: 'text', text: '第二段', dispatchId: 'd1' }, c);
  assert.deepEqual(texts(c.sessionStates['child-1'].blocks), ['任务说明书', '第一段第二段']);
});

test('②b 快照落后几个 delta（前缀）→ 取本地更长的那份，不重复', () => {
  const c = makeController();
  openChild(c);
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', kind: 'text', text: 'ABCD', dispatchId: 'd1' }, c);
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [{ role: 'user', content: '任务说明书' }, { role: 'assistant', content: 'AB' }]),
  }, c);
  assert.deepEqual(texts(c.sessionStates['child-1'].blocks), ['任务说明书', 'ABCD'],
    '快照是前缀 → 同一条回复，正文取本地那份（delta 只增不减）');
});

test('③ 关闭期间实时流 → 重开重拉 → 不丢不重复（child-reopen 契约不变）', () => {
  const c = makeController();
  openChild(c, [{ role: 'user', content: '任务说明书' }, { role: 'assistant', content: '第一轮结论' }]);
  handleAgentEvent({ type: 'userMessage', sessionId: 'parent-1', text: '（续跑指令）', dispatchId: 'd1', task: true }, c);
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', kind: 'text', text: '第二轮正在写', dispatchId: 'd1' }, c);
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [
      { role: 'user', content: '任务说明书' },
      { role: 'assistant', content: '第一轮结论' },
      { role: 'user', content: '（续跑指令）' },
      { role: 'assistant', content: '第二轮正在写结论' },
    ]),
  }, c);
  const all = texts(c.sessionStates['child-1'].blocks);
  assert.deepEqual(all, ['任务说明书', '第一轮结论', '（续跑指令）', '第二轮正在写结论'],
    '历史完整重建，且流式尾巴按正文去重（不重复）');
});

test('④ 工具行由快照重建，不接重复（实时尾巴只认流式 assistant）', () => {
  const c = makeController();
  openChild(c);
  handleAgentEvent({ type: 'toolCall', sessionId: 'parent-1', id: 'c1', name: 'read_file', arguments: '{}', dispatchId: 'd1' }, c);
  handleAgentEvent({ type: 'toolResult', sessionId: 'parent-1', id: 'c1', name: 'read_file', content: '内容', isError: false, dispatchId: 'd1' }, c);
  // 工具行已落库（历史里有 assistant.tool_calls + tool 结果）
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [
      { role: 'user', content: '任务说明书' },
      { role: 'assistant', content: '', tool_calls: [{ id: 'c1', function: { name: 'read_file', arguments: '{}' } }] },
      { role: 'tool', tool_call_id: 'c1', content: '内容' },
    ]),
  }, c);
  const tools = c.sessionStates['child-1'].blocks.filter((b) => b.kind === 'tool');
  assert.equal(tools.length, 1, '工具行由快照重建，不接重复');
  assert.equal(tools[0].result, '内容');
});

test('⑤ 主会话同样受保护：leafChanged 重拉不冲掉正在流的回复', () => {
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: 's1', operationError: null, context: null, historyReady: true };
  s = reduce(s, { type: 'delta', kind: 'text', text: '实时正文' });
  s = reduce(s, {
    type: 'historyLoaded', sessionId: 's1',
    history: { sessionId: 's1', messages: [{ role: 'user', content: '问题' }], busy: false, pending: null, todos: [] },
  });
  assert.deepEqual(texts(s.blocks), ['问题', '实时正文'], '切叶副作用重拉（快照 busy 可能是 false）不冲掉流式块');
});
