import test from 'node:test';
import assert from 'node:assert/strict';
import { reduce, reduceSessionStates, resolveConfirmEverywhere, checkpointBody } from '../src/shared/store.ts';
const state = { blocks: [{ kind: 'assistant', uid: 1, content: 'partial', reasoning: '', streaming: true }], busy: true, pending: { id: 'c' }, todos: [{ content: 'work', status: 'active' }], currentId: 's1', operationError: null, context: null };
test('会话列表变化不改变任何 Session 的运行态', () => {
  for (const reason of ['renamed', 'archived', 'started']) {
    assert.equal(reduce(state, { type: 'sessionChanged', id: 'other', reason }), state);
  }
  const withErr = reduce(state, { type: 'operationError', message: 'failure' });
  assert.equal(withErr.operationError, 'failure');
  assert.equal(withErr.blocks, state.blocks);
});
test('并发 Session 的消息、busy 与确认状态互不串线', () => {
  let all = {};
  all = reduceSessionStates(all, { type: 'userMessage', sessionId: 's1', text: '第一路' });
  all = reduceSessionStates(all, { type: 'busy', sessionId: 's1', busy: true });
  all = reduceSessionStates(all, { type: 'userMessage', sessionId: 's2', text: '第二路' });
  all = reduceSessionStates(all, { type: 'busy', sessionId: 's2', busy: true });
  all = reduceSessionStates(all, { type: 'todoUpdated', sessionId: 's2', items: [{ content: 's2 todo', status: 'active' }] });
  all = reduceSessionStates(all, { type: 'confirmRequest', sessionId: 's2', request: { id: 'confirm-2', name: 'bash', arguments: '{}', prompt: 'run' } });
  assert.equal(all.s1.busy, true);
  assert.equal(all.s1.blocks.length, 1);
  assert.equal(all.s1.pending, null);
  assert.deepEqual(all.s1.todos, []);
  assert.equal(all.s2.busy, true);
  assert.equal(all.s2.pending.id, 'confirm-2');
  assert.deepEqual(all.s2.todos, [{ content: 's2 todo', status: 'active' }]);
  all = reduceSessionStates(all, { type: 'busy', sessionId: 's1', busy: false });
  assert.equal(all.s1.busy, false);
  assert.equal(all.s2.busy, true, '停止一条会话不能解除另一条的 busy');
});
test('切回生成中的已缓存 Session 时 history 不覆盖实时回复与 dispatch 卡', () => {
  let all = {};
  all = reduceSessionStates(all, {
    type: 'historyLoaded', sessionId: 's1',
    history: { sessionId: 's1', messages: [{ role: 'user', content: '之前的问题' }], busy: false, pending: null, todos: [] },
  });
  all = reduceSessionStates(all, { type: 'userMessage', sessionId: 's1', text: '问题' });
  all = reduceSessionStates(all, { type: 'busy', sessionId: 's1', busy: true });
  all = reduceSessionStates(all, { type: 'delta', sessionId: 's1', kind: 'text', text: '已经显示的部分回复' });
  all = reduceSessionStates(all, { type: 'dispatchStart', sessionId: 's1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: '继续工作' });
  const liveBlocks = all.s1.blocks;
  all = reduceSessionStates(all, {
    type: 'historyLoaded', sessionId: 's1',
    history: { sessionId: 's1', messages: [{ role: 'user', content: '问题' }], busy: true, pending: null, todos: [] },
  });
  assert.strictEqual(all.s1.blocks, liveBlocks, '正在生成的会话保留实时 block 对象');
  assert.equal(all.s1.blocks.some((block) => block.kind === 'assistant' && block.content === '已经显示的部分回复'), true);
  assert.equal(all.s1.blocks.some((block) => block.kind === 'dispatch' && block.id === 'd1'), true);
  assert.equal(all.s1.busy, true);
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
  s = reduce(s, { type: 'compacted', sessionId: 's1', before: 8000, after: 2400, shadowed: 12, summary: '## 摘要', manual: true });
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
  s = reduce(s, { type: 'dispatchStart', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1', agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: 't' });
  assert.equal(s.blocks[0].sessionId, 'child-1', '子会话 id 应上卡（可续跑/可回放）');
  s = reduce(s, { type: 'compacted', sessionId: 'parent-1', before: 100, after: 40, shadowed: 3, summary: '子摘要', dispatchId: 'd1' });
  assert.equal(s.blocks.length, 1, '子会话的压缩不该插到主时间线');
  const card = s.blocks[0];
  assert.equal(card.subBlocks.length, 1);
  assert.equal(card.subBlocks[0].kind, 'compacted');
  assert.equal(card.subBlocks[0].summary, '子摘要');
  // dispatchEnd 也带子会话 id
  s = reduce(s, { type: 'dispatchEnd', sessionId: 'parent-1', dispatchId: 'd1', childSessionId: 'child-1', result: 'r', isError: false });
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

// ---------- 子事件双投：子会话标签页看到的实时流（2026-09-30 用户拍板） ----------
//
// 子 Agent 的事件按**父会话 id** 广播（后端由父会话的 emitter 发出 + dispatch_id），
// 所以父会话的卡能实时更新；而子会话**自己**那个 id 的 state 也要拿到同一份——
// 否则子会话标签页只有一次性的历史快照（用户报的「点开之后他里面就没有接着思考」）。

/** 造一个「父会话里已挂 d1 卡（子会话 child-1）+ 子会话自己的 state 已建」的两路局面。 */
function withChild() {
  let all = {};
  all = reduceSessionStates(all, { type: 'userMessage', sessionId: 'parent', text: '派活' });
  all = reduceSessionStates(all, {
    type: 'dispatchStart', sessionId: 'parent', dispatchId: 'd1', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: '改代码',
  });
  // 打开子会话标签页 → 装载历史（App 的 ChildSessionPage 走 source.childHistory）
  all = reduceSessionStates(all, {
    type: 'historyLoaded', sessionId: 'child-1',
    history: { sessionId: 'child-1', messages: [{ role: 'user', content: '改代码' }], busy: false, pending: null, todos: [] },
  });
  return all;
}

test('子事件双投：父会话的卡与子会话自己的时间线各拿一份（实时流）', () => {
  let all = withChild();
  all = reduceSessionStates(all, { type: 'delta', sessionId: 'parent', kind: 'reasoning', text: '先看 runTools', dispatchId: 'd1' });
  all = reduceSessionStates(all, { type: 'delta', sessionId: 'parent', kind: 'text', text: '改完了', dispatchId: 'd1' });
  all = reduceSessionStates(all, { type: 'toolCall', sessionId: 'parent', dispatchId: 'd1', id: 'tc1', name: 'edit', arguments: '{}' });
  all = reduceSessionStates(all, { type: 'toolResult', sessionId: 'parent', dispatchId: 'd1', id: 'tc1', name: 'edit', content: '已替换', isError: false });

  // 父会话：卡里拿到同一份（卡的状态/摘要来源），主时间线上**不**出现子 Agent 的正文
  const card = all.parent.blocks.find((b) => b.kind === 'dispatch' && b.id === 'd1');
  // 两条 delta 合成一个 assistant 块、toolCall 建工具行、toolResult 就地回填 → 卡里 2 个块
  assert.equal(card.subBlocks.length, 2, '父会话的卡里应累积子事件（assistant + tool）');
  assert.equal(card.subBlocks.find((b) => b.kind === 'tool').result, '已替换', 'toolResult 要回填到卡里的工具行');
  assert.equal(all.parent.blocks.some((b) => b.kind === 'assistant' && b.content === '改完了'),
    false, '子 Agent 的正文不许进主时间线');

  // 子会话：自己的时间线拿到**同一份**事件的普通形态（历史那条 user 消息 + 实时块）
  const kinds = all['child-1'].blocks.map((b) => b.kind);
  assert.deepEqual(kinds, ['user', 'assistant', 'tool'], '子会话自己应拿到 user(历史) + assistant(实时) + tool(实时)');
  const assistant = all['child-1'].blocks.find((b) => b.kind === 'assistant');
  assert.equal(assistant.content, '改完了');
  assert.equal(assistant.reasoning, '先看 runTools');
  const tool = all['child-1'].blocks.find((b) => b.kind === 'tool');
  assert.equal(tool.result, '已替换', 'toolResult 也要按 id 回填到子会话自己那条工具行');
});

test('子会话自己的 state 还不存在时不凭空建（半截时间线）——标签页装载时用历史重建', () => {
  let all = {};
  all = reduceSessionStates(all, { type: 'userMessage', sessionId: 'parent', text: '派活' });
  all = reduceSessionStates(all, {
    type: 'dispatchStart', sessionId: 'parent', dispatchId: 'd1', childSessionId: 'child-1',
    agentId: 'coder', agentName: '代码 Agent', agentColor: '#000', task: '改代码',
  });
  all = reduceSessionStates(all, { type: 'delta', sessionId: 'parent', kind: 'text', text: '边跑边说', dispatchId: 'd1' });
  assert.equal(all['child-1'], undefined, '子会话 state 没建起来之前不许凭空拼一份半截时间线');
  assert.equal(all.parent.blocks.find((b) => b.kind === 'dispatch').subBlocks.length, 1, '父会话的卡照旧拿到子事件');
});

test('子会话自己的压缩标记落在它自己的时间线里（不是卡里）', () => {
  let all = withChild();
  all = reduceSessionStates(all, {
    type: 'compacted', sessionId: 'parent', dispatchId: 'd1',
    before: 9000, after: 1200, shadowed: 8, summary: '## 摘要', manual: false,
  });
  const child = all['child-1'].blocks.find((b) => b.kind === 'compacted');
  assert.ok(child, '子会话自己的压缩要在它自己的时间线上插标记块');
  assert.equal(child.summary, '## 摘要');
});

test('子会话的确认卡双投，裁决时两处一起定格（不留永远待确认的僵尸卡）', () => {
  let all = withChild();
  const request = { id: 'confirm-9', name: 'bash', arguments: '{}', prompt: '跑测试', dispatch_id: 'd1' };
  all = reduceSessionStates(all, { type: 'confirmRequest', sessionId: 'parent', request });
  // 两处都挂上了
  const cardConfirm = all.parent.blocks.find((b) => b.kind === 'dispatch').subBlocks
    .find((s) => s.kind === 'confirm');
  assert.ok(cardConfirm, '父会话的卡里要有这张确认（用户没开子会话标签也得能批）');
  assert.ok(all['child-1'].blocks.some((b) => b.kind === 'confirm' && b.request.id === 'confirm-9'),
    '子会话自己的时间线里也要有（标签页里能批）');

  // 裁决：两处一起定格
  all = resolveConfirmEverywhere(all, 'confirm-9', 'allow');
  const cardTool = all.parent.blocks.find((b) => b.kind === 'dispatch').subBlocks.find((s) => s.id === 'confirm-9');
  assert.equal(cardTool.kind, 'tool', '卡里那张确认要就地变成工具行（允许）');
  const childTool = all['child-1'].blocks.find((b) => b.id === 'confirm-9');
  assert.equal(childTool.kind, 'tool', '子会话里那张也要一起定格——只定格一处会永远挂着「待确认」');
  assert.equal(all.parent.pending, null);
  assert.equal(all['child-1'].pending, null);
});

test('裁决只碰真的有这个确认的会话（不许清别人的挂起确认）', () => {
  let all = withChild();
  all = reduceSessionStates(all, {
    type: 'confirmRequest', sessionId: 'other', request: { id: 'confirm-other', name: 'bash', arguments: '{}', prompt: '别的' },
  });
  all = resolveConfirmEverywhere(all, 'confirm-9', 'allow');
  assert.equal(all.other.pending.id, 'confirm-other', '裁决别的确认不许动这条挂起确认');
});

test('子会话的 historyLoaded 重建它自己、不动主时间线（按 sessionId 路由）', () => {
  let all = withChild();
  const before = all.parent.blocks;
  all = reduceSessionStates(all, {
    type: 'historyLoaded', sessionId: 'child-1',
    history: {
      sessionId: 'child-1', busy: false, pending: null, todos: [], model: 'deepseek-chat',
      messages: [{ role: 'user', content: '改代码' }, { role: 'assistant', content: '改完了' }],
    },
  });
  assert.strictEqual(all.parent.blocks, before, '子会话的历史重建不许碰父会话的块');
  assert.equal(all['child-1'].blocks.length, 2);
  assert.equal(all['child-1'].model, 'deepseek-chat', '子会话页头要显示**它自己的**模型');
});

