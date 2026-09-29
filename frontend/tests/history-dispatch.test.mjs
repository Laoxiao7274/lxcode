// 回放路径重建 dispatch 卡（用户实测 bug：重启之后子 Agent 卡退化成一行
// 光秃秃的 `agent_dispatch` 工具行——卡片、Agent 名、任务、结论全没了）。
//
// 背景：实时路径（reduce.ts 的 toolCall）**故意不给 agent_dispatch 建工具行**
// （它的渲染形态是 dispatch 卡，随后由 chat.dispatchStart 挂卡）；而回放路径
// （history.ts 的 reduceHistory）原本给**每一个** tool_call 一律建 kind: "tool"
// 行。两条路径分叉 → 刷新/重启之后同一张卡换一张脸。
//
// 这个文件钉住三件事：① 回放为调度调用建**卡**（不是工具行）；② 普通工具行
// 照旧（不许为了修调度把工具行改坏）；③ 回放**不许编数据**——没有结果的中断
// 派发不许假装"执行中"，子块为空是诚实的降级（子会话的明细在它自己的历史里）。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  reduceHistory,
  parseDispatchArgs,
  splitDispatchResult,
  DISPATCH_INTERRUPTED,
  DISPATCH_NO_TEXT,
} from '../src/shared/history.ts';
import { DISPATCH_TOOL_NAME } from '../src/shared/blocks.ts';
import { MAIN_TOOL } from '../src/shared/agent-seeds.ts';
import { reduce } from '../src/shared/store.ts';

const base = {
  blocks: [],
  busy: false,
  pending: null,
  todos: [],
  currentId: 's1',
  operationError: null,
  context: null,
  historyReady: false,
};

/** 后端在 dispatch 结果末尾追加的续跑提示行（internal/agent/tools.go 逐字）。 */
const hint = (childId) =>
  `\n\n[子会话 id: ${childId} —— 只在这次**没做完**时填进 session 参数续跑；已经给出结论就别再派]`;

/** 一条带 tool_calls 的 assistant 消息。 */
const assistant = (calls) => ({ role: 'assistant', content: '我派个子 Agent', tool_calls: calls });
/** 一条 agent_dispatch 调用（**字面量写死**：常量漂移必须让测试红，不能跟着一起漂）。 */
const dispatchCall = (id, args) => ({ id, function: { name: 'agent_dispatch', arguments: JSON.stringify(args) } });
const toolCall = (id, name, args) => ({ id, function: { name, arguments: JSON.stringify(args) } });
const toolResult = (id, content) => ({ role: 'tool', content, tool_call_id: id });
const snap = (messages) => ({ sessionId: 's1', messages, busy: false, pending: null, todos: [] });
const cards = (s) => s.blocks.filter((b) => b.kind === 'dispatch');
const rows = (s) => s.blocks.filter((b) => b.kind === 'tool');

test('判定字面量只有一处来源（回放与实时共用 blocks.ts 的常量）', () => {
  assert.equal(DISPATCH_TOOL_NAME, 'agent_dispatch');
  assert.equal(MAIN_TOOL.id, DISPATCH_TOOL_NAME, '演示目录的条目 id 与判定字符串必须一致');
});

test('① agent_dispatch 的 tool_call 回放成 dispatch 卡（不是工具行）', () => {
  const s = reduceHistory(base, snap([
    assistant([dispatchCall('d1', { agent: 'coder', task: '修掉登录超时' })]),
  ]));
  assert.equal(cards(s).length, 1);
  assert.equal(rows(s).length, 0, '调度调用建了工具行 = 用户看到的那行光秃秃的 agent_dispatch');
  const card = cards(s)[0];
  assert.equal(card.id, 'd1');
  assert.equal(card.agentId, 'coder');
  assert.equal(card.task, '修掉登录超时');
  // 子块为空是**诚实的降级**：子会话的明细在它自己的历史里（父会话历史没有）
  assert.deepEqual(card.subBlocks, []);
  // 实时路径同款形态：不为调度调用建工具行
  const live = reduce({ ...base, busy: true }, { type: 'toolCall', id: 'd1', name: 'agent_dispatch', arguments: '{}' });
  assert.equal(live.blocks.filter((b) => b.kind === 'tool').length, 0);
});

test('② 普通工具（read_file）回放仍建工具行且结果照常回填（不许回归）', () => {
  const s = reduceHistory(base, snap([
    assistant([toolCall('c1', 'read_file', { path: 'a.go' })]),
    toolResult('c1', '1→package main'),
  ]));
  assert.equal(rows(s).length, 1);
  assert.equal(cards(s).length, 0);
  assert.equal(rows(s)[0].name, 'read_file');
  assert.equal(rows(s)[0].result, '1→package main');
  assert.equal(rows(s)[0].isError, false);
});

test('③ 配对的 tool 结果回填进卡：status done + 结论正文（尾部提示行被剥掉）', () => {
  const s = reduceHistory(base, snap([
    assistant([dispatchCall('d1', { agent: 'coder', task: '跑测试' })]),
    toolResult('d1', '测试全绿，改了 3 个文件。' + hint('s-child-1')),
  ]));
  const card = cards(s)[0];
  assert.equal(card.status, 'done');
  assert.equal(card.result, '测试全绿，改了 3 个文件。');
  assert.ok(!card.result.includes('子会话 id'), '提示行是给模型看的续跑线索，不是给用户看的结论');
  assert.equal(card.isError, false);
  // 结果正文为空（只回了一行提示）时给占位说明，不留空白
  const only = reduceHistory(base, snap([
    assistant([dispatchCall('d2', { agent: 'coder', task: 't' })]),
    toolResult('d2', hint('s-child-2')),
  ]));
  assert.equal(cards(only)[0].result, DISPATCH_NO_TEXT);
});

test('④ arguments 是坏 JSON：不抛异常，仍然产出 dispatch 卡（task 空串）', () => {
  const broken = '{"agent":"cod'; // max_tokens 截断的形态（AGENTS.md §5 坑 12）
  let s;
  assert.doesNotThrow(() => {
    s = reduceHistory(base, snap([assistant([{ id: 'd1', function: { name: 'agent_dispatch', arguments: broken } }])]));
  }, '畸形历史来自 SQLite——抛异常会让整个会话打不开');
  assert.equal(cards(s).length, 1);
  assert.equal(rows(s).length, 0);
  assert.equal(cards(s)[0].task, '');
  assert.equal(cards(s)[0].agentId, '');
  // 空参数 / 非对象 JSON 同样不炸
  assert.deepEqual(parseDispatchArgs(undefined), { agent: '', task: '', session: '' });
  assert.deepEqual(parseDispatchArgs('[]'), { agent: '', task: '', session: '' });
  assert.deepEqual(parseDispatchArgs('null'), { agent: '', task: '', session: '' });
  // 非字符串字段不被当成字符串用（模型给数字/对象时回落空串，不渲染 "[object Object]"）
  assert.deepEqual(parseDispatchArgs('{"agent":7,"task":{"a":1}}'), { agent: '', task: '', session: '' });
});

test('⑤ 子会话 id 优先取结果提示行里的（arguments.session 为空也能拿到）', () => {
  const s = reduceHistory(base, snap([
    assistant([dispatchCall('d1', { agent: 'coder', task: '继续上次的进度' })]), // 无 session
    toolResult('d1', '接着做完了。' + hint('s-child-real')),
  ]));
  assert.equal(cards(s)[0].sessionId, 's-child-real');
  // 与提示行冲突时也以提示行为准（它是本次真正用的那个子会话）
  const conflict = reduceHistory(base, snap([
    assistant([dispatchCall('d2', { agent: 'coder', task: 't', session: 's-stale' })]),
    toolResult('d2', '做完了。' + hint('s-child-real')),
  ]));
  assert.equal(cards(conflict)[0].sessionId, 's-child-real');
  // 没有提示行时回落 arguments.session（老历史/错误路径）
  const fallback = reduceHistory(base, snap([
    assistant([dispatchCall('d3', { agent: 'coder', task: 't', session: 's-arg' })]),
    toolResult('d3', '做完了。'),
  ]));
  assert.equal(cards(fallback)[0].sessionId, 's-arg');
});

test('⑥ 没有配对结果的 agent_dispatch 不许假装 running（中断就是中断）', () => {
  const s = reduceHistory(base, snap([
    assistant([dispatchCall('d1', { agent: 'coder', task: 't' })]),
  ]));
  const card = cards(s)[0];
  assert.notEqual(card.status, 'running', '重启后没有结果 = 那次派发中断了，不是"正在跑"');
  assert.equal(card.status, 'done');
  assert.equal(card.isError, true);
  assert.equal(card.result, DISPATCH_INTERRUPTED);
  assert.ok(card.result.length > 0, '必须给出为什么没有结论，不许留空白');
});

test('并行派发：同一条 assistant 消息里的多个 dispatch 各建一张卡，顺序不变', () => {
  const s = reduceHistory(base, snap([
    assistant([
      dispatchCall('d1', { agent: 'coder', task: 'A' }),
      dispatchCall('d2', { agent: 'tester', task: 'B' }),
    ]),
    toolResult('d1', 'A 完成' + hint('s-a')),
    toolResult('d2', 'B 完成' + hint('s-b')),
  ]));
  assert.deepEqual(cards(s).map((c) => c.id), ['d1', 'd2']);
  assert.deepEqual(cards(s).map((c) => c.result), ['A 完成', 'B 完成']);
  assert.deepEqual(cards(s).map((c) => c.sessionId), ['s-a', 's-b']);
});

test('孤儿 tool 结果（没有前置调用）被丢弃，不炸也不挂错', () => {
  const s = reduceHistory(base, snap([toolResult('nobody', '孤儿结果')]));
  assert.deepEqual(s.blocks, []);
});

test('splitDispatchResult：提示行不在末尾也能摘除（不吞结论）', () => {
  const { body, sessionId } = splitDispatchResult('前' + hint('s-1') + '后');
  assert.equal(sessionId, 's-1');
  // 提示行自带的空行留在中间（那是原文的一部分）——只清**尾部**空白，
  // 不做全文 trim（结论正文里的空行是 markdown 结构，吞掉会改变渲染）
  assert.equal(body, '前\n\n后');
  // 提示行在末尾时：空行一起清掉，结论正文干干净净
  assert.equal(splitDispatchResult('结论' + hint('s-2')).body, '结论');
});
