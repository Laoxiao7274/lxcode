import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce, checkpointBody } from '../src/shared/store.ts';
const state = { blocks: [{ kind: 'assistant', uid: 1, content: 'partial', reasoning: '', streaming: true }], busy: true, pending: { id: 'c' }, todos: [{ content: 'work', status: 'active' }], currentId: 's1', operationError: null, context: null };
test('rename/archive and operation failures preserve current chat, pending and todos', () => {
  for (const reason of ['renamed', 'archived', 'started']) {
    const next = reduce(state, { type: 'sessionChanged', id: 'other', reason });
    // 会话 id 跟随（started 是懒建行——高亮切过去），其余原样
    assert.deepEqual(next, { ...state, currentId: 'other' });
    assert.equal(next.blocks, state.blocks);
  }
  // operationError 是一次性提示——归约收敛（不再是穿透态）
  const withErr = reduce(state, { type: 'operationError', message: 'failure' });
  assert.equal(withErr.operationError, 'failure');
  assert.equal(withErr.blocks, state.blocks);
});
test('new/resumed reset prior conversation and todo state', () => {
  for (const reason of ['new', 'resumed']) {
    assert.deepEqual(reduce(state, { type: 'sessionChanged', id: 'new', reason }), { blocks: [], busy: false, pending: null, todos: [], currentId: 'new', operationError: null, context: null });
  }
});
test('mid-turn usage is cleared when a new assistant block starts (stats only on final answer)', () => {
  // 第一轮 assistant 完成（带 usage）→ 工具行 → 第二轮正文开新块：
  // 中间轮的 usage 应被清——DSH 的 tokens 统计只在整轮末尾显示。
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null, context: null };
  s = reduce(s, { type: 'delta', kind: 'text', text: '我先读一下' });
  s = reduce(s, { type: 'done', usageTokens: 2209, finishReason: 'tool_calls' });
  s = reduce(s, { type: 'toolCall', id: 'c1', name: 'read_file', arguments: '{}' });
  s = reduce(s, { type: 'toolResult', id: 'c1', name: 'read_file', content: 'ok', isError: false });
  // 第二轮开新 assistant 块（delta）——中间轮 usage 被清
  s = reduce(s, { type: 'delta', kind: 'text', text: '读完了' });
  const first = s.blocks.find((b) => b.kind === 'assistant');
  assert.equal(first.usageTokens, undefined, '中间轮 usage 应被清');
  // 整轮最终块正常带 usage
  s = reduce(s, { type: 'done', usageTokens: 120, finishReason: 'stop' });
  const last = s.blocks.filter((b) => b.kind === 'assistant').at(-1);
  assert.equal(last.usageTokens, 120, '最终轮 usage 保留');
});

// ---------- P1 上下文占用（后端测量 → 指示器） ----------

const ctx = { used: 777, window: 32768, system: 300, tool_results: 200, messages: 277, reasoning: 0 };

test('主轮 done 更新上下文占用；没有 assistant 块也要更新', () => {
  // 没有 assistant 块的轮次（纯工具轮）——指示器不能停在旧值
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null, context: null };
  s = reduce(s, { type: 'done', usageTokens: 5, finishReason: 'stop', context: ctx });
  assert.deepEqual(s.context, ctx);
});

test('子轮的 done 不携带占用（指示器只跟主轮走）', () => {
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null, context: ctx };
  s = reduce(s, { type: 'dispatchStart', dispatchId: 'd1', agentId: 'coder', agentName: 'c', agentColor: '#000', task: 't' });
  s = reduce(s, { type: 'done', usageTokens: 730, finishReason: 'stop', dispatchId: 'd1' });
  assert.deepEqual(s.context, ctx, '子轮的 done 不带 context，不应改动主指示器');
});

test('historyLoaded 用后端测量；未知则置 null（不沿用上一会话的数字）', () => {
  let s = { blocks: [], busy: false, pending: null, todos: [], currentId: '', operationError: null, context: ctx };
  s = reduce(s, {
    type: 'historyLoaded',
    history: { sessionId: 's2', messages: [], busy: false, pending: null, todos: [], context: { used: 100, window: 8192 } },
  });
  assert.deepEqual(s.context, { used: 100, window: 8192 });

  // 后端刚重启/刚切会话：没有测量 → null（指示器显示中性态）
  s = reduce(s, {
    type: 'historyLoaded',
    history: { sessionId: 's3', messages: [], busy: false, pending: null, todos: [] },
  });
  assert.equal(s.context, null);
});

// ---------- P3 历史压缩（compaction） ----------

test('compacted 事件插一条标记块（不改动已有块）', () => {
  let s = { blocks: [{ kind: 'user', uid: 1, text: '早先的话' }], busy: false, pending: null, todos: [], currentId: '', operationError: null, context: ctx };
  s = reduce(s, { type: 'compacted', before: 8000, after: 2400, shadowed: 12, summary: '## 摘要', manual: true });
  assert.equal(s.blocks.length, 2, '已有块不该被改动，只追加标记块');
  const card = s.blocks.at(-1);
  assert.equal(card.kind, 'compacted');
  assert.equal(card.before, 8000);
  assert.equal(card.after, 2400);
  assert.equal(card.shadowed, 12);
  assert.equal(card.summary, '## 摘要');
  assert.equal(card.manual, true);
});

// 子会话自己的压缩（带 dispatchId）归属进卡内，不插主时间线。
test('子会话的压缩进卡内（主时间线不受影响）', () => {
  let s = { blocks: [], busy: true, pending: null, todos: [], currentId: '', operationError: null, context: null };
  s = reduce(s, { type: 'dispatchStart', dispatchId: 'd1', sessionId: 'child-1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: 't' });
  assert.equal(s.blocks[0].sessionId, 'child-1', '子会话 id 应上卡（可续跑/可回放）');
  s = reduce(s, { type: 'compacted', before: 100, after: 40, shadowed: 3, summary: '子摘要', dispatchId: 'd1' });
  assert.equal(s.blocks.length, 1, '子会话的压缩不该插到主时间线');
  const card = s.blocks[0];
  assert.equal(card.subBlocks.length, 1);
  assert.equal(card.subBlocks[0].kind, 'compacted');
  assert.equal(card.subBlocks[0].summary, '子摘要');
  // dispatchEnd 也带子会话 id
  s = reduce(s, { type: 'dispatchEnd', dispatchId: 'd1', sessionId: 'child-1', result: 'r', isError: false });
  assert.equal(s.blocks[0].sessionId, 'child-1');
  assert.equal(s.blocks[0].status, 'done');
});

test('historyLoaded 把检查点渲染成标记块（不是用户气泡）', () => {
  const checkpoint = '这是前言\n\n<compacted-summary>\n## 摘要正文\n</compacted-summary>';
  let s = { blocks: [], busy: false, pending: null, todos: [], currentId: '', operationError: null, context: null };
  s = reduce(s, {
    type: 'historyLoaded',
    history: {
      sessionId: 's1', busy: false, pending: null, todos: [],
      messages: [
        { role: 'user', content: checkpoint },
        { role: 'user', content: '真正的问题' },
      ],
      checkpoints: [0],
    },
  });
  assert.equal(s.blocks.length, 2);
  assert.equal(s.blocks[0].kind, 'compacted', '检查点必须是标记块');
  assert.equal(s.blocks[0].summary, '## 摘要正文', '标记块只展示摘要正文（前言与标记剥离）');
  assert.equal(s.blocks[1].kind, 'user');
  assert.equal(s.blocks[1].text, '真正的问题');
});

test('checkpointBody 剥离前言与定界标记；坏数据原样返回', () => {
  assert.equal(checkpointBody('<compacted-summary>\n正文\n</compacted-summary>'), '正文');
  assert.equal(checkpointBody('前言\n<compacted-summary>正文</compacted-summary>尾'), '正文');
  // 标记缺失（坏数据）不该渲染成空白
  assert.equal(checkpointBody('普通用户消息'), '普通用户消息');
  assert.equal(checkpointBody('<compacted-summary>没闭合'), '<compacted-summary>没闭合');
});

