// 子会话临时视图「关闭 → 重开」的数据面钉子（2026-12 用户报：关闭子会话视图 →
// 操作其他会话 → 再点派发卡回来，子 Agent 的历史丢了——空白或只剩实时新事件）。
//
// 前端契约（ChildSessionPage 每次挂载都重新 source.childHistory；store 对
// historyLoaded 一律全量重建 blocks，没有「已装载就跳过」的守卫）：
//   ① 第一次打开：historyLoaded（子会话 id）把历史建成 blocks；
//   ② 关闭视图后子会话继续跑：带 dispatch_id 的实时事件**双投**进子会话 state
//      （视图不在也照收——state 已存在）；
//   ③ 重开：再来一条 historyLoaded（全量历史）必须**完整重建**——关闭期间
//      实时积累的块被历史快照整体替换，既不丢（历史 ⊇ 已见事件落库后的内容）
//      也不重复（重建是替换不是追加）；
//   ④ state 已存在且 historyReady=true 时重建照常发生（没有「只拉一次」守卫——
//      谁加守卫，重开就是陈旧/空白时间线，这条就红）。
//
// 服务端侧的根因与修复见 internal/server/child_history_fresh_test.go（子会话
// 运行时不进 s.sessions 缓存——缓存的那份是 AttachTo 时刻的静态快照）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { handleAgentEvent } from '../src/shared/store.ts';

/** 与 child-busy.test.mjs ③ 同款的可变 controller。 */
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

/** 子会话历史快照（chat.history 回放形态：消息是落库的 llm.Message）。 */
const snapshot = (sessionId, messages, overrides = {}) => ({
  sessionId,
  busy: false,
  pending: null,
  todos: [],
  checkpoints: [],
  messages,
  ...overrides,
});

test('① 首次打开：historyLoaded（子会话 id）建成子会话自己的 state', () => {
  const c = makeController();
  handleAgentEvent({ type: 'dispatchStart', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: 't' }, c);
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [
      { role: 'user', content: '任务说明书' },
      { role: 'assistant', content: '第一轮结论' },
    ]),
  }, c);
  const child = c.sessionStates['child-1'];
  assert.ok(child, '子会话 state 应由 historyLoaded 建（ChildSessionPage 画的就是它）');
  assert.equal(child.blocks.length, 2);
  assert.equal(child.historyReady, true);
});

test('②③④ 关闭 → 实时双投继续 → 重开全量重建：不丢、不重复、无「只拉一次」守卫', () => {
  const c = makeController();
  handleAgentEvent({ type: 'dispatchStart', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: 't' }, c);
  // ① 首次打开（此刻子会话刚跑完第一轮）
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [
      { role: 'user', content: '任务说明书' },
      { role: 'assistant', content: '第一轮结论' },
    ]),
  }, c);
  // ② 关闭视图，子会话继续跑：实时事件双投进子会话 state（视图不在也照收）
  handleAgentEvent({ type: 'userMessage', sessionId: 'parent-1', text: '（续跑指令）', dispatchId: 'd1', task: true }, c);
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', kind: 'text', text: '第二轮正在', dispatchId: 'd1' }, c);
  handleAgentEvent({ type: 'delta', sessionId: 'parent-1', kind: 'text', text: '写结论', dispatchId: 'd1' }, c);
  const whileClosed = c.sessionStates['child-1'];
  assert.ok(whileClosed.blocks.length > 2, '关闭期间实时事件应继续双投（state 已存在）');
  // ③ 重开视图：ChildSessionPage 重新拉历史 → 全量 historyLoaded（含关闭期间的轮次）
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [
      { role: 'user', content: '任务说明书' },
      { role: 'assistant', content: '第一轮结论' },
      { role: 'user', content: '（续跑指令）' },
      { role: 'assistant', content: '第二轮正在写结论' },
    ]),
  }, c);
  const reopened = c.sessionStates['child-1'];
  const texts = reopened.blocks.map((b) => b.content ?? b.text ?? '');
  assert.deepEqual(texts, ['任务说明书', '第一轮结论', '（续跑指令）', '第二轮正在写结论'],
    '重开后历史必须完整重建：任务 + 每轮结论都在，实时流被快照整体替换');
  // 不重复：第一轮结论只出现一次（重建是替换不是追加）
  assert.equal(texts.filter((t) => t === '第一轮结论').length, 1);
  // ④ 再来一次 historyLoaded（state 已存在且 historyReady=true）重建照常发生——
  //    「只拉一次」守卫会让第二次重开停在陈旧时间线上
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'child-1',
    history: snapshot('child-1', [
      { role: 'user', content: '任务说明书' },
      { role: 'assistant', content: '第一轮结论' },
      { role: 'user', content: '（续跑指令）' },
      { role: 'assistant', content: '第二轮正在写结论' },
      { role: 'assistant', content: '第三轮补充' },
    ]),
  }, c);
  const again = c.sessionStates['child-1'];
  assert.ok(again.blocks.some((b) => (b.content ?? '') === '第三轮补充'),
    'state 已装载后再次 historyLoaded 仍必须全量重建（不许「只拉一次」）');
  assert.equal(again.blocks.length, 5, '重建后块数与历史一致（无累积重复）');
});
