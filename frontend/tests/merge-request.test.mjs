// 合并进程的前端契约（第四批）：合并子会话 = 子 Agent 同款标签页（dispatchStart
// 挂卡 + 双投实时流 + 提问卡在标签页里回答），后台任务面板的「合并请求」入口。
//
// 后端只复用既有事件（chat.dispatchStart/End），所以前端这里测三件事：
//   1. 归约：合并任务的 dispatchStart 让主会话出现摘要卡；子会话 state 装载后，
//      带 dispatch_id 的 ask 提问落进子会话 state（标签页内可回答）；
//   2. WSAgent.mergeRequest 走 chat.mergeRequest 协议方法（参数与 job_id 回读）；
//   3. 面板入口：未分组会话禁用并说明，项目会话可用（按钮文案）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { reduce, reduceSessionStates } from '../src/shared/store.ts';
import { WSAgent } from '../src/agent/ws/index.ts';
import { DemoAgent } from '../src/agent/demo/index.ts';
import { JobsProvider } from '../src/shared/jobs-admin.tsx';
import { JobsPanel } from '../src/components/jobs/JobsPanel.tsx';

const base = { blocks: [], busy: false, pending: null, todos: [], currentId: '', operationError: null, context: null, historyReady: false };

/** 合并任务的 dispatchStart（后端 RunAgentTask 广播，owner=父会话、
 *  dispatch_id=子会话 id——与确认/提问代理同一个归属键）。 */
const MERGE_START = {
  type: 'dispatchStart', sessionId: 'main',
  dispatchId: 'child-merge', childSessionId: 'child-merge',
  agentId: 'merger', agentName: '合并 Agent', agentColor: '#3b82f6',
  task: '把源会话分支 lxcode/session-x 的改动合并到目标分支 lxcode/integration',
};

const ASK = {
  id: 'ask-1', name: 'ask_user',
  arguments: '{"question":"两边都改了 main.go，保留哪边?","options":["我的","对方的"]}',
  prompt: '两边都改了 main.go，保留哪边?', dispatch_id: 'child-merge', kind: 'ask',
};

test('合并任务的 dispatchStart 在主会话挂摘要卡（点开进子会话标签页的那张）', () => {
  const s = reduce(base, MERGE_START);
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.ok(card, '主会话时间线应有 dispatch 卡');
  assert.equal(card.id, 'child-merge');
  assert.equal(card.sessionId, 'child-merge', '卡上记着子会话 id（标签页寻址依据）');
  assert.equal(card.agentName, '合并 Agent');
  assert.equal(card.status, 'running');
});

test('合并子会话的 ask 提问落进子会话 state（标签页内可回答），主会话卡内同步一份', () => {
  let all = {};
  all = reduceSessionStates(all, MERGE_START);
  // 打开标签页：childHistory 装载（historyLoaded，sessionId 是子会话自己的）
  all = reduceSessionStates(all, {
    type: 'historyLoaded', sessionId: 'child-merge',
    history: { sessionId: 'child-merge', messages: [{ role: 'user', content: MERGE_START.task }], busy: true, pending: null, todos: [] },
  });
  // 提问（chat.confirmRequest，dispatch_id 归属）：双投进子会话 state
  all = reduceSessionStates(all, { type: 'confirmRequest', sessionId: 'main', request: ASK });
  const child = all['child-merge'];
  assert.ok(child, '子会话 state 应已建立');
  assert.deepEqual(child.pending, ASK, '子会话标签页里应挂着这个提问（可回答）');
  const card = all.main.blocks.find((b) => b.kind === 'dispatch');
  const sub = card.subBlocks.find((b) => b.kind === 'confirm');
  assert.ok(sub, '主会话的合并卡内也同步一份（未开标签页时可见可答）');
  // dispatchEnd 定格卡片（合并结论）
  all = reduceSessionStates(all, {
    type: 'dispatchEnd', sessionId: 'main', dispatchId: 'child-merge',
    childSessionId: 'child-merge', result: '合并完成', isError: false,
  });
  assert.equal(all.main.blocks.find((b) => b.kind === 'dispatch').status, 'done');
});

// ---------- WSAgent（live）：chat.mergeRequest 协议方法 ----------

class FakeSocket {
  static latest;
  readyState = 1; sent = []; onopen = null; onmessage = null; onclose = null; onerror = null;
  constructor() { FakeSocket.latest = this; }
  send(data) { this.sent.push(JSON.parse(data)); }
  close() { this.readyState = 3; this.onclose?.(); }
  receive(message) { this.onmessage?.({ data: JSON.stringify({ jsonrpc: '2.0', ...message }) }); }
  reply(result) { this.receive({ id: this.sent.at(-1).id, result }); }
}

function setup(t) {
  const original = globalThis.WebSocket;
  globalThis.WebSocket = FakeSocket;
  t.after(() => { globalThis.WebSocket = original; });
  const agent = new WSAgent('localhost:1234');
  const off = agent.subscribe(() => {});
  t.after(off);
  return { agent, ws: FakeSocket.latest };
}

test('mergeRequest 走 chat.mergeRequest：session_id 必带、target_branch 空则缺省', async (t) => {
  const { agent, ws } = setup(t);
  const p = agent.mergeRequest('s1', 'lxcode/integration');
  assert.equal(ws.sent.at(-1).method, 'chat.mergeRequest');
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 's1', target_branch: 'lxcode/integration' });
  ws.reply({ job_id: 'job-1' });
  assert.equal(await p, 'job-1');

  const p2 = agent.mergeRequest('s1');
  assert.deepEqual(ws.sent.at(-1).params, { session_id: 's1' }, '空目标分支 = 后端默认，不传键');
  ws.reply({ job_id: 'job-2' });
  assert.equal(await p2, 'job-2');
});

test('mergeRequest 缺 job_id 应报错（不静默返回空串）', async (t) => {
  const { agent, ws } = setup(t);
  const p = agent.mergeRequest('s1');
  ws.reply({});
  await assert.rejects(p, /任务 id/);
});

// ---------- DemoAgent：简单模拟（不假造后端事件） ----------

test('演示模式：mergeRequest 可用（返回演示任务 id，UI 链路不炸）', async () => {
  const agent = new DemoAgent();
  assert.equal(await agent.mergeRequest('s1'), 'demo-merge');
});

// ---------- 面板入口：未分组禁用并说明，项目会话可用 ----------

function renderPanel(t, merge) {
  const demo = new DemoAgent();
  const html = renderToStaticMarkup(
    createElement(JobsProvider, { source: demo },
      createElement(JobsPanel, { merge })),
  );
  t.after(() => demo.close?.());
  return html;
}

test('面板合并入口：未分组会话禁用，显示「当前会话没有独立工作区」', (t) => {
  const html = renderPanel(t, { sessionId: 's1', workspace: '', onStart: async () => {} });
  assert.ok(html.includes('合并请求'), '面板应有合并请求入口');
  assert.ok(html.includes('当前会话没有独立工作区'), '禁用原因要说清');
  assert.ok(/disabled/.test(html), '输入与按钮应禁用');
});

test('面板合并入口：项目会话可用，默认目标分支 lxcode/integration', (t) => {
  const html = renderPanel(t, { sessionId: 's1', workspace: 'p1', onStart: async () => {} });
  assert.ok(!html.includes('当前会话没有独立工作区'), '项目会话不该显示禁用说明');
  assert.ok(html.includes('lxcode/integration'), '分支输入默认 lxcode/integration');
  assert.ok(html.includes('发起合并'), '应有发起按钮');
});
