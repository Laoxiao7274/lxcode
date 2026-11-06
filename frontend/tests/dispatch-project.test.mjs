// run_task 派发卡显示项目归属（「coder · lxcode」消解「助手自己派了代码 Agent」
// 的误读，2026-12 用户报）：
// ① wire 映射：project_name 可选键——带值映射、缺席不写键（键缺席纪律）；
// ② 归约：dispatchStart 的 projectName 进卡、复用已有卡时以新载荷为准（续跑
//    没带项目名就清掉旧值，不顶旧项目显示）；
// ③ 渲染：DispatchCard 有 projectName 显示「{agentName} · {projectName}」，
//    缺席时现状不变（主域派发只显示 Agent 名）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { mapEvent } from '../src/agent/ws/events.ts';
import { reduce } from '../src/shared/store.ts';
import { Block } from '../src/components/thread/blocks/Block.tsx';
import { AgentsProvider } from '../src/shared/agents.tsx';
import { SettingsProvider } from '../src/shared/settings.tsx';

const base = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null, context: null, historyReady: false };

// ---------- ① wire 映射 ----------

test('① dispatchStart 带 project_name → 映射成 projectName', () => {
  const ev = mapEvent('chat.dispatchStart', {
    owner_session_id: 'asst-1', dispatch_id: 'd1', session_id: 'child-1',
    agent_id: 'coder', agent_name: '代码 Agent', agent_color: '#3b82f6',
    task: '修一下构建', project_name: 'lxcode',
  });
  assert.equal(ev.projectName, 'lxcode');
});

test('① project_name 缺席 → 不写 projectName 键（常规 agent_dispatch 委派，键缺席纪律）', () => {
  const ev = mapEvent('chat.dispatchStart', {
    owner_session_id: 'm', dispatch_id: 'd',
    agent_id: 'coder', agent_name: '代码', agent_color: '#f00', task: 't',
  });
  assert.equal('projectName' in ev, false, '缺席时不许写 projectName: undefined');
});

// ---------- ② 归约 ----------

test('② projectName 进卡；复用已有卡时缺席清旧值（续跑不顶旧项目显示）', () => {
  let s = reduce(base, {
    type: 'dispatchStart', dispatchId: 'd1', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码 Agent', agentColor: '#3b82f6',
    task: '修一下构建', projectName: 'lxcode',
  });
  let card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.projectName, 'lxcode');
  // 同一 dispatch_id 再派（续跑形态）：这次没带项目名 → 旧值清掉
  s = reduce(s, {
    type: 'dispatchStart', dispatchId: 'd1', childSessionId: 'child-2',
    agentId: 'coder', agentName: '代码 Agent', agentColor: '#3b82f6', task: '再修一次',
  });
  card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.projectName, undefined, '缺席 = 这轮没带，不许顶上一轮的旧项目');
});

test('② 常规委派（无 projectName）卡不带 projectName 键', () => {
  const s = reduce(base, {
    type: 'dispatchStart', dispatchId: 'd2', childSessionId: 'child-9',
    agentId: 'coder', agentName: '代码 Agent', agentColor: '#3b82f6', task: '跑测试',
  });
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal('projectName' in card, false);
});

// ---------- ③ 渲染 ----------

// SettingsProvider/AgentsProvider 静态渲染所需的最小 source（渲染期只读
// 存在性，useEffect 在 SSR 不执行——与 attachments.test.mjs 同款桩）。
function fakeSource() {
  return {
    subscribe: () => () => {},
    send: () => {}, setApproval: async () => {}, confirm: async () => {}, answer: async () => {},
    cancel: () => {}, compact: async () => ({ compacted: false }), rewind: async () => ({ removed: 0 }),
    newSession: async () => 's1', mergeRequest: async () => 'j1',
    resumeSession: () => {}, renameSession: () => {}, archiveSession: async () => ({ ok: true }),
    childHistory: async () => ({ messages: [] }),
    sessions: () => [], projects: () => [], addProject: () => {},
    readInstructions: async () => ({ content: '', exists: false }),
    saveInstructions: async () => {}, label: 'demo',
  };
}

const renderBlock = (el) => renderToStaticMarkup(
  createElement(SettingsProvider, { source: fakeSource() },
    createElement(AgentsProvider, { source: fakeSource() }, el)),
);

const dispatchBlock = (over = {}) => ({
  kind: 'dispatch', uid: 1, id: 'd1', sessionId: 'child-1',
  agentId: 'coder', agentName: '代码 Agent', agentColor: '#3b82f6',
  task: '修一下构建', status: 'running', subBlocks: [], ...over,
});

test('③ run_task 派发卡显示「代码 Agent · lxcode」', () => {
  const html = renderBlock(createElement(Block, {
    block: dispatchBlock({ projectName: 'lxcode' }),
    onConfirm: () => {},
  }));
  assert.ok(html.includes('代码 Agent · lxcode'), html);
});

test('③ 常规委派（projectName 缺席）只显示 Agent 名——现状不变', () => {
  const html = renderBlock(createElement(Block, {
    block: dispatchBlock(),
    onConfirm: () => {},
  }));
  assert.ok(html.includes('代码 Agent'), html);
  assert.ok(!html.includes('· lxcode'), '主域派发不许出现项目后缀');
});

test('③ 注册表名回落时同样带项目后缀（回放路径 agentName 为空）', () => {
  const html = renderBlock(createElement(Block, {
    block: dispatchBlock({ agentName: '', projectName: 'demo' }),
    onConfirm: () => {},
  }));
  // 种子 coder 的名字兜底后拼项目名——不许把项目名吞掉
  assert.match(html, /· demo/);
});
