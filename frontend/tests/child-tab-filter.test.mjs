// 子会话标签按需显示（体验修复批次 5）：持久子→父映射 + 标签过滤 + 标题反查。
//
// 三条被钉住的行为：
// ① 过滤规则——只显示「父会话是当前活跃主会话」的子会话标签；active 的子标签
//    本身恒可见（历史导航到它时不能悬空）；映射缺失的不可见（猜父 = 挂错地方）。
// ② 映射的两个数据源——dispatchStart 事件自带的 owner_session_id（实时）与
//    historyLoaded 回放时对 blocks 的一次性扫描（刷新/冷恢复），都驱动真实现
//    （handleAgentEvent），不复刻副本。
// ③ 标题反查——childTabTitle 必须喂**父会话**的 blocks（App 现在按映射取父）；
//    喂当前活跃会话的 blocks 是被修掉的缺陷（切走后回落 id 前缀）。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  childLinksFromBlocks,
  childTabTitle,
  recordChildLink,
  recordChildParent,
  visibleChildTabs,
} from '../src/shared/workspace-tabs.ts';
import { handleAgentEvent } from '../src/shared/store.ts';
import { initial } from '../src/shared/blocks.ts';

// ---------- 映射的记录 ----------

test('recordChildParent：记录、幂等、空 id 忽略', () => {
  let parents = recordChildParent({}, 'c1', 'p1');
  assert.deepEqual(parents, { c1: 'p1' });
  // 同一条重复记录 → 同一对象（不触发无谓的重渲染）
  assert.equal(recordChildParent(parents, 'c1', 'p1'), parents);
  // 空 childId / 空 parentId 都是坏数据，不进表
  assert.equal(recordChildParent(parents, '', 'p1'), parents);
  assert.equal(recordChildParent(parents, 'c2', ''), parents);
});

test('recordChildLink：一次 dispatch 事实同时更新两张映射（键不同：子会话 id / dispatch id）', () => {
  const empty = { childParents: {}, dispatchChild: {} };
  let links = recordChildLink(empty, { childId: 'c1', parentId: 'p1', dispatchId: 'd1' });
  assert.deepEqual(links.childParents, { c1: 'p1' }, '标签过滤/标题反查用的子→父映射');
  assert.deepEqual(links.dispatchChild, { d1: 'c1' }, '子事件双投归属路由用的 dispatch→子会话映射');
  // 幂等
  assert.equal(recordChildLink(links, { childId: 'c1', parentId: 'p1', dispatchId: 'd1' }), links);
  // 坏数据不进表
  assert.equal(recordChildLink(links, { childId: '', parentId: 'p1' }), links);
  assert.equal(recordChildLink(links, { childId: 'c2', parentId: '' }), links);
});

test('childLinksFromBlocks：从父会话 blocks 扫出它派发的子会话（回放路径，两张映射一起）', () => {
  const blocks = [
    { kind: 'user', uid: 1, text: '开工' },
    { kind: 'dispatch', uid: 2, id: 'd1', sessionId: 'child-a', agentId: 'researcher', agentName: '', agentColor: '', task: '通读 internal/agent', status: 'done', isError: false, result: '读完', subBlocks: [] },
    { kind: 'dispatch', uid: 3, id: 'd2', sessionId: 'child-b', agentId: 'coder', agentName: '', agentColor: '', task: '改代码', status: 'done', isError: false, result: '改完', subBlocks: [] },
    // 没有 sessionId 的卡（老数据/续跑线索缺失）不进映射
    { kind: 'dispatch', uid: 4, id: 'd3', sessionId: undefined, agentId: 'x', agentName: '', agentColor: '', task: '', status: 'done', isError: true, result: '断了', subBlocks: [] },
  ];
  const links = childLinksFromBlocks('p1', blocks);
  assert.deepEqual(links.childParents, { 'child-a': 'p1', 'child-b': 'p1' });
  assert.deepEqual(links.dispatchChild, { d1: 'child-a', d2: 'child-b' });
  assert.deepEqual(childLinksFromBlocks('p1', [{ kind: 'user', uid: 1, text: 'hi' }]), { childParents: {}, dispatchChild: {} });
});

// ---------- 过滤规则 ----------

const PARENTS = { c1: 'p1', c2: 'p2' };
const TABS = ['child:c1', 'child:c2'];

test('① 父会话活跃时显示其子会话标签，切到别的会话后隐藏', () => {
  // 在 p1 的 chat 视图：c1 可见，c2（别人的孩子）不可见
  assert.deepEqual(visibleChildTabs(TABS, 'chat', PARENTS, 'p1'), ['child:c1']);
  // 切到 p2 的会话：反转
  assert.deepEqual(visibleChildTabs(TABS, 'chat', PARENTS, 'p2'), ['child:c2']);
  // 看的是 p1 的子会话标签（c1 活跃）：活跃主会话 = c1 的父 p1 → c1 可见
  assert.deepEqual(visibleChildTabs(TABS, 'child:c1', PARENTS, 'p1'), ['child:c1']);
});

test('① active 的子标签本身恒可见（父映射缺失也不悬空）', () => {
  // c2 活跃但映射里它的父不是 activeParent（映射缺失时 App 回落 currentId 的窗口期）：
  // active 标签不能被过滤掉——历史导航到它却看不见，比多显示一个标签更糟
  assert.deepEqual(visibleChildTabs(TABS, 'child:c2', PARENTS, 'p1'), ['child:c1', 'child:c2']);
  // 映射完全缺失（应用刚起）：非 active 的隐藏，active 的仍在
  assert.deepEqual(visibleChildTabs(TABS, 'chat', {}, 'p1'), []);
  assert.deepEqual(visibleChildTabs(TABS, 'child:c1', {}, 'p1'), ['child:c1']);
});

// ---------- 数据源（驱动真实现 handleAgentEvent） ----------

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

test('① 数据源 A：dispatchStart 自带的 owner_session_id 直接记映射', () => {
  const c = makeController();
  handleAgentEvent({ type: 'dispatchStart', sessionId: 'p1', dispatchId: 'd1', childSessionId: 'c1', agentId: 'researcher', agentName: 'researcher', agentColor: '#888', task: '通读' }, c);
  assert.deepEqual(c.childLinks.childParents, { c1: 'p1' });
  assert.deepEqual(c.childLinks.dispatchChild, { d1: 'c1' }, '同一事件顺带记下双投路由用的 dispatch→子会话映射');
});

test('① 数据源 B：historyLoaded 回放从 blocks 一次性扫出映射（刷新/冷恢复后仍在）', () => {
  const c = makeController();
  // 没有实时 dispatchStart（应用重启后切回老会话）：历史里有那次派发的
  // tool_call（agent_dispatch）与结果（带子会话 id 提示行）
  handleAgentEvent({
    type: 'historyLoaded', sessionId: 'p1',
    history: {
      sessionId: 'p1',
      messages: [
        { role: 'user', content: '派个人去读代码' },
        { role: 'assistant', content: '', tool_calls: [{ id: 't1', function: { name: 'agent_dispatch', arguments: JSON.stringify({ agent: 'researcher', task: '通读 internal/agent' }) } }] },
        { role: 'tool', tool_call_id: 't1', content: '读完\n\n[子会话 id: child-a —— 只在这次没做完时填进 session 参数续跑；已经给出结论就别再派]' },
      ],
      busy: false, pending: null, todos: [],
    },
  }, c);
  assert.deepEqual(c.childLinks.childParents, { 'child-a': 'p1' });
  assert.deepEqual(c.childLinks.dispatchChild, { t1: 'child-a' }, '回放块的工具调用 id 同时记进双投映射');
});

test('① 映射建立后双投查表路由（不再依赖对 blocks 的全量反查）', () => {
  const c = makeController();
  // 子会话 state 已存在（标签打开过、历史已装载），但 dispatch 卡不在任何
  // 会话的 blocks 里（父的历史还没回放）——只有查表能命中
  c.sessionStates = { 'child-a': { ...initial, currentId: 'child-a' } };
  c.childLinks = { childParents: { 'child-a': 'p1' }, dispatchChild: { d1: 'child-a' } };
  handleAgentEvent({ type: 'delta', sessionId: 'p1', kind: 'text', text: '子过程输出', dispatchId: 'd1' }, c);
  assert.equal(
    c.sessionStates['child-a'].blocks.some((b) => b.kind === 'assistant' && b.content === '子过程输出'),
    true,
    '带映射时子事件双投进子会话自己的时间线',
  );
  // 没有映射、卡也不在：不猜归属（原样返回，子会话不被误喂）
  const c2 = makeController();
  c2.sessionStates = { 'child-a': { ...initial, currentId: 'child-a' } };
  handleAgentEvent({ type: 'delta', sessionId: 'p1', kind: 'text', text: '子过程输出', dispatchId: 'd1' }, c2);
  assert.equal(c2.sessionStates['child-a'].blocks.length, 0, '无映射无卡时不双投');
});

// ---------- 标题反查（父 blocks） ----------

const dispatchCard = (sessionId) => ({
  kind: 'dispatch', uid: 1, id: 'd1', sessionId,
  agentId: 'researcher', agentName: 'researcher', agentColor: '#888',
  task: '通读 internal/agent', status: 'done', isError: false, result: '读完', subBlocks: [],
});

test('① childTabTitle 用父 blocks 反查得到完整标题；喂错 blocks 回落 id 前缀（被修掉的缺陷）', () => {
  // 正确喂法（App 现在的链路）：映射拿父 → 父会话的 blocks 里找卡
  assert.equal(childTabTitle([dispatchCard('child-a')], 'child-a'), 'researcher · 通读 internal/agent');
  // 喂错 blocks（如当前活跃会话的不是父）：回落「子会话 <id 前 8 位>」——
  // 这就是切走会话后子标签标题退化成 id 前缀的旧缺陷形态，钉住以区分正确喂法
  assert.equal(childTabTitle([], 'child-a'), '子会话 child-a');
  // 回放块 agentName 为空（展示名是注册表的知识）→ nameOf 回落口
  const replayCard = { ...dispatchCard('child-a'), agentName: '' };
  assert.equal(
    childTabTitle([replayCard], 'child-a', (agentId) => (agentId === 'researcher' ? '研究员' : agentId)),
    '研究员 · 通读 internal/agent',
  );
});
