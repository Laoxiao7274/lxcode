// 子会话附件回传主会话（docs/child-session-ui.md §2.6）：
// ① 实时路径——agent_dispatch 的 toolResult 事件带 attachments → 挂到 dispatch 卡；
// ② 没带附件的调度结果照旧 no-op（与旧行为一致）；
// ③ 回放路径——调度结果消息上的同一份元数据 → historyBlocks 的 dispatch 卡分支；
// ④ 两条路径产出同一份形状（同一组件渲染）。
import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce } from '../src/shared/store.ts';
import { historyBlocks } from '../src/shared/history.ts';

const base = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null, context: null };
const att = { path: 'child-1/abc.png', name: 'chart.png', size: 1200, kind: 'image', caption: '本月数据' };

function startCard(state) {
  return reduce(state, { type: 'dispatchStart', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#3b82f6', task: '把图共享' });
}

test('实时：dispatch 的 toolResult 带附件 → 挂到卡（卡头形态不动）', () => {
  let s = startCard(base);
  s = reduce(s, { type: 'dispatchEnd', dispatchId: 'd1', result: '已共享', isError: false, usageTokens: 10, childSessionId: 'child-1' });
  s = reduce(s, { type: 'toolResult', id: 'd1', name: 'agent_dispatch', content: '已共享\n\n[附件] chart.png → attachments/child-1/abc.png', isError: false, attachments: [att] });
  const card = s.blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.status, 'done');
  assert.deepEqual(card.attachments, [att], '附件挂到卡上（成果条）');
  // 卡头一行摘要的形态不变：没有子过程块、没有结论正文渲染位
  assert.equal(card.subBlocks.length, 0);
});

test('实时：没带附件的 dispatch toolResult 是 no-op（不碰卡）', () => {
  let s = startCard(base);
  const before = s.blocks[0];
  s = reduce(s, { type: 'toolResult', id: 'd1', name: 'agent_dispatch', content: 'ok', isError: false });
  assert.equal(s.blocks[0], before, '对象都不换（引用相等）');
  // 卡不存在时同样 no-op（不炸）
  const s2 = reduce(base, { type: 'toolResult', id: 'ghost', name: 'agent_dispatch', content: 'x', isError: false, attachments: [att] });
  assert.deepEqual(s2.blocks, []);
});

test('回放：调度结果消息的 attachments 挂到 dispatch 卡（与实时同形状）', () => {
  const blocks = historyBlocks({
    messages: [
      { role: 'user', content: '派个 Agent 把图发给用户', seq: 1 },
      { role: 'assistant', content: '', tool_calls: [{ id: 'd1', function: { name: 'agent_dispatch', arguments: '{"agent":"coder","task":"把图共享"}' } }] },
      { role: 'tool', tool_call_id: 'd1', content: '已共享\n\n[附件] chart.png → attachments/child-1/abc.png\n\n[子会话 id: child-1 —— 只在这次**没做完**时填进 session 参数续跑；已经给出结论就别再派]', attachments: [att] },
    ],
  });
  const card = blocks.find((b) => b.kind === 'dispatch');
  assert.equal(card.status, 'done');
  assert.equal(card.sessionId, 'child-1');
  assert.deepEqual(card.attachments, [att], '回放路径与实时路径同一份形状');
});

test('回放：老历史的调度结果没有 attachments → 卡不带附件键（键缺席纪律）', () => {
  const blocks = historyBlocks({
    messages: [
      { role: 'user', content: '派活', seq: 1 },
      { role: 'assistant', content: '', tool_calls: [{ id: 'd1', function: { name: 'agent_dispatch', arguments: '{"agent":"coder","task":"t"}' } }] },
      { role: 'tool', tool_call_id: 'd1', content: '结论' },
    ],
  });
  const card = blocks.find((b) => b.kind === 'dispatch');
  assert.equal('attachments' in card, false, '老历史不出空附件条');
});
